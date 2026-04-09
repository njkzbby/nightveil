// Package cover generates background fake HTTP requests to mask
// real traffic patterns from DPI/ML analysis.
//
// Without cover: traffic appears/disappears with user activity → fingerprintable
// With cover: constant background noise → real traffic hidden in the noise
package cover

import (
	"context"
	"crypto/rand"
	"io"
	"log"
	"math/big"
	"net/http"
	"time"
)

// Config for cover traffic generation.
type Config struct {
	// Enabled controls whether cover traffic is active.
	Enabled bool

	// Endpoints to send fake requests to (through the tunnel SOCKS5 proxy).
	// Should be high-traffic CDN-hosted sites that won't look suspicious.
	Endpoints []string

	// IntervalMin/Max: random delay between cover requests.
	IntervalMinMs int // default 2000
	IntervalMaxMs int // default 10000

	// PayloadMinBytes/Max: random body size for POST requests.
	PayloadMinBytes int // default 100
	PayloadMaxBytes int // default 2000
}

func (c *Config) defaults() {
	if len(c.Endpoints) == 0 {
		c.Endpoints = []string{
			"https://www.google.com/gen_204",
			"https://cp.cloudflare.com/",
			"https://detectportal.firefox.com/success.txt",
			"https://www.gstatic.com/generate_204",
			"https://connectivity-check.ubuntu.com/",
		}
	}
	if c.IntervalMinMs <= 0 {
		c.IntervalMinMs = 2000
	}
	if c.IntervalMaxMs <= c.IntervalMinMs {
		c.IntervalMaxMs = 10000
	}
	if c.PayloadMinBytes <= 0 {
		c.PayloadMinBytes = 100
	}
	if c.PayloadMaxBytes <= c.PayloadMinBytes {
		c.PayloadMaxBytes = 2000
	}
}

// Generator produces background cover traffic.
type Generator struct {
	config Config
	client *http.Client // should go through the tunnel (SOCKS5)
	ctx    context.Context
	cancel context.CancelFunc
	stats  Stats
}

// Stats tracks cover traffic metrics.
type Stats struct {
	RequestsSent int64
	BytesSent    int64
	Errors       int64
}

// NewGenerator creates a cover traffic generator.
// The httpClient should be configured to go through the tunnel's SOCKS5 proxy.
func NewGenerator(cfg Config, httpClient *http.Client) *Generator {
	cfg.defaults()
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	ctx, cancel := context.WithCancel(context.Background())

	return &Generator{
		config: cfg,
		client: httpClient,
		ctx:    ctx,
		cancel: cancel,
	}
}

// Start begins generating cover traffic in the background.
func (g *Generator) Start() {
	if !g.config.Enabled {
		return
	}
	go g.loop()
	log.Printf("[cover] started: %d endpoints, interval %d-%dms",
		len(g.config.Endpoints), g.config.IntervalMinMs, g.config.IntervalMaxMs)
}

// Stop halts cover traffic generation.
func (g *Generator) Stop() {
	g.cancel()
}

// GetStats returns current cover traffic statistics.
func (g *Generator) GetStats() Stats {
	return g.stats
}

func (g *Generator) loop() {
	for {
		// Random interval between requests
		delay := randRange(g.config.IntervalMinMs, g.config.IntervalMaxMs)

		select {
		case <-time.After(time.Duration(delay) * time.Millisecond):
			g.sendCoverRequest()
		case <-g.ctx.Done():
			return
		}
	}
}

func (g *Generator) sendCoverRequest() {
	// Pick random endpoint
	idx := randRange(0, len(g.config.Endpoints)-1)
	endpoint := g.config.Endpoints[idx]

	// Random method: 70% GET, 30% HEAD (to vary pattern)
	method := "GET"
	if randRange(0, 9) < 3 {
		method = "HEAD"
	}

	ctx, cancel := context.WithTimeout(g.ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		g.stats.Errors++
		return
	}

	// Add realistic headers
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := g.client.Do(req)
	if err != nil {
		g.stats.Errors++
		return
	}

	// Read and discard body
	n, _ := io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	g.stats.RequestsSent++
	g.stats.BytesSent += n
}

func randRange(min, max int) int {
	if max <= min {
		return min
	}
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(max-min+1)))
	return min + int(n.Int64())
}
