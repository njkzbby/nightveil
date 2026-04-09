package transport

import (
	"context"
	"crypto/rand"
	"log"
	"sync"
	"time"
)

// PoolConfig configures connection pooling.
type PoolConfig struct {
	Size        int           // number of pre-established connections (default 2)
	RefillDelay time.Duration // delay between refill attempts (default 5s)
}

func (c *PoolConfig) defaults() {
	if c.Size <= 0 {
		c.Size = 2
	}
	if c.RefillDelay <= 0 {
		c.RefillDelay = 5 * time.Second
	}
}

// PooledTransport maintains a pool of pre-established connections.
// When Dial is called, it returns a connection from the pool instantly.
// Pool is refilled in the background.
type PooledTransport struct {
	inner  ClientTransport
	config PoolConfig

	pool   chan Conn
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewPooledTransport creates a connection pool around a transport.
func NewPooledTransport(inner ClientTransport, cfg PoolConfig) *PooledTransport {
	cfg.defaults()
	ctx, cancel := context.WithCancel(context.Background())

	p := &PooledTransport{
		inner:  inner,
		config: cfg,
		pool:   make(chan Conn, cfg.Size),
		ctx:    ctx,
		cancel: cancel,
	}

	// Initial fill
	p.wg.Add(1)
	go p.refillLoop()

	return p
}

// Dial returns a connection from the pool, or dials a new one if pool is empty.
func (p *PooledTransport) Dial(ctx context.Context, sessionID [16]byte) (Conn, error) {
	// Try pool first (non-blocking)
	select {
	case conn := <-p.pool:
		return conn, nil
	default:
	}

	// Pool empty — dial directly
	return p.inner.Dial(ctx, sessionID)
}

// Close drains the pool and shuts down.
func (p *PooledTransport) Close() error {
	p.cancel()
	p.wg.Wait()

	// Close all pooled connections
	close(p.pool)
	for conn := range p.pool {
		conn.Close()
	}

	return p.inner.Close()
}

func (p *PooledTransport) refillLoop() {
	defer p.wg.Done()

	for {
		// Fill pool to capacity
		for len(p.pool) < p.config.Size {
			var sessionID [16]byte
			rand.Read(sessionID[:])

			conn, err := p.inner.Dial(p.ctx, sessionID)
			if err != nil {
				if p.ctx.Err() != nil {
					return
				}
				log.Printf("[pool] refill dial failed: %v", err)
				break // wait before retrying
			}

			select {
			case p.pool <- conn:
			default:
				conn.Close() // pool full
			}
		}

		select {
		case <-time.After(p.config.RefillDelay):
		case <-p.ctx.Done():
			return
		}
	}
}
