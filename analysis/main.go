// Command limiter-analysis reproduces the 32-channel overload scenario reported by the audio
// team, runs the raw sum and the effects.Limiter output through the same measurement pipeline,
// and writes waveform/band-energy/gain-reduction plots plus dynamic-range metrics and reference
// WAV files. It only depends on the Go standard library and the parent beep module.
package main

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"

	"github.com/faiface/beep"
	"github.com/faiface/beep/effects"
)

const (
	sampleRate = 48000
	durationS  = 4.0
	voices     = 32
)

func main() {
	outDir := "artifacts/limiter-report"
	if len(os.Args) > 1 {
		outDir = os.Args[1]
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fatal(err)
	}

	rng := rand.New(rand.NewSource(20261113))
	raw := buildScenario(rng)

	limited, limiter, grCurve := process(raw)

	rawMetrics := measure(raw, "raw sum")
	limMetrics := measure(limited, "limited")

	if err := writeWAV(filepath.Join(outDir, "raw_mix.wav"), raw); err != nil {
		fatal(err)
	}
	if err := writeWAV(filepath.Join(outDir, "limited_mix.wav"), limited); err != nil {
		fatal(err)
	}

	cfg := effects.DefaultLimiterConfig(sampleRate)
	if err := writeReport(filepath.Join(outDir, "report.md"), cfg, rawMetrics, limMetrics); err != nil {
		fatal(err)
	}
	if err := writeWaveformPlot(filepath.Join(outDir, "waveform_compare.svg"), raw, limited); err != nil {
		fatal(err)
	}
	if err := writeBandPlot(filepath.Join(outDir, "band_energy.svg"), rawMetrics, limMetrics); err != nil {
		fatal(err)
	}
	if err := writeGainReductionPlot(filepath.Join(outDir, "gain_reduction.svg"), grCurve, limiter.MaxGainReduction()); err != nil {
		fatal(err)
	}

	printSummary(rawMetrics, limMetrics, outDir)
}

// impact is a single explosion/collision voice: a short noise burst through a decaying
// exponential envelope, optionally low-passed, with a distinct start time and pan position.
type impact struct {
	start    int
	length   int
	decayMs  float64
	baseFreq float64
	amp      float64
	pan      float64 // -1 left .. +1 right
	noise    []float64
}

func buildScenario(rng *rand.Rand) [][2]float64 {
	total := int(durationS * sampleRate)
	mix := make([][2]float64, total)

	// 32 voices scheduled across the timeline, with a dense cluster around 1.0 s where many
	// impacts overlap (the worst-case "32 channels at once" condition).
	for v := 0; v < voices; v++ {
		var start int
		if v < 24 {
			// dense simultaneous cluster within a 120 ms window
			start = sampleRate + rng.Intn(sampleRate*120/1000)
		} else {
			// scattered isolated impacts elsewhere
			start = rng.Intn(total - sampleRate/2)
		}
		length := sampleRate * (120 + rng.Intn(260)) / 1000
		if start+length > total {
			length = total - start
		}
		im := impact{
			start:    start,
			length:   length,
			decayMs:  float64(60 + rng.Intn(140)),
			baseFreq: 60 + rng.Float64()*180,
			amp:      0.45 + rng.Float64()*0.5,
			pan:      rng.Float64()*2 - 1,
			noise:    make([]float64, length),
		}
		// brown-ish noise gives the burst a body-like spectrum instead of white hiss
		last := 0.0
		for i := range im.noise {
			last = 0.7*last + 0.3*(rng.Float64()*2-1)
			im.noise[i] = last * 3
		}
		addVoice(mix, im)
	}

	// low-level ambient bed so the leveler has continuous program material to ride on
	for i := range mix {
		bed := 0.02 * math.Sin(2*math.Pi*55*float64(i)/sampleRate)
		mix[i][0] += bed
		mix[i][1] += bed * 0.9
	}
	return mix
}

func addVoice(mix [][2]float64, im impact) {
	decay := float64(sampleRate) / 1000 * im.decayMs
	leftGain := math.Sqrt((1 - im.pan) / 2)
	rightGain := math.Sqrt((1 + im.pan) / 2)
	// one-pole low-pass to take the harsh edge off the noise burst
	lpCoef := 2 * math.Pi * (im.baseFreq * 8) / sampleRate
	if lpCoef > 1 {
		lpCoef = 1
	}
	lpL, lpR := 0.0, 0.0
	for i := 0; i < im.length; i++ {
		env := math.Exp(-float64(i) / decay)
		thump := math.Sin(2 * math.Pi * im.baseFreq * float64(i) / sampleRate)
		raw := im.amp * env * (im.noise[i] + 0.8*thump)
		lpL += lpCoef * (raw*leftGain - lpL)
		lpR += lpCoef * (raw*rightGain - lpR)
		idx := im.start + i
		mix[idx][0] += lpL
		mix[idx][1] += lpR
	}
}

// process runs the exact production code path (effects.SoftLimiter) over the raw mix.
func process(raw [][2]float64) ([][2]float64, *effects.Limiter, []float64) {
	src := &sliceSource{samples: raw}
	lim := effects.SoftLimiter(src, sampleRate)
	out := make([][2]float64, len(raw))
	var grCurve []float64
	pos := 0
	buf := make([][2]float64, 512)
	for pos < len(out) {
		n, _ := lim.Stream(buf)
		copy(out[pos:], buf[:n])
		grCurve = append(grCurve, lim.GainReduction())
		pos += n
		if n == 0 {
			break
		}
	}
	return out, lim, grCurve
}

type sliceSource struct {
	samples [][2]float64
	pos     int
}

func (s *sliceSource) Stream(p [][2]float64) (int, bool) {
	if s.pos >= len(s.samples) {
		return 0, false
	}
	n := copy(p, s.samples[s.pos:])
	s.pos += n
	return n, true
}

func (s *sliceSource) Err() error { return nil }

var _ beep.Streamer = (*sliceSource)(nil)

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "limiter-analysis:", err)
	os.Exit(1)
}
