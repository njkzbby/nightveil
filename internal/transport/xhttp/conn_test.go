package xhttp

import (
	"bytes"
	"testing"

	"github.com/njkzbby/nightveil/internal/session"
)

// TestSplitConnReadSmallBuffer verifies that Read with a buffer SMALLER than
// the underlying chunk does not lose bytes. The previous implementation
// truncated via copy() and silently dropped the tail.
func TestSplitConnReadSmallBuffer(t *testing.T) {
	sess := session.NewSession([16]byte{42})
	defer sess.Close()

	// Push a 14 KiB chunk (the default MaxChunkSize from Config.defaults).
	chunk := make([]byte, 14*1024)
	for i := range chunk {
		chunk[i] = byte(i % 251) // pseudo-pattern, prime modulus to avoid alignment artefacts
	}
	sess.PushUpload(0, chunk)

	conn := newSplitConn(sess, &dummyAddr{"tcp", "local"}, &dummyAddr{"tcp", "remote"})

	// Drain with a 1 KiB buffer; we should need at least 14 reads.
	got := make([]byte, 0, 14*1024)
	buf := make([]byte, 1024)
	for len(got) < len(chunk) {
		n, err := conn.Read(buf)
		if err != nil {
			t.Fatalf("read after %d bytes: %v", len(got), err)
		}
		if n == 0 {
			t.Fatalf("zero read at %d bytes", len(got))
		}
		got = append(got, buf[:n]...)
	}

	if !bytes.Equal(got, chunk) {
		t.Fatalf("data mismatch: len(got)=%d len(want)=%d", len(got), len(chunk))
	}
}

// TestSplitConnReadExactBuffer verifies that a buffer exactly the chunk size
// returns the whole chunk in one Read with no leftover state.
func TestSplitConnReadExactBuffer(t *testing.T) {
	sess := session.NewSession([16]byte{43})
	defer sess.Close()

	chunk := []byte("hello world")
	sess.PushUpload(0, chunk)

	conn := newSplitConn(sess, &dummyAddr{"tcp", "l"}, &dummyAddr{"tcp", "r"})

	buf := make([]byte, len(chunk))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if n != len(chunk) || string(buf) != "hello world" {
		t.Fatalf("got n=%d %q", n, buf[:n])
	}
	if conn.leftover != nil {
		t.Fatalf("leftover should be nil, got %v", conn.leftover)
	}
}

// TestSplitConnReadAcrossChunks verifies that small-buffer reads correctly
// transition from one chunk's leftover to the next chunk.
func TestSplitConnReadAcrossChunks(t *testing.T) {
	sess := session.NewSession([16]byte{44})
	defer sess.Close()

	sess.PushUpload(0, []byte("AAAA"))
	sess.PushUpload(1, []byte("BBBB"))

	conn := newSplitConn(sess, &dummyAddr{"tcp", "l"}, &dummyAddr{"tcp", "r"})

	// Read 3 bytes at a time. First read pulls "AAA" from chunk 0 (leftover=A).
	// Second read pulls leftover "A". Third read pulls "BBB" from chunk 1
	// (leftover=B). Fourth read pulls leftover "B". Total = "AAAABBBB".
	got := make([]byte, 0, 8)
	buf := make([]byte, 3)
	for i := 0; i < 4; i++ {
		n, err := conn.Read(buf)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		got = append(got, buf[:n]...)
	}
	if string(got) != "AAAABBBB" {
		t.Fatalf("got %q want AAAABBBB", got)
	}
}
