package session

import (
	"sync"
	"testing"
	"time"
)

func TestSessionUploadOrdering(t *testing.T) {
	s := NewSession([16]byte{1})
	defer s.Close()

	// Push out of order
	s.PushUpload(2, []byte("third"))
	s.PushUpload(0, []byte("first"))
	s.PushUpload(1, []byte("second"))

	// Read in order
	data, ok := s.ReadUpload()
	if !ok || string(data) != "first" {
		t.Fatalf("seq 0: got %q, ok=%v", data, ok)
	}
	data, ok = s.ReadUpload()
	if !ok || string(data) != "second" {
		t.Fatalf("seq 1: got %q", data)
	}
	data, ok = s.ReadUpload()
	if !ok || string(data) != "third" {
		t.Fatalf("seq 2: got %q", data)
	}
}

func TestSessionUploadBlocks(t *testing.T) {
	s := NewSession([16]byte{2})
	defer s.Close()

	done := make(chan string, 1)
	go func() {
		data, _ := s.ReadUpload()
		done <- string(data)
	}()

	// Should block until data arrives
	time.Sleep(50 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("should have blocked")
	default:
	}

	s.PushUpload(0, []byte("arrived"))

	select {
	case got := <-done:
		if got != "arrived" {
			t.Fatalf("got %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestSessionCloseUnblocksRead(t *testing.T) {
	s := NewSession([16]byte{3})

	done := make(chan bool, 1)
	go func() {
		_, ok := s.ReadUpload()
		done <- ok
	}()

	time.Sleep(50 * time.Millisecond)
	s.Close()

	select {
	case ok := <-done:
		if ok {
			t.Fatal("expected ok=false after close")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout — close didn't unblock ReadUpload")
	}
}

func TestSessionDownloadBuffer(t *testing.T) {
	s := NewSession([16]byte{4})
	defer s.Close()

	msg := []byte("download data")
	go func() {
		s.DownloadBuf.Write(msg)
	}()

	// Wait for data notification
	<-s.DownloadBuf.Notify()

	buf := make([]byte, 100)
	n, _, err := s.DownloadBuf.ReadFrom(0, buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != "download data" {
		t.Fatalf("got %q", buf[:n])
	}
}

func TestDownloadBufferOffsetResume(t *testing.T) {
	db := NewDownloadBuffer()
	defer db.Close()

	// Write 3 chunks
	db.Write([]byte("aaa"))
	db.Write([]byte("bbb"))
	db.Write([]byte("ccc"))

	buf := make([]byte, 100)

	// Read from offset 0 — gets everything
	n, off, _ := db.ReadFrom(0, buf)
	if string(buf[:n]) != "aaabbbccc" || off != 9 {
		t.Fatalf("read0: got %q offset=%d", buf[:n], off)
	}

	// Read from offset 3 — skips first chunk
	n, off, _ = db.ReadFrom(3, buf)
	if string(buf[:n]) != "bbbccc" || off != 9 {
		t.Fatalf("read3: got %q offset=%d", buf[:n], off)
	}

	// Read from offset 9 — no data yet
	n, off, _ = db.ReadFrom(9, buf)
	if n != 0 || off != 9 {
		t.Fatalf("read9: got n=%d offset=%d", n, off)
	}

	// Write more, read from 9
	db.Write([]byte("ddd"))
	n, off, _ = db.ReadFrom(9, buf)
	if string(buf[:n]) != "ddd" || off != 12 {
		t.Fatalf("read9+: got %q offset=%d", buf[:n], off)
	}
}

func TestDownloadBufferClose(t *testing.T) {
	db := NewDownloadBuffer()
	db.Write([]byte("data"))
	db.Close()

	buf := make([]byte, 100)

	// Can still read existing data
	n, off, err := db.ReadFrom(0, buf)
	if n != 4 || off != 4 || err != nil {
		t.Fatalf("got n=%d off=%d err=%v", n, off, err)
	}

	// Reading past end returns error
	_, _, err = db.ReadFrom(4, buf)
	if err != ErrSessionClosed {
		t.Fatalf("expected ErrSessionClosed, got %v", err)
	}
}

func TestSessionMarkConnected(t *testing.T) {
	s := NewSession([16]byte{5})
	defer s.Close()

	go func() {
		time.Sleep(50 * time.Millisecond)
		s.MarkConnected()
	}()

	if !s.WaitConnected() {
		t.Fatal("expected connected=true")
	}
}

func TestSessionWaitConnectedClosed(t *testing.T) {
	s := NewSession([16]byte{6})

	go func() {
		time.Sleep(50 * time.Millisecond)
		s.Close()
	}()

	if s.WaitConnected() {
		t.Fatal("expected connected=false after close")
	}
}

func TestManagerGetOrCreate(t *testing.T) {
	m := NewManager(10 * time.Second)

	id := [16]byte{10}
	s1 := m.GetOrCreate(id)
	s2 := m.GetOrCreate(id)

	if s1 != s2 {
		t.Fatal("GetOrCreate should return same session")
	}
	if m.Count() != 1 {
		t.Fatalf("count: got %d, want 1", m.Count())
	}
}

func TestManagerRemove(t *testing.T) {
	m := NewManager(10 * time.Second)

	id := [16]byte{11}
	m.GetOrCreate(id)
	m.Remove(id)

	if m.Count() != 0 {
		t.Fatalf("count after remove: got %d", m.Count())
	}
	if s := m.Get(id); s != nil {
		t.Fatal("session should be nil after remove")
	}
}

func TestManagerTTLCleanup(t *testing.T) {
	m := NewManager(100 * time.Millisecond)

	id := [16]byte{12}
	m.GetOrCreate(id)
	// Don't mark connected — should expire

	time.Sleep(200 * time.Millisecond)

	if s := m.Get(id); s != nil {
		t.Fatal("session should have been cleaned up by TTL")
	}
}

func TestManagerConnectedNotExpired(t *testing.T) {
	m := NewManager(100 * time.Millisecond)

	id := [16]byte{13}
	s := m.GetOrCreate(id)
	s.MarkConnected()

	time.Sleep(200 * time.Millisecond)

	if got := m.Get(id); got == nil {
		t.Fatal("connected session should NOT be cleaned up by TTL")
	}

	m.Remove(id)
}

func TestSessionConcurrentUpload(t *testing.T) {
	s := NewSession([16]byte{20})
	defer s.Close()

	count := 100
	var wg sync.WaitGroup
	wg.Add(count)

	for i := 0; i < count; i++ {
		go func(seq int) {
			defer wg.Done()
			s.PushUpload(int64(seq), []byte{byte(seq)})
		}(i)
	}

	wg.Wait()

	// Read all in order
	for i := 0; i < count; i++ {
		data, ok := s.ReadUpload()
		if !ok {
			t.Fatalf("read seq %d: not ok", i)
		}
		if len(data) != 1 || data[0] != byte(i) {
			t.Fatalf("seq %d: got %v", i, data)
		}
	}
}
