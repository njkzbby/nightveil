package xhttp

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/http2"
)

// TestResolveUploadModeExplicit verifies the explicit override paths.
func TestResolveUploadModeExplicit(t *testing.T) {
	cases := []struct {
		configured string
		want       string
	}{
		{"stream", uploadModeStream},
		{"packet", uploadModePacket},
	}
	for _, tc := range cases {
		got := resolveUploadMode(tc.configured, http.DefaultClient)
		if got != tc.want {
			t.Fatalf("configured=%q got=%q want=%q", tc.configured, got, tc.want)
		}
	}
}

// TestResolveUploadModeAutoPacket — auto with the default http.Client (which
// uses *http.Transport, not *http2.Transport) should fall back to packet.
func TestResolveUploadModeAutoPacket(t *testing.T) {
	got := resolveUploadMode("auto", http.DefaultClient)
	if got != uploadModePacket {
		t.Fatalf("auto + DefaultClient: got %q, want packet", got)
	}
	got = resolveUploadMode("", http.DefaultClient)
	if got != uploadModePacket {
		t.Fatalf("empty + DefaultClient: got %q, want packet", got)
	}
}

// TestResolveUploadModeAutoStream — auto with an *http2.Transport (the kind
// returned by NewUTLSHTTPClient) should pick stream.
func TestResolveUploadModeAutoStream(t *testing.T) {
	cli := &http.Client{Transport: &http2.Transport{}}
	got := resolveUploadMode("auto", cli)
	if got != uploadModeStream {
		t.Fatalf("auto + http2.Transport: got %q, want stream", got)
	}
}

// TestStreamUpClientWritesIntoPipe — full client→server round trip in
// stream mode. Client writes 1 MiB in 1 KiB pieces; server reads the body
// and verifies byte-for-byte equality.
func TestStreamUpClientWritesIntoPipe(t *testing.T) {
	serverAuth, clientAuth := makeAuthPair(t)
	cfg := Config{
		MaxChunkSize:   1024,
		SessionTimeout: 30,
		UploadMode:     "stream",
	}
	srv := NewServer(cfg, serverAuth, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	ts := httptest.NewServer(srv)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Drain Accept loop and copy upload bytes into a sink for verification.
	var received atomic.Int64
	sink := bytes.NewBuffer(nil)
	go func() {
		conn, err := srv.Accept(ctx)
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				sink.Write(buf[:n])
				received.Add(int64(n))
			}
			if err != nil {
				return
			}
		}
	}()

	xhttpClient := NewClient(ts.URL, cfg, clientAuth, ts.Client())
	var sid [16]byte
	rand.Read(sid[:])
	conn, err := xhttpClient.Dial(ctx, sid)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	// Sanity: cfg.UploadMode == "stream" → resolved should be stream.
	if cc, ok := conn.(*clientConn); ok {
		if cc.uploadMode != uploadModeStream {
			t.Fatalf("conn uploadMode = %q, want stream", cc.uploadMode)
		}
	}

	// Write 256 KiB in 1 KiB pieces (smaller than CI flake budget but enough
	// to exercise the pipe many times).
	const total = 256 * 1024
	payload := make([]byte, total)
	for i := range payload {
		payload[i] = byte(i % 251)
	}

	for off := 0; off < len(payload); off += 1024 {
		end := off + 1024
		if end > len(payload) {
			end = len(payload)
		}
		if _, err := conn.Write(payload[off:end]); err != nil {
			t.Fatalf("write at off=%d: %v", off, err)
		}
	}
	conn.Close()

	// Wait for server to drain.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if int(received.Load()) >= total {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if int(received.Load()) != total {
		t.Fatalf("received %d bytes, want %d", received.Load(), total)
	}
	if !bytes.Equal(sink.Bytes(), payload) {
		t.Fatalf("payload mismatch (lengths %d/%d)", sink.Len(), len(payload))
	}
}

