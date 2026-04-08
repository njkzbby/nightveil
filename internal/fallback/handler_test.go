package fallback

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDefaultFallback(t *testing.T) {
	h, err := New("default", "", "")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "It works!") {
		t.Fatalf("body: %q", body)
	}

	if resp.Header.Get("Server") != "nginx" {
		t.Fatalf("server header: %q", resp.Header.Get("Server"))
	}
}

func TestReverseProxyFallback(t *testing.T) {
	// Start upstream server
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("upstream response"))
	}))
	defer upstream.Close()

	h, err := New("reverse_proxy", upstream.URL, "")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/page", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	body, _ := io.ReadAll(w.Result().Body)
	if string(body) != "upstream response" {
		t.Fatalf("got %q", body)
	}
}

func TestStaticFallback(t *testing.T) {
	h, err := New("static", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// Should return something (even 404 for empty dir is fine — it's serving)
	if w.Result().StatusCode == 0 {
		t.Fatal("no response")
	}
}

func TestInvalidMode(t *testing.T) {
	h, err := New("something_weird", "", "")
	if err != nil {
		t.Fatal(err)
	}
	// Falls back to default
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Result().StatusCode != 200 {
		t.Fatal("should default to 200")
	}
}
