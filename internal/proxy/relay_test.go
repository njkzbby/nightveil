package proxy

import (
	"bytes"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func TestRelayBidirectional(t *testing.T) {
	leftClient, leftServer := net.Pipe()
	rightClient, rightServer := net.Pipe()

	go Relay(leftServer, rightServer)

	// Left → Right
	msg := []byte("hello from left")
	go leftClient.Write(msg)

	buf := make([]byte, 100)
	n, err := rightClient.Read(buf)
	if err != nil {
		t.Fatalf("read from right: %v", err)
	}
	if !bytes.Equal(buf[:n], msg) {
		t.Fatalf("got %q, want %q", buf[:n], msg)
	}

	// Right → Left
	msg2 := []byte("hello from right")
	go rightClient.Write(msg2)

	n, err = leftClient.Read(buf)
	if err != nil {
		t.Fatalf("read from left: %v", err)
	}
	if !bytes.Equal(buf[:n], msg2) {
		t.Fatalf("got %q, want %q", buf[:n], msg2)
	}

	leftClient.Close()
	rightClient.Close()
}

func TestRelayLargeData(t *testing.T) {
	leftClient, leftServer := net.Pipe()
	rightClient, rightServer := net.Pipe()

	go Relay(leftServer, rightServer)

	size := 1024 * 1024
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 251)
	}

	received := make(chan []byte, 1)
	go func() {
		buf, _ := io.ReadAll(rightClient)
		received <- buf
	}()

	// Write in chunks to avoid blocking on synchronous pipe
	go func() {
		chunkSize := 32768
		for i := 0; i < len(data); i += chunkSize {
			end := i + chunkSize
			if end > len(data) {
				end = len(data)
			}
			leftClient.Write(data[i:end])
		}
		leftClient.Close()
	}()

	select {
	case got := <-received:
		if len(got) != size {
			t.Fatalf("received %d bytes, want %d", len(got), size)
		}
		if !bytes.Equal(got, data) {
			t.Fatal("data mismatch")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for large data transfer")
	}

	rightClient.Close()
}

func TestRelayOneDirectionClose(t *testing.T) {
	leftClient, leftServer := net.Pipe()
	rightClient, rightServer := net.Pipe()

	done := make(chan error, 1)
	go func() {
		done <- Relay(leftServer, rightServer)
	}()

	leftClient.Close()

	select {
	case <-done:
		// ok
	case <-time.After(3 * time.Second):
		t.Fatal("relay did not finish after close")
	}

	rightClient.Close()
}

func TestRelayConcurrent(t *testing.T) {
	leftClient, leftServer := net.Pipe()
	rightClient, rightServer := net.Pipe()

	done := make(chan struct{})
	go func() {
		Relay(leftServer, rightServer)
		close(done)
	}()

	var wg sync.WaitGroup
	wg.Add(4)

	// Writers
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			leftClient.Write([]byte("left"))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			rightClient.Write([]byte("right"))
		}
	}()

	// Readers (drain)
	go func() {
		defer wg.Done()
		io.Copy(io.Discard, leftClient)
	}()
	go func() {
		defer wg.Done()
		io.Copy(io.Discard, rightClient)
	}()

	// Let writers finish, then close to unblock readers
	time.Sleep(100 * time.Millisecond)
	leftClient.Close()
	rightClient.Close()

	wg.Wait()
}
