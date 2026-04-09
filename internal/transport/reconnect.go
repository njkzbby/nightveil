// Package transport provides reconnection logic for tunnel connections.
package transport

import (
	"context"
	"log"
	"sync"
	"time"
)

// ReconnectConfig configures auto-reconnection behavior.
type ReconnectConfig struct {
	MaxRetries    int           // 0 = unlimited
	InitialDelay  time.Duration // default 1s
	MaxDelay      time.Duration // default 30s
	BackoffFactor float64       // default 2.0
}

func (c *ReconnectConfig) defaults() {
	if c.InitialDelay <= 0 {
		c.InitialDelay = time.Second
	}
	if c.MaxDelay <= 0 {
		c.MaxDelay = 30 * time.Second
	}
	if c.BackoffFactor <= 0 {
		c.BackoffFactor = 2.0
	}
}

// ReconnectingTransport wraps a ClientTransport with auto-reconnect.
type ReconnectingTransport struct {
	inner  ClientTransport
	config ReconnectConfig

	mu      sync.Mutex
	closed  bool
}

// NewReconnectingTransport wraps a transport with reconnection logic.
func NewReconnectingTransport(inner ClientTransport, cfg ReconnectConfig) *ReconnectingTransport {
	cfg.defaults()
	return &ReconnectingTransport{
		inner:  inner,
		config: cfg,
	}
}

// Dial attempts to connect with exponential backoff on failure.
func (r *ReconnectingTransport) Dial(ctx context.Context, sessionID [16]byte) (Conn, error) {
	delay := r.config.InitialDelay
	attempt := 0

	for {
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			return nil, context.Canceled
		}
		r.mu.Unlock()

		conn, err := r.inner.Dial(ctx, sessionID)
		if err == nil {
			if attempt > 0 {
				log.Printf("[reconnect] connected after %d retries", attempt)
			}
			return conn, nil
		}

		attempt++
		if r.config.MaxRetries > 0 && attempt >= r.config.MaxRetries {
			return nil, err
		}

		log.Printf("[reconnect] dial failed (attempt %d): %v, retrying in %v", attempt, err, delay)

		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}

		// Exponential backoff
		delay = time.Duration(float64(delay) * r.config.BackoffFactor)
		if delay > r.config.MaxDelay {
			delay = r.config.MaxDelay
		}
	}
}

// Close shuts down the transport.
func (r *ReconnectingTransport) Close() error {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	return r.inner.Close()
}
