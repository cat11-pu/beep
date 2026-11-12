package main

import "math"

// referenceAnalyzer is an independent, deliberately straightforward spectrum
// implementation used as the comparison baseline for the production
// spectrum.Analyzer. It recomputes every DFT coefficient directly (O(n^2)) and
// allocates fresh slices on every frame, which is exactly the behavior a
// real-time visualizer must avoid.
type referenceAnalyzer struct {
	sampleRate float64
	windowSize int
	window     []float64
	windowS2   float64
	bands      int
	minFreq    float64
	maxFreq    float64
	bandLow    []int
	bandHigh   []int
	edges      []float64
}

func newReferenceAnalyzer(sampleRate float64, windowSize, bands int, minFreq, maxFreq float64) *referenceAnalyzer {
	r := &referenceAnalyzer{
		sampleRate: sampleRate,
		windowSize: windowSize,
		window:     make([]float64, windowSize),
		bands:      bands,
		minFreq:    minFreq,
		maxFreq:    maxFreq,
		bandLow:    make([]int, bands),
		bandHigh:   make([]int, bands),
		edges:      make([]float64, bands+1),
	}
	for i := range r.window {
		w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(windowSize))
		r.window[i] = w
		r.windowS2 += w * w
	}
	binHz := sampleRate / float64(windowSize)
	logMin, logMax := math.Log(minFreq), math.Log(maxFreq)
	lastHigh := -1
	for b := 0; b <= bands; b++ {
		r.edges[b] = math.Exp(logMin + (logMax-logMin)*float64(b)/float64(bands))
	}
	for b := 0; b < bands; b++ {
		low := int(math.Ceil(r.edges[b]/binHz - 0.5))
		high := int(math.Ceil(r.edges[b+1]/binHz - 0.5))
		if low < 1 {
			low = 1
		}
		if high > windowSize/2 {
			high = windowSize / 2
		}
		if low <= lastHigh {
			low = lastHigh + 1
		}
		if high < low {
			high = low
		}
		r.bandLow[b], r.bandHigh[b] = low, high
		lastHigh = high
	}
	return r
}

// analyze runs one frame and returns band energies, the dominant-bin frequency
// found by picking the largest raw DFT bin (no interpolation), and the raw
// one-sided bin powers.
func (r *referenceAnalyzer) analyze(frame []float64) (bands []float64, peakFreq float64, powers []float64) {
	n := r.windowSize
	windowed := make([]float64, n)
	for i := 0; i < n; i++ {
		windowed[i] = frame[i] * r.window[i]
	}
	powers = make([]float64, n/2+1)
	peakBin, peakMag := 1, 0.0
	norm := 2.0 / (float64(n) * r.windowS2)
	for k := 0; k <= n/2; k++ {
		var re, im float64
		angle := -2 * math.Pi * float64(k) / float64(n)
		for j, v := range windowed {
			c, s := math.Cos(angle*float64(j)), math.Sin(angle*float64(j))
			re += v * c
			im += v * s
		}
		p := (re*re + im*im) * norm
		if k == 0 || k == n/2 {
			p *= 0.5
		}
		powers[k] = p
		if k >= 1 && k < n/2 && math.Sqrt(p) > peakMag {
			peakMag = math.Sqrt(p)
			peakBin = k
		}
	}
	bands = make([]float64, r.bands)
	for b := range bands {
		var sum float64
		for k := r.bandLow[b]; k <= r.bandHigh[b]; k++ {
			sum += powers[k]
		}
		bands[b] = sum
	}
	peakFreq = float64(peakBin) * r.sampleRate / float64(n)
	return bands, peakFreq, powers
}