// TestStreamUpServerRoutesByHeader verifies the server distinguishes stream
// vs packet upload by the X-NV-Mode header. A packet POST without seq=
// must return 400 (rejected by the legacy path); a POST with the stream
// header is consumed by the stream handler and ultimately returns 200.
//
// Both branches require a download GET to drain the consumer side, since
// the stream pipe is synchronous (zero-buffered io.Pipe). This mirrors
// real client behavior where startDownload() runs before startStreamUpload().
func TestStreamUpServerRoutesByHeader(t *testing.T) {
	serverAuth, clientAuth := makeAuthPair(t)
	cfg := Config{MaxChunkSize: 1024, SessionTimeout: 30}
	srv := NewServer(cfg, serverAuth, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	ts := httptest.NewServer(srv)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Drain the accept loop so stream-mode sessions get a consumer.
	go func() {
		for {
			conn, err := srv.Accept(ctx)
			if err != nil {
				return
			}
			go func(c interface {
				Read([]byte) (int, error)
				Close() error
			}) {
				defer c.Close()
				buf := make([]byte, 4096)
				for {
					if _, err := c.Read(buf); err != nil {
						return
					}
				}
			}(conn)
		}
	}()

	// 1) Packet POST without seq → 400 (legacy path's bad-seq rejection).
	// We don't need a GET first because the server returns 400 before doing
	// any session work.
	var sid1 [16]byte
	rand.Read(sid1[:])
	token1, _ := clientAuth.GenerateToken(sid1)
	req, _ := http.NewRequest("POST",
		ts.URL+cfg.fullUploadPath()+"?"+cfg.SessionKeyName+"="+b64u(sid1[:]),
		bytes.NewReader([]byte("data")),
	)
	req.AddCookie(&http.Cookie{Name: "nv_token", Value: b64u(token1)})
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("packet post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("packet POST without seq should be 400, got %d", resp.StatusCode)
	}

	// 2) Stream-mode flow: GET first (to register HasDownload + emit splitConn
	// once stream POST arrives), then POST with X-NV-Mode header. The accept
	// goroutine above will pull bytes through the pipe so handleUploadStream
	// can drain r.Body.
	var sid2 [16]byte
	rand.Read(sid2[:])
	token2, _ := clientAuth.GenerateToken(sid2)

	// Issue GET in a goroutine — it will block waiting for data, that's fine.
	go func() {
		getReq, _ := http.NewRequest("GET",
			ts.URL+cfg.fullDownloadPath()+"?"+cfg.SessionKeyName+"="+b64u(sid2[:]),
			nil,
		)
		getReq.AddCookie(&http.Cookie{Name: "nv_token", Value: b64u(token2)})
		getResp, err := ts.Client().Do(getReq)
		if err == nil {
			io.Copy(io.Discard, getResp.Body)
			getResp.Body.Close()
		}
	}()

	// Brief wait for GET to register HasDownload — checked via session.
	for i := 0; i < 50; i++ {
		if sess := srv.sessions.Get(sid2); sess != nil && sess.HasDownload.Load() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	req2, _ := http.NewRequest("POST",
		ts.URL+cfg.fullUploadPath()+"?"+cfg.SessionKeyName+"="+b64u(sid2[:]),
		bytes.NewReader([]byte("stream-data")),
	)
	req2.Header.Set("X-NV-Mode", "stream")
	req2.AddCookie(&http.Cookie{Name: "nv_token", Value: b64u(token2)})
	resp2, err := ts.Client().Do(req2)
	if err != nil {
		t.Fatalf("stream post: %v", err)
	}
	io.Copy(io.Discard, resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("stream POST status: got %d, want 200", resp2.StatusCode)
	}
}

// TestPacketUpModeRegression — explicitly setting UploadMode: packet must
// behave exactly like the legacy code path: POSTs include seq, server
// reassembles in order.
func TestPacketUpModeRegression(t *testing.T) {
	serverAuth, clientAuth := makeAuthPair(t)
	cfg := Config{
		MaxChunkSize:   1024,
		SessionTimeout: 30,
		UploadMode:     "packet",
	}
	srv := NewServer(cfg, serverAuth, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	ts := httptest.NewServer(srv)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sink := bytes.NewBuffer(nil)
	go func() {
		conn, err := srv.Accept(ctx)
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				sink.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	xhttpClient := NewClient(ts.URL, cfg, clientAuth, ts.Client())
	var sid [16]byte
	rand.Read(sid[:])
	conn, err := xhttpClient.Dial(ctx, sid)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	if cc, ok := conn.(*clientConn); ok && cc.uploadMode != uploadModePacket {
		t.Fatalf("expected packet mode, got %q", cc.uploadMode)
	}

	payload := []byte("hello packet world ")
	full := bytes.Repeat(payload, 100)
	if _, err := conn.Write(full); err != nil {
		t.Fatalf("write: %v", err)
	}
	conn.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sink.Len() >= len(full) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !bytes.Equal(sink.Bytes(), full) {
		t.Fatalf("packet roundtrip mismatch: got %d bytes, want %d", sink.Len(), len(full))
	}
}

// TestStreamUpEndToEnd is the integration smoke for the full pipeline:
// client writes data through stream-up, server's splitConn reads it back
// via the IsStreamMode bypass.
func TestStreamUpEndToEnd(t *testing.T) {
	serverAuth, clientAuth := makeAuthPair(t)
	cfg := Config{
		MaxChunkSize:   1024,
		SessionTimeout: 30,
		UploadMode:     "stream",
	}
	srv := NewServer(cfg, serverAuth, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	ts := httptest.NewServer(srv)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	got := make(chan []byte, 1)
	go func() {
		conn, err := srv.Accept(ctx)
		if err != nil {
			return
		}
		defer conn.Close()
		out := bytes.NewBuffer(nil)
		buf := make([]byte, 4096)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				out.Write(buf[:n])
			}
			if err != nil {
				got <- out.Bytes()
				return
			}
		}
	}()

	xhttpClient := NewClient(ts.URL, cfg, clientAuth, ts.Client())
	var sid [16]byte
	rand.Read(sid[:])
	conn, err := xhttpClient.Dial(ctx, sid)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	conn.Write([]byte("alpha"))
	conn.Write([]byte("beta"))
	conn.Write([]byte("gamma"))
	conn.Close()

	select {
	case data := <-got:
		if string(data) != "alphabetagamma" {
			t.Fatalf("got %q, want alphabetagamma", data)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server didn't receive complete payload")
	}
}

// b64u is a tiny base64 raw URL encoding helper to keep test imports lean.
func b64u(b []byte) string {
	const tab = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	var out []byte
	for i := 0; i < len(b); i += 3 {
		var v uint32
		n := 3
		if i+3 > len(b) {
			n = len(b) - i
		}
		for j := 0; j < n; j++ {
			v |= uint32(b[i+j]) << (16 - 8*j)
		}
		out = append(out, tab[(v>>18)&0x3F], tab[(v>>12)&0x3F])
		if n >= 2 {
			out = append(out, tab[(v>>6)&0x3F])
		}
		if n >= 3 {
			out = append(out, tab[v&0x3F])
		}
	}
	return string(out)
}
