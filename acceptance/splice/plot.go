package splice

import (
	"fmt"
	"io"
	"strings"
)

// PlotConfig controls the evidence waveform plot.
type PlotConfig struct {
	Width  int
	Height int
	// Window is the number of samples shown on each side of the splice point.
	Window int
}

// WriteWaveformPlot renders a before/after waveform comparison as an SVG. It shows the
// raw concatenation of the two decoded (delay-bearing) segments above the compensated
// splice result below, plus a zoomed view of the junction, with the splice point marked
// on every trace.
func WriteWaveformPlot(w io.Writer, r Result, first, second Segment, cfg PlotConfig) error {
	if cfg.Width == 0 {
		cfg.Width = 1000
	}
	if cfg.Height == 0 {
		cfg.Height = 740
	}
	if cfg.Window == 0 || cfg.Window > len(r.Samples)/2 {
		cfg.Window = len(r.Samples) / 2
	}

	raw := make([][2]float64, 0, len(first.Decoded)+len(second.Decoded))
	raw = append(raw, first.Decoded...)
	raw = append(raw, second.Decoded...)
	rawSpliceIdx := len(first.Decoded)

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`,
		cfg.Width, cfg.Height, cfg.Width, cfg.Height)
	b.WriteString(`<rect width="100%" height="100%" fill="white"/>`)
	b.WriteString(`<style>text{font-family:monospace;font-size:14px;fill:#222}.axis{stroke:#888;stroke-width:1}.trace{fill:none;stroke:#1f6feb;stroke-width:1.5}.raw{stroke:#d1242f}.mark{stroke:#e3b341;stroke-width:2;stroke-dasharray:6 4}.zero{stroke:#ccc;stroke-width:1}</style>`)

	pad := 60
	panelH := (cfg.Height - 4*pad) / 3

	drawPanel(&b, "Before: raw decoded concat (encoder delay uncompensated)", raw,
		rawSpliceIdx, pad, pad, cfg.Width-2*pad, panelH, cfg.Window, true)
	drawPanel(&b, "After: delay-compensated splice", r.Samples,
		r.SpliceIndex, pad, 2*pad+panelH, cfg.Width-2*pad, panelH, cfg.Window, false)
	drawPanel(&b, "Junction zoom: 60 samples either side of the splice point", r.Samples,
		r.SpliceIndex, pad, 3*pad+2*panelH, cfg.Width-2*pad, panelH, 60, false)

	b.WriteString(`</svg>`)
	_, err := io.WriteString(w, b.String())
	return err
}

func drawPanel(b *strings.Builder, title string, samples [][2]float64, spliceIdx, x, y, w, h, window int, raw bool) {
	fmt.Fprintf(b, `<text x="%d" y="%d">%s</text>`, x, y-12, title)

	// Panel frame and zero line.
	fmt.Fprintf(b, `<line class="axis" x1="%d" y1="%d" x2="%d" y2="%d"/>`, x, y, x, y+h)
	fmt.Fprintf(b, `<line class="axis" x1="%d" y1="%d" x2="%d" y2="%d"/>`, x, y+h, x+w, y+h)
	zeroY := y + h/2
	fmt.Fprintf(b, `<line class="zero" x1="%d" y1="%d" x2="%d" y2="%d"/>`, x, zeroY, x+w, zeroY)

	// Center the view on the splice point so the junction is visible in both panels
	// even when the two streams have different lengths/offsets.
	start := spliceIdx - window
	end := spliceIdx + window
	if start < 0 {
		end -= start
		start = 0
	}
	if end > len(samples) {
		overflow := end - len(samples)
		end = len(samples)
		start -= overflow
		if start < 0 {
			start = 0
		}
	}
	span := end - start
	if span <= 1 {
		return
	}

	xScale := float64(w) / float64(span-1)
	yScale := float64(h) / 2.0 / 1.05

	cls := "trace"
	if raw {
		cls += " raw"
	}
	fmt.Fprintf(b, `<path class="%s" d="M `, cls)
	for i := start; i < end; i++ {
		px := x + int(float64(i-start)*xScale)
		py := zeroY - int(samples[i][0]*yScale)
		if i > start {
			b.WriteString("L ")
		}
		fmt.Fprintf(b, "%d %d ", px, py)
	}
	b.WriteString(`"/>`)

	// Splice point marker.
	markX := x + int(float64(spliceIdx-start)*xScale)
	fmt.Fprintf(b, `<line class="mark" x1="%d" y1="%d" x2="%d" y2="%d"/>`, markX, y, markX, y+h)
	fmt.Fprintf(b, `<text x="%d" y="%d">splice @%d</text>`, markX+6, y+16, spliceIdx)
}
