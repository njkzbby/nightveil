package session

import (
	"testing"
)

// TestSessionCloseDropsOutOfOrderBuffer verifies that Close() releases the
// upload reassembly map even when chunks are stuck out-of-order. Without
// this, every session that died mid-pipeline would leak its in-flight
// chunks until GC eventually traced over the orphaned Session struct.
func TestSessionCloseDropsOutOfOrderBuffer(t *testing.T) {
	s := NewSession([16]byte{99})

	// Push seqs 5,6,7 — ReadUpload would block forever waiting for seq 0.
	s.PushUpload(5, []byte("five"))
	s.PushUpload(6, []byte("six"))
	s.PushUpload(7, []byte("seven"))

	// Sanity: buf has 3 entries before close.
	s.uploadMu.Lock()
	pre := len(s.uploadBuf)
	s.uploadMu.Unlock()
	if pre != 3 {
		t.Fatalf("pre-close len=%d want 3", pre)
	}

	s.Close()

	// After close: uploadBuf must be nil so the map+entries become unreachable.
	s.uploadMu.Lock()
	post := s.uploadBuf
	s.uploadMu.Unlock()
	if post != nil {
		t.Fatalf("post-close uploadBuf not nil: len=%d", len(post))
	}
}

// TestSessionPushAfterCloseSafe verifies that PushUpload after Close is a
// no-op (does NOT panic from writing to a nil map). The uploadDone gate
// must short-circuit before touching the map.
func TestSessionPushAfterCloseSafe(t *testing.T) {
	s := NewSession([16]byte{100})
	s.Close()

	// Should not panic. Should not store anything.
	s.PushUpload(0, []byte("late"))

	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()
	if s.uploadBuf != nil {
		t.Fatalf("uploadBuf should remain nil after push-post-close, got len=%d", len(s.uploadBuf))
	}
}

// TestSessionReadAfterCloseReturnsFalse verifies that ReadUpload safely
// returns (nil, false) once the session is closed, even with a nil
// uploadBuf — no nil map panics.
func TestSessionReadAfterCloseReturnsFalse(t *testing.T) {
	s := NewSession([16]byte{101})
	s.Close()

	data, ok := s.ReadUpload()
	if ok || data != nil {
		t.Fatalf("expected (nil,false) after close; got (%v,%v)", data, ok)
	}
}
