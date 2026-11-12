package effects

import (
	"math"
	"testing"

	"github.com/faiface/beep"
)

const limiterTestRate = 48000

// sliceStreamer replays a fixed slice in arbitrarily sized chunks, so tests can verify that the
// limiter state carries correctly across Stream boundaries.
type sliceStreamer struct {
	data [][2]float64
	pos  int
}

func (s *sliceStreamer) Stream(samples [][2]float64) (n int, ok bool) {
	if s.pos >= len(s.data) {
		return 0, false
	}
	n = copy(samples, s.data[s.pos:])
	s.pos += n
	return n, true
}

func (s *sliceStreamer) Err() error { return nil }

func drain(t *testing.T, l *Limiter, chunk int) [][2]float64 {
	t.Helper()
	var out [][2]float64
	buf := make([][2]float64, chunk)
	for {
		n, ok := l.Stream(buf)
		out = append(out, buf[:n]...)
		if !ok {
			return out
		}
	}
}

// A mix of 32 full-scale streams would reach +/- 32 without limiting. The limiter must keep every
// sample at or below the -1 dBFS ceiling.
func TestLimiterHonorsPeakCeiling(t *testing.T) {
	const voices = 32
	const samples = limiterTestRate // 1 s

	data := make([][2]float64, samples)
	for i := range data {
		v := math.Sin(2 * math.Pi * 440 * float64(i) / limiterTestRate)
		var sum [2]float64
		for j := 0; j < voices; j++ {
			// slightly detuned voices so the peaks are not perfectly phase aligned
			vv := math.Sin(2*math.Pi*(440+float64(j))*float64(i)/limiterTestRate) * 0.5
			sum[0] += vv
			sum[1] += vv * v
		}
		data[i] = sum
	}

	l := SoftLimiter(&sliceStreamer{data: data}, limiterTestRate)
	out := drain(t, l, 397)

	ceiling := dbToLinear(DefaultLimiterConfig(limiterTestRate).PeakCeiling)
	peak := 0.0
	for _, s := range out {
		for _, c := range s {
			if math.Abs(c) > peak {
				peak = math.Abs(c)
			}
		}
	}
	if peak > ceiling+1e-9 {
		t.Errorf("peak %.5f exceeds ceiling %.5f (%.2f dBFS)", peak, ceiling, linearToDB(peak))
	}
	if l.MaxGainReduction() <= 0 {
		t.Error("expected the limiter to apply gain reduction on the overloaded mix")
	}
}

// Quiet program material well below the target and threshold must pass through essentially
// unchanged once the leveler has settled, preserving low-level detail and noise floor.
func TestLimiterPassesQuietSignal(t *testing.T) {
	const samples = limiterTestRate * 2
	data := make([][2]float64, samples)
	for i := range data {
		v := 0.05 * math.Sin(2*math.Pi*1000*float64(i)/limiterTestRate)
		data[i] = [2]float64{v, -v}
	}

	l := SoftLimiter(&sliceStreamer{data: data}, limiterTestRate)
	out := drain(t, l, 512)

	// measure on the second half, after the leveler has converged
	var rmsIn, rmsOut float64
	for i := samples / 2; i < samples; i++ {
		rmsIn += data[i][0] * data[i][0]
		rmsOut += out[i][0] * out[i][0]
	}
	rmsIn = math.Sqrt(rmsIn / float64(samples/2))
	rmsOut = math.Sqrt(rmsOut / float64(samples/2))
	target := dbToLinear(DefaultLimiterConfig(limiterTestRate).TargetLoudness)

	if math.Abs(rmsOut-target) > target*0.25 {
		t.Errorf("leveled RMS %.4f not within 25%% of target %.4f (input RMS %.4f)", rmsOut, target, rmsIn)
	}
	peak := 0.0
	for _, s := range out {
		peak = math.Max(peak, math.Max(math.Abs(s[0]), math.Abs(s[1])))
	}
	if peak > dbToLinear(DefaultLimiterConfig(limiterTestRate).PeakCeiling) {
		t.Error("quiet signal exceeded the peak ceiling")
	}
}

// A single isolated transient must remain a transient: its peak stays close to the ceiling rather
// being clamped to a plateau, and surrounding silence is untouched.
func TestLimiterPreservesTransient(t *testing.T) {
	const samples = limiterTestRate / 4
	data := make([][2]float64, samples)
	// one loud impact, roughly 60 ms of exponential decay, embedded in silence
	impact := limiterTestRate * 60 / 1000
	for i := 0; i < impact; i++ {
		env := math.Exp(-float64(i) / float64(limiterTestRate/1000*8))
		// shaped carrier so the impact resembles a percussive/explosion body rather than a click
		carrier := math.Sin(2 * math.Pi * 90 * float64(i) / limiterTestRate)
		v := 6 * env * carrier
		data[2000+i] = [2]float64{v, v}
	}

	l := SoftLimiter(&sliceStreamer{data: data}, limiterTestRate)
	out := drain(t, l, 128)

	peakIdx, peak := 0, 0.0
	for i, s := range out {
		if math.Abs(s[0]) > peak {
			peak, peakIdx = math.Abs(s[0]), i
		}
	}
	ceiling := dbToLinear(DefaultLimiterConfig(limiterTestRate).PeakCeiling)
	if peak > ceiling+1e-9 {
		t.Fatalf("transient peak %.4f exceeds ceiling %.4f", peak, ceiling)
	}
	// the peak should still reach near the ceiling, proving it was not ducked away
	if peak < ceiling*0.85 {
		t.Errorf("transient peak %.4f was over-attenuated (ceiling %.4f)", peak, ceiling)
	}
	// samples well before the impact must remain silent
	for i := 0; i < 800; i++ {
		if out[i][0] != 0 || out[i][1] != 0 {
			t.Fatalf("limiter altered silence before transient at sample %d", i)
		}
	}
	// the transient must not be smeared into a long plateau: it decays well below its peak
	tail := math.Abs(out[min(peakIdx+limiterTestRate/50, samples-1)][0])
	if tail > peak*0.6 {
		t.Errorf("transient tail %.4f too high relative to peak %.4f (smearing)", tail, peak)
	}
}

