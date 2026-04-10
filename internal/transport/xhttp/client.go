package xhttp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/njkzbby/nightveil/internal/crypto/auth"
	"github.com/njkzbby/nightveil/internal/transport"
)

// Client implements transport.ClientTransport over XHTTP.
type Client struct {
	ServerURL  string // e.g. "http://127.0.0.1:8080" or "https://domain.com"
	HTTPClient *http.Client
	Config     Config
	Auth       auth.ClientAuth
}

// NewClient creates an XHTTP client transport.
func NewClient(serverURL string, cfg Config, authenticator auth.ClientAuth, httpClient *http.Client) *Client {
	cfg.defaults()
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		ServerURL:  serverURL,
		HTTPClient: httpClient,
		Config:     cfg,
		Auth:       authenticator,
	}
}

// Dial establishes a new XHTTP tunnel connection.
func (c *Client) Dial(ctx context.Context, sessionID [16]byte) (transport.Conn, error) {
	// Generate auth token
	token, err := c.Auth.GenerateToken(sessionID)
	if err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}
	tokenB64 := base64.RawURLEncoding.EncodeToString(token)
	sessionB64 := base64.RawURLEncoding.EncodeToString(sessionID[:])

	conn := &clientConn{
		client:     c,
		sessionID:  sessionID,
		tokenB64:   tokenB64,
		sessionB64: sessionB64,
		ctx:        ctx,
		uploadMode: resolveUploadMode(c.Config.UploadMode, c.HTTPClient),
		closed:     make(chan struct{}),
	}

	// Start download (GET) in background
	if err := conn.startDownload(); err != nil {
		return nil, fmt.Errorf("start download: %w", err)
	}

	// In stream-up mode, also start the long-lived upload POST. The fast path
	// in Write() then writes straight into the request body via io.Pipe.
	if conn.uploadMode == uploadModeStream {
		if err := conn.startStreamUpload(); err != nil {
			conn.Close()
			return nil, fmt.Errorf("start stream upload: %w", err)
		}
	}

	return conn, nil
}

// Close shuts down the client transport.
func (c *Client) Close() error { return nil }

// --- clientConn ---

const (
	uploadModePacket = "packet"
	uploadModeStream = "stream"
)

type clientConn struct {
	client     *Client
	sessionID  [16]byte
	tokenB64   string
	sessionB64 string
	ctx        context.Context

	// Upload mode resolved at Dial time. Either uploadModePacket (POST per
	// chunk with sequence numbers) or uploadModeStream (single long-lived
	// POST with chunked transfer encoding via io.Pipe).
	uploadMode string

	// Packet-up state
	uploadSeq atomic.Int64

	// Stream-up state. streamWriter is the producer end of the io.Pipe whose
	// consumer is the long-lived POST request body. Only set when
	// uploadMode == uploadModeStream.
	streamWriter *io.PipeWriter

	// Download
	downloadBody   io.ReadCloser
	downloadOffset atomic.Int64 // bytes successfully read from download

	// Broken-conn state. When any pipelined POST in Write fails, the entire
	// connection is marked broken so subsequent Read/Write return the same
	// error promptly instead of stalling on a missing sequence number on the
	// server side.
	brokenMu   sync.Mutex
	brokenErr  error
	brokenOnce sync.Once

	// Close
	closed    chan struct{}
	closeOnce sync.Once
}

// resolveUploadMode picks the actual upload mode for a Dial based on the
// configured preference and a heuristic on the underlying HTTP transport.
//
//   - "stream" / "packet" → honored verbatim (explicit override).
//   - "auto" / "" → stream when the http.Client is configured with an
//     HTTP/2 transport (golang.org/x/net/http2.Transport — what
//     internal/security.NewUTLSHTTPClient returns), packet otherwise.
//     Plain http.DefaultClient or stdlib *http.Transport falls back to
//     packet for safety, since CDNs are sometimes unhappy with one
//     extremely long-lived POST.
func resolveUploadMode(configured string, httpClient *http.Client) string {
	switch configured {
	case uploadModeStream:
		return uploadModeStream
	case uploadModePacket:
		return uploadModePacket
	}
	// "auto" or empty
	if httpClient != nil && isHTTP2Transport(httpClient.Transport) {
		return uploadModeStream
	}
	return uploadModePacket
}

