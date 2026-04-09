package transport

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestManagerFirstTransport(t *testing.T) {
	m := NewManager([]NamedTransport{
		{"primary", &mockTransport{dialFunc: func(ctx context.Context, sid [16]byte) (Conn, error) {
			return &mockConn{}, nil
		}}},
	})

	conn, err := m.Dial(context.Background(), [16]byte{})
	if err != nil || conn == nil {
		t.Fatalf("expected success, got err=%v", err)
	}
	if m.ActiveTransport() != "primary" {
		t.Fatalf("active: %s", m.ActiveTransport())
	}
}

func TestManagerFailover(t *testing.T) {
	m := NewManager([]NamedTransport{
		{"primary", &mockTransport{dialFunc: func(ctx context.Context, sid [16]byte) (Conn, error) {
			return nil, errors.New("down")
		}}},
		{"fallback", &mockTransport{dialFunc: func(ctx context.Context, sid [16]byte) (Conn, error) {
			return &mockConn{}, nil
		}}},
	})

	conn, err := m.Dial(context.Background(), [16]byte{})
	if err != nil || conn == nil {
		t.Fatalf("expected fallback success, got err=%v", err)
	}
	if m.ActiveTransport() != "fallback" {
		t.Fatalf("should have switched to fallback, got %s", m.ActiveTransport())
	}
}

func TestManagerAllFail(t *testing.T) {
	m := NewManager([]NamedTransport{
		{"a", &mockTransport{dialFunc: func(ctx context.Context, sid [16]byte) (Conn, error) {
			return nil, errors.New("fail a")
		}}},
		{"b", &mockTransport{dialFunc: func(ctx context.Context, sid [16]byte) (Conn, error) {
			return nil, errors.New("fail b")
		}}},
	})

	_, err := m.Dial(context.Background(), [16]byte{})
	if err == nil {
		t.Fatal("expected error when all transports fail")
	}
}

func TestManagerHealthCheckPromote(t *testing.T) {
	primaryOK := false
	m := NewManager([]NamedTransport{
		{"primary", &mockTransport{dialFunc: func(ctx context.Context, sid [16]byte) (Conn, error) {
			if !primaryOK {
				return nil, errors.New("down")
			}
			return &mockConn{}, nil
		}}},
		{"fallback", &mockTransport{dialFunc: func(ctx context.Context, sid [16]byte) (Conn, error) {
			return &mockConn{}, nil
		}}},
	})

	// Initial dial → fallback
	m.Dial(context.Background(), [16]byte{})
	if m.ActiveTransport() != "fallback" {
		t.Fatalf("expected fallback, got %s", m.ActiveTransport())
	}

	// "Fix" primary
	primaryOK = true

	// Start health check with fast interval
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.StartHealthCheck(ctx, 50*time.Millisecond)

	time.Sleep(200 * time.Millisecond)

	if m.ActiveTransport() != "primary" {
		t.Fatalf("expected promotion back to primary, got %s", m.ActiveTransport())
	}
}

func TestManagerClose(t *testing.T) {
	m := NewManager([]NamedTransport{
		{"test", &mockTransport{dialFunc: func(ctx context.Context, sid [16]byte) (Conn, error) {
			return &mockConn{}, nil
		}}},
	})
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestManagerReplace(t *testing.T) {
	callCount := 0
	m := NewManager([]NamedTransport{
		{"old", &mockTransport{dialFunc: func(ctx context.Context, sid [16]byte) (Conn, error) {
			callCount++
			return &mockConn{}, nil
		}}},
	})

	// Dial with old
	m.Dial(context.Background(), [16]byte{})
	if callCount != 1 {
		t.Fatalf("old should be called once, got %d", callCount)
	}

	// Replace with new
	newCount := 0
	m.Replace(0, NamedTransport{Name: "new", Transport: &mockTransport{dialFunc: func(ctx context.Context, sid [16]byte) (Conn, error) {
		newCount++
		return &mockConn{}, nil
	}}})

	// Dial with new
	m.Dial(context.Background(), [16]byte{})
	if newCount != 1 {
		t.Fatalf("new should be called, got %d", newCount)
	}
	if m.ActiveTransport() != "new" {
		t.Fatalf("active: %s", m.ActiveTransport())
	}
	m.Close()
}

func TestManagerConcurrentDial(t *testing.T) {
	dialCount := int32(0)
	m := NewManager([]NamedTransport{
		{"primary", &mockTransport{dialFunc: func(ctx context.Context, sid [16]byte) (Conn, error) {
			atomic.AddInt32(&dialCount, 1)
			return &mockConn{}, nil
		}}},
	})
	defer m.Close()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := m.Dial(context.Background(), [16]byte{})
			if err != nil || conn == nil {
				t.Errorf("concurrent dial failed")
			}
		}()
	}
	wg.Wait()

	if atomic.LoadInt32(&dialCount) != 50 {
		t.Fatalf("expected 50 dials, got %d", dialCount)
	}
}

func TestManagerFailoverUnderConcurrentLoad(t *testing.T) {
	primaryFail := int32(0) // 0 = ok, 1 = fail
	m := NewManager([]NamedTransport{
		{"primary", &mockTransport{dialFunc: func(ctx context.Context, sid [16]byte) (Conn, error) {
			if atomic.LoadInt32(&primaryFail) == 1 {
				return nil, errors.New("primary down")
			}
			return &mockConn{}, nil
		}}},
		{"fallback", &mockTransport{dialFunc: func(ctx context.Context, sid [16]byte) (Conn, error) {
			return &mockConn{}, nil
		}}},
	})
	defer m.Close()

	// Start 20 goroutines, fail primary after 5
	var wg sync.WaitGroup
	errCount := int32(0)

	for i := 0; i < 20; i++ {
		if i == 5 {
			atomic.StoreInt32(&primaryFail, 1)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := m.Dial(context.Background(), [16]byte{})
			if err != nil {
				atomic.AddInt32(&errCount, 1)
			}
		}()
	}
	wg.Wait()

	if errCount > 0 {
		t.Fatalf("failover should catch all — got %d errors", errCount)
	}
}
