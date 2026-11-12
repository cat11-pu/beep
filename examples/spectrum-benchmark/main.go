// Command spectrum-benchmark compares the production spectrum.Analyzer against
// an independent naive-DFT reference implementation and writes precision,
// timing and memory reports (CSV, Markdown and an SVG memory curve) into the
// report directory.
package main

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"time"

	"github.com/faiface/beep/spectrum"
)

const (
	sampleRate  = 48000
	bandCount   = 32
	minFreq     = 20
	maxFreq     = 20000
	frameCount  = 200
	frameBudget = 1000.0 / 60.0 // ms per 60 FPS frame
)

type sizeResult struct {
	windowSize int
	// Frequency accuracy of the production analyzer.
	freqMaxErr  float64
	freqMeanErr float64
	// Frequency accuracy of the non-interpolated reference peak picker.
	refFreqMaxErr  float64
	refFreqMeanErr float64
	// Band-energy difference versus the reference, relative and absolute.
	energyMaxRel  float64
	energyMeanRel float64
	// Timing of one full analysis frame, milliseconds.
	prodP50Ms float64
	prodP99Ms float64
	refP50Ms  float64
	refP99Ms  float64
	// Steady-state allocation behavior of the production analyzer.
	allocsPerFrame int
	bytesPerFrame  int
	// Heap footprint sampled while streaming.
	heapCurve []heapPoint
}

type heapPoint struct {
	frame      int
	heapAlloc  uint64
	totalAlloc uint64
	mallocs    uint64
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	var results []sizeResult
	for _, n := range []int{1024, 2048} {
		res, err := benchmarkSize(n)
		if err != nil {
			return err
		}
		results = append(results, res)
	}

	outDir := "report"
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	if err := writeAccuracyCSV(outDir, results); err != nil {
		return err
	}
	if err := writeTimingCSV(outDir, results); err != nil {
		return err
	}
	if err := writeMemoryCSV(outDir, results); err != nil {
		return err
	}
	if err := writeMemorySVG(outDir, results); err != nil {
		return err
	}
	if err := writeMarkdown(outDir, results); err != nil {
		return err
	}
	printSummary(results)
	return nil
}

func benchmarkSize(n int) (sizeResult, error) {
	res := sizeResult{windowSize: n}
	cfg := spectrum.DefaultConfig(sampleRate, n)
	cfg.Bands, cfg.MinFreq, cfg.MaxFreq = bandCount, minFreq, maxFreq
	analyzer, err := spectrum.NewAnalyzer(cfg)
	if err != nil {
		return res, err
	}
	reference := newReferenceAnalyzer(sampleRate, n, bandCount, minFreq, maxFreq)

	// One long signal concatenating every scenario gives the ring buffer
	// realistic transitions; per-scenario windows are evaluated separately for
	// the accuracy tables.
	var freqErrs []float64
	var energyRel []float64
	var referenceFreqErrs []float64
	for _, sc := range scenarios {
		analyzer, err = spectrum.NewAnalyzer(cfg)
		if err != nil {
			return res, err
		}
		totalSamples := n + frameCount*analyzer.HopSize()
		sig := render(sampleRate, totalSamples, sc.tones)
		// Fill the ring with the first window before entering frame cadence.
		for _, v := range sig[:n] {
			analyzer.Push(v)
		}
		for f := 0; f < frameCount; f++ {
			// Both implementations analyze exactly the same window: the n
			// samples ending at n+f*hop. The production analyzer reaches it
			// purely through streaming Push calls into its ring buffer.
			end := n + f*analyzer.HopSize()
			frame := sig[end-n : end]
			refBands, refPeak, _ := reference.analyze(frame)

			if f > 0 {
				for _, v := range sig[end-analyzer.HopSize() : end] {
					analyzer.Push(v)
				}
			}
			if analyzer.FrameCount() != int64(f+1) {
				return res, fmt.Errorf("frame sync mismatch: got %d frames, want %d", analyzer.FrameCount(), f+1)
			}
			gotBands := analyzer.BandEnergies(nil)

			// Dominant-frequency error, evaluated for single-tone scenarios
			// where the true peak is unambiguous.
			if len(sc.tones) == 1 {
				trueFreq := sc.tones[0].freq
				freqErrs = append(freqErrs, math.Abs(analyzer.PeakFrequency()-trueFreq))
				// Reference uses no interpolation: record its bin-grid error
				// for the report via a parallel slice below.
				referenceFreqErrs = append(referenceFreqErrs, math.Abs(refPeak-trueFreq))
			}
			for b := range gotBands {
				if refBands[b] > 1e-12 {
					energyRel = append(energyRel, math.Abs(gotBands[b]-refBands[b])/refBands[b])
				}
			}
		}
	}
	res.freqMaxErr, res.freqMeanErr = stats(freqErrs)
	res.refFreqMaxErr, res.refFreqMeanErr = stats(referenceFreqErrs)
	res.energyMaxRel, res.energyMeanRel = stats(energyRel)

	res.prodP50Ms, res.prodP99Ms = timeProduction(analyzer, n)
	res.refP50Ms, res.refP99Ms = timeReference(reference, n)
	res.allocsPerFrame, res.bytesPerFrame = countSteadyStateAllocs(analyzer, n)
	res.heapCurve = sampleHeapCurve(analyzer, n)
	return res, nil
}

