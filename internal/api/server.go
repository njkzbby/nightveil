// Package api implements the HTTP JSON management API.
// Exposes status, control, and metrics endpoints on a local-only port.
package api

import (
	"encoding/json"
	"net/http"
	"sync"
)

// HTTPServer serves the management API.
type HTTPServer struct {
	mux    *http.ServeMux
	secret string

	mu       sync.RWMutex
	status   *Status       // client status, updated externally
	srvStatus *ServerStatus // server status, updated externally
}

// NewHTTPServer creates the API server.
func NewHTTPServer(secret string) *HTTPServer {
	s := &HTTPServer{
		mux:    http.NewServeMux(),
		secret: secret,
		status: &Status{},
	}

	s.mux.HandleFunc("GET /api/status", s.authMiddleware(s.handleStatus))
	s.mux.HandleFunc("POST /api/connect", s.authMiddleware(s.handleConnect))
	s.mux.HandleFunc("POST /api/disconnect", s.authMiddleware(s.handleDisconnect))
	s.mux.HandleFunc("POST /api/reconnect", s.authMiddleware(s.handleReconnect))

	return s
}

// Handler returns the HTTP handler.
func (s *HTTPServer) Handler() http.Handler { return s.mux }

// UpdateStatus sets the current status (thread-safe).
func (s *HTTPServer) UpdateStatus(status *Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
}

// SetConnectHandler sets the callback for connect requests.
var connectHandler func() error
var disconnectHandler func() error
var reconnectHandler func() error

// SetHandlers configures action handlers.
func (s *HTTPServer) SetHandlers(connect, disconnect, reconnect func() error) {
	connectHandler = connect
	disconnectHandler = disconnect
	reconnectHandler = reconnect
}

func (s *HTTPServer) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.secret != "" {
			token := r.Header.Get("Authorization")
			if token != "Bearer "+s.secret {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next(w, r)
	}
}

func (s *HTTPServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	status := s.status
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (s *HTTPServer) handleConnect(w http.ResponseWriter, r *http.Request) {
	if connectHandler == nil {
		http.Error(w, "not implemented", http.StatusNotImplemented)
		return
	}
	if err := connectHandler(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *HTTPServer) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	if disconnectHandler == nil {
		http.Error(w, "not implemented", http.StatusNotImplemented)
		return
	}
	if err := disconnectHandler(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *HTTPServer) handleReconnect(w http.ResponseWriter, r *http.Request) {
	if reconnectHandler == nil {
		http.Error(w, "not implemented", http.StatusNotImplemented)
		return
	}
	if err := reconnectHandler(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}
