package main

import (
	"fmt"
	"math"
	"os"
	"strings"
)

const (
	plotW = 1100
	plotH = 460
)

var (
	colorRaw     = "#d9534f" // red: clipped raw sum
	colorLimited = "#2e8b57" // green: limited output
	colorCeiling = "#555555"
	colorGrid    = "#dddddd"
	colorGR      = "#8a5cf6"
)

// writeWaveformPlot renders the raw and limited waveforms on shared axes, with the -1 dBFS
// ceiling and the full-scale clip lines marked.
func writeWaveformPlot(path string, raw, limited [][2]float64) error {
	var b strings.Builder
	b.WriteString(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`, plotW, plotH, plotW, plotH))
	b.WriteString(`<rect width="100%" height="100%" fill="white"/>`)

	left, top, right, bottom := 70.0, 40.0, float64(plotW)-30, float64(plotH)-50
	midY := (top + bottom) / 2
	ampH := (bottom - top) / 2

	// horizontal gridlines at 0, -1, -6, -12, -24 dBFS and full scale
	for _, db := range []float64{0, -1, -6, -12, -24} {
		y := midY - ampH*dbToAmp(db)
		col := colorGrid
		width := 1.0
		if db == 0 {
			col, width = colorRaw, 1.6
		}
		if db == -1 {
			col, width = colorCeiling, 1.6
		}
		b.WriteString(fmt.Sprintf(`<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="%.1f" stroke-dasharray="%s"/>`,
			left, y, right, y, col, width, map[bool]string{true: "6 4", false: ""}[db == -1 || db == 0]))
		label := fmt.Sprintf("%g dBFS", db)
		b.WriteString(fmt.Sprintf(`<text x="%.1f" y="%.1f" font-size="12" fill="%s" text-anchor="end">%s</text>`, left-6, y+4, col, label))
	}

	drawWave := func(data [][2]float64, col string, opacity float64) {
		var pts []string
		n := len(data)
		for px := left; px < right; px++ {
			i := int(float64(px-left) / (right - left) * float64(n))
			v := 0.5 * (data[i][0] + data[i][1])
			v = math.Max(-1.2, math.Min(1.2, v))
			y := midY - ampH*v
			pts = append(pts, fmt.Sprintf("%.1f,%.1f", px, y))
		}
		b.WriteString(fmt.Sprintf(`<polyline points="%s" fill="none" stroke="%s" stroke-width="1" opacity="%.2f"/>`,
			strings.Join(pts, " "), col, opacity))
	}
	drawWave(raw, colorRaw, 0.85)
	drawWave(limited, colorLimited, 0.95)

	// time axis labels
	for s := 0; s <= int(durationS); s++ {
		x := left + (right-left)*float64(s)/durationS
		b.WriteString(fmt.Sprintf(`<text x="%.1f" y="%.1f" font-size="12" fill="#333" text-anchor="middle">%ds</text>`, x, bottom+18, s))
	}

	legend(&b, left, 14, []legendItem{
		{colorRaw, "raw 32-channel sum (hard-clipped at +/-1)"},
		{colorLimited, "soft-limited output (ceiling -1 dBFS)"},
		{colorCeiling, "-1 dBFS ceiling"},
	})
	b.WriteString(`</svg>`)
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// writeBandPlot draws grouped bars of per-band spectral energy for raw vs limited.
func writeBandPlot(path string, rawM, limM Metrics) error {
	var b strings.Builder
	b.WriteString(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`, plotW, plotH, plotW, plotH))
	b.WriteString(`<rect width="100%" height="100%" fill="white"/>`)

	left, top, right, bottom := 70.0, 50.0, float64(plotW)-30, float64(plotH)-90
	// energy axis from -60 dB to 0 dB (relative to loudest band)
	yFor := func(db float64) float64 {
		return top + (bottom-top)*(0-db)/60
	}
	for db := 0; db >= -60; db -= 10 {
		y := yFor(float64(db))
		b.WriteString(fmt.Sprintf(`<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s"/>`, left, y, right, y, colorGrid))
		b.WriteString(fmt.Sprintf(`<text x="%.1f" y="%.1f" font-size="12" fill="#333" text-anchor="end">%d dB</text>`, left-6, y+4, db))
	}

	groupW := (right - left) / float64(len(bandEdges))
	barW := groupW * 0.36
	for i := range bandEdges {
		gx := left + groupW*float64(i)
		rawY := yFor(rawM.BandEnergyDB[i])
		limY := yFor(limM.BandEnergyDB[i])
		b.WriteString(fmt.Sprintf(`<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="%s"/>`,
			gx+groupW*0.12, rawY, barW, bottom-rawY, colorRaw))
		b.WriteString(fmt.Sprintf(`<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="%s"/>`,
			gx+groupW*0.52, limY, barW, bottom-limY, colorLimited))
		b.WriteString(fmt.Sprintf(`<text x="%.1f" y="%.1f" font-size="11" fill="#333" text-anchor="middle" transform="rotate(-25 %.1f %.1f)">%s</text>`,
			gx+groupW/2, bottom+18, gx+groupW/2, bottom+18, shortBand(bandEdges[i].name)))
	}
	legend(&b, left, 22, []legendItem{
		{colorRaw, "raw mix band energy"},
		{colorLimited, "limited mix band energy"},
	})
	b.WriteString(fmt.Sprintf(`<text x="70" y="%d" font-size="13" fill="#333">Energy relative to loudest band (dB)</text>`, plotH-18))
	b.WriteString(`</svg>`)
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// writeGainReductionPlot shows the limiter's actual per-chunk gain reduction over time, sampled
// from Limiter.GainReduction. Conventional meter orientation is used: 0 dB (no limiting) at the
// top and increasing reduction downward.
func writeGainReductionPlot(path string, grCurve []float64, maxGR float64) error {
	var b strings.Builder
	b.WriteString(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`, plotW, plotH, plotW, plotH))
	b.WriteString(`<rect width="100%" height="100%" fill="white"/>`)

	left, top, right, bottom := 70.0, 40.0, float64(plotW)-30, float64(plotH)-50
	if maxGR < 20 {
		maxGR = 20
	}
	yFor := func(gr float64) float64 {
		return top + (bottom-top)*(gr/maxGR)
	}
	for gr := 0; gr <= int(maxGR); gr += 5 {
		y := yFor(float64(gr))
		b.WriteString(fmt.Sprintf(`<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s"/>`, left, y, right, y, colorGrid))
		b.WriteString(fmt.Sprintf(`<text x="%.1f" y="%.1f" font-size="12" fill="#333" text-anchor="end">%d dB</text>`, left-6, y+4, gr))
	}

	var pts []string
	for i, gr := range grCurve {
		if gr < 0 {
			gr = 0
		}
		if gr > maxGR {
			gr = maxGR
		}
		x := left + (right-left)*float64(i)/float64(len(grCurve))
		pts = append(pts, fmt.Sprintf("%.1f,%.1f", x, yFor(gr)))
	}
	// fill under the curve
	b.WriteString(fmt.Sprintf(`<polygon points="%.1f,%.1f %s %.1f,%.1f" fill="%s" opacity="0.15"/>`,
		left, bottom, strings.Join(pts, " "), right, bottom, colorGR))
	b.WriteString(fmt.Sprintf(`<polyline points="%s" fill="none" stroke="%s" stroke-width="1.4"/>`, strings.Join(pts, " "), colorGR))
	for s := 0; s <= int(durationS); s++ {
		x := left + (right-left)*float64(s)/durationS
		b.WriteString(fmt.Sprintf(`<text x="%.1f" y="%.1f" font-size="12" fill="#333" text-anchor="middle">%ds</text>`, x, bottom+18, s))
	}
	b.WriteString(fmt.Sprintf(`<text x="70" y="%d" font-size="13" fill="#333">Peak-limiter gain reduction (0 dB at top); max %.1f dB</text>`, plotH-18, maxGR))
	b.WriteString(`</svg>`)
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

type legendItem struct {
	color string
	text  string
}

func legend(b *strings.Builder, x, y float64, items []legendItem) {
	for i, it := range items {
		ix := x + float64(i)*330
		b.WriteString(fmt.Sprintf(`<rect x="%.1f" y="%.1f" width="14" height="14" fill="%s"/>`, ix, y-11, it.color))
		b.WriteString(fmt.Sprintf(`<text x="%.1f" y="%.1f" font-size="13" fill="#333">%s</text>`, ix+20, y, it.text))
	}
}

func dbToAmp(db float64) float64 {
	return math.Pow(10, db/20)
}

func shortBand(name string) string {
	if i := strings.Index(name, " "); i > 0 {
		return name[i+1:]
	}
	return name
}
