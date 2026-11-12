package spectrum

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/faiface/beep"
)

// naiveDFT is an independent O(n^2) reference implementation used only in
// tests to validate the radix-2 FFT and the derived energy figures.
func naiveDFT(in []float64) []complex128 {
	n := len(in)
	out := make([]complex128, n)
	for k := 0; k < n; k++ {
		var re, im float64
		angle := -2 * math.Pi * float64(k) / float64(n)
		for j := 0; j < n; j++ {
			c, s := math.Cos(angle*float64(j)), math.Sin(angle*float64(j))
			re += in[j] * c
			im += in[j] * s
		}
		out[k] = complex(re, im)
	}
	return out
}

func tone(n int, sampleRate, freq, amp float64) []float64 {
	sig := make([]float64, n)
	for i := range sig {
		sig[i] = amp * math.Sin(2*math.Pi*freq*float64(i)/sampleRate)
	}
	return sig
}

func TestFFTMatchesNaiveDFT(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, n := range []int{1024, 2048} {
		in := make([]float64, n)
		got := make([]complex128, n)
		for i := range in {
			in[i] = rng.Float64()*2 - 1
			got[i] = complex(in[i], 0)
		}
		newFFTPlan(n).transform(got)
		want := naiveDFT(in)
		for k := range want {
			if d := real(got[k]) - real(want[k]); math.Abs(d) > 1e-9 {
				t.Fatalf("n=%d bin=%d real mismatch: got %v want %v", n, k, real(got[k]), real(want[k]))
			}
			if d := imag(got[k]) - imag(want[k]); math.Abs(d) > 1e-9 {
				t.Fatalf("n=%d bin=%d imag mismatch: got %v want %v", n, k, imag(got[k]), imag(want[k]))
			}
		}
	}
}

func TestWindowTables(t *testing.T) {
	n := 64
	hann := newWindowTable(WindowHann, n)
	if got := hann.values[0]; math.Abs(got) > 1e-12 {
		t.Fatalf("hann should start at 0, got %v", got)
	}
	if got := hann.values[n/2]; math.Abs(got-1) > 1e-12 {
		t.Fatalf("hann midpoint should be 1, got %v", got)
	}
	rect := newWindowTable(WindowRectangular, n)
	if rect.s2 != float64(n) {
		t.Fatalf("rectangular s2 = %v, want %d", rect.s2, n)
	}
	for _, w := range []Window{WindowHann, WindowHamming, WindowBlackman, WindowNuttall, WindowRectangular} {
		if !w.valid() || w.String() == "unknown" {
			t.Fatalf("window %v not wired up", w)
		}
	}
}

func TestConfigValidation(t *testing.T) {
	bad := []Config{
		{SampleRate: 0, WindowSize: 1024, Bands: 8, MinFreq: 20, MaxFreq: 1000},
		{SampleRate: 48000, WindowSize: 512, Bands: 8, MinFreq: 20, MaxFreq: 1000},
		{SampleRate: 48000, WindowSize: 1024, Bands: 0, MinFreq: 20, MaxFreq: 1000},
		{SampleRate: 48000, WindowSize: 1024, Bands: 8, MinFreq: -1, MaxFreq: 1000},
		{SampleRate: 48000, WindowSize: 1024, Bands: 8, MinFreq: 1000, MaxFreq: 1000},
		{SampleRate: 8000, WindowSize: 1024, Bands: 8, MinFreq: 20, MaxFreq: 5000},
	}
	for i, cfg := range bad {
		if _, err := NewAnalyzer(cfg); err == nil {
			t.Fatalf("case %d: expected error", i)
		}
	}

	cfg := DefaultConfig(48000, 2048)
	if cfg.Window != WindowHann || cfg.HopSize != 0 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	a, err := NewAnalyzer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if a.HopSize() != 1024 {
		t.Fatalf("default hop = %d, want 1024", a.HopSize())
	}
}

// runFrames pushes a full signal through the analyzer and returns the last
// frame's band energies together with the frame count.
func runFrames(t *testing.T, a *Analyzer, sig []float64) (frames int64) {
	t.Helper()
	dst := make([]float64, a.Bands())
	for _, v := range sig {
		if a.Push(v) {
			frames++
			if len(a.BandEnergies(dst)) != a.Bands() {
				t.Fatal("band count mismatch")
			}
		}
	}
	if frames == 0 {
		t.Fatal("no frame produced")
	}
	return frames
}

