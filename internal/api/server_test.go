package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAPIStatusEndpoint(t *testing.T) {
	srv := NewHTTPServer("")

	srv.UpdateStatus(&Status{
		Connected:   true,
		ServerAddr:  "example.com:443",
		Transport:   "xhttp",
		Uptime:      5 * time.Minute,
		BytesSent:   1024,
		BytesRecv:   2048,
		ActiveConns: 3,
		Throttled:   false,
		CurrentMode: "stealth",
	})

	req := httptest.NewRequest("GET", "/api/status", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status code: %d", w.Code)
	}

	var status Status
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}

	if !status.Connected {
		t.Error("expected connected=true")
	}
	if status.ServerAddr != "example.com:443" {
		t.Errorf("server: %q", status.ServerAddr)
	}
	if status.BytesSent != 1024 {
		t.Errorf("bytes sent: %d", status.BytesSent)
	}
}

func TestAPIAuthRequired(t *testing.T) {
	srv := NewHTTPServer("my-secret")

	// Without auth
	req := httptest.NewRequest("GET", "/api/status", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d", w.Code)
	}

	// With wrong auth
	req = httptest.NewRequest("GET", "/api/status", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong auth, got %d", w.Code)
	}

	// With correct auth
	req = httptest.NewRequest("GET", "/api/status", nil)
	req.Header.Set("Authorization", "Bearer my-secret")
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200 with correct auth, got %d", w.Code)
	}
}

func TestAPINoAuth(t *testing.T) {
	srv := NewHTTPServer("") // no auth required

	req := httptest.NewRequest("GET", "/api/status", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestAPIConnect(t *testing.T) {
	srv := NewHTTPServer("")

	// Without handler
	req := httptest.NewRequest("POST", "/api/connect", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 without handler, got %d", w.Code)
	}

	// With handler
	srv.SetHandlers(
		func() error { return nil },
		func() error { return nil },
		func() error { return nil },
	)

	req = httptest.NewRequest("POST", "/api/connect", nil)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestAPIReconnect(t *testing.T) {
	srv := NewHTTPServer("")
	srv.SetHandlers(nil, nil, func() error { return nil })

	req := httptest.NewRequest("POST", "/api/reconnect", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}
