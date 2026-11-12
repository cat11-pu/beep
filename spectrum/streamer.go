package spectrum

import "github.com/faiface/beep"

// Tap wraps a Streamer so every streamed sample is also fed to an Analyzer.
// The audio passes through unchanged; analysis happens inline on the caller's
// goroutine and never allocates after the first frame.
type Tap struct {
	streamer beep.Streamer
	analyzer *Analyzer
	onFrame  func(a *Analyzer)
}

// NewTap returns a Streamer wrapping s. onFrame, when non-nil, is invoked each
// time the analyzer finishes a frame, allowing visualization code to read the
// fresh band energies without any polling.
func NewTap(s beep.Streamer, a *Analyzer, onFrame func(a *Analyzer)) *Tap {
	return &Tap{streamer: s, analyzer: a, onFrame: onFrame}
}

// Stream implements beep.Streamer.
func (t *Tap) Stream(samples [][2]float64) (n int, ok bool) {
	n, ok = t.streamer.Stream(samples)
	for i := 0; i < n; i++ {
		if t.analyzer.PushStereo(samples[i][0], samples[i][1]) && t.onFrame != nil {
			t.onFrame(t.analyzer)
		}
	}
	return n, ok
}

// Err implements beep.Streamer.
func (t *Tap) Err() error {
	return t.streamer.Err()
}
