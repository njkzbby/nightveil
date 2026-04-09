package xhttp

import (
	"net/http"
	"sync"
	"time"
)

// RateLimiter limits requests per IP address.
type RateLimiter struct {
	mu       sync.Mutex
	clients  map[string]*clientRate
	maxRate  int           // max requests per window
	window   time.Duration // time window
}

type clientRate struct {
	count    int
	windowAt time.Time
}

func NewRateLimiter(maxPerWindow int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		clients: make(map[string]*clientRate),
		maxRate: maxPerWindow,
		window:  window,
	}
	go rl.cleanup()
	return rl
}

// Allow checks if a request from the given IP is allowed.
func (rl *RateLimiter) Allow(r *http.Request) bool {
	if rl == nil || rl.maxRate <= 0 {
		return true
	}

	ip := r.RemoteAddr
	// Strip port
	for i := len(ip) - 1; i >= 0; i-- {
		if ip[i] == ':' {
			ip = ip[:i]
			break
		}
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cr, ok := rl.clients[ip]
	if !ok {
		rl.clients[ip] = &clientRate{count: 1, windowAt: now}
		return true
	}

	if now.Sub(cr.windowAt) > rl.window {
		cr.count = 1
		cr.windowAt = now
		return true
	}

	cr.count++
	return cr.count <= rl.maxRate
}

func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(rl.window * 2)
	defer ticker.Stop()

	for range ticker.C {
		rl.mu.Lock()
		now := time.Now()
		for ip, cr := range rl.clients {
			if now.Sub(cr.windowAt) > rl.window*2 {
				delete(rl.clients, ip)
			}
		}
		rl.mu.Unlock()
	}
}
