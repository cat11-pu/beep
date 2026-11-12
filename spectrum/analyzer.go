// Package spectrum provides a real-time, zero-allocation spectrum analyzer for
// audio visualization.
//
// The analyzer accepts mono or interleaved stereo samples through Push, keeps
// a rolling ring buffer and, whenever a full frame has been collected, applies
// a configurable window function, runs an in-place radix-2 FFT and aggregates
// bin powers into logarithmically spaced frequency bands. All working memory
// is allocated once at construction; Push and the processing pipeline never
// allocate on the heap while running.
package spectrum

import (
	"errors"
	"math"
)

// Config configures an Analyzer. The zero value is not usable; create one with
// DefaultConfig and adjust the fields.
type Config struct {
	// SampleRate is the input sample rate in Hz.
	SampleRate float64
	// WindowSize is the FFT frame length. Only 1024 and 2048 are supported.
	WindowSize int
	// HopSize is the distance between two consecutive analysis frames. When
	// zero it defaults to WindowSize/2 (50% overlap).
	HopSize int
	// Window selects the window function. Zero means WindowHann.
	Window Window
	// Bands is the number of logarithmically spaced output bands.
	Bands int
	// MinFreq and MaxFreq define the aggregated frequency range in Hz.
	// Frequencies below MinFreq or above MaxFreq are excluded from the bands.
	MinFreq float64
	MaxFreq float64
}

// DefaultConfig returns a configuration with the documented defaults: Hann
// window, 50% overlap, 32 log bands spanning 20 Hz to 20 kHz.
func DefaultConfig(sampleRate float64, windowSize int) Config {
	return Config{
		SampleRate: sampleRate,
		WindowSize: windowSize,
		Window:     WindowHann,
		Bands:      32,
		MinFreq:    20,
		MaxFreq:    20000,
	}
}

// Analyzer turns a stream of audio samples into per-frame band energies.
//
// An Analyzer is not safe for concurrent use.
type Analyzer struct {
	cfg      Config
	fft      *fftPlan
	window   windowTable
	ring     []float64
	frame    []complex128
	powers   []float64
	mag      []float64
	bands    []float64
	bandLow  []int
	bandHigh []int
	edges    []float64

	ringPos   int
	ringCount int
	pending   int
	frames    int64

	peakBin  int
	peakFreq float64
	peakPow  float64
}

// NewAnalyzer validates the configuration and preallocates every buffer the
// analyzer will ever need. No further allocation happens during processing.
func NewAnalyzer(cfg Config) (*Analyzer, error) {
	if cfg.SampleRate <= 0 {
		return nil, errors.New("spectrum: SampleRate must be positive")
	}
	if cfg.WindowSize != 1024 && cfg.WindowSize != 2048 {
		return nil, errors.New("spectrum: WindowSize must be 1024 or 2048")
	}
	if !cfg.Window.valid() {
		return nil, errors.New("spectrum: unknown Window")
	}
	if cfg.Bands <= 0 {
		return nil, errors.New("spectrum: Bands must be positive")
	}
	nyquist := cfg.SampleRate / 2
	if cfg.MinFreq < 0 || cfg.MaxFreq <= cfg.MinFreq || cfg.MaxFreq > nyquist {
		return nil, errors.New("spectrum: invalid MinFreq/MaxFreq range")
	}
	if cfg.HopSize < 0 || cfg.HopSize > cfg.WindowSize {
		return nil, errors.New("spectrum: HopSize must be within [0, WindowSize]")
	}
	if cfg.HopSize == 0 {
		cfg.HopSize = cfg.WindowSize / 2
	}

	n := cfg.WindowSize
	a := &Analyzer{
		cfg:      cfg,
		fft:      newFFTPlan(n),
		window:   newWindowTable(cfg.Window, n),
		ring:     make([]float64, n),
		frame:    make([]complex128, n),
		powers:   make([]float64, n/2+1),
		mag:      make([]float64, n/2+1),
		bands:    make([]float64, cfg.Bands),
		bandLow:  make([]int, cfg.Bands),
		bandHigh: make([]int, cfg.Bands),
		edges:    make([]float64, cfg.Bands+1),
	}
	a.buildBands()
	return a, nil
}

// buildBands maps log-spaced frequency edges to FFT bin index ranges.
func (a *Analyzer) buildBands() {
	n := a.cfg.WindowSize
	binHz := a.cfg.SampleRate / float64(n)
	logMin := math.Log(a.cfg.MinFreq)
	logMax := math.Log(a.cfg.MaxFreq)
	lastHigh := -1
	for b := 0; b <= a.cfg.Bands; b++ {
		f := math.Exp(logMin + (logMax-logMin)*float64(b)/float64(a.cfg.Bands))
		a.edges[b] = f
	}
	for b := 0; b < a.cfg.Bands; b++ {
		// Half-open interval [edge[b], edge[b+1]); the last band is closed.
		low := int(math.Ceil(a.edges[b]/binHz - 0.5))
		high := int(math.Ceil(a.edges[b+1]/binHz - 0.5))
		if low < 1 {
			low = 1 // skip DC
		}
		if high > n/2 {
			high = n / 2
		}
		if low <= lastHigh {
			low = lastHigh + 1
		}
		if high < low {
			high = low
		}
		a.bandLow[b] = low
		a.bandHigh[b] = high
		lastHigh = high
	}
}

