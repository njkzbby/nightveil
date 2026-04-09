package throttle

import (
	"testing"
	"time"
)

func TestDetectorStateString(t *testing.T) {
	tests := []struct {
		state State
		want  string
	}{
		{StateNormal, "normal"},
		{StateSuspected, "suspected"},
		{StateThrottled, "throttled"},
		{State(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("State(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestDetectorDefaults(t *testing.T) {
	d := NewDetector(DetectorConfig{})

	if d.config.RTTSpikeMs != 500 {
		t.Errorf("default RTTSpikeMs: %d", d.config.RTTSpikeMs)
	}
	if d.config.ThroughputDropPct != 70 {
		t.Errorf("default ThroughputDropPct: %d", d.config.ThroughputDropPct)
	}
	if d.config.WindowSize != 20 {
		t.Errorf("default WindowSize: %d", d.config.WindowSize)
	}
	if d.config.ConfirmCount != 3 {
		t.Errorf("default ConfirmCount: %d", d.config.ConfirmCount)
	}
}

func TestAdaptiveDefaults(t *testing.T) {
	d := NewDetector(DetectorConfig{})
	a := NewAdaptive(AdaptiveConfig{}, d)

	if a.config.BaseParallelConns != 1 {
		t.Errorf("default BaseParallelConns: %d", a.config.BaseParallelConns)
	}
	if a.config.MaxParallelConns != 8 {
		t.Errorf("default MaxParallelConns: %d", a.config.MaxParallelConns)
	}
	if a.config.Strategy != StrategyBoth {
		t.Errorf("default Strategy: %s", a.config.Strategy)
	}
}

func TestRotatorDefaults(t *testing.T) {
	r := NewRotator(RotatorConfig{}, DefaultRanges(), nil)

	if r.config.ParamRotateInterval != 5*time.Minute {
		t.Errorf("default ParamRotateInterval: %v", r.config.ParamRotateInterval)
	}
	if r.config.SessionRotateInterval != 30*time.Minute {
		t.Errorf("default SessionRotateInterval: %v", r.config.SessionRotateInterval)
	}

	p := r.GetParams()
	if p.PathPrefix == "" {
		t.Error("initial params should have PathPrefix")
	}
	if p.ChunkSize < 8192 || p.ChunkSize > 14336 {
		t.Errorf("ChunkSize out of range: %d", p.ChunkSize)
	}
}

func TestRotatorSetInitialParams(t *testing.T) {
	r := NewRotator(RotatorConfig{}, DefaultRanges(), nil)

	r.SetInitialParams(LiveParams{
		PathPrefix: "/custom",
		ChunkSize:  12000,
	})

	p := r.GetParams()
	if p.PathPrefix != "/custom" {
		t.Errorf("PathPrefix: %q", p.PathPrefix)
	}
	if p.ChunkSize != 12000 {
		t.Errorf("ChunkSize: %d", p.ChunkSize)
	}
	// Non-set fields should keep their random values
	if p.UploadPath == "" {
		t.Error("UploadPath should keep random value")
	}
}

func TestRotatorSetInitialParamsIgnoresEmpty(t *testing.T) {
	r := NewRotator(RotatorConfig{}, DefaultRanges(), nil)
	originalPath := r.GetParams().PathPrefix

	r.SetInitialParams(LiveParams{}) // all zero values

	if r.GetParams().PathPrefix != originalPath {
		t.Error("empty SetInitialParams should not change PathPrefix")
	}
}

func TestDetectorConcurrentAccess(t *testing.T) {
	d := NewDetector(DetectorConfig{ConfirmCount: 2})

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			d.RecordRTT(50 * time.Millisecond)
			d.GetState()
		}
		close(done)
	}()

	for i := 0; i < 100; i++ {
		d.RecordThroughput(1000000)
		d.GetState()
	}

	<-done
}