// isHTTP2Transport checks whether t is the golang.org/x/net/http2.Transport
// type used by NewUTLSHTTPClient. Done via reflection-style fmt.Sprintf to
// avoid pulling http2 as a hard dependency of this package.
func isHTTP2Transport(t http.RoundTripper) bool {
	if t == nil {
		return false
	}
	// %T renders the fully qualified type name; we just match on suffix.
	typeName := fmt.Sprintf("%T", t)
	return typeName == "*http2.Transport"
}

// markBroken records the first error that broke the conn and triggers Close.
// Idempotent — subsequent calls are no-ops.
func (c *clientConn) markBroken(err error) {
	c.brokenOnce.Do(func() {
		c.brokenMu.Lock()
		c.brokenErr = err
		c.brokenMu.Unlock()
		c.Close()
	})
}

// isBroken returns the recorded error or nil.
func (c *clientConn) isBroken() error {
	c.brokenMu.Lock()
	defer c.brokenMu.Unlock()
	return c.brokenErr
}

// startStreamUpload opens the long-lived upload POST and stores the producer
// end of the io.Pipe on the conn. The HTTP request body is the consumer end
// — the http.Client.Do call drives it on a background goroutine until the
// pipe writer is closed.
func (c *clientConn) startStreamUpload() error {
	pr, pw := io.Pipe()

	url := c.client.ServerURL + c.client.Config.fullUploadPath() +
		"?" + c.client.Config.SessionKeyName + "=" + c.sessionB64

	req, err := http.NewRequestWithContext(c.ctx, "POST", url, pr)
	if err != nil {
		pr.Close()
		pw.Close()
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-NV-Mode", "stream")
	// Force chunked transfer encoding — without an explicit Content-Length
	// the http client uses chunked over HTTP/1.1, and over HTTP/2 it streams
	// frames as they come.
	req.ContentLength = -1
	c.addAuth(req)

	c.streamWriter = pw

	go func() {
		defer pr.Close()
		resp, err := c.client.HTTPClient.Do(req)
		if err != nil {
			c.markBroken(fmt.Errorf("stream upload do: %w", err))
			pw.CloseWithError(err)
			return
		}
		defer resp.Body.Close()
		// Drain to allow connection reuse.
		io.Copy(io.Discard, resp.Body)
		if resp.StatusCode != http.StatusOK {
			err := fmt.Errorf("stream upload: status %d", resp.StatusCode)
			c.markBroken(err)
			pw.CloseWithError(err)
		}
	}()

	return nil
}

func (c *clientConn) startDownload() error {
	offset := c.downloadOffset.Load()
	url := c.client.ServerURL + c.client.Config.fullDownloadPath() +
		"?" + c.client.Config.SessionKeyName + "=" + c.sessionB64 +
		"&off=" + strconv.FormatInt(offset, 10)

	req, err := http.NewRequestWithContext(c.ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	c.addAuth(req)

	resp, err := c.client.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return fmt.Errorf("download: status %d", resp.StatusCode)
	}

	c.downloadBody = resp.Body
	return nil
}

// Read reads from the download stream (GET response body).
// If the stream closes prematurely, it attempts to reconnect.
func (c *clientConn) Read(p []byte) (int, error) {
	if err := c.isBroken(); err != nil {
		return 0, err
	}
	for retries := 0; retries < 3; retries++ {
		if c.downloadBody == nil {
			if err := c.startDownload(); err != nil {
				if retries < 2 {
					// Randomized retry delay to avoid detectable patterns
					jitter, _ := rand.Int(rand.Reader, big.NewInt(200))
					delay := 50 + jitter.Int64() + int64(retries)*100
					time.Sleep(time.Duration(delay) * time.Millisecond)
					continue
				}
				return 0, fmt.Errorf("download reconnect failed: %w", err)
			}
		}

		n, err := c.downloadBody.Read(p)
		if n > 0 {
			c.downloadOffset.Add(int64(n))
			return n, nil
		}
		if err != nil {
			// Stream closed — try to reconnect
			c.downloadBody.Close()
			c.downloadBody = nil
			if retries < 2 {
				continue
			}
			return 0, err
		}
	}
	return 0, io.EOF
}

// Write sends data through the upload channel.
//
// In stream-up mode it writes straight into the io.Pipe feeding the
// long-lived POST request body — no chunking, no sequence numbers, no
// goroutines per call. The TLS layer below shapes the wire stream into
// ≤16 KiB records on its own.
//
// In packet-up mode it splits into chunks and sends concurrently — up to
// 8 POSTs in flight simultaneously, server reassembles in order using
// sequence numbers.
func (c *clientConn) Write(p []byte) (int, error) {
	if err := c.isBroken(); err != nil {
		return 0, err
	}
	if c.uploadMode == uploadModeStream {
		n, err := c.streamWriter.Write(p)
		if err != nil {
			c.markBroken(err)
		}
		return n, err
	}
	maxChunk := c.client.Config.MaxChunkSize
	if maxChunk <= 0 {
		maxChunk = 65536
	}

	// Single chunk — fast path, no goroutine overhead
	if len(p) <= maxChunk {
		if err := c.postChunk(p); err != nil {
			c.markBroken(err)
			return 0, err
		}
		return len(p), nil
	}

	// Multiple chunks — pipeline with semaphore
	// Pre-assign sequence numbers to maintain order
	type seqChunk struct {
		seq  int64
		data []byte
	}

	var chunks []seqChunk
	for i := 0; i < len(p); i += maxChunk {
		end := i + maxChunk
		if end > len(p) {
			end = len(p)
		}
		seq := c.uploadSeq.Add(1) - 1
		cp := make([]byte, end-i)
		copy(cp, p[i:end])
		chunks = append(chunks, seqChunk{seq: seq, data: cp})
	}

	// Send concurrently (max 8 in flight)
	sem := make(chan struct{}, 8)
	errCh := make(chan error, len(chunks))

	for _, ch := range chunks {
		sem <- struct{}{}
		go func(s int64, data []byte) {
			defer func() { <-sem }()
			errCh <- c.postChunkWithSeq(data, s)
		}(ch.seq, ch.data)
	}

	var firstErr error
	for range chunks {
		if err := <-errCh; err != nil && firstErr == nil {
			firstErr = err
		}
	}

	if firstErr != nil {
		// Any pipeline error means the server has a permanent gap in its
		// reassembly queue (some chunks landed, others didn't). Mark the
		// whole conn broken so subsequent Read/Write fail fast instead of
		// the relay deadlocking on a hopeless state.
		c.markBroken(firstErr)
		return 0, firstErr
	}
	return len(p), nil
}

func (c *clientConn) postChunk(data []byte) error {
	seq := c.uploadSeq.Add(1) - 1
	return c.postChunkWithSeq(data, seq)
}

func (c *clientConn) postChunkWithSeq(data []byte, seq int64) error {
	url := c.client.ServerURL + c.client.Config.fullUploadPath() +
		"?seq=" + strconv.FormatInt(seq, 10) +
		"&" + c.client.Config.SessionKeyName + "=" + c.sessionB64

	req, err := http.NewRequestWithContext(c.ctx, "POST", url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	c.addAuth(req)

	resp, err := c.client.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("upload: status %d", resp.StatusCode)
	}
	return nil
}

func (c *clientConn) addAuth(req *http.Request) {
	req.AddCookie(&http.Cookie{
		Name:  "nv_token",
		Value: c.tokenB64,
	})
	// HTTP-level padding: random header to vary request size
	padLen := 32
	diff := 128 - 32
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(diff+1)))
	padLen += int(n.Int64())
	padBytes := make([]byte, padLen)
	rand.Read(padBytes)
	req.Header.Set("X-Request-ID", base64.RawStdEncoding.EncodeToString(padBytes))
}

func (c *clientConn) Close() error {
	c.closeOnce.Do(func() {
		close(c.closed)
		if c.streamWriter != nil {
			// Closing the producer end signals EOF to the long-lived POST,
			// which lets http.Client.Do return and the goroutine exit.
			c.streamWriter.Close()
		}
		if c.downloadBody != nil {
			c.downloadBody.Close()
		}
	})
	return nil
}

func (c *clientConn) LocalAddr() net.Addr  { return &dummyAddr{"tcp", "xhttp-client"} }
func (c *clientConn) RemoteAddr() net.Addr { return &dummyAddr{"tcp", c.client.ServerURL} }
