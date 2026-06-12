package quota

// Parser converts an upstream HTTP response body into a Snapshot.
// Each upstream (MiniMax, DeepSeek, ...) supplies its own Parser.
type Parser interface {
	Format() string
	Parse(body []byte) (Snapshot, error)
}

var registry = map[string]Parser{}

// Register makes a parser available by its format id. Called from init() in
// each provider-specific file.
func Register(p Parser) {
	registry[p.Format()] = p
}

// Lookup returns the parser for the given format id (case-insensitive).
// Returns nil if the format is unknown — callers should treat this as "skip
// this provider for quota refresh" rather than a hard error.
func Lookup(format string) Parser {
	if format == "" {
		return nil
	}
	return registry[format]
}
