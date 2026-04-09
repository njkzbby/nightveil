package throttle

import (
	"log"
	"sync/atomic"
)

// Strategy defines how to respond to throttling.
type Strategy string

const (
	StrategyMultiply Strategy = "multiply" // Increase parallel connections
	StrategyRotate   Strategy = "rotate"   // Rotate to fresh connections
	StrategyBoth     Strategy = "both"     // Both multiply and rotate
)

// AdaptiveConfig configures the adaptive response to throttling.
type AdaptiveConfig struct {
	Strategy           Strategy
	BaseParallelConns  int // Starting parallel connections (default 1)
	MaxParallelConns   int // Maximum parallel connections (default 8)
	RotateIntervalSec  int // Rotate connections every N seconds (default 60)
}

func (c *AdaptiveConfig) defaults() {
	if c.Strategy == "" {
		c.Strategy = StrategyBoth
	}
	if c.BaseParallelConns <= 0 {
		c.BaseParallelConns = 1
	}
	if c.MaxParallelConns <= 0 {
		c.MaxParallelConns = 8
	}
	if c.RotateIntervalSec <= 0 {
		c.RotateIntervalSec = 60
	}
}

// Adaptive manages the response to detected throttling.
type Adaptive struct {
	config   AdaptiveConfig
	detector *Detector

	parallelConns atomic.Int32
	rotateSignal  chan struct{} // signaled when connections should rotate
}

// NewAdaptive creates an adaptive throttle responder.
func NewAdaptive(cfg AdaptiveConfig, detector *Detector) *Adaptive {
	cfg.defaults()

	a := &Adaptive{
		config:       cfg,
		detector:     detector,
		rotateSignal: make(chan struct{}, 1),
	}
	a.parallelConns.Store(int32(cfg.BaseParallelConns))

	detector.OnStateChange(a.onStateChange)

	return a
}

// ParallelConns returns the current recommended number of parallel upload connections.
func (a *Adaptive) ParallelConns() int {
	return int(a.parallelConns.Load())
}

// RotateSignal returns a channel that receives when connections should be rotated.
func (a *Adaptive) RotateSignal() <-chan struct{} {
	return a.rotateSignal
}

func (a *Adaptive) onStateChange(state State) {
	switch state {
	case StateThrottled:
		// Increase parallel connections
		current := int(a.parallelConns.Load())
		newVal := min(current*2, a.config.MaxParallelConns)
		if newVal > current {
			a.parallelConns.Store(int32(newVal))
			log.Printf("[throttle] throttling detected — increasing parallel conns: %d → %d", current, newVal)
		}

		// Signal rotation
		if a.config.Strategy == StrategyRotate || a.config.Strategy == StrategyBoth {
			select {
			case a.rotateSignal <- struct{}{}:
			default:
			}
		}

	case StateNormal:
		// Gradually decrease back to base
		current := int(a.parallelConns.Load())
		if current > a.config.BaseParallelConns {
			newVal := max(current/2, a.config.BaseParallelConns)
			a.parallelConns.Store(int32(newVal))
			log.Printf("[throttle] throttling cleared — decreasing parallel conns: %d → %d", current, newVal)
		}
	}
}
