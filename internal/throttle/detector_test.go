package throttle

import (
	"sync"
	"testing"
	"time"
)

func TestDetectorNormalState(t *testing.T) {
	d := NewDetector(DetectorConfig{
		RTTSpikeMs:        500,
		ThroughputDropPct: 70,
		ConfirmCount:      3,
	})

	// Feed normal RTT samples
	for i := 0; i < 10; i++ {
		d.RecordRTT(50 * time.Millisecond)
	}

	if d.GetState() != StateNormal {
		t.Fatalf("expected normal, got %s", d.GetState())
	}
}

func TestDetectorRTTSpike(t *testing.T) {
	d := NewDetector(DetectorConfig{
		RTTSpikeMs:   100,
		ConfirmCount: 3,
		WindowSize:   20,
	})

	// Establish baseline
	for i := 0; i < 5; i++ {
		d.RecordRTT(30 * time.Millisecond)
	}

	if d.GetState() != StateNormal {
		t.Fatalf("should be normal after baseline, got %s", d.GetState())
	}

	// Spike RTT
	for i := 0; i < 5; i++ {
		d.RecordRTT(500 * time.Millisecond)
	}

	state := d.GetState()
	if state != StateThrottled {
		t.Fatalf("expected throttled after RTT spikes, got %s", state)
	}
}

func TestDetectorThroughputDrop(t *testing.T) {
	d := NewDetector(DetectorConfig{
		ThroughputDropPct: 70,
		ConfirmCount:      3,
		WindowSize:        20,
	})

	// Baseline: 1MB/s
	for i := 0; i < 5; i++ {
		d.RecordThroughput(1_000_000)
	}

	// Drop to 100KB/s (90% drop)
	for i := 0; i < 5; i++ {
		d.RecordThroughput(100_000)
	}

	if d.GetState() != StateThrottled {
		t.Fatalf("expected throttled after throughput drop, got %s", d.GetState())
	}
}

func TestDetectorRecovery(t *testing.T) {
	d := NewDetector(DetectorConfig{
		RTTSpikeMs:   100,
		ConfirmCount: 2,
		WindowSize:   20,
	})

	// Baseline
	for i := 0; i < 5; i++ {
		d.RecordRTT(30 * time.Millisecond)
	}

	// Spike → throttled
	for i := 0; i < 3; i++ {
		d.RecordRTT(500 * time.Millisecond)
	}
	if d.GetState() != StateThrottled {
		t.Fatalf("expected throttled, got %s", d.GetState())
	}

	// Recover → normal
	for i := 0; i < 3; i++ {
		d.RecordRTT(30 * time.Millisecond)
	}
	if d.GetState() != StateNormal {
		t.Fatalf("expected normal after recovery, got %s", d.GetState())
	}
}

func TestDetectorCallback(t *testing.T) {
	d := NewDetector(DetectorConfig{
		RTTSpikeMs:   100,
		ConfirmCount: 2,
		WindowSize:   20,
	})

	var mu sync.Mutex
	var states []State

	d.OnStateChange(func(s State) {
		mu.Lock()
		states = append(states, s)
		mu.Unlock()
	})

	// Baseline
	for i := 0; i < 5; i++ {
		d.RecordRTT(30 * time.Millisecond)
	}

	// Trigger suspected
	d.RecordRTT(500 * time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	// Trigger throttled
	d.RecordRTT(500 * time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	mu.Lock()
	if len(states) < 1 {
		mu.Unlock()
		t.Fatal("expected at least one state change callback")
	}
	mu.Unlock()
}

func TestAdaptiveIncrease(t *testing.T) {
	d := NewDetector(DetectorConfig{
		RTTSpikeMs:   100,
		ConfirmCount: 2,
		WindowSize:   20,
	})

	a := NewAdaptive(AdaptiveConfig{
		BaseParallelConns: 1,
		MaxParallelConns:  8,
		Strategy:          StrategyMultiply,
	}, d)

	if a.ParallelConns() != 1 {
		t.Fatalf("initial: got %d", a.ParallelConns())
	}

	// Baseline
	for i := 0; i < 5; i++ {
		d.RecordRTT(30 * time.Millisecond)
	}

	// Trigger throttling
	for i := 0; i < 3; i++ {
		d.RecordRTT(500 * time.Millisecond)
	}

	// Wait for async callback
	time.Sleep(50 * time.Millisecond)

	conns := a.ParallelConns()
	if conns <= 1 {
		t.Fatalf("expected parallel conns to increase, got %d", conns)
	}
}

func TestAdaptiveDecrease(t *testing.T) {
	d := NewDetector(DetectorConfig{
		RTTSpikeMs:   100,
		ConfirmCount: 2,
		WindowSize:   20,
	})

	a := NewAdaptive(AdaptiveConfig{
		BaseParallelConns: 1,
		MaxParallelConns:  8,
		Strategy:          StrategyMultiply,
	}, d)

	// Baseline
	for i := 0; i < 5; i++ {
		d.RecordRTT(30 * time.Millisecond)
	}

	// Throttle → increase
	for i := 0; i < 3; i++ {
		d.RecordRTT(500 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	increased := a.ParallelConns()

	// Recover → decrease
	for i := 0; i < 3; i++ {
		d.RecordRTT(30 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)

	decreased := a.ParallelConns()
	if decreased >= increased {
		t.Fatalf("expected decrease: was %d, now %d", increased, decreased)
	}
}

func TestAdaptiveMaxCap(t *testing.T) {
	d := NewDetector(DetectorConfig{
		RTTSpikeMs:   100,
		ConfirmCount: 2,
		WindowSize:   20,
	})

	a := NewAdaptive(AdaptiveConfig{
		BaseParallelConns: 2,
		MaxParallelConns:  4,
		Strategy:          StrategyMultiply,
	}, d)

	// Baseline
	for i := 0; i < 5; i++ {
		d.RecordRTT(30 * time.Millisecond)
	}

	// Throttle multiple times
	for round := 0; round < 5; round++ {
		for i := 0; i < 3; i++ {
			d.RecordRTT(500 * time.Millisecond)
		}
		time.Sleep(50 * time.Millisecond)

		// Briefly recover to allow re-trigger
		d.RecordRTT(30 * time.Millisecond)
		time.Sleep(10 * time.Millisecond)
	}

	if a.ParallelConns() > 4 {
		t.Fatalf("exceeded max: got %d", a.ParallelConns())
	}
}

func TestAdaptiveRotateSignal(t *testing.T) {
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

	// Baseline
	for i := 0; i < 5; i++ {
		d.RecordRTT(30 * time.Millisecond)
	}

	// Trigger throttling
	for i := 0; i < 3; i++ {
		d.RecordRTT(500 * time.Millisecond)
	}

	// Should get rotate signal
	select {
	case <-a.RotateSignal():
		// Good
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected rotate signal on throttling")
	}
}