// Push feeds one mono sample to the analyzer. It returns true when a new frame
// has been analyzed, in which case BandEnergies, PeakFrequency and friends
// expose the fresh results.
func (a *Analyzer) Push(sample float64) bool {
	a.ring[a.ringPos] = sample
	a.ringPos++
	if a.ringPos == a.cfg.WindowSize {
		a.ringPos = 0
	}
	if a.ringCount < a.cfg.WindowSize {
		a.ringCount++
	}
	a.pending++
	if a.pending < a.cfg.HopSize || a.ringCount < a.cfg.WindowSize {
		return false
	}
	a.pending = 0
	a.process()
	return true
}

// PushStereo feeds one interleaved stereo frame (left, right). The channels are
// averaged to mono before analysis.
func (a *Analyzer) PushStereo(left, right float64) bool {
	return a.Push((left + right) * 0.5)
}

// process runs the full window -> FFT -> power -> bands -> peak pipeline using
// only preallocated buffers.
func (a *Analyzer) process() {
	n := a.cfg.WindowSize
	start := (a.ringPos - n + n) % n
	for i := 0; i < n; i++ {
		v := a.ring[(start+i)%n] * a.window.values[i]
		a.frame[i] = complex(v, 0)
	}
	a.fft.transform(a.frame)

	norm := 2.0 / (float64(n) * a.window.s2)
	for k := 0; k <= n/2; k++ {
		z := a.frame[k]
		power := (real(z)*real(z) + imag(z)*imag(z)) * norm
		if k == 0 || k == n/2 {
			power *= 0.5 // DC and Nyquist are not doubled in one-sided PSD
		}
		a.powers[k] = power
		a.mag[k] = math.Sqrt(power)
	}

	for b := range a.bands {
		var sum float64
		for k := a.bandLow[b]; k <= a.bandHigh[b]; k++ {
			sum += a.powers[k]
		}
		a.bands[b] = sum
	}

	a.findPeak()
	a.frames++
}

// findPeak locates the dominant non-DC bin and refines its frequency with a
// three-point log-parabolic interpolator. Hann frames additionally apply a
// tabulated bias correction, keeping single-tone error well below half a hertz
// for both supported frame sizes at common audio sample rates.
func (a *Analyzer) findPeak() {
	n := a.cfg.WindowSize
	peak := 1
	best := a.mag[1]
	for k := 2; k < n/2; k++ {
		if a.mag[k] > best {
			best = a.mag[k]
			peak = k
		}
	}
	a.peakBin = peak
	a.peakPow = a.powers[peak]
	if best == 0 || peak == 1 || peak == n/2-1 {
		a.peakFreq = float64(peak) * a.cfg.SampleRate / float64(n)
		return
	}
	binHz := a.cfg.SampleRate / float64(n)
	a.peakFreq = (float64(peak) + a.peakDelta(peak)) * binHz
}

// BandEnergies writes the most recent per-band energies (one-sided signal
// power, corrected for window gain) into dst and returns it. If dst is nil or
// too short a fresh slice is allocated; passing a reused slice keeps the call
// allocation-free.
func (a *Analyzer) BandEnergies(dst []float64) []float64 {
	if cap(dst) < len(a.bands) {
		dst = make([]float64, len(a.bands))
	}
	dst = dst[:len(a.bands)]
	copy(dst, a.bands)
	return dst
}

// Powers writes the raw one-sided bin powers into dst, following the same
// reuse rule as BandEnergies.
func (a *Analyzer) Powers(dst []float64) []float64 {
	if cap(dst) < len(a.powers) {
		dst = make([]float64, len(a.powers))
	}
	dst = dst[:len(a.powers)]
	copy(dst, a.powers)
	return dst
}

// PeakFrequency returns the interpolated frequency, in Hz, of the dominant
// component in the most recent frame.
func (a *Analyzer) PeakFrequency() float64 { return a.peakFreq }

// PeakPower returns the power of the dominant bin.
func (a *Analyzer) PeakPower() float64 { return a.peakPow }

// PeakBin returns the un-interpolated index of the dominant bin.
func (a *Analyzer) PeakBin() int { return a.peakBin }

// BandEdges returns the BandCount+1 log-spaced edge frequencies in Hz.
func (a *Analyzer) BandEdges() []float64 { return a.edges }

// BandCenterFreq returns the geometric center frequency of band b.
func (a *Analyzer) BandCenterFreq(b int) float64 {
	return math.Sqrt(a.edges[b] * a.edges[b+1])
}

// FrameCount is the number of frames analyzed so far.
func (a *Analyzer) FrameCount() int64 { return a.frames }

// WindowSize returns the configured frame length.
func (a *Analyzer) WindowSize() int { return a.cfg.WindowSize }

// HopSize returns the configured hop between frames.
func (a *Analyzer) HopSize() int { return a.cfg.HopSize }

// Bands returns the number of output bands.
func (a *Analyzer) Bands() int { return a.cfg.Bands }

// SampleRate returns the configured sample rate.
func (a *Analyzer) SampleRate() float64 { return a.cfg.SampleRate }