// timeProduction measures full-frame processing cost. Every iteration pushes
// exactly one hop of fresh samples, so one frame is processed per iteration.
func timeProduction(a *spectrum.Analyzer, n int) (p50, p99 float64) {
	sig := render(sampleRate, n*8, []tone{{1000.7, 0.8}})
	for i := 0; i < n; i++ {
		a.Push(sig[i])
	}
	const iters = 500
	durations := make([]time.Duration, 0, iters)
	bands := make([]float64, a.Bands())
	for i := 0; i < iters; i++ {
		start := time.Now()
		for j := 0; j < a.HopSize(); j++ {
			if a.Push(sig[(i*a.HopSize()+j)%len(sig)]) {
				a.BandEnergies(bands)
			}
		}
		durations = append(durations, time.Since(start))
	}
	return percentileMs(durations, 50), percentileMs(durations, 99)
}

func timeReference(r *referenceAnalyzer, n int) (p50, p99 float64) {
	sig := render(sampleRate, n*4, []tone{{1000.7, 0.8}})
	const iters = 30 // O(n^2) reference: keep the run time sane
	durations := make([]time.Duration, 0, iters)
	for i := 0; i < iters; i++ {
		start := time.Now()
		r.analyze(sig[:n])
		durations = append(durations, time.Since(start))
	}
	return percentileMs(durations, 50), percentileMs(durations, 99)
}

func countSteadyStateAllocs(a *spectrum.Analyzer, n int) (allocs, bytes int) {
	sig := render(sampleRate, n*4, []tone{{440, 0.8}})
	for i := 0; i < n; i++ {
		a.Push(sig[i])
	}
	bands := make([]float64, a.Bands())
	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)
	const frames = 1000
	for f := 0; f < frames; f++ {
		for j := 0; j < a.HopSize(); j++ {
			if a.Push(sig[j]) {
				a.BandEnergies(bands)
			}
		}
	}
	runtime.ReadMemStats(&m1)
	return int(m1.Mallocs-m0.Mallocs) / frames,
		int(m1.TotalAlloc-m0.TotalAlloc) / frames
}

func sampleHeapCurve(a *spectrum.Analyzer, n int) []heapPoint {
	sig := render(sampleRate, n*64, []tone{{440, 0.7}, {1760, 0.2}})
	for i := 0; i < n; i++ {
		a.Push(sig[i])
	}
	const checkpoints = 12
	curve := make([]heapPoint, 0, checkpoints)
	framesPerPoint := 1000
	bands := make([]float64, a.Bands())
	for cp := 0; cp < checkpoints; cp++ {
		for f := 0; f < framesPerPoint; f++ {
			for j := 0; j < a.HopSize(); j++ {
				if a.Push(sig[(cp*framesPerPoint*a.HopSize()+f*a.HopSize()+j)%len(sig)]) {
					a.BandEnergies(bands)
				}
			}
		}
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		curve = append(curve, heapPoint{
			frame:      (cp + 1) * framesPerPoint,
			heapAlloc:  m.HeapAlloc,
			totalAlloc: m.TotalAlloc,
			mallocs:    m.Mallocs,
		})
	}
	return curve
}

func stats(xs []float64) (max, mean float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
		if x > max {
			max = x
		}
	}
	return max, sum / float64(len(xs))
}

func percentileMs(ds []time.Duration, p float64) float64 {
	// Partial insertion sort is unnecessary at these sizes; copy and sort.
	cp := append([]time.Duration(nil), ds...)
	for i := 1; i < len(cp); i++ {
		for j := i; j > 0 && cp[j] < cp[j-1]; j-- {
			cp[j], cp[j-1] = cp[j-1], cp[j]
		}
	}
	idx := int(float64(len(cp)-1) * p / 100)
	return float64(cp[idx].Microseconds()) / 1000.0
}

func printSummary(results []sizeResult) {
	fmt.Printf("frame budget at 60 FPS: %.3f ms (one-third = %.3f ms)\n\n", frameBudget, frameBudget/3)
	for _, r := range results {
		fmt.Printf("N=%d\n", r.windowSize)
		fmt.Printf("  peak freq error   max %.4f Hz  mean %.4f Hz (reference max %.4f Hz)\n",
			r.freqMaxErr, r.freqMeanErr, r.refFreqMaxErr)
		fmt.Printf("  band energy error max %.2e  mean %.2e (relative)\n", r.energyMaxRel, r.energyMeanRel)
		fmt.Printf("  production timing p50 %.4f ms  p99 %.4f ms  (%.1f%% of frame budget)\n",
			r.prodP50Ms, r.prodP99Ms, 100*r.prodP99Ms/frameBudget)
		fmt.Printf("  reference timing  p50 %.4f ms  p99 %.4f ms\n", r.refP50Ms, r.refP99Ms)
		fmt.Printf("  steady-state      %d allocs/frame, %d bytes/frame\n", r.allocsPerFrame, r.bytesPerFrame)
		fmt.Printf("  heap start->end   %d -> %d bytes\n", r.heapCurve[0].heapAlloc, r.heapCurve[len(r.heapCurve)-1].heapAlloc)
	}
}
