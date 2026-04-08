// Package fallback serves a real website when authentication fails.
// This defeats active probing — censors see a legitimate site.
package fallback

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
)

// Handler serves fallback content for unauthenticated requests.
type Handler struct {
	mode     string
	proxy    *httputil.ReverseProxy
	static   http.Handler
}

// New creates a fallback handler.
// mode: "reverse_proxy" (upstream URL) or "static" (file root).
func New(mode, upstream, staticRoot string) (*Handler, error) {
	h := &Handler{mode: mode}

	switch mode {
	case "reverse_proxy":
		u, err := url.Parse(upstream)
		if err != nil {
			return nil, fmt.Errorf("parse upstream URL %q: %w", upstream, err)
		}
		h.proxy = httputil.NewSingleHostReverseProxy(u)
		// Don't leak internal errors to the client
		h.proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			w.WriteHeader(http.StatusBadGateway)
		}
	case "static":
		if staticRoot == "" {
			return nil, fmt.Errorf("static root required for static mode")
		}
		h.static = http.FileServer(http.Dir(staticRoot))
	default:
		// Default: simple 200 response
		h.mode = "default"
	}

	return h, nil
}

// ServeHTTP serves the fallback content.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch h.mode {
	case "reverse_proxy":
		h.proxy.ServeHTTP(w, r)
	case "static":
		h.static.ServeHTTP(w, r)
	default:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Server", "nginx")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("<!DOCTYPE html><html><head><title>Welcome</title></head><body><h1>It works!</h1></body></html>"))
	}
}
