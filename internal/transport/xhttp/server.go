package xhttp

import (
	"context"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/njkzbby/nightveil/internal/crypto/auth"
	"github.com/njkzbby/nightveil/internal/session"
	"github.com/njkzbby/nightveil/internal/transport"
)

// Server implements transport.ServerTransport over XHTTP.
type Server struct {
	config   Config
	auth     auth.ServerAuth
	sessions *session.Manager
	fallback http.Handler
	acceptCh chan transport.Conn
}

// NewServer creates an XHTTP server transport.
func NewServer(cfg Config, authenticator auth.ServerAuth, fallback http.Handler) *Server {
	cfg.defaults()
	mgr := session.NewManager(time.Duration(cfg.SessionTimeout) * time.Second)
	mgr.DownloadBufferBytes = cfg.DownloadBufferBytes
	return &Server{
		config:   cfg,
		auth:     authenticator,
		sessions: mgr,
		fallback: fallback,
		acceptCh: make(chan transport.Conn, 64),
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
	// Stream-up mode: a single long-lived POST whose body is the entire
	// upload stream until the client closes. Routed by an explicit header
	// so old packet-up clients keep working untouched.
	if r.Header.Get("X-NV-Mode") == "stream" {
		s.handleUploadStream(w, r, sessionID)
		return
	}

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
	sess.HasUpload.Store(true)

	s.tryEmit(sess, r.RemoteAddr)

	w.WriteHeader(http.StatusOK)
}

// handleUploadStream serves a single long-lived POST whose body is the
// session's entire upload stream. Bytes are pumped straight into the
// session's stream pipe with io.Copy — no chunking, no sequence numbers,
// no per-chunk ACKs. The TLS layer below already chunks the wire stream
// into ≤16 KiB records, so TSPU thresholds are not violated.
func (s *Server) handleUploadStream(w http.ResponseWriter, r *http.Request, sessionID [16]byte) {
	sess := s.sessions.GetOrCreate(sessionID)
	sess.HasUpload.Store(true)
	// Stream mode allows the consumer (splitConn) to start reading even
	// before the first byte arrives — emit immediately if a download GET
	// has already arrived (or arrives later).
	s.tryEmit(sess, r.RemoteAddr)

	writer := sess.StreamUploadWriter()

	// Copy until the client closes the request body. Buffer size is matched
	// to the existing splitConn read path. io.Copy returns nil on clean EOF
	// from r.Body — that's normal end-of-session.
	buf := make([]byte, 32*1024)
	if _, err := io.CopyBuffer(writer, r.Body, buf); err != nil {
		// Surface the error to the consumer via pipe close.
		log.Printf("[xhttp] stream upload copy: session=%x err=%v", sessionID[:4], err)
	}
	sess.CloseStreamUpload()

	// Respond after the body is drained — many HTTP/2 clients don't read
	// the response until they're done writing the request, so this is just
	// best-effort acknowledgement.
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request, sessionID [16]byte) {
	sess := s.sessions.GetOrCreate(sessionID)
	sess.HasDownload.Store(true)

	// Mark connected and try to emit
	sess.MarkConnected()
	s.tryEmit(sess, r.RemoteAddr)

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
			// Tell the sliding buffer this offset has been delivered so it
			// can compact older data within the replay window.
			sess.DownloadBuf.Advance(offset)
			continue // check for more data immediately
		}
		if err != nil {
			return // session closed or offset too old
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
// State lives on the Session itself so cleanup happens automatically when
// the session is GC'd — no per-server map to leak.
func (s *Server) tryEmit(sess *session.Session, remoteAddr string) {
	// Download GET is sufficient to emit — upload POST will feed data later.
	// Previously required both, causing streaming (SSE) to stall until first POST.
	if !sess.HasDownload.Load() {
		return
	}
	if !sess.Accepted.CompareAndSwap(false, true) {
		return // already emitted
	}

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
