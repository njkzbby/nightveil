package xhttp

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
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
		acceptCh: make(chan transport.Conn, 64),
		accepted: make(map[[16]byte]bool),
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
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// Match paths: any prefix + our upload/download suffixes
	isUpload := strings.HasSuffix(path, s.config.UploadPath)
	isDownload := strings.HasSuffix(path, s.config.DownloadPath)

	if !isUpload && !isDownload {
		s.fallback.ServeHTTP(w, r)
		return
	}

	// Authenticate
	sessionID, err := s.auth.Validate(r.Context(), r)
	if err != nil {
		s.fallback.ServeHTTP(w, r)
		return
	}

	if isUpload && r.Method == http.MethodPost {
		s.handleUpload(w, r, sessionID)
	} else if isDownload && r.Method == http.MethodGet {
		s.handleDownload(w, r, sessionID)
	} else {
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

	// Try to emit this session as a new connection
	s.tryEmit(sess, sessionID, r.RemoteAddr)

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request, sessionID [16]byte) {
	sess := s.sessions.GetOrCreate(sessionID)

	// Mark download channel present
	sess.MarkConnected()
	s.tryEmit(sess, sessionID, r.RemoteAddr)

	// Stream — flush headers immediately so client's http.Client.Do() returns
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	flusher, canFlush := w.(http.Flusher)
	if canFlush {
		flusher.Flush() // Critical: send headers now so client unblocks
	}
	buf := make([]byte, 32768)

	for {
		n, err := sess.DownloadReader.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				break
			}
			if canFlush {
				flusher.Flush()
			}
		}
		if err != nil {
			break
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

	// Only emit once both upload and download are registered.
	// We mark connected on download, and upload always has data.
	// Check if connected (non-blocking).
	select {
	case <-sess.Connected():
		// Ready
	default:
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
