// Package soundfx provides a preloaded, pooled, non-blocking one-shot and looping
// sound-effect trigger layer built on top of beep.
//
// The trigger hot path (Pool.Trigger) never decodes audio, never performs I/O and
// never waits on the audio output thread: all effects are decoded into beep.Buffer
// values during scene changes (see Pool.Preload), and Trigger only enqueues a
// lightweight voice onto the audio pump.
package soundfx

import (
	"errors"
	"sync"
	"time"

	"github.com/faiface/beep"
)

// DegradePolicy selects what Trigger does when every voice is busy.
type DegradePolicy int

const (
	// PolicyReject refuses the new trigger. Trigger returns ErrPoolExhausted,
	// the rejected counter is incremented and no currently playing sound is
	// affected. This keeps the trigger path wait-free.
	PolicyReject DegradePolicy = iota

	// PolicyStealOldest stops the voice that has been playing the longest, frees
	// its slot and plays the new effect in that slot. The stolen counter is
	// incremented.
	PolicyStealOldest
)

// ErrPoolExhausted is returned by Trigger under PolicyReject when no voice is free.
var ErrPoolExhausted = errors.New("soundfx: effect pool exhausted, trigger rejected")

// PoolConfig configures a Pool.
type PoolConfig struct {
	// SampleRate is the mixing/output rate the pool runs at.
	SampleRate beep.SampleRate

	// MaxVoices is the maximum number of concurrently playing effects. The
	// concurrency requirement uses 32.
	MaxVoices int

	// BlockSamples is the number of stereo frames the audio pump pulls per tick.
	// Smaller blocks reduce trigger and stop latency, at the cost of CPU usage.
	// To guarantee the sub-millisecond loop stop requirement, BlockSamples must
	// stay below one millisecond of audio (see Pool.BlockDuration).
	BlockSamples int

	// Policy defines the degradation behaviour when the pool is saturated.
	Policy DegradePolicy

	// StartPump runs a dedicated goroutine that drives the mixer into Sink. When
	// false, the caller drives output manually with Pool.Step (used by headless
	// benchmarks and tests).
	StartPump bool
}

// TriggerHandle represents one triggered effect. It is safe to use from any
// goroutine.
type TriggerHandle struct {
	id   uint64
	at   time.Time
	gate *StopGate
}

// ID returns the monotonically increasing trigger identifier.
func (h TriggerHandle) ID() uint64 { return h.id }

// TriggeredAt returns the timestamp taken at the entry of the trigger call.
func (h TriggerHandle) TriggeredAt() time.Time { return h.at }

// Stop immediately silences the effect. A stopped one-shot is removed from the
// mixer and a stopped loop is muted within one pump block plus the gate fade
// (see StopGate). It is safe to call Stop multiple times.
func (h TriggerHandle) Stop() {
	h.gate.Stop()
}

type voice struct {
	gate  *StopGate
	probe *firstFrameProbe
}

// Pool is a preloaded effect pool with a fixed voice budget.
type Pool struct {
	cfg   PoolConfig
	mixer *beep.Mixer
	sink  Sink
	clock Clock

	mu       sync.Mutex
	sounds   map[string]*beep.Buffer
	pending  []beep.Streamer
	voices   map[uint64]voice
	nextID   uint64
	oldestID uint64

	pendingMu sync.Mutex

	metrics *Metrics

	stopCh    chan struct{}
	closeOnce sync.Once
}

// NewPool creates a Pool. If sink is nil, outputs are discarded (useful for pure
// scheduling measurements). If clock is nil, the real wall clock is used.
func NewPool(cfg PoolConfig, sink Sink, clock Clock) (*Pool, error) {
	if cfg.SampleRate <= 0 {
		return nil, errors.New("soundfx: SampleRate must be positive")
	}
	if cfg.MaxVoices <= 0 {
		return nil, errors.New("soundfx: MaxVoices must be positive")
	}
	if cfg.BlockSamples <= 0 {
		return nil, errors.New("soundfx: BlockSamples must be positive")
	}
	if sink == nil {
		sink = discardSink{}
	}
	if clock == nil {
		clock = wallClock{}
	}
	p := &Pool{
		cfg:     cfg,
		mixer:   &beep.Mixer{},
		sink:    sink,
		clock:   clock,
		sounds:  make(map[string]*beep.Buffer),
		voices:  make(map[uint64]voice),
		metrics: NewMetrics(),
		stopCh:  make(chan struct{}),
	}
	if cfg.StartPump {
		go p.run()
	}
	return p, nil
}

// Metrics returns the pool's latency and degradation metrics.
func (p *Pool) Metrics() *Metrics { return p.metrics }

// BlockDuration returns the wall-clock duration of one pump block.
func (p *Pool) BlockDuration() time.Duration {
	return p.cfg.SampleRate.D(p.cfg.BlockSamples)
}

// ActiveCount returns the number of currently playing voices.
func (p *Pool) ActiveCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.voices)
}

// Trigger starts the named effect. loop=true plays the effect until the handle is
// stopped; loop=false plays it once.
//
// The call is non-blocking: it only looks up the preloaded buffer, wraps a fresh
// streamer and enqueues it. The first audio frame is pulled by the audio pump on
// its next tick, which is where the end-of-latency timestamp is recorded.
func (p *Pool) Trigger(name string, loop bool) (TriggerHandle, error) {
	t0 := p.clock.Now()

	p.mu.Lock()
	buf, ok := p.sounds[name]
	if !ok {
		p.mu.Unlock()
		return TriggerHandle{}, errors.New("soundfx: effect not preloaded: " + name)
	}

	if len(p.voices) >= p.cfg.MaxVoices {
		switch p.cfg.Policy {
		case PolicyStealOldest:
			oldest := p.voices[p.oldestID]
			oldest.gate.Stop()
			delete(p.voices, p.oldestID)
			p.oldestID = p.nextOldestLocked()
			p.metrics.AddStolen()
		default:
			p.metrics.AddRejected()
			p.mu.Unlock()
			return TriggerHandle{}, ErrPoolExhausted
		}
	}

	p.nextID++
	id := p.nextID
	if len(p.voices) == 0 {
		p.oldestID = id
	}

	src := buf.Streamer(0, buf.Len())
	var s beep.Streamer = src
	if loop {
		s = beep.Loop(-1, src)
	}
	gate := newStopGate(s)
	probe := newFirstFrameProbe(gate, id, t0, p.clock, p.metrics)
	p.voices[id] = voice{gate: gate, probe: probe}
	pending := probe
	p.mu.Unlock()

	// Enqueue outside the pool lock so the audio pump never blocks triggerers on
	// mixer bookkeeping.
	p.pendingMu.Lock()
	p.pending = append(p.pending, pending)
	p.pendingMu.Unlock()

	return TriggerHandle{id: id, at: t0, gate: gate}, nil
}

// drainPending moves queued voices into the mixer. Called only by the pump.
func (p *Pool) drainPending() {
	p.pendingMu.Lock()
	if len(p.pending) == 0 {
		p.pendingMu.Unlock()
		return
	}
	queued := p.pending
	p.pending = nil
	p.pendingMu.Unlock()

	p.mu.Lock()
	p.mixer.Add(queued...)
	p.mu.Unlock()
}
