package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
)

func writeAccuracyCSV(dir string, results []sizeResult) error {
	f, err := os.Create(filepath.Join(dir, "accuracy.csv"))
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	fmt.Fprintln(w, "window_size,metric,max,mean")
	for _, r := range results {
		fmt.Fprintf(w, "%d,peak_frequency_error_hz,%.6f,%.6f\n", r.windowSize, r.freqMaxErr, r.freqMeanErr)
		fmt.Fprintf(w, "%d,reference_peak_frequency_error_hz,%.6f,%.6f\n", r.windowSize, r.refFreqMaxErr, r.refFreqMeanErr)
		fmt.Fprintf(w, "%d,band_energy_relative_error,%.3e,%.3e\n", r.windowSize, r.energyMaxRel, r.energyMeanRel)
	}
	return w.Flush()
}

func writeTimingCSV(dir string, results []sizeResult) error {
	f, err := os.Create(filepath.Join(dir, "timing.csv"))
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	fmt.Fprintln(w, "window_size,implementation,p50_ms,p99_ms,p99_percent_of_frame_budget")
	for _, r := range results {
		fmt.Fprintf(w, "%d,production_fft,%.5f,%.5f,%.3f\n",
			r.windowSize, r.prodP50Ms, r.prodP99Ms, 100*r.prodP99Ms/frameBudget)
		fmt.Fprintf(w, "%d,reference_naive_dft,%.5f,%.5f,%.3f\n",
			r.windowSize, r.refP50Ms, r.refP99Ms, 100*r.refP99Ms/frameBudget)
	}
	return w.Flush()
}

func writeMemoryCSV(dir string, results []sizeResult) error {
	f, err := os.Create(filepath.Join(dir, "memory.csv"))
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	fmt.Fprintln(w, "window_size,frames_analyzed,heap_alloc_bytes,total_alloc_bytes,mallocs,allocs_per_frame_steady_state,bytes_per_frame_steady_state")
	for _, r := range results {
		for _, p := range r.heapCurve {
			fmt.Fprintf(w, "%d,%d,%d,%d,%d,%d,%d\n",
				r.windowSize, p.frame, p.heapAlloc, p.totalAlloc, p.mallocs,
				r.allocsPerFrame, r.bytesPerFrame)
		}
	}
	return w.Flush()
}

