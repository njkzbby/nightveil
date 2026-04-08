package xhttp

// Config holds XHTTP transport configuration.
type Config struct {
	PathPrefix     string // URL path prefix, e.g. "/x7k2m9"
	UploadPath     string // relative upload path, e.g. "/u/p3q"
	DownloadPath   string // relative download path, e.g. "/d/r8w"
	SessionKeyName string // query param / cookie name for session ID, e.g. "cf_tok"
	MaxChunkSize   int    // max bytes per POST upload chunk (default 14336)
	SessionTimeout int    // seconds before orphaned session cleanup
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
}

func (c *Config) fullUploadPath() string   { return c.PathPrefix + c.UploadPath }
func (c *Config) fullDownloadPath() string { return c.PathPrefix + c.DownloadPath }
