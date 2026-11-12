package main

import "math"

// tone is one sinusoidal component of a test signal.
type tone struct {
	freq float64
	amp  float64
}

// scenarios are deterministic, reproducible test signals. Each contains a
// clearly dominant component plus harmonics so peak picking and band
// aggregation both have something to chew on.
var scenarios = []struct {
	name  string
	tones []tone
}{
	{"single_440", []tone{{440, 0.8}}},
	{"offgrid_1234_5", []tone{{1234.5, 0.8}}},
	{"multi_voice", []tone{{220, 0.7}, {660, 0.35}, {1760, 0.2}, {5200, 0.12}}},
	{"bright_mix", []tone{{1000.7, 0.6}, {4000, 0.3}, {9900, 0.2}, {15500, 0.1}}},
	{"bass_heavy", []tone{{55, 0.8}, {110, 0.4}, {330, 0.2}}},
}

// render produces samples samples of the given signal at sampleRate.
func render(sampleRate float64, samples int, tones []tone) []float64 {
	out := make([]float64, samples)
	for i := range out {
		var v float64
		for _, t := range tones {
			v += t.amp * math.Sin(2*math.Pi*t.freq*float64(i)/sampleRate)
		}
		out[i] = v
	}
	return out
}
