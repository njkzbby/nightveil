package transport

import (
	"context"
	"errors"
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
	closed := false
	m := NewManager([]NamedTransport{
		{"test", &mockTransport{dialFunc: func(ctx context.Context, sid [16]byte) (Conn, error) {
			return &mockConn{}, nil
		}}},
	})
	_ = closed

	err := m.Close()
	if err != nil {
		t.Fatal(err)
	}
}