func TestPeakFrequencyAccuracy(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for _, sr := range []float64{44100, 48000} {
		for _, n := range []int{1024, 2048} {
			for _, win := range []Window{WindowHann, WindowHamming, WindowBlackman, WindowNuttall} {
				cfg := DefaultConfig(sr, n)
				cfg.Window = win
				a, err := NewAnalyzer(cfg)
				if err != nil {
					t.Fatal(err)
				}
				binHz := sr / float64(n)
				for trial := 0; trial < 25; trial++ {
					// Tones placed at arbitrary offsets inside a bin, never on-grid.
					freq := 100 + rng.Float64()*(sr/2-1000)
					sig := tone(n*4, sr, freq, 0.8)
					runFrames(t, a, sig)
					errHz := math.Abs(a.PeakFrequency() - freq)
					if errHz > 0.5 {
						t.Fatalf("sr=%g n=%d win=%s freq=%.3f (bin %.2f) peak=%.3f error=%.4f Hz",
							sr, n, win, freq, freq/binHz, a.PeakFrequency(), errHz)
					}
				}
			}
		}
	}
}

func BenchmarkPushFrame(b *testing.B) {
	const sr = 48000
	for _, n := range []int{1024, 2048} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			a, err := NewAnalyzer(DefaultConfig(sr, n))
			if err != nil {
				b.Fatal(err)
			}
			sig := tone(n*4, sr, 1000.7, 0.8)
			bands := make([]float64, a.Bands())
			// Prime the ring buffer.
			for i := 0; i < n; i++ {
				a.Push(sig[i])
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for j := 0; j < a.HopSize(); j++ {
					if a.Push(sig[(i*a.HopSize()+j)%len(sig)]) {
						a.BandEnergies(bands)
					}
				}
			}
		})
	}
}

// TestFrameBudget drives one thousand back-to-back frames per configuration
// and requires every single-frame computation to finish within one third of
// the 60 FPS frame budget (5.56 ms), i.e. analysis never causes a dropped
// frame even if visualization consumes the remaining budget.
func TestFrameBudget(t *testing.T) {
	const (
		sr          = 48000
		frameBudget = time.Second / 60
		budgetLimit = frameBudget / 3
		totalFrames = 1000
	)
	for _, n := range []int{1024, 2048} {
		a, err := NewAnalyzer(DefaultConfig(sr, n))
		if err != nil {
			t.Fatal(err)
		}
		sig := tone(n+totalFrames*a.HopSize(), sr, 2718.28, 0.9)
		for i := 0; i < n; i++ {
			a.Push(sig[i])
		}
		bands := make([]float64, a.Bands())
		var worst time.Duration
		frames := 0
		for f := 0; f < totalFrames; f++ {
			start := time.Now()
			for j := 0; j < a.HopSize(); j++ {
				if a.Push(sig[n+f*a.HopSize()+j]) {
					a.BandEnergies(bands)
				}
			}
			if d := time.Since(start); d > worst {
				worst = d
			}
			frames++
		}
		if frames != totalFrames {
			t.Fatalf("n=%d processed %d frames, want %d", n, frames, totalFrames)
		}
		if worst >= budgetLimit {
			t.Fatalf("n=%d worst frame %v exceeds budget %v", n, worst, budgetLimit)
		}
		t.Logf("n=%d worst frame %v (%.2f%% of one-third budget)",
			n, worst, 100*float64(worst)/float64(budgetLimit))
	}
}

