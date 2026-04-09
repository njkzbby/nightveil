package jitter

import (
	"bytes"
	"net"
	"testing"
	"time"
)

type pipeConn struct{ net.Conn }

func (p *pipeConn) LocalAddr() net.Addr  { return p.Conn.LocalAddr() }
func (p *pipeConn) RemoteAddr() net.Addr { return p.Conn.RemoteAddr() }

func TestJitterRoundTrip(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	mw := New(Config{MinMs: 1, MaxMs: 5, WriteOnly: true})
	writerConn := mw.Wrap(&pipeConn{left})
	readerConn := &pipeConn{right} // no jitter on read side

	msg := []byte("hello jitter")
	go func() {
		writerConn.Write(msg)
	}()

	buf := make([]byte, 100)
	n, err := readerConn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf[:n], msg) {
		t.Fatalf("got %q, want %q", buf[:n], msg)
	}
}

func TestJitterAddsDelay(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	mw := New(Config{MinMs: 50, MaxMs: 50, WriteOnly: true})
	writerConn := mw.Wrap(&pipeConn{left})

	start := time.Now()
	go func() {
		writerConn.Write([]byte("test"))
	}()

	buf := make([]byte, 10)
	right.Read(buf)
	elapsed := time.Since(start)

	if elapsed < 40*time.Millisecond {
		t.Fatalf("expected at least ~50ms delay, got %v", elapsed)
	}
}

func TestJitterZero(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	mw := New(Config{MinMs: 0, MaxMs: 0})
	writerConn := mw.Wrap(&pipeConn{left})

	start := time.Now()
	go func() {
		writerConn.Write([]byte("fast"))
	}()

	buf := make([]byte, 10)
	right.Read(buf)
	elapsed := time.Since(start)

	if elapsed > 20*time.Millisecond {
		t.Fatalf("zero jitter should be fast, got %v", elapsed)
	}
}

func TestJitterWriteOnly(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	mw := New(Config{MinMs: 50, MaxMs: 50, WriteOnly: true})
	conn := mw.Wrap(&pipeConn{left})

	// Write should be delayed
	go func() {
		right.Write([]byte("from right"))
	}()

	start := time.Now()
	buf := make([]byte, 100)
	conn.Read(buf) // WriteOnly=true → read should NOT be delayed
	elapsed := time.Since(start)

	if elapsed > 20*time.Millisecond {
		t.Fatalf("read should not be delayed with WriteOnly, got %v", elapsed)
	}
}
