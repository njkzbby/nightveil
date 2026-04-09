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

	"github.com/nightveil/nv/internal/crypto/auth"
	"github.com/nightveil/nv/internal/transport"
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
		closed:     make(chan struct{}),
	}

	// Start download (GET) in background
	if err := conn.startDownload(); err != nil {
		return nil, fmt.Errorf("start download: %w", err)
	}

	return conn, nil
}

// Close shuts down the client transport.
func (c *Client) Close() error { return nil }

// --- clientConn ---

type clientConn struct {
	client     *Client
	sessionID  [16]byte
	tokenB64   string
	sessionB64 string
	ctx        context.Context

	// Upload
	uploadSeq atomic.Int64

	// Download
	downloadBody   io.ReadCloser
	downloadOffset atomic.Int64 // bytes successfully read from download

	// Close
	closed    chan struct{}
	closeOnce sync.Once
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

// Write sends data as a POST upload chunk.
func (c *clientConn) Write(p []byte) (int, error) {
	maxChunk := c.client.Config.MaxChunkSize
	written := 0

	for written < len(p) {
		end := written + maxChunk
		if end > len(p) {
			end = len(p)
		}
		chunk := p[written:end]

		if err := c.postChunk(chunk); err != nil {
			return written, err
		}
		written = end
	}

	return written, nil
}

func (c *clientConn) postChunk(data []byte) error {
	seq := c.uploadSeq.Add(1) - 1

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
		if c.downloadBody != nil {
			c.downloadBody.Close()
		}
	})
	return nil
}

func (c *clientConn) LocalAddr() net.Addr  { return &dummyAddr{"tcp", "xhttp-client"} }
func (c *clientConn) RemoteAddr() net.Addr { return &dummyAddr{"tcp", c.client.ServerURL} }
