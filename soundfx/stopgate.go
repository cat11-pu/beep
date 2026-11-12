package soundfx

import (
	"sync/atomic"

	"github.com/faiface/beep"
)

// maxFadeSamples caps the stop fade-out at 0.5 ms of audio. The fade removes the
// click/pop that an abrupt sample cut would produce while keeping the time from
// Stop() to actual silence below one millisecond on any supported rate.
const maxFadeSamples = 24

// StopGate wraps a Streamer so it can be silenced immediately from any goroutine.
//
// When Stop is called, the next Stream call (i.e. the next pump block) fades the
// already-pulled audio out within at most maxFadeSamples and then reports
// drained, so no audio produced after the stop call can reach the output. Because
// the pump block itself is shorter than one millisecond (see PoolConfig), the
// requirement "loop stopped => silence within 1 ms, no tail" holds without
// touching the output thread from the caller.
type StopGate struct {
	streamer beep.Streamer
	stopped  uint32
}

func newStopGate(s beep.Streamer) *StopGate {
	return &StopGate{streamer: s}
}

// Stop silences the wrapped Streamer. It is lock-free and safe to call any number
// of times from any goroutine.
func (g *StopGate) Stop() {
	atomic.StoreUint32(&g.stopped, 1)
}

// Stopped reports whether Stop has been called.
func (g *StopGate) Stopped() bool {
	return atomic.LoadUint32(&g.stopped) == 1
}

// Stream implements beep.Streamer.
func (g *StopGate) Stream(samples [][2]float64) (n int, ok bool) {
	stoppedAtEntry := g.Stopped()
	if stoppedAtEntry {
		// Stop preceded this pull: the previous block already carried the fade,
		// so drain immediately and emit no tail at all.
		return 0, false
	}
	n, ok = g.streamer.Stream(samples)
	if g.Stopped() {
		// Stop happened before or during this pull: fade everything produced so
		// no post-stop audio leaves the mixer at full amplitude, then drain.
		g.fadeOut(samples[:n])
		return n, true
	}
	if !ok {
		return 0, false
	}
	return n, ok
}

// fadeOut applies a linear gain ramp from 1 down to 0 over at most maxFadeSamples
// and zeroes the remainder of the block.
func (g *StopGate) fadeOut(samples [][2]float64) {
	if len(samples) == 0 {
		return
	}
	fade := len(samples)
	if fade > maxFadeSamples {
		fade = maxFadeSamples
	}
	for i := 0; i < fade; i++ {
		gain := 1.0 - float64(i+1)/float64(fade+1)
		samples[i][0] *= gain
		samples[i][1] *= gain
	}
	for i := fade; i < len(samples); i++ {
		samples[i] = [2]float64{}
	}
}

// Err implements beep.Streamer.
func (g *StopGate) Err() error {
	return g.streamer.Err()
}
