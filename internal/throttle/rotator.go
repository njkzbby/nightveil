package throttle

import (
	"crypto/rand"
	"math/big"
	"sync"
	"time"
)

// RotatorConfig configures automatic parameter rotation.
type RotatorConfig struct {
	// Level 1: rotate padding/chunk/jitter params (seamless, no reconnect)
	ParamRotateInterval time.Duration // default 5 minutes

	// Level 2: rotate paths/cookies + new HTTP connection (new session)
	SessionRotateInterval time.Duration // default 30 minutes

	// Level 3: emergency rotation on throttle detection
	EmergencyRotate bool // default true
}

func (c *RotatorConfig) defaults() {
	if c.ParamRotateInterval <= 0 {
		c.ParamRotateInterval = 5 * time.Minute
	}
	if c.SessionRotateInterval <= 0 {
		c.SessionRotateInterval = 30 * time.Minute
	}
}

// LiveParams holds the currently active randomized parameters.
type LiveParams struct {
	PaddingMin   int
	PaddingMax   int
	ChunkSize    int
	JitterMinMs  int
	JitterMaxMs  int
	PathPrefix   string
	UploadPath   string
	DownloadPath string
	SessionKey   string
}

// ParamRanges defines the allowed bounds for randomization.
type ParamRanges struct {
	PaddingMinRange   [2]int // [low, high] for PaddingMin
	PaddingMaxRange   [2]int // [low, high] for PaddingMax
	ChunkSizeRange    [2]int // [low, high] in bytes
	JitterMinMsRange  [2]int
	JitterMaxMsRange  [2]int
	PathLength        int    // length of random path segments
}

// DefaultRanges returns sensible defaults tuned for TSPU evasion.
func DefaultRanges() ParamRanges {
	return ParamRanges{
		PaddingMinRange:  [2]int{32, 128},
		PaddingMaxRange:  [2]int{128, 512},
		ChunkSizeRange:   [2]int{8192, 14336}, // 8-14KB, all under TSPU 15KB threshold
		JitterMinMsRange: [2]int{5, 30},
		JitterMaxMsRange: [2]int{30, 100},
		PathLength:       6,
	}
}

// Rotator automatically rotates tunnel parameters to prevent DPI pattern accumulation.
type Rotator struct {
	config   RotatorConfig
	ranges   ParamRanges
	adaptive *Adaptive // optional, for emergency rotation

	mu     sync.RWMutex
	params LiveParams

	// Callbacks
	onParamRotate   []func(LiveParams) // level 1: params changed
	onSessionRotate []func(LiveParams) // level 2: full session reset needed

	stopCh chan struct{}
}

// NewRotator creates a parameter rotator.
func NewRotator(cfg RotatorConfig, ranges ParamRanges, adaptive *Adaptive) *Rotator {
	cfg.defaults()
	r := &Rotator{
		config:   cfg,
		ranges:   ranges,
		adaptive: adaptive,
		stopCh:   make(chan struct{}),
	}

	// Generate initial params
	r.params = r.randomizeAll()

	// Watch for emergency rotation
	if adaptive != nil && cfg.EmergencyRotate {
		adaptive.detector.OnStateChange(func(state State) {
			if state == StateThrottled {
				r.EmergencyRotateNow()
			}
		})
	}

	return r
}

// Start begins the rotation timers.
func (r *Rotator) Start() {
	go r.paramLoop()
	go r.sessionLoop()
}

// Stop halts rotation.
func (r *Rotator) Stop() {
	close(r.stopCh)
}

// GetParams returns the current live parameters (thread-safe).
func (r *Rotator) GetParams() LiveParams {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.params
}

// SetInitialParams overrides the initial random params with config values.
// Call before Start(). Only non-empty fields are applied.
func (r *Rotator) SetInitialParams(p LiveParams) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p.PathPrefix != "" {
		r.params.PathPrefix = p.PathPrefix
	}
	if p.UploadPath != "" {
		r.params.UploadPath = p.UploadPath
	}
	if p.DownloadPath != "" {
		r.params.DownloadPath = p.DownloadPath
	}
	if p.SessionKey != "" {
		r.params.SessionKey = p.SessionKey
	}
	if p.ChunkSize > 0 {
		r.params.ChunkSize = p.ChunkSize
	}
	if p.PaddingMin > 0 {
		r.params.PaddingMin = p.PaddingMin
	}
	if p.PaddingMax > 0 {
		r.params.PaddingMax = p.PaddingMax
	}
}

