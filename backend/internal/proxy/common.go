package proxy

import (
	"net/http"
	"time"
)

// timeoutFromConfig converts the user-facing timeout config (seconds; -1 =
// no timeout) into a net/http timeout value.
func timeoutFromConfig(seconds int) time.Duration {
	if seconds == -1 {
		return 0 // no timeout
	}
	if seconds <= 0 {
		return 60 * time.Second
	}
	return time.Duration(seconds) * time.Second
}

// streamingTransport disables connection reuse for long-lived streams
// (mostly a no-op — http.Client handles this fine), but we use it as a
// place to plug in tracing / metrics later.
var streamingTransport = &http.Transport{
	DisableCompression: true,
	MaxIdleConns:       100,
	IdleConnTimeout:    90 * time.Second,
}

func strPtr(s string) *string    { return &s }
func intPtr(i int) *int          { return &i }
