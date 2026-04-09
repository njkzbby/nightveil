package cover

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestConfigDefaults(t *testing.T) {
	cfg := Config{}
	cfg.defaults()

	if len(cfg.Endpoints) == 0 {
		t.Error("should have default endpoints")
	}
	if cfg.IntervalMinMs != 2000 {
		t.Errorf("IntervalMinMs: %d", cfg.IntervalMinMs)
	}
	if cfg.IntervalMaxMs != 10000 {
		t.Errorf("IntervalMaxMs: %d", cfg.IntervalMaxMs)
	}
	if cfg.PayloadMinBytes != 100 {
		t.Errorf("PayloadMinBytes: %d", cfg.PayloadMinBytes)
	}
}

func TestGeneratorDisabled(t *testing.T) {
	g := NewGenerator(Config{Enabled: false}, nil)
	g.Start() // should be no-op
	time.Sleep(50 * time.Millisecond)
	g.Stop()

	stats := g.GetStats()
	if stats.RequestsSent != 0 {
		t.Fatalf("disabled generator sent %d requests", stats.RequestsSent)
	}
}

func TestGeneratorSendsRequests(t *testing.T) {
	var count atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.WriteHeader(204)
	}))
	defer srv.Close()

	g := NewGenerator(Config{
		Enabled:       true,
		Endpoints:     []string{srv.URL},
		IntervalMinMs: 50,
		IntervalMaxMs: 100,
	}, srv.Client())

	g.Start()
	time.Sleep(350 * time.Millisecond)
	g.Stop()

	if count.Load() < 2 {
		t.Fatalf("expected at least 2 requests, got %d", count.Load())
	}
}

func TestGeneratorStats(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("response body data"))
	}))
	defer srv.Close()

	g := NewGenerator(Config{
		Enabled:       true,
		Endpoints:     []string{srv.URL},
		IntervalMinMs: 30,
		IntervalMaxMs: 50,
	}, srv.Client())

	g.Start()
	time.Sleep(200 * time.Millisecond)
	g.Stop()

	stats := g.GetStats()
	if stats.RequestsSent == 0 {
		t.Error("expected some requests sent")
	}
	if stats.BytesSent == 0 {
		t.Error("expected some bytes sent")
	}
}

func TestGeneratorStopIsClean(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	}))
	defer srv.Close()

	g := NewGenerator(Config{
		Enabled:       true,
		Endpoints:     []string{srv.URL},
		IntervalMinMs: 10,
		IntervalMaxMs: 20,
	}, srv.Client())

	g.Start()
	time.Sleep(50 * time.Millisecond)
	g.Stop()

	// After stop, no more requests should be sent
	statsBefore := g.GetStats()
	time.Sleep(100 * time.Millisecond)
	statsAfter := g.GetStats()

	if statsAfter.RequestsSent > statsBefore.RequestsSent+1 {
		t.Error("generator continued sending after stop")
	}
}

func TestGeneratorHandlesErrors(t *testing.T) {
	// Endpoint that always fails
	g := NewGenerator(Config{
		Enabled:       true,
		Endpoints:     []string{"http://192.0.2.1:1"}, // unreachable
		IntervalMinMs: 30,
		IntervalMaxMs: 50,
	}, &http.Client{Timeout: 100 * time.Millisecond})

	g.Start()
	time.Sleep(200 * time.Millisecond)
	g.Stop()

	stats := g.GetStats()
	if stats.Errors == 0 {
		t.Error("expected some errors for unreachable endpoint")
	}
}

func TestGeneratorMultipleEndpoints(t *testing.T) {
	var ep1Count, ep2Count atomic.Int64

	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ep1Count.Add(1)
		w.WriteHeader(204)
	}))
	defer srv1.Close()

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ep2Count.Add(1)
		w.WriteHeader(204)
	}))
	defer srv2.Close()

	g := NewGenerator(Config{
		Enabled:       true,
		Endpoints:     []string{srv1.URL, srv2.URL},
		IntervalMinMs: 20,
		IntervalMaxMs: 40,
	}, nil) // default client works for httptest

	g.Start()
	time.Sleep(300 * time.Millisecond)
	g.Stop()

	// Both endpoints should have been hit (statistically)
	total := ep1Count.Load() + ep2Count.Load()
	if total < 3 {
		t.Fatalf("expected at least 3 total requests, got %d", total)
	}
}