func TestBandEnergyMatchesNaiveReference(t *testing.T) {
	const sr = 48000.0
	for _, n := range []int{1024, 2048} {
		cfg := DefaultConfig(sr, n)
		cfg.Bands, cfg.MinFreq, cfg.MaxFreq = 16, 50, 16000
		a, err := NewAnalyzer(cfg)
		if err != nil {
			t.Fatal(err)
		}
		sig := make([]float64, n)
		for i := range sig {
			sig[i] = 0.6*math.Sin(2*math.Pi*440*float64(i)/sr) +
				0.3*math.Sin(2*math.Pi*3000*float64(i)/sr)
		}
		runFrames(t, a, sig)

		// Independent reference: windowed naive DFT with identical normalization.
		ref := make([]float64, n/2+1)
		win := make([]float64, n)
		var s2 float64
		for i := range win {
			win[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n))
			s2 += win[i] * win[i]
		}
		windowed := make([]float64, n)
		for i := range sig {
			windowed[i] = sig[i] * win[i]
		}
		dft := naiveDFT(windowed)
		norm := 2.0 / (float64(n) * s2)
		for k := 0; k <= n/2; k++ {
			p := (real(dft[k])*real(dft[k]) + imag(dft[k])*imag(dft[k])) * norm
			if k == 0 || k == n/2 {
				p *= 0.5
			}
			ref[k] = p
		}

		got := a.BandEnergies(nil)
		var totalRef, totalGot float64
		for b := 0; b < a.Bands(); b++ {
			var want float64
			for k := a.bandLow[b]; k <= a.bandHigh[b]; k++ {
				want += ref[k]
			}
			totalRef += want
			totalGot += got[b]
			if want == 0 {
				if got[b] > 1e-18 {
					t.Fatalf("n=%d band %d: got %v, want 0", n, b, got[b])
				}
				continue
			}
			// Floating-point floor dominates for bands that contain only
			// side-lobe leakage (absolute energy ~1e-15); signal bands stay
			// tight, which the absolute guard below verifies.
			absErr := math.Abs(got[b] - want)
			if rel := absErr / want; rel > 1e-7 && absErr > 1e-12 {
				t.Fatalf("n=%d band %d: got %.12g want %.12g relErr %.2e", n, b, got[b], want, rel)
			}
		}
		// Parseval-style sanity: total band energy must be a stable fraction.
		if rel := math.Abs(totalGot-totalRef) / totalRef; rel > 1e-11 {
			t.Fatalf("n=%d total relErr %.2e", n, rel)
		}
	}
}

func TestBandsCoverConfiguredRange(t *testing.T) {
	cfg := DefaultConfig(48000, 2048)
	cfg.Bands, cfg.MinFreq, cfg.MaxFreq = 24, 30, 18000
	a, err := NewAnalyzer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	edges := a.BandEdges()
	if math.Abs(edges[0]-30) > 1e-9 || math.Abs(edges[len(edges)-1]-18000) > 1e-9 {
		t.Fatalf("edges = %v...%v", edges[0], edges[len(edges)-1])
	}
	for b := 1; b < a.Bands(); b++ {
		if a.bandLow[b] <= a.bandHigh[b-1] && a.bandLow[b] != a.bandHigh[b-1]+1 {
			t.Fatalf("bands overlap/gap at %d: %d %d", b, a.bandHigh[b-1], a.bandLow[b])
		}
		if a.bandHigh[b] < a.bandLow[b] {
			t.Fatalf("band %d empty range", b)
		}
	}
}

func TestNoAllocationsWhileRunning(t *testing.T) {
	const sr = 48000.0
	for _, n := range []int{1024, 2048} {
		a, err := NewAnalyzer(DefaultConfig(sr, n))
		if err != nil {
			t.Fatal(err)
		}
		bands := make([]float64, a.Bands())
		sig := tone(a.WindowSize()*8, sr, 1234.5, 0.7)
		// Warm up: fill the ring and complete several frames.
		for _, v := range sig[:a.WindowSize()*2] {
			a.Push(v)
		}
		allocs := testing.AllocsPerRun(1000, func() {
			for i := 0; i < a.HopSize(); i++ {
				if a.Push(sig[i]) {
					a.BandEnergies(bands)
					_ = a.PeakFrequency()
				}
			}
		})
		if allocs != 0 {
			t.Fatalf("n=%d: %v allocations per run, want 0", n, allocs)
		}
	}
}

func TestTapPassesAudioThrough(t *testing.T) {
	const sr = 48000
	a, err := NewAnalyzer(DefaultConfig(sr, 1024))
	if err != nil {
		t.Fatal(err)
	}
	var callbacks int64
	src := beep.StreamerFunc(func(samples [][2]float64) (int, bool) {
		for i := range samples {
			samples[i] = [2]float64{0.5, -0.5}
		}
		return len(samples), true
	})
	tap := NewTap(src, a, func(*Analyzer) { callbacks++ })
	buf := make([][2]float64, 256)
	n, ok := tap.Stream(buf)
	if n != 256 || !ok {
		t.Fatalf("stream returned %d %v", n, ok)
	}
	if buf[0] != [2]float64{0.5, -0.5} {
		t.Fatalf("audio altered: %v", buf[0])
	}
	if tap.Err() != nil {
		t.Fatal(tap.Err())
	}
}
