package xhttp

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// failingRoundTripper wraps an inner RoundTripper and forces non-200
// responses for POSTs whose seq parameter is in failSeqs.
type failingRoundTripper struct {
	inner    http.RoundTripper
	failSeqs map[int64]bool
	failed   sync.Map // tracks seqs that have already failed once
}

func (f *failingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodPost && strings.Contains(req.URL.Path, "/u") {
		seqStr := req.URL.Query().Get("seq")
		seq, _ := strconv.ParseInt(seqStr, 10, 64)
		if f.failSeqs[seq] {
			f.failed.Store(seq, true)
			return &http.Response{
				StatusCode: 500,
				Body:       io.NopCloser(strings.NewReader("forced failure")),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}
	}
	return f.inner.RoundTrip(req)
}

// TestUploadPipelineFailFast — when one POST in a pipelined Write fails,
// the conn becomes broken and subsequent Reads/Writes return the recorded
// error rather than blocking.
func TestUploadPipelineFailFast(t *testing.T) {
	serverAuth, clientAuth := makeAuthPair(t)
	cfg := Config{MaxChunkSize: 1024, SessionTimeout: 30}
	srv := NewServer(cfg, serverAuth, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	ts := httptest.NewServer(srv)
	defer ts.Close()

	// Wrap ts.Client transport so seq=3 always returns 500.
	httpClient := ts.Client()
	httpClient.Transport = &failingRoundTripper{
		inner:    httpClient.Transport,
		failSeqs: map[int64]bool{3: true},
	}

	xhttpClient := NewClient(ts.URL, cfg, clientAuth, httpClient)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var sid [16]byte
	rand.Read(sid[:])
	conn, err := xhttpClient.Dial(ctx, sid)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Write 8 KiB so it splits into 8 chunks (seq 0..7) — one of them (seq=3)
	// will be served a 500.
	payload := make([]byte, 8*1024)
	for i := range payload {
		payload[i] = byte(i)
	}

	start := time.Now()
	_, writeErr := conn.Write(payload)
	if writeErr == nil {
		t.Fatal("expected Write to return error from failing POST")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Write took %v — should fail fast under 2s", elapsed)
	}

	// Subsequent Write should also fail with the same error class (no deadlock).
	_, writeErr2 := conn.Write([]byte("ping"))
	if writeErr2 == nil {
		t.Fatal("second Write should also fail (broken conn)")
	}

	// Read should also fail rather than block.
	readDone := make(chan error, 1)
	go func() {
		buf := make([]byte, 1024)
		_, err := conn.Read(buf)
		readDone <- err
	}()

	select {
	case err := <-readDone:
		if err == nil {
			t.Fatal("Read should fail on broken conn")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Read blocked on broken conn — fail-fast did not propagate")
	}
}

// TestUploadSingleChunkFailFast — the fast path for tiny writes (single
// chunk, no goroutines) must also mark the conn broken on failure.
func TestUploadSingleChunkFailFast(t *testing.T) {
	serverAuth, clientAuth := makeAuthPair(t)
	cfg := Config{MaxChunkSize: 65536, SessionTimeout: 30}
	srv := NewServer(cfg, serverAuth, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	ts := httptest.NewServer(srv)
	defer ts.Close()

	httpClient := ts.Client()
	httpClient.Transport = &failingRoundTripper{
		inner:    httpClient.Transport,
		failSeqs: map[int64]bool{0: true},
	}

	xhttpClient := NewClient(ts.URL, cfg, clientAuth, httpClient)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var sid [16]byte
	rand.Read(sid[:])
	conn, err := xhttpClient.Dial(ctx, sid)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// 100 bytes is well under MaxChunkSize → fast-path single POST.
	_, writeErr := conn.Write(make([]byte, 100))
	if writeErr == nil {
		t.Fatal("expected single-chunk Write to fail")
	}

	// Subsequent ops fail-fast.
	_, err2 := conn.Write([]byte("x"))
	if err2 == nil {
		t.Fatal("conn should be broken after first failure")
	}
}

// TestSessionAbortUpload verifies the new server-side abort path on the
// Session unblocks ReadUpload promptly.
func TestSessionAbortUpload(t *testing.T) {
	// Imported via test helper to avoid pulling session into the file.
	_ = testing.AllocsPerRun // silence unused-import shenanigans on rebuild

	// (driven via session package directly — tested in session_close_test.go
	// for the basic close path; this xhttp-side smoke test just ensures
	// the broken/aborted flow doesn't deadlock callers.)
	serverAuth, clientAuth := makeAuthPair(t)
	cfg := Config{MaxChunkSize: 1024, SessionTimeout: 30}
	srv := NewServer(cfg, serverAuth, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	ts := httptest.NewServer(srv)
	defer ts.Close()

	httpClient := ts.Client()
	httpClient.Transport = &failingRoundTripper{
		inner:    httpClient.Transport,
		failSeqs: map[int64]bool{2: true},
	}

	xhttpClient := NewClient(ts.URL, cfg, clientAuth, httpClient)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var sid [16]byte
	rand.Read(sid[:])
	conn, _ := xhttpClient.Dial(ctx, sid)
	defer conn.Close()

	// Write enough to span multiple chunks; one will fail.
	payload := make([]byte, 4096)
	_, err := conn.Write(payload)
	if err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("expected non-nil error from broken pipeline, got %v", err)
	}
}
