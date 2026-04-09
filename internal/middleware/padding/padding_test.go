package padding

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"testing"
)

// pipeConn wraps net.Conn to implement transport.Conn
type pipeConn struct{ net.Conn }

func (p *pipeConn) LocalAddr() net.Addr  { return p.Conn.LocalAddr() }
func (p *pipeConn) RemoteAddr() net.Addr { return p.Conn.RemoteAddr() }

func TestPaddingRoundTrip(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	mw := New(Config{MinBytes: 10, MaxBytes: 50})
	writerConn := mw.Wrap(&pipeConn{left})
	readerConn := mw.Wrap(&pipeConn{right})

	msg := []byte("hello padding")

	go func() {
		writerConn.Write(msg)
	}()

	buf := make([]byte, 100)
	n, err := readerConn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(buf[:n], msg) {
		t.Fatalf("got %q, want %q", buf[:n], msg)
	}
}

func TestPaddingMultipleFrames(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	mw := New(Config{MinBytes: 5, MaxBytes: 20})
	writerConn := mw.Wrap(&pipeConn{left})
	readerConn := mw.Wrap(&pipeConn{right})

	messages := []string{"first", "second message", "third!"}

	go func() {
		for _, m := range messages {
			writerConn.Write([]byte(m))
		}
	}()

	for _, want := range messages {
		buf := make([]byte, 100)
		n, err := readerConn.Read(buf)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if string(buf[:n]) != want {
			t.Fatalf("got %q, want %q", buf[:n], want)
		}
	}
}

func TestPaddingLargeData(t *testing.T) {
	left, right := net.Pipe()

	mw := New(Config{MinBytes: 32, MaxBytes: 128})
	writerConn := mw.Wrap(&pipeConn{left})
	readerConn := mw.Wrap(&pipeConn{right})

	size := 100000
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 251)
	}

	var wg sync.WaitGroup
	wg.Add(1)

	var received []byte
	go func() {
		defer wg.Done()
		buf := make([]byte, 4096)
		for len(received) < size {
			n, err := readerConn.Read(buf)
			if err != nil {
				return
			}
			received = append(received, buf[:n]...)
		}
	}()

	// Write in chunks
	chunkSize := 1000
	for i := 0; i < len(data); i += chunkSize {
		end := i + chunkSize
		if end > len(data) {
			end = len(data)
		}
		if _, err := writerConn.Write(data[i:end]); err != nil {
			t.Fatalf("write at %d: %v", i, err)
		}
	}

	wg.Wait()
	left.Close()
	right.Close()

	if len(received) < size {
		t.Fatalf("received %d bytes, want %d", len(received), size)
	}
	if !bytes.Equal(received[:size], data) {
		t.Fatal("data mismatch")
	}
}

func TestPaddingZeroPadding(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	mw := New(Config{MinBytes: 0, MaxBytes: 0})
	writerConn := mw.Wrap(&pipeConn{left})
	readerConn := mw.Wrap(&pipeConn{right})

	msg := []byte("no padding")
	go func() {
		writerConn.Write(msg)
	}()

	buf := make([]byte, 100)
	n, err := readerConn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != "no padding" {
		t.Fatalf("got %q", buf[:n])
	}
}

func TestPaddingWireSize(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()

	mw := New(Config{MinBytes: 10, MaxBytes: 10})
	writerConn := mw.Wrap(&pipeConn{left})

	msg := []byte("test")
	go func() {
		writerConn.Write(msg)
		left.Close()
	}()

	raw, _ := io.ReadAll(right)
	expected := headerSize + len(msg) + 10
	if len(raw) != expected {
		t.Fatalf("wire size: got %d, want %d", len(raw), expected)
	}
}

func TestPaddingHugeWrite(t *testing.T) {
	// Test data > 65535 bytes — triggers multi-frame split
	left, right := net.Pipe()

	mw := New(Config{MinBytes: 0, MaxBytes: 0})
	writerConn := mw.Wrap(&pipeConn{left})
	readerConn := mw.Wrap(&pipeConn{right})

	size := 100000
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 199)
	}

	received := make(chan []byte, 1)
	go func() {
		var buf []byte
		tmp := make([]byte, 8192)
		for len(buf) < size {
			n, err := readerConn.Read(tmp)
			if err != nil {
				break
			}
			buf = append(buf, tmp[:n]...)
		}
		received <- buf
	}()

	writerConn.Write(data)
	writerConn.Close()

	got := <-received
	if len(got) < size {
		t.Fatalf("received %d, want %d", len(got), size)
	}
	for i := 0; i < size; i++ {
		if got[i] != data[i] {
			t.Fatalf("mismatch at byte %d", i)
			break
		}
	}
}

func TestPaddingRangeStatistical(t *testing.T) {
	// Verify padding is within configured range over many writes
	left, right := net.Pipe()
	defer right.Close()

	mw := New(Config{MinBytes: 20, MaxBytes: 100})
	writerConn := mw.Wrap(&pipeConn{left})

	go func() {
		for i := 0; i < 50; i++ {
			writerConn.Write([]byte("x"))
		}
		left.Close()
	}()

	for i := 0; i < 50; i++ {
		var header [4]byte
		if _, err := io.ReadFull(right, header[:]); err != nil {
			break
		}
		realLen := int(binary.LittleEndian.Uint16(header[0:2]))
		padLen := int(binary.LittleEndian.Uint16(header[2:4]))

		if realLen != 1 {
			t.Fatalf("frame %d: realLen=%d, want 1", i, realLen)
		}
		if padLen < 20 || padLen > 100 {
			t.Fatalf("frame %d: padLen=%d out of range [20,100]", i, padLen)
		}

		// Drain the data + padding
		discard := make([]byte, realLen+padLen)
		io.ReadFull(right, discard)
	}
}

func TestPaddingClosePropagates(t *testing.T) {
	left, right := net.Pipe()

	mw := New(Config{MinBytes: 0, MaxBytes: 0})
	writerConn := mw.Wrap(&pipeConn{left})
	readerConn := mw.Wrap(&pipeConn{right})

	writerConn.Close()

	buf := make([]byte, 10)
	_, err := readerConn.Read(buf)
	if err == nil {
		t.Fatal("expected error after close")
	}
}
