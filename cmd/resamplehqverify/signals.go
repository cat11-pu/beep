package main

import (
	"math"
	"math/rand"

	"github.com/faiface/beep"
)

// monoStreamer wraps a mono signal as a beep.Streamer (both channels identical).
type monoStreamer struct {
	x   []float64
	pos int
}

func (s *monoStreamer) Stream(out [][2]float64) (n int, ok bool) {
	if s.pos >= len(s.x) {
		return 0, false
	}
	for n < len(out) && s.pos < len(s.x) {
		v := s.x[s.pos]
		out[n] = [2]float64{v, v}
		s.pos++
		n++
	}
	return n, true
}

func (s *monoStreamer) Err() error { return nil }

// resampleMono runs the HQ resampler over a mono signal and returns the left
// channel of the result.
func resampleMono(oldRate, newRate beep.SampleRate, x []float64) []float64 {
	r := beep.ResampleHQ(oldRate, newRate, &monoStreamer{x: x})
	var out []float64
	buf := make([][2]float64, 1024)
	for {
		n, ok := r.Stream(buf)
		for i := 0; i < n; i++ {
			out = append(out, buf[i][0])
		}
		if !ok {
			break
		}
	}
	return out
}

// resamplerDelay exposes the measured/design group delay in seconds.
func resamplerDelaySeconds(oldRate, newRate beep.SampleRate) float64 {
	r := beep.ResampleHQ(oldRate, newRate, &monoStreamer{x: make([]float64, 16)})
	return r.Delay().Seconds()
}

// gaussianNoise returns a deterministic zero-mean unit-variance white Gaussian
// sequence. The fixed seed makes every acceptance run reproducible.
func gaussianNoise(n, seed int) []float64 {
	rng := rand.New(rand.NewSource(int64(seed)))
	x := make([]float64, n)
	for i := range x {
		x[i] = rng.NormFloat64()
	}
	return x
}

// sineTone returns n samples of an amplitude-a sine at frequency fHz.
func sineTone(n int, rate, fHz, amp float64) []float64 {
	x := make([]float64, n)
	for i := range x {
		x[i] = amp * math.Sin(2*math.Pi*fHz*float64(i)/rate)
	}
	return x
}

// logSweep generates an exponential swept-sine from f1 to f2 over duration
// seconds at the given sample rate. It is used as a qualitative excitation and
// for end-to-end spectral inspection. The second returned slice is a
// time-reversed amplitude-matched inverse that deconvolves the sweep into an
// impulse response (Farina method).
func logSweep(rate float64, durationSec, f1, f2 float64) (sweep, inverse []float64) {
	n := int(rate * durationSec)
	sweep = make([]float64, n)
	k := math.Log(f2 / f1)
	t1 := durationSec
	// Instantaneous phase: phi(t) = 2*pi*f1*T/k * (exp(t*k/T) - 1).
	for i := 0; i < n; i++ {
		t := float64(i) / rate
		phase := 2 * math.Pi * f1 * t1 / k * (math.Exp(t*k/t1) - 1)
		// Gentle amplitude envelope to avoid start/end clicks.
		env := 1.0
		ramp := int(0.01 * rate)
		if i < ramp {
			env = float64(i) / float64(ramp)
		} else if i > n-ramp {
			env = float64(n-i) / float64(ramp)
		}
		sweep[i] = env * math.Sin(phase)
	}
	// Analytical inverse filter in the time domain (time-reversed, amplitude
	// modulated sweep). Deconvolving the recorded sweep yields the impulse
	// response plus separated harmonic distortion products.
	inverse = make([]float64, n)
	for i := 0; i < n; i++ {
		t := float64(i) / rate
		phase := 2 * math.Pi * f1 * t1 / k * (math.Exp(t*k/t1) - 1)
		inverse[n-1-i] = math.Exp(-t*k/t1) * math.Sin(phase)
	}
	return sweep, inverse
}
