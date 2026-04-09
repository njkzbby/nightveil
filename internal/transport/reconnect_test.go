package transport

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// mockTransport for testing
type mockTransport struct {
	dialFunc func(ctx context.Context, sid [16]byte) (Conn, error)
}

func (m *mockTransport) Dial(ctx context.Context, sid [16]byte) (Conn, error) {
	return m.dialFunc(ctx, sid)
}
func (m *mockTransport) Close() error { return nil }

type mockConn struct{}

func (m *mockConn) Read(p []byte) (int, error)  { return 0, nil }
func (m *mockConn) Write(p []byte) (int, error) { return len(p), nil }
func (m *mockConn) Close() error                { return nil }
func (m *mockConn) LocalAddr() net.Addr          { return &net.TCPAddr{} }
func (m *mockConn) RemoteAddr() net.Addr         { return &net.TCPAddr{} }

func TestReconnectSuccess(t *testing.T) {
	inner := &mockTransport{
		dialFunc: func(ctx context.Context, sid [16]byte) (Conn, error) {
			return &mockConn{}, nil
		},
	}

	rt := NewReconnectingTransport(inner, ReconnectConfig{})
	conn, err := rt.Dial(context.Background(), [16]byte{})
	if err != nil {
		t.Fatal(err)
	}
	if conn == nil {
		t.Fatal("expected non-nil conn")
	}
}

func TestReconnectRetryThenSuccess(t *testing.T) {
	attempt := 0
	inner := &mockTransport{
		dialFunc: func(ctx context.Context, sid [16]byte) (Conn, error) {
			attempt++
			if attempt < 3 {
				return nil, errors.New("connection refused")
			}
			return &mockConn{}, nil
		},
	}

	rt := NewReconnectingTransport(inner, ReconnectConfig{
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     50 * time.Millisecond,
	})

	conn, err := rt.Dial(context.Background(), [16]byte{})
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if conn == nil {
		t.Fatal("expected non-nil conn")
	}
	if attempt != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempt)
	}
}

func TestReconnectMaxRetries(t *testing.T) {
	inner := &mockTransport{
		dialFunc: func(ctx context.Context, sid [16]byte) (Conn, error) {
			return nil, errors.New("always fails")
		},
	}

	rt := NewReconnectingTransport(inner, ReconnectConfig{
		MaxRetries:   3,
		InitialDelay: 10 * time.Millisecond,
	})

	_, err := rt.Dial(context.Background(), [16]byte{})
	if err == nil {
		t.Fatal("expected error after max retries")
	}
}

func TestReconnectCancelledContext(t *testing.T) {
	inner := &mockTransport{
		dialFunc: func(ctx context.Context, sid [16]byte) (Conn, error) {
			return nil, errors.New("fail")
		},
	}

	rt := NewReconnectingTransport(inner, ReconnectConfig{
		InitialDelay: time.Second, // long delay
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := rt.Dial(ctx, [16]byte{})
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}

func TestReconnectClosedTransport(t *testing.T) {
	inner := &mockTransport{
		dialFunc: func(ctx context.Context, sid [16]byte) (Conn, error) {
			time.Sleep(100 * time.Millisecond)
			return nil, errors.New("fail")
		},
	}

	rt := NewReconnectingTransport(inner, ReconnectConfig{
		InitialDelay: 10 * time.Millisecond,
	})
	rt.Close()

	_, err := rt.Dial(context.Background(), [16]byte{})
	if err == nil {
		t.Fatal("expected error on closed transport")
	}
}
