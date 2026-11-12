package soundfx

import (
	"sync"

	"github.com/faiface/beep"
)

// Sink is the final destination of mixed audio. The production implementation
// wraps an oto player (see the speaker-based benchmark command); tests use
// MemorySink for deterministic, headless measurement.
type Sink interface {
	// WriteBlock consumes one block of interleaved stereo float64 samples.
	WriteBlock(samples [][2]float64)
}

type discardSink struct{}

func (discardSink) WriteBlock([][2]float64) {}

// MemorySink records every written block. It is safe for concurrent use and never
// blocks the pump on allocator pressure beyond slice growth.
type MemorySink struct {
	mu     sync.Mutex
	blocks [][][2]float64

	// Clamped counts samples that exceeded the [-1, 1] range and would be
	// hard-clipped at the int16 conversion. A correctly bounded mix keeps this at
	// zero (the "no clipping/爆音" requirement).
	Clamped int
}

// WriteBlock implements Sink.
func (s *MemorySink) WriteBlock(samples [][2]float64) {
	cp := make([][2]float64, len(samples))
	copy(cp, samples)
	for i := range cp {
		for c := range cp[i] {
			v := cp[i][c]
			if v != v || v < -1 || v > 1 { // NaN check via v != v
				s.Clamped++
			}
		}
	}
	s.mu.Lock()
	s.blocks = append(s.blocks, cp)
	s.mu.Unlock()
}

// Blocks returns a copy of all recorded blocks.
func (s *MemorySink) Blocks() [][][2]float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][][2]float64, len(s.blocks))
	copy(out, s.blocks)
	return out
}

// SampleCount returns the total number of stereo frames written so far.
func (s *MemorySink) SampleCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, b := range s.blocks {
		n += len(b)
	}
	return n
}

// LastNonSilent returns the index (in frames) of the last frame whose absolute
// amplitude in either channel exceeds threshold, or -1 if none exists.
func (s *MemorySink) LastNonSilent(threshold float64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	last := -1
	frame := 0
	for _, b := range s.blocks {
		for i := range b {
			if abs64(b[i][0]) > threshold || abs64(b[i][1]) > threshold {
				last = frame + i
			}
		}
		frame += len(b)
	}
	return last
}

func abs64(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

var _ beep.Streamer = (*firstFrameProbe)(nil)
