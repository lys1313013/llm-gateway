package quota

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/lys1313013/llm-gateway/backend/internal/db"
	"github.com/lys1313013/llm-gateway/backend/internal/models"
)

// Fetcher owns the quota cache and the HTTP client used to talk to upstreams.
type Fetcher struct {
	Cache *Cache
	HTTP  *http.Client
}

func NewFetcher() *Fetcher {
	return &Fetcher{
		Cache: NewCache(),
		HTTP:  &http.Client{Timeout: 8 * time.Second},
	}
}

// global is the process-wide Fetcher, set by InitGlobal in main.
var global *Fetcher

// InitGlobal installs the package-level Fetcher. Must be called once at
// startup before any handler runs.
func InitGlobal(f *Fetcher) { global = f }

// Global returns the Fetcher installed by InitGlobal. Panics if not set.
func Global() *Fetcher {
	if global == nil {
		panic("quota: Global() called before InitGlobal()")
	}
	return global
}

// RefreshOne fetches and caches quota for a single provider. Used both by the
// background refresher and the on-demand POST .../refresh handler.
func (f *Fetcher) RefreshOne(ctx context.Context, p models.Provider) {
	if p.QuotaURL == nil || *p.QuotaURL == "" {
		return
	}
	if p.QuotaFormat == nil {
		slog.Warn("quota: provider has quota_url but no format", "id", p.ID, "name", p.Name)
		return
	}
	parser := Lookup(*p.QuotaFormat)
	if parser == nil {
		slog.Warn("quota: unknown format, skipping", "id", p.ID, "format", *p.QuotaFormat)
		return
	}
	if p.APIKey == nil || *p.APIKey == "" {
		f.Cache.Set(p.ID, Snapshot{
			DisplayType: "",
			LastError:   "api_key 未配置",
			FetchedAt:   time.Now(),
		})
		return
	}

	snap, err := f.fetchAndParse(ctx, *p.QuotaURL, *p.APIKey, parser)
	if err != nil {
		slog.Warn("quota: refresh failed", "id", p.ID, "name", p.Name, "err", err)
		// Preserve the previous good snapshot's payload but mark error.
		prev, _ := f.Cache.Get(p.ID)
		prev.LastError = err.Error()
		prev.FetchedAt = time.Now()
		f.Cache.Set(p.ID, prev)
		return
	}
	f.Cache.Set(p.ID, snap)
	slog.Info("quota: refreshed", "id", p.ID, "name", p.Name, "type", snap.DisplayType)
}

func (f *Fetcher) fetchAndParse(ctx context.Context, url, apiKey string, parser Parser) (Snapshot, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Snapshot{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := f.HTTP.Do(req)
	if err != nil {
		return Snapshot{}, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Snapshot{}, fmt.Errorf("upstream status %d", resp.StatusCode)
	}
	body, err := readAllLimited(resp.Body, 1<<20) // 1 MiB
	if err != nil {
		return Snapshot{}, fmt.Errorf("read body: %w", err)
	}
	return parser.Parse(body)
}

// RunRefresher drives a periodic refresh of all providers that have
// quota_url set. Blocks until ctx is cancelled.
func (f *Fetcher) RunRefresher(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()

	// Immediate first pass so the cache isn't empty for the first 5 minutes
	// after startup.
	f.refreshAllProviders(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			f.refreshAllProviders(ctx)
		}
	}
}

func (f *Fetcher) refreshAllProviders(ctx context.Context) {
	providers, err := db.GetProviders(ctx)
	if err != nil {
		slog.Error("quota: list providers", "err", err)
		return
	}
	for _, p := range providers {
		if p.QuotaURL == nil || *p.QuotaURL == "" {
			continue
		}
		f.RefreshOne(ctx, p)
	}
}
