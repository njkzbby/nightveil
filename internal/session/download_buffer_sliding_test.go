package session

import (
	"runtime"
	"testing"
)

// TestDownloadBufferCompactsAfterAdvance verifies that Advance() allows the
// buffer to drop delivered bytes, keeping only the replay window.
func TestDownloadBufferCompactsAfterAdvance(t *testing.T) {
	db := NewDownloadBufferSized(1024) // 1 KiB replay window for easy math
	defer db.Close()

	chunk := make([]byte, 512)
	for i := range chunk {
		chunk[i] = 0xAB
	}

	// Write 8 KiB in 512 B chunks (16 chunks)
	for i := 0; i < 16; i++ {
		if _, err := db.Write(chunk); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	// Before advance, everything is retained (no compaction until delivered moves).
	if got := db.Len(); got != 8*1024 {
		t.Fatalf("before advance: Len=%d want 8192", got)
	}

	// Server has delivered the first 7 KiB to a GET reader.
	db.Advance(7 * 1024)

	// Now the compactor should drop everything before (delivered - maxRetain)
	// = 7168 - 1024 = 6144. Remaining buf = 8192 - 6144 = 2048.
	if got := db.Len(); got > 2*1024+512 {
		t.Fatalf("after advance: Len=%d want <= 2560", got)
	}
}

// TestDownloadBufferReadFromAbsoluteOffsetAfterCompact verifies that offsets
// remain absolute across compaction, and stale offsets return ErrOffsetTooOld.
func TestDownloadBufferReadFromAbsoluteOffsetAfterCompact(t *testing.T) {
	db := NewDownloadBufferSized(512) // 512 B replay window
	defer db.Close()

	// Write 2 KiB in 256 B chunks
	chunk := make([]byte, 256)
	for i := range chunk {
		chunk[i] = 1
	}
	for i := 0; i < 8; i++ {
		db.Write(chunk)
	}

	// Advance past the entire 2 KiB
	db.Advance(2048)
	// Force compaction by writing one more byte (triggers the compactor path)
	db.Write([]byte{2})

	// Reading from offset 0 (absolute) should return ErrOffsetTooOld — that
	// data has been discarded.
	buf := make([]byte, 256)
	_, _, err := db.ReadFrom(0, buf)
	if err != ErrOffsetTooOld {
		t.Fatalf("read from offset 0 after compact: err=%v want ErrOffsetTooOld", err)
	}

	// Reading from the CURRENT absolute offset should still work (last byte).
	n, newOff, err := db.ReadFrom(2048, buf)
	if err != nil {
		t.Fatalf("read from offset 2048: %v", err)
	}
	if n != 1 || buf[0] != 2 || newOff != 2049 {
		t.Fatalf("read current: n=%d buf[0]=%d newOff=%d", n, buf[0], newOff)
	}
}

// TestDownloadBufferReconnectWithinWindow verifies that a GET reader can
// resume from an offset that was delivered but still within the replay window.
func TestDownloadBufferReconnectWithinWindow(t *testing.T) {
	db := NewDownloadBufferSized(2048) // 2 KiB replay
	defer db.Close()

	// Write 2 KiB
	data := make([]byte, 2048)
	for i := range data {
		data[i] = byte(i % 256)
	}
	db.Write(data)

	// Advance 1 KiB — first half is "delivered"
	db.Advance(1024)

	// Simulate reconnect: new GET resumes from 1024 (not 0) but replay window
	// still has from offset 0 because we haven't forced compaction below that.
	// Actually: after Advance(1024), since maxRetain=2048 and len(buf)=2048,
	// compactor keeps delivered-discarded = 1024 as "already delivered but
	// replay-eligible" + maxRetain - that cushion = 2048 bytes, so nothing drops.
	// Read from 1024 — should see the second half.
	buf := make([]byte, 2048)
	n, newOff, err := db.ReadFrom(1024, buf)
	if err != nil {
		t.Fatalf("reconnect read: %v", err)
	}
	if n != 1024 || newOff != 2048 {
		t.Fatalf("reconnect read: n=%d newOff=%d", n, newOff)
	}
	for i := 0; i < 1024; i++ {
		if buf[i] != byte((1024+i)%256) {
			t.Fatalf("byte %d: got %d want %d", i, buf[i], (1024+i)%256)
		}
	}
}

// TestDownloadBufferMemoryBoundedLong verifies that transferring many MB
// through the buffer with Advance keeps resident memory bounded by the
// replay window. Guarded by -short so CI can skip.
func TestDownloadBufferMemoryBoundedLong(t *testing.T) {
	if testing.Short() {
		t.Skip("long memory test skipped in -short")
	}

	db := NewDownloadBufferSized(4 * 1024 * 1024) // 4 MiB replay window
	defer db.Close()

	var baseline runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&baseline)

	chunk := make([]byte, 64*1024) // 64 KiB
	totalBytes := int64(100 * 1024 * 1024) // 100 MiB total
	written := int64(0)

	for written < totalBytes {
		if _, err := db.Write(chunk); err != nil {
			t.Fatalf("write: %v", err)
		}
		written += int64(len(chunk))
		// Simulate reader consuming everything that's been written.
		db.Advance(int(written))
	}

	// Length must be <= maxRetain (after final advance, compaction is triggered
	// on the next Write; here we just check it's not O(totalBytes)).
	bufLen := db.Len()
	if bufLen > 8*1024*1024 {
		t.Fatalf("buffer grew to %d bytes (want <= 8 MiB)", bufLen)
	}

	var after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&after)

	delta := int64(after.HeapInuse) - int64(baseline.HeapInuse)
	if delta > 32*1024*1024 {
		t.Fatalf("heap grew by %d bytes (want <= 32 MiB)", delta)
	}
}

// TestDownloadBufferAdvanceBeforeWrite — Advance called before anything
// is written should be a no-op.
func TestDownloadBufferAdvanceBeforeWrite(t *testing.T) {
	db := NewDownloadBufferSized(1024)
	defer db.Close()

	db.Advance(0)
	db.Advance(100) // past end — should clamp, not panic

	db.Write([]byte("hello"))
	buf := make([]byte, 32)
	// After a phantom advance past the current end, the next read from
	// offset 0 should still succeed — we never actually compacted anything.
	n, _, err := db.ReadFrom(0, buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if n != 5 || string(buf[:n]) != "hello" {
		t.Fatalf("read: n=%d got %q", n, buf[:n])
	}
}

// TestDownloadBufferBackwardCompatConstructor — NewDownloadBuffer (no size)
// still works with the default replay size.
func TestDownloadBufferBackwardCompatConstructor(t *testing.T) {
	db := NewDownloadBuffer()
	defer db.Close()

	db.Write([]byte("ping"))
	buf := make([]byte, 32)
	n, _, err := db.ReadFrom(0, buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf[:n]) != "ping" {
		t.Fatalf("got %q", buf[:n])
	}
}
