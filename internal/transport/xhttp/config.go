package xhttp

// Config holds XHTTP transport configuration.
type Config struct {
	PathPrefix     string // URL path prefix, e.g. "/x7k2m9"
	UploadPath     string // relative upload path, e.g. "/u/p3q"
	DownloadPath   string // relative download path, e.g. "/d/r8w"
	SessionKeyName string // query param / cookie name for session ID, e.g. "cf_tok"
	MaxChunkSize   int    // max bytes per POST upload chunk (default 14336)
	SessionTimeout int    // seconds before orphaned session cleanup

	// DownloadBufferBytes is the per-session sliding-window replay buffer
	// for the download direction. Bytes already delivered to the active GET
	// reader are dropped beyond this watermark. Default 4 MiB. Larger values
	// allow longer reconnect windows at the cost of memory per session.
	DownloadBufferBytes int

	// UploadMode selects the upload transport submode:
	//   "auto"   — stream-up if the HTTP client uses uTLS (direct/REALITY mode),
	//              packet-up otherwise (CDN-friendly).
	//   "stream" — single long-lived POST with chunked transfer encoding.
	//              Lowest overhead, highest throughput. Required server v0.2+.
	//   "packet" — POST per chunk, ordered via sequence numbers. CDN-compat.
	UploadMode string
}

func (c *Config) defaults() {
	if c.PathPrefix == "" {
		c.PathPrefix = "/api"
	}
	if c.UploadPath == "" {
		c.UploadPath = "/u"
	}
	if c.DownloadPath == "" {
		c.DownloadPath = "/d"
	}
	if c.SessionKeyName == "" {
		c.SessionKeyName = "sid"
	}
	if c.MaxChunkSize <= 0 {
		c.MaxChunkSize = 14336 // 14KB, under TSPU 15-20KB threshold
	}
	if c.SessionTimeout <= 0 {
		c.SessionTimeout = 30
	}
	if c.DownloadBufferBytes <= 0 {
		c.DownloadBufferBytes = 4 * 1024 * 1024 // 4 MiB replay window
	}
	if c.UploadMode == "" {
		c.UploadMode = "auto"
	}
}

func (c *Config) fullUploadPath() string   { return c.PathPrefix + c.UploadPath }
func (c *Config) fullDownloadPath() string { return c.PathPrefix + c.DownloadPath }
