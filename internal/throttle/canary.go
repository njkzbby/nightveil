package throttle

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

// CanaryConfig configures the canary detection system.
type CanaryConfig struct {
	// Endpoints to probe (should be highly-available sites)
	Endpoints []string
	// Interval between probes
	Interval time.Duration
	// Timeout per probe
	Timeout time.Duration
	// FailThreshold: consecutive failures before declaring interference
	FailThreshold int
}

func (c *CanaryConfig) defaults() {
	if len(c.Endpoints) == 0 {
		c.Endpoints = []string{
			"https://www.google.com/generate_204",
			"https://cp.cloudflare.com/",
			"https://detectportal.firefox.com/success.txt",
		}
	}
	if c.Interval <= 0 {
		c.Interval = 30 * time.Second
	}
	if c.Timeout <= 0 {
		c.Timeout = 10 * time.Second
	}
	if c.FailThreshold <= 0 {
		c.FailThreshold = 3
	}
}

// CanaryState represents the detected network condition.
type CanaryState int

const (
	CanaryOK          CanaryState = iota // Direct internet works
	CanaryDegraded                       // Partial failures
	CanaryBlocked                        // TSPU is interfering
)

func (s CanaryState) String() string {
	switch s {
	case CanaryOK:
		return "ok"
	case CanaryDegraded:
		return "degraded"
	case CanaryBlocked:
		return "blocked"
	default:
		return "unknown"
	}
}

// Canary probes the internet through the tunnel to detect TSPU interference.
type Canary struct {
	config    CanaryConfig
	client    *http.Client // should go through the tunnel (SOCKS5 proxy)
	detector  *Detector    // optional: feed results to throttle detector

	mu           sync.Mutex
	state        CanaryState
	consecutiveFails int
	listeners    []func(CanaryState)

	ctx    context.Context
	cancel context.CancelFunc
}

// NewCanary creates a canary detector.
func NewCanary(cfg CanaryConfig, httpClient *http.Client, detector *Detector) *Canary {
	cfg.defaults()
	if httpClient == nil {
		httpClient = &http.Client{Timeout: cfg.Timeout}
	}
	ctx, cancel := context.WithCancel(context.Background())

	return &Canary{
		config:   cfg,
		client:   httpClient,
		detector: detector,
		ctx:      ctx,
		cancel:   cancel,
	}
}

// Start begins periodic probing.
func (c *Canary) Start() {
	go c.probeLoop()
}

// Stop halts probing.
func (c *Canary) Stop() {
	c.cancel()
}

// GetState returns the current canary state.
func (c *Canary) GetState() CanaryState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// OnStateChange registers a callback.
func (c *Canary) OnStateChange(fn func(CanaryState)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.listeners = append(c.listeners, fn)
}

func (c *Canary) probeLoop() {
	ticker := time.NewTicker(c.config.Interval)
	defer ticker.Stop()

	// Initial probe
	c.probe()

	for {
		select {
		case <-ticker.C:
			c.probe()
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *Canary) probe() {
	successes := 0
	failures := 0

	for _, endpoint := range c.config.Endpoints {
		start := time.Now()
		err := c.probeEndpoint(endpoint)
		rtt := time.Since(start)

		if err != nil {
			failures++
			log.Printf("[canary] probe %s failed: %v", endpoint, err)
		} else {
			successes++
			// Feed RTT to detector
			if c.detector != nil {
				c.detector.RecordRTT(rtt)
			}
		}
	}

	c.mu.Lock()
	oldState := c.state

	if failures == 0 {
		c.consecutiveFails = 0
		c.state = CanaryOK
	} else if successes > 0 {
		c.consecutiveFails++
		c.state = CanaryDegraded
	} else {
		c.consecutiveFails++
		if c.consecutiveFails >= c.config.FailThreshold {
			c.state = CanaryBlocked
		} else {
			c.state = CanaryDegraded
		}
	}

	newState := c.state
	callbacks := make([]func(CanaryState), len(c.listeners))
	copy(callbacks, c.listeners)
	c.mu.Unlock()

	if oldState != newState {
		log.Printf("[canary] state changed: %s → %s", oldState, newState)
		for _, fn := range callbacks {
			go fn(newState)
		}
	}
}

func (c *Canary) probeEndpoint(url string) error {
	ctx, cancel := context.WithTimeout(c.ctx, c.config.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}