// OnParamRotate registers a callback for level 1 rotation.
func (r *Rotator) OnParamRotate(fn func(LiveParams)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onParamRotate = append(r.onParamRotate, fn)
}

// OnSessionRotate registers a callback for level 2 rotation (needs reconnect).
func (r *Rotator) OnSessionRotate(fn func(LiveParams)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onSessionRotate = append(r.onSessionRotate, fn)
}

// EmergencyRotateNow forces an immediate full rotation (level 3).
func (r *Rotator) EmergencyRotateNow() {
	r.mu.Lock()
	r.params = r.randomizeAll()
	callbacks := make([]func(LiveParams), len(r.onSessionRotate))
	copy(callbacks, r.onSessionRotate)
	params := r.params
	r.mu.Unlock()

	for _, fn := range callbacks {
		go fn(params)
	}
}

func (r *Rotator) paramLoop() {
	ticker := time.NewTicker(r.config.ParamRotateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.rotateParams()
		case <-r.stopCh:
			return
		}
	}
}

func (r *Rotator) sessionLoop() {
	ticker := time.NewTicker(r.config.SessionRotateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.rotateSession()
		case <-r.stopCh:
			return
		}
	}
}

// rotateParams does level 1: change padding/chunk/jitter, keep paths.
func (r *Rotator) rotateParams() {
	r.mu.Lock()
	r.params.PaddingMin = randRange(r.ranges.PaddingMinRange)
	r.params.PaddingMax = randRange(r.ranges.PaddingMaxRange)
	if r.params.PaddingMax < r.params.PaddingMin {
		r.params.PaddingMax = r.params.PaddingMin + 64
	}
	r.params.ChunkSize = randRange(r.ranges.ChunkSizeRange)
	r.params.JitterMinMs = randRange(r.ranges.JitterMinMsRange)
	r.params.JitterMaxMs = randRange(r.ranges.JitterMaxMsRange)
	if r.params.JitterMaxMs < r.params.JitterMinMs {
		r.params.JitterMaxMs = r.params.JitterMinMs + 20
	}
	callbacks := make([]func(LiveParams), len(r.onParamRotate))
	copy(callbacks, r.onParamRotate)
	params := r.params
	r.mu.Unlock()

	for _, fn := range callbacks {
		go fn(params)
	}
}

// rotateSession does level 2: change everything including paths.
func (r *Rotator) rotateSession() {
	r.mu.Lock()
	r.params = r.randomizeAll()
	callbacks := make([]func(LiveParams), len(r.onSessionRotate))
	copy(callbacks, r.onSessionRotate)
	params := r.params
	r.mu.Unlock()

	for _, fn := range callbacks {
		go fn(params)
	}
}

func (r *Rotator) randomizeAll() LiveParams {
	return LiveParams{
		PaddingMin:   randRange(r.ranges.PaddingMinRange),
		PaddingMax:   randRange(r.ranges.PaddingMaxRange),
		ChunkSize:    randRange(r.ranges.ChunkSizeRange),
		JitterMinMs:  randRange(r.ranges.JitterMinMsRange),
		JitterMaxMs:  randRange(r.ranges.JitterMaxMsRange),
		PathPrefix:   "/" + randomAlphaNum(r.ranges.PathLength),
		UploadPath:   "/u/" + randomAlphaNum(3),
		DownloadPath: "/d/" + randomAlphaNum(3),
		SessionKey:   randomAlphaNum(5),
	}
}

func randRange(r [2]int) int {
	if r[1] <= r[0] {
		return r[0]
	}
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(r[1]-r[0]+1)))
	return r[0] + int(n.Int64())
}

func randomAlphaNum(n int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	buf := make([]byte, n)
	for i := range buf {
		idx, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		buf[i] = charset[idx.Int64()]
	}
	return string(buf)
}
