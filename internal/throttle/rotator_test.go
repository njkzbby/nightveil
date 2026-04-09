package throttle

import (
	"sync"
	"testing"
	"time"
)

func TestRotatorInitialParams(t *testing.T) {
	r := NewRotator(
		RotatorConfig{ParamRotateInterval: time.Hour, SessionRotateInterval: time.Hour},
		DefaultRanges(),
		nil,
	)

	p := r.GetParams()
	if p.PathPrefix == "" || p.UploadPath == "" || p.DownloadPath == "" {
		t.Fatal("initial params should have non-empty paths")
	}
	if p.PaddingMin < 0 || p.PaddingMax < p.PaddingMin {
		t.Fatalf("bad padding range: %d-%d", p.PaddingMin, p.PaddingMax)
	}
	if p.ChunkSize < 8192 || p.ChunkSize > 14336 {
		t.Fatalf("chunk size out of range: %d", p.ChunkSize)
	}
}

func TestRotatorParamRotation(t *testing.T) {
	r := NewRotator(
		RotatorConfig{
			ParamRotateInterval:   100 * time.Millisecond,
			SessionRotateInterval: time.Hour,
		},
		DefaultRanges(),
		nil,
	)

	initial := r.GetParams()
	r.Start()
	defer r.Stop()

	// Wait for at least one rotation
	time.Sleep(250 * time.Millisecond)

	rotated := r.GetParams()
	// Paths should be same (level 1 doesn't rotate paths)
	if rotated.PathPrefix != initial.PathPrefix {
		t.Fatal("level 1 rotation should not change paths")
	}
	// Numeric params should likely differ (not guaranteed but statistically near-certain)
	// We can't assert they're different since random could produce same value
	// Just verify they're in valid ranges
	if rotated.ChunkSize < 8192 || rotated.ChunkSize > 14336 {
		t.Fatalf("rotated chunk size out of range: %d", rotated.ChunkSize)
	}
}

func TestRotatorSessionRotation(t *testing.T) {
	r := NewRotator(
		RotatorConfig{
			ParamRotateInterval:   time.Hour,
			SessionRotateInterval: 100 * time.Millisecond,
		},
		DefaultRanges(),
		nil,
	)

	initial := r.GetParams()

	var mu sync.Mutex
	var callbackParams *LiveParams

	r.OnSessionRotate(func(p LiveParams) {
		mu.Lock()
		callbackParams = &p
		mu.Unlock()
	})

	r.Start()
	defer r.Stop()

	time.Sleep(250 * time.Millisecond)

	rotated := r.GetParams()
	// Level 2 rotates EVERYTHING including paths
	// Statistically they should differ (6-char random, collision = 1/2B)
	if rotated.PathPrefix == initial.PathPrefix &&
		rotated.UploadPath == initial.UploadPath &&
		rotated.SessionKey == initial.SessionKey {
		t.Fatal("session rotation should change paths (extremely unlikely collision)")
	}

	mu.Lock()
	if callbackParams == nil {
		mu.Unlock()
		t.Fatal("session rotate callback was not called")
	}
	mu.Unlock()
}

func TestRotatorEmergency(t *testing.T) {
	d := NewDetector(DetectorConfig{
		RTTSpikeMs:   100,
		ConfirmCount: 2,
		WindowSize:   20,
	})

	a := NewAdaptive(AdaptiveConfig{
		BaseParallelConns: 1,
		MaxParallelConns:  4,
		Strategy:          StrategyBoth,
	}, d)

	r := NewRotator(
		RotatorConfig{
			ParamRotateInterval:   time.Hour,
			SessionRotateInterval: time.Hour,
			EmergencyRotate:       true,
		},
		DefaultRanges(),
		a,
	)

	initial := r.GetParams()

	var mu sync.Mutex
	emergencyCalled := false
	r.OnSessionRotate(func(p LiveParams) {
		mu.Lock()
		emergencyCalled = true
		mu.Unlock()
	})

	// Baseline
	for i := 0; i < 5; i++ {
		d.RecordRTT(30 * time.Millisecond)
	}

	// Trigger throttling → emergency rotation
	for i := 0; i < 3; i++ {
		d.RecordRTT(500 * time.Millisecond)
	}

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	called := emergencyCalled
	mu.Unlock()

	if !called {
		t.Fatal("emergency rotation callback should have been called")
	}

	rotated := r.GetParams()
	if rotated.PathPrefix == initial.PathPrefix &&
		rotated.UploadPath == initial.UploadPath {
		t.Fatal("emergency rotation should change all params")
	}
}

func TestRotatorCallbackOrder(t *testing.T) {
	r := NewRotator(
		RotatorConfig{
			ParamRotateInterval:   50 * time.Millisecond,
			SessionRotateInterval: time.Hour,
		},
		DefaultRanges(),
		nil,
	)

	var mu sync.Mutex
	count := 0

	r.OnParamRotate(func(p LiveParams) {
		mu.Lock()
		count++
		mu.Unlock()
	})

	r.Start()
	defer r.Stop()

	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	if count < 2 {
		mu.Unlock()
		t.Fatalf("expected at least 2 param rotations, got %d", count)
	}
	mu.Unlock()
}
