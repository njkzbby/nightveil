package transport

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// Manager handles transport selection and failover.
// It tries transports in priority order, switching to the next on failure.
type Manager struct {
	transports []NamedTransport
	mu         sync.RWMutex
	activeIdx  int
	healthCh   chan struct{} // signaled when health check should run
}

// NamedTransport is a transport with a human-readable name.
type NamedTransport struct {
	Name      string
	Transport ClientTransport
}

// NewManager creates a transport manager with failover.
// Transports are tried in order — first is highest priority.
func NewManager(transports []NamedTransport) *Manager {
	return &Manager{
		transports: transports,
		healthCh:   make(chan struct{}, 1),
	}
}

// Dial tries the active transport first, then falls back to others.
func (m *Manager) Dial(ctx context.Context, sessionID [16]byte) (Conn, error) {
	m.mu.RLock()
	startIdx := m.activeIdx
	transports := m.transports
	m.mu.RUnlock()

	// Try active first
	conn, err := transports[startIdx].Transport.Dial(ctx, sessionID)
	if err == nil {
		return conn, nil
	}

	log.Printf("[manager] %s failed: %v, trying fallbacks", transports[startIdx].Name, err)

	// Try others in order
	for i := range transports {
		if i == startIdx {
			continue
		}
		conn, err = transports[i].Transport.Dial(ctx, sessionID)
		if err == nil {
			m.mu.Lock()
			m.activeIdx = i
			m.mu.Unlock()
			log.Printf("[manager] switched to %s", transports[i].Name)
			return conn, nil
		}
		log.Printf("[manager] %s failed: %v", transports[i].Name, err)
	}

	return nil, fmt.Errorf("all transports failed, last error: %w", err)
}

// Close shuts down all transports.
func (m *Manager) Close() error {
	for _, t := range m.transports {
		t.Transport.Close()
	}
	return nil
}

// Replace swaps a transport at the given index (for parameter rotation).
func (m *Manager) Replace(idx int, nt NamedTransport) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if idx >= 0 && idx < len(m.transports) {
		old := m.transports[idx]
		m.transports[idx] = nt
		go old.Transport.Close() // close old in background
	}
}

// ActiveTransport returns the name of the currently active transport.
func (m *Manager) ActiveTransport() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.activeIdx < len(m.transports) {
		return m.transports[m.activeIdx].Name
	}
	return "none"
}

// StartHealthCheck periodically tries to switch back to higher-priority transport.
func (m *Manager) StartHealthCheck(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				m.tryPromote(ctx)
			case <-ctx.Done():
				return
			}
		}
	}()
}

// tryPromote checks if a higher-priority transport is available again.
func (m *Manager) tryPromote(ctx context.Context) {
	m.mu.RLock()
	currentIdx := m.activeIdx
	m.mu.RUnlock()

	if currentIdx == 0 {
		return // already on highest priority
	}

	// Try higher-priority transports
	for i := 0; i < currentIdx; i++ {
		testCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		var testID [16]byte
		conn, err := m.transports[i].Transport.Dial(testCtx, testID)
		cancel()

		if err == nil {
			conn.Close()
			m.mu.Lock()
			m.activeIdx = i
			m.mu.Unlock()
			log.Printf("[manager] promoted back to %s", m.transports[i].Name)
			return
		}
	}
}