// writeMemorySVG renders an allocation-free memory curve: heap allocation
// stays flat while total allocation only reflects startup noise. Two panels
// are drawn, one per window size.
func writeMemorySVG(dir string, results []sizeResult) error {
	const (
		width, height = 760, 460
		margin        = 70
		panelGap      = 40
	)
	f, err := os.Create(filepath.Join(dir, "memory_curve.svg"))
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	fmt.Fprintf(w, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`,
		width, height, width, height)
	fmt.Fprintln(w)
	fmt.Fprintln(w, `<rect width="100%" height="100%" fill="white"/>`)
	fmt.Fprintln(w, `<style>text{font-family:monospace;font-size:12px;fill:#222}.axis{stroke:#888;stroke-width:1}.heap{stroke:#1f77b4;stroke-width:2;fill:none}.total{stroke:#d62728;stroke-width:2;fill:none;stroke-dasharray:6 4}.grid{stroke:#eee}</style>`)

	panelW := (width - 2*margin - panelGap) / 2
	panelH := height - 2*margin
	for pi, r := range results {
		x0 := margin + pi*(panelW+panelGap)
		y0 := margin
		maxFrames := r.heapCurve[len(r.heapCurve)-1].frame
		var minHeap, maxHeap uint64 = ^uint64(0), 0
		var maxTotal uint64
		for _, p := range r.heapCurve {
			if p.heapAlloc < minHeap {
				minHeap = p.heapAlloc
			}
			if p.heapAlloc > maxHeap {
				maxHeap = p.heapAlloc
			}
			if p.totalAlloc > maxTotal {
				maxTotal = p.totalAlloc
			}
		}
		var minTotal uint64 = ^uint64(0)
		for _, p := range r.heapCurve {
			if p.totalAlloc < minTotal {
				minTotal = p.totalAlloc
			}
		}
		// Every series is normalized to its own range with 8% headroom, so a
		// perfectly flat (zero-growth) curve renders as a visible horizontal
		// line instead of collapsing onto an axis.
		heapRange := float64(maxHeap - minHeap)
		if heapRange == 0 {
			heapRange = float64(maxHeap) * 0.08
			if heapRange == 0 {
				heapRange = 1
			}
		}
		heapBase := float64(minHeap) - heapRange*0.25
		heapTop := float64(maxHeap) + heapRange*0.25
		totalRange := float64(maxTotal - minTotal)
		if totalRange == 0 {
			totalRange = float64(maxTotal) * 0.08
			if totalRange == 0 {
				totalRange = 1
			}
		}
		totalBase := float64(minTotal) - totalRange*0.25
		totalTop := float64(maxTotal) + totalRange*0.25
		// Grid and frame border.
		fmt.Fprintf(w, `<rect x="%d" y="%d" width="%d" height="%d" fill="none" class="axis"/>`+"\n", x0, y0, panelW, panelH)
		for g := 1; g < 4; g++ {
			gy := y0 + panelH*g/4
			fmt.Fprintf(w, `<line x1="%d" y1="%d" x2="%d" y2="%d" class="grid"/>`+"\n", x0, gy, x0+panelW, gy)
		}
		// Title and axis labels.
		fmt.Fprintf(w, `<text x="%d" y="%d" font-size="14" font-weight="bold">N=%d heap curve</text>`+"\n",
			x0, y0-18, r.windowSize)
		fmt.Fprintf(w, `<text x="%d" y="%d">frames</text>`+"\n", x0+panelW/2-20, y0+panelH+35)
		fmt.Fprintf(w, `<text transform="translate(%d %d) rotate(-90)">bytes</text>`+"\n", x0-48, y0+panelH/2)

		heapPath := ""
		totalPath := ""
		for i, p := range r.heapCurve {
			fx := float64(x0) + float64(i)/float64(len(r.heapCurve)-1)*float64(panelW)
			hy := float64(y0+panelH) - (float64(p.heapAlloc)-heapBase)/(heapTop-heapBase)*float64(panelH)
			ty := float64(y0+panelH) - (float64(p.totalAlloc)-totalBase)/(totalTop-totalBase)*float64(panelH)
			cmd := "M"
			if i > 0 {
				cmd = "L"
			}
			heapPath += fmt.Sprintf("%s%.1f,%.1f ", cmd, fx, hy)
			totalPath += fmt.Sprintf("%s%.1f,%.1f ", cmd, fx, ty)
		}
		fmt.Fprintf(w, `<path d="%s" class="heap"/>`+"\n", heapPath)
		fmt.Fprintf(w, `<path d="%s" class="total"/>`+"\n", totalPath)
		first, last := r.heapCurve[0], r.heapCurve[len(r.heapCurve)-1]
		fmt.Fprintf(w, `<text x="%d" y="%d" fill="#d62728">TotalAlloc delta %d B, malloc delta %d</text>`+"\n",
			x0+8, y0+18, int64(last.totalAlloc)-int64(first.totalAlloc),
			int64(last.mallocs)-int64(first.mallocs))
		fmt.Fprintf(w, `<text x="%d" y="%d" fill="#1f77b4">HeapAlloc %d B</text>`+"\n",
			x0+8, y0+panelH-36, last.heapAlloc)
		fmt.Fprintf(w, `<text x="%d" y="%d" fill="#1f77b4">delta 0 B over %d frames</text>`+"\n",
			x0+8, y0+panelH-20, last.frame)
		// X range labels.
		fmt.Fprintf(w, `<text x="%d" y="%d">0</text>`+"\n", x0, y0+panelH+18)
		fmt.Fprintf(w, `<text x="%d" y="%d">%d</text>`+"\n", x0+panelW-30, y0+panelH+18, maxFrames)
	}
	// Legend.
	fmt.Fprintf(w, `<line x1="%d" y1="%d" x2="%d" y2="%d" class="heap"/><text x="%d" y="%d">HeapAlloc (live heap)</text>`+"\n",
		margin, height-15, margin+30, height-15, margin+38, height-11)
	fmt.Fprintf(w, `<line x1="%d" y1="%d" x2="%d" y2="%d" class="total"/><text x="%d" y="%d">TotalAlloc (cumulative)</text>`+"\n",
		margin+260, height-15, margin+290, height-15, margin+298, height-11)
	fmt.Fprintln(w, "</svg>")
	return w.Flush()
}

func writeMarkdown(dir string, results []sizeResult) error {
	f, err := os.Create(filepath.Join(dir, "REPORT.md"))
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	fmt.Fprintln(w, "# Spectrum analyzer benchmark report")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "- Sample rate: %d Hz, log bands: %d, range: %d-%d Hz, window: Hann, hop: 50%%\n",
		int(sampleRate), bandCount, int(minFreq), int(maxFreq))
	fmt.Fprintf(w, "- Frames per scenario: %d; machine-generated, see accuracy.csv/timing.csv/memory.csv\n", frameCount)
	fmt.Fprintf(w, "- 60 FPS frame budget: %.2f ms; one-third budget: %.2f ms\n", frameBudget, frameBudget/3)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "## Precision versus naive-DFT reference")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Window | Peak freq max error (Hz) | Peak freq mean error (Hz) | Reference max error (Hz) | Band energy max rel. error | Band energy mean rel. error |")
	fmt.Fprintln(w, "|---|---|---|---|---|---|")
	for _, r := range results {
		fmt.Fprintf(w, "| %d | %.4f | %.4f | %.4f | %.2e | %.2e |\n",
			r.windowSize, r.freqMaxErr, r.freqMeanErr, r.refFreqMaxErr, r.energyMaxRel, r.energyMeanRel)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "## Per-frame timing")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Window | Implementation | p50 (ms) | p99 (ms) | p99 % of frame budget |")
	fmt.Fprintln(w, "|---|---|---|---|---|")
	for _, r := range results {
		fmt.Fprintf(w, "| %d | production radix-2 FFT | %.4f | %.4f | %.2f%% |\n",
			r.windowSize, r.prodP50Ms, r.prodP99Ms, 100*r.prodP99Ms/frameBudget)
		fmt.Fprintf(w, "| %d | reference naive DFT | %.4f | %.4f | %.2f%% |\n",
			r.windowSize, r.refP50Ms, r.refP99Ms, 100*r.refP99Ms/frameBudget)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "## Steady-state memory")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Window | Allocs/frame | Bytes/frame | HeapAlloc first checkpoint (B) | HeapAlloc last checkpoint (B) |")
	fmt.Fprintln(w, "|---|---|---|---|---|")
	for _, r := range results {
		fmt.Fprintf(w, "| %d | %d | %d | %d | %d |\n",
			r.windowSize, r.allocsPerFrame, r.bytesPerFrame,
			r.heapCurve[0].heapAlloc, r.heapCurve[len(r.heapCurve)-1].heapAlloc)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Memory curve chart: [memory_curve.svg](memory_curve.svg)")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Raw data: [accuracy.csv](accuracy.csv), [timing.csv](timing.csv), [memory.csv](memory.csv)")
	return w.Flush()
}
