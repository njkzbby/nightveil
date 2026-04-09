// Package throttle detects and adapts to network throttling.
package throttle

import (
	"sync"
	"time"
)

// State represents the current throttling assessment.
type State int

const (
	StateNormal    State = iota // No throttling detected
	StateSuspected              // Possible throttling
	StateThrottled              // Confirmed throttling
)

func (s State) String() string {
	switch s {
	case StateNormal:
		return "normal"
	case StateSuspected:
		return "suspected"
	case StateThrottled:
		return "throttled"
	default:
		return "unknown"
	}
}

// DetectorConfig configures throttling detection thresholds.
type DetectorConfig struct {
	RTTSpikeMs        int     // RTT above this = suspected (default 500)
	ThroughputDropPct int     // Throughput drops by this % = suspected (default 70)
	WindowSize        int     // Number of samples to keep (default 20)
	ConfirmCount      int     // Consecutive suspected samples to confirm (default 3)
}

func (c *DetectorConfig) defaults() {
	if c.RTTSpikeMs <= 0 {
		c.RTTSpikeMs = 500
	}
	if c.ThroughputDropPct <= 0 {
		c.ThroughputDropPct = 70
	}
	if c.WindowSize <= 0 {
		c.WindowSize = 20
	}
	if c.ConfirmCount <= 0 {
		c.ConfirmCount = 3
	}
}

// Detector monitors RTT and throughput to detect throttling.
type Detector struct {
	config DetectorConfig

	mu             sync.Mutex
	rttSamples     []time.Duration
	tputSamples    []float64 // bytes per second
	baselineRTT    time.Duration
	baselineTput   float64
	suspectedCount int
	state          State
	listeners      []func(State)
}

// NewDetector creates a throttle detector.
func NewDetector(cfg DetectorConfig) *Detector {
	cfg.defaults()
	return &Detector{
		config:      cfg,
		rttSamples:  make([]time.Duration, 0, cfg.WindowSize),
		tputSamples: make([]float64, 0, cfg.WindowSize),
	}
}

// RecordRTT adds an RTT measurement.
func (d *Detector) RecordRTT(rtt time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.rttSamples = append(d.rttSamples, rtt)
	if len(d.rttSamples) > d.config.WindowSize {
		d.rttSamples = d.rttSamples[1:]
	}

	// Update baseline from first few samples
	if len(d.rttSamples) <= 5 {
		d.baselineRTT = d.medianRTT()
	}

	d.evaluate()
}

// RecordThroughput adds a throughput measurement (bytes/sec).
func (d *Detector) RecordThroughput(bytesPerSec float64) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.tputSamples = append(d.tputSamples, bytesPerSec)
	if len(d.tputSamples) > d.config.WindowSize {
		d.tputSamples = d.tputSamples[1:]
	}

	if len(d.tputSamples) <= 5 {
		d.baselineTput = d.avgThroughput()
	}

	d.evaluate()
}

// State returns the current throttling state.
func (d *Detector) GetState() State {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state
}

// OnStateChange registers a callback for state transitions.
func (d *Detector) OnStateChange(fn func(State)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.listeners = append(d.listeners, fn)
}

// evaluate checks if throttling is detected. Must be called with mu held.
func (d *Detector) evaluate() {
	suspected := false

	// Check RTT spike
	if d.baselineRTT > 0 && len(d.rttSamples) >= 3 {
		recent := d.rttSamples[len(d.rttSamples)-1]
		threshold := time.Duration(d.config.RTTSpikeMs) * time.Millisecond
		if recent > threshold && recent > d.baselineRTT*3 {
			suspected = true
		}
	}

	// Check throughput drop
	if d.baselineTput > 0 && len(d.tputSamples) >= 3 {
		recent := d.tputSamples[len(d.tputSamples)-1]
		dropPct := (1.0 - recent/d.baselineTput) * 100
		if dropPct >= float64(d.config.ThroughputDropPct) {
			suspected = true
		}
	}

	oldState := d.state

	if suspected {
		d.suspectedCount++
		if d.suspectedCount >= d.config.ConfirmCount {
			d.state = StateThrottled
		} else {
			d.state = StateSuspected
		}
	} else {
		d.suspectedCount = 0
		d.state = StateNormal
	}

	if oldState != d.state {
		for _, fn := range d.listeners {
			go fn(d.state)
		}
	}
}

func (d *Detector) medianRTT() time.Duration {
	if len(d.rttSamples) == 0 {
		return 0
	}
	// Simple: use middle element (not sorted, approximate)
	return d.rttSamples[len(d.rttSamples)/2]
}

func (d *Detector) avgThroughput() float64 {
	if len(d.tputSamples) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range d.tputSamples {
		sum += v
	}
	return sum / float64(len(d.tputSamples))
}
