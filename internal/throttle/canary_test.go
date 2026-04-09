package throttle

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestCanaryStateString(t *testing.T) {
	tests := []struct {
		state CanaryState
		want  string
	}{
		{CanaryOK, "ok"},
		{CanaryDegraded, "degraded"},
		{CanaryBlocked, "blocked"},
		{CanaryState(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("CanaryState(%d) = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestCanaryAllEndpointsOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	}))
	defer srv.Close()

	c := NewCanary(CanaryConfig{
		Endpoints:     []string{srv.URL, srv.URL},
		Interval:      50 * time.Millisecond,
		Timeout:       5 * time.Second,
		FailThreshold: 2,
	}, srv.Client(), nil)

	c.Start()
	defer c.Stop()

	time.Sleep(100 * time.Millisecond)

	if c.GetState() != CanaryOK {
		t.Fatalf("expected OK, got %s", c.GetState())
	}
}

func TestCanaryAllEndpointsFail(t *testing.T) {
	// Server that always returns 500
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	c := NewCanary(CanaryConfig{
		Endpoints:     []string{srv.URL},
		Interval:      50 * time.Millisecond,
		Timeout:       5 * time.Second,
		FailThreshold: 2,
	}, srv.Client(), nil)

	c.Start()
	defer c.Stop()

	time.Sleep(200 * time.Millisecond)

	state := c.GetState()
	if state != CanaryBlocked {
		t.Fatalf("expected blocked, got %s", state)
	}
}

func TestCanaryCallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	c := NewCanary(CanaryConfig{
		Endpoints:     []string{srv.URL},
		Interval:      50 * time.Millisecond,
		Timeout:       5 * time.Second,
		FailThreshold: 2,
	}, srv.Client(), nil)

	var mu sync.Mutex
	var states []CanaryState
	c.OnStateChange(func(s CanaryState) {
		mu.Lock()
		states = append(states, s)
		mu.Unlock()
	})

	c.Start()
	defer c.Stop()

	time.Sleep(250 * time.Millisecond)

	mu.Lock()
	if len(states) == 0 {
		mu.Unlock()
		t.Fatal("expected at least one state change callback")
	}
	mu.Unlock()
}

func TestCanaryFeedsDetector(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	}))
	defer srv.Close()

	detector := NewDetector(DetectorConfig{})

	c := NewCanary(CanaryConfig{
		Endpoints: []string{srv.URL},
		Interval:  50 * time.Millisecond,
		Timeout:   5 * time.Second,
	}, srv.Client(), detector)

	c.Start()
	defer c.Stop()

	time.Sleep(150 * time.Millisecond)

	// Detector should have received RTT samples
	if detector.GetState() != StateNormal {
		t.Fatalf("detector should be normal, got %s", detector.GetState())
	}
}

func TestCanaryDefaults(t *testing.T) {
	cfg := CanaryConfig{}
	cfg.defaults()

	if len(cfg.Endpoints) == 0 {
		t.Error("should have default endpoints")
	}
	if cfg.Interval <= 0 {
		t.Error("should have default interval")
	}
	if cfg.Timeout <= 0 {
		t.Error("should have default timeout")
	}
	if cfg.FailThreshold <= 0 {
		t.Error("should have default fail threshold")
	}
}

func TestCanaryUnreachableEndpoint(t *testing.T) {
	c := NewCanary(CanaryConfig{
		Endpoints:     []string{"http://192.0.2.1:1"}, // RFC 5737 — guaranteed unreachable
		Interval:      100 * time.Millisecond,
		Timeout:       200 * time.Millisecond,
		FailThreshold: 2,
	}, &http.Client{Timeout: 200 * time.Millisecond}, nil)

	c.Start()
	defer c.Stop()

	// Wait enough for initial probe + 2 more (3 probes total, fail threshold = 2)
	time.Sleep(500 * time.Millisecond)

	state := c.GetState()
	if state == CanaryOK {
		t.Fatalf("should not be OK with unreachable endpoint, got %s", state)
	}
}