func TestLimiterChunkIndependence(t *testing.T) {
	const samples = limiterTestRate
	data := make([][2]float64, samples)
	for i := range data {
		v := math.Sin(2*math.Pi*300*float64(i)/limiterTestRate) * 3
		data[i] = [2]float64{v, v}
	}

	one := SoftLimiter(&sliceStreamer{data: data}, limiterTestRate)
	big := drain(t, one, samples)

	two := SoftLimiter(&sliceStreamer{data: data}, limiterTestRate)
	small := drain(t, two, 37)

	for i := range big {
		if math.Abs(big[i][0]-small[i][0]) > 1e-12 || math.Abs(big[i][1]-small[i][1]) > 1e-12 {
			t.Fatalf("chunked processing diverges at sample %d", i)
		}
	}
}

func TestLimiterConfigValidation(t *testing.T) {
	bad := []LimiterConfig{
		func() LimiterConfig {
			c := DefaultLimiterConfig(48000)
			c.SampleRate = 0
			return c
		}(),
		func() LimiterConfig {
			c := DefaultLimiterConfig(48000)
			c.PeakCeiling = 2
			return c
		}(),
		func() LimiterConfig {
			c := DefaultLimiterConfig(48000)
			c.LimiterThreshold = 0
			return c
		}(),
		func() LimiterConfig {
			c := DefaultLimiterConfig(48000)
			c.LimiterRelease = -1
			return c
		}(),
	}
	for i, cfg := range bad {
		if cfg.Validate() == nil {
			t.Errorf("bad config %d should have failed validation", i)
		}
	}
	if err := DefaultLimiterConfig(48000).Validate(); err != nil {
		t.Errorf("default config should be valid, got %v", err)
	}
}

func TestSoftClipBoundsAndContinuity(t *testing.T) {
	ceiling := dbToLinear(-1)
	for _, x := range []float64{-100, -10, -1.01, -0.9, 0, 0.9, 1.01, 10, 100} {
		y := softClip(x, ceiling)
		if math.Abs(y) > 1+1e-12 {
			t.Errorf("softClip(%f) = %f out of full scale", x, y)
		}
	}
	// identity at or below the ceiling
	if y := softClip(ceiling/2, ceiling); y != ceiling/2 {
		t.Errorf("softClip changed in-range value: %f", y)
	}
	// slope of 1 at the boundary (C1 continuity with the identity region)
	eps := 1e-6
	d := (softClip(ceiling+eps, ceiling) - softClip(ceiling, ceiling)) / eps
	if math.Abs(d-1) > 1e-4 {
		t.Errorf("softClip slope at ceiling is %.4f, want 1", d)
	}
}

func TestNewLimiterPanicsOnInvalidConfig(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewLimiter should panic on invalid config")
		}
	}()
	cfg := DefaultLimiterConfig(48000)
	cfg.TargetLoudness = 10
	NewLimiter(&sliceStreamer{}, cfg)
}

// All-silence must stay exactly silent and must never produce NaN/Inf from the divide-by-level
// paths in the gain computer.
func TestLimiterSilence(t *testing.T) {
	data := make([][2]float64, limiterTestRate)
	l := SoftLimiter(&sliceStreamer{data: data}, limiterTestRate)
	out := drain(t, l, 257)
	for i, s := range out {
		for c, v := range s {
			if math.IsNaN(v) || math.IsInf(v, 0) || v != 0 {
				t.Fatalf("silence altered at sample %d channel %d: %v", i, c, v)
			}
		}
	}
}

// A continuous full-scale tone must have its loudness aligned to the target and every sample
// bounded by the ceiling. Because the leveler acts first, a full-scale tone (RMS 0.707) is
// turned down toward -16 dBFS before the peak limiter ever needs to intervene.
func TestLimiterFullScaleTone(t *testing.T) {
	const samples = limiterTestRate * 2
	data := make([][2]float64, samples)
	for i := range data {
		v := math.Sin(2 * math.Pi * 220 * float64(i) / limiterTestRate)
		data[i] = [2]float64{v, v}
	}
	l := SoftLimiter(&sliceStreamer{data: data}, limiterTestRate)
	out := drain(t, l, 512)

	ceiling := dbToLinear(DefaultLimiterConfig(limiterTestRate).PeakCeiling)
	peak := 0.0
	for _, s := range out {
		peak = math.Max(peak, math.Max(math.Abs(s[0]), math.Abs(s[1])))
	}
	if peak > ceiling+1e-9 {
		t.Fatalf("full-scale tone peak %.5f exceeds ceiling %.5f", peak, ceiling)
	}
	var tailSq float64
	for i := samples * 3 / 4; i < samples; i++ {
		tailSq += out[i][0] * out[i][0]
	}
	tailRMS := math.Sqrt(tailSq / float64(samples/4))
	target := dbToLinear(DefaultLimiterConfig(limiterTestRate).TargetLoudness)
	if math.Abs(tailRMS-target) > target*0.15 {
		t.Errorf("settled RMS %.4f not aligned to target %.4f (-16 dBFS)", tailRMS, target)
	}
}

// keep the beep import meaningful if helpers move around
var _ beep.Streamer = (*sliceStreamer)(nil)

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
