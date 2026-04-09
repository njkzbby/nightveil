package xhttp

import (
	"context"
	"io"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/nightveil/nv/internal/crypto/auth"
	"github.com/nightveil/nv/internal/session"
	"github.com/nightveil/nv/internal/transport"
)

// Server implements transport.ServerTransport over XHTTP.
type Server struct {
	config   Config
	auth     auth.ServerAuth
	sessions *session.Manager
	fallback http.Handler
	acceptCh chan transport.Conn

	// Track which sessions have been sent to acceptCh
	accepted   map[[16]byte]bool
	hasUpload  map[[16]byte]bool
	hasDownload map[[16]byte]bool
	acceptedMu sync.Mutex
}

// NewServer creates an XHTTP server transport.
func NewServer(cfg Config, authenticator auth.ServerAuth, fallback http.Handler) *Server {
	cfg.defaults()
	return &Server{
		config:   cfg,
		auth:     authenticator,
		sessions: session.NewManager(time.Duration(cfg.SessionTimeout) * time.Second),
		fallback: fallback,
		acceptCh:    make(chan transport.Conn, 64),
		accepted:    make(map[[16]byte]bool),
		hasUpload:   make(map[[16]byte]bool),
		hasDownload: make(map[[16]byte]bool),
	}
}

// Accept blocks until a fully-connected session is available.
func (s *Server) Accept(ctx context.Context) (transport.Conn, error) {
	select {
	case conn, ok := <-s.acceptCh:
		if !ok {
			return nil, context.Canceled
		}
		return conn, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close shuts down the server transport.
func (s *Server) Close() error {
	close(s.acceptCh)
	return nil
}

// ServeHTTP routes incoming HTTP requests.
// Server doesn't know per-client paths — it authenticates first,
// then routes by HTTP method: POST=upload, GET=download.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Try to authenticate first — if auth fails, serve fallback
	sessionID, err := s.auth.Validate(r.Context(), r)
	if err != nil {
		// Not authenticated → serve real website (anti-probing)
		s.fallback.ServeHTTP(w, r)
		return
	}

	log.Printf("[xhttp] auth OK: %s %s session=%x", r.Method, r.URL.Path, sessionID[:4])

	// Authenticated — route by method
	switch r.Method {
	case http.MethodPost:
		s.handleUpload(w, r, sessionID)
	case http.MethodGet:
		s.handleDownload(w, r, sessionID)
	default:
		s.fallback.ServeHTTP(w, r)
	}
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request, sessionID [16]byte) {
	seqStr := r.URL.Query().Get("seq")
	seq, err := strconv.ParseInt(seqStr, 10, 64)
	if err != nil {
		http.Error(w, "bad seq", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, int64(s.config.MaxChunkSize)+1024))
	if err != nil {
		http.Error(w, "read error", http.StatusInternalServerError)
		return
	}

	sess := s.sessions.GetOrCreate(sessionID)
	sess.PushUpload(seq, body)

	s.acceptedMu.Lock()
	s.hasUpload[sessionID] = true
	s.acceptedMu.Unlock()

	s.tryEmit(sess, sessionID, r.RemoteAddr)

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request, sessionID [16]byte) {
	sess := s.sessions.GetOrCreate(sessionID)

	s.acceptedMu.Lock()
	s.hasDownload[sessionID] = true
	s.acceptedMu.Unlock()

	// Mark connected and try to emit
	sess.MarkConnected()
	s.tryEmit(sess, sessionID, r.RemoteAddr)

	// Stream — flush headers so client unblocks
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	flusher, canFlush := w.(http.Flusher)
	if canFlush {
		flusher.Flush()
	}

	// Client sends offset of bytes already received (&off=N).
	// On reconnect, resume from that offset — zero data loss, zero duplicates.
	offset := 0
	if offStr := r.URL.Query().Get("off"); offStr != "" {
		if v, err := strconv.Atoi(offStr); err == nil && v > 0 {
			offset = v
		}
	}

	buf := make([]byte, 32768)

	for {
		// Try to read available data
		n, newOffset, err := sess.DownloadBuf.ReadFrom(offset, buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return // GET broke — data safe in buffer, next GET resumes
			}
			if canFlush {
				flusher.Flush()
			}
			offset = newOffset
			continue // check for more data immediately
		}
		if err != nil {
			return // session closed
		}

		// No data yet — wait for notification or context cancel
		select {
		case <-sess.DownloadBuf.Notify():
			// New data available — loop back to ReadFrom
		case <-r.Context().Done():
			return // HTTP request cancelled
		}
	}
}

// tryEmit sends a new splitConn to acceptCh exactly once per session.
func (s *Server) tryEmit(sess *session.Session, id [16]byte, remoteAddr string) {
	s.acceptedMu.Lock()
	if s.accepted[id] {
		s.acceptedMu.Unlock()
		return
	}

	// Download GET is sufficient to emit — upload POST will feed data later.
	// Previously required both, causing streaming (SSE) to stall until first POST.
	if !s.hasDownload[id] {
		s.acceptedMu.Unlock()
		return
	}

	s.accepted[id] = true
	s.acceptedMu.Unlock()

	conn := newSplitConn(
		sess,
		&dummyAddr{"tcp", "xhttp-server"},
		&dummyAddr{"tcp", remoteAddr},
	)

	// Non-blocking send — if acceptCh is full, drop (shouldn't happen with buffer=64)
	select {
	case s.acceptCh <- conn:
	default:
	}
}
