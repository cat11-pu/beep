package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

const (
	plotW  = 900
	plotH  = 520
	margin = 60
)

// plotCurve renders a single y-vs-x line chart to a PNG. x may be logarithmic
// when logX is true. hLines are horizontal reference levels (e.g. 0 dB and the
// spec limits). The renderer deliberately uses only the standard library, so it
// works in environments without font files.
func plotCurve(path, title string, xs, ys []float64, logX bool, yLo, yHi float64, hLines map[float64]color.Color) error {
	img := image.NewRGBA(image.Rect(0, 0, plotW, plotH))
	fill(img, color.RGBA{255, 255, 255, 255})

	plotArea := image.Rect(margin, margin, plotW-margin, plotH-margin)
	drawRect(img, plotArea, color.RGBA{235, 235, 235, 255})

	xMin, xMax := xs[0], xs[len(xs)-1]
	toX := func(x float64) int {
		v := x
		if logX {
			v = math.Log10(x)
			l0, l1 := math.Log10(xMin), math.Log10(xMax)
			return plotArea.Min.X + int((v-l0)/(l1-l0)*float64(plotArea.Dx()))
		}
		return plotArea.Min.X + int((v-xMin)/(xMax-xMin)*float64(plotArea.Dx()))
	}
	toY := func(y float64) int {
		return plotArea.Max.Y - int((y-yLo)/(yHi-yLo)*float64(plotArea.Dy()))
	}

	grid := color.RGBA{200, 200, 200, 255}
	for gx := plotArea.Min.X; gx <= plotArea.Max.X; gx += plotArea.Dx() / 8 {
		drawVLine(img, gx, plotArea.Min.Y, plotArea.Max.Y, grid)
	}
	for gy := plotArea.Min.Y; gy <= plotArea.Max.Y; gy += plotArea.Dy() / 8 {
		drawHLine(img, plotArea.Min.X, plotArea.Max.X, gy, grid)
	}
	for level, c := range hLines {
		if level >= yLo && level <= yHi {
			drawHLine(img, plotArea.Min.X, plotArea.Max.X, toY(level), c)
		}
	}

	line := color.RGBA{20, 80, 200, 255}
	for i := 1; i < len(xs); i++ {
		drawLine(img, toX(xs[i-1]), toY(ys[i-1]), toX(xs[i]), toY(ys[i]), line)
	}

	// Border.
	drawRectOutline(img, plotArea, color.RGBA{80, 80, 80, 255})

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func fill(img *image.RGBA, c color.Color) {
	for y := 0; y < plotH; y++ {
		for x := 0; x < plotW; x++ {
			img.Set(x, y, c)
		}
	}
}

func drawRect(img *image.RGBA, r image.Rectangle, c color.Color) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			img.Set(x, y, c)
		}
	}
}

func drawRectOutline(img *image.RGBA, r image.Rectangle, c color.Color) {
	drawHLine(img, r.Min.X, r.Max.X, r.Min.Y, c)
	drawHLine(img, r.Min.X, r.Max.X, r.Max.Y-1, c)
	drawVLine(img, r.Min.X, r.Min.Y, r.Max.Y, c)
	drawVLine(img, r.Max.X-1, r.Min.Y, r.Max.Y, c)
}

func drawHLine(img *image.RGBA, x0, x1, y int, c color.Color) {
	for x := x0; x <= x1; x++ {
		img.Set(x, y, c)
	}
}

func drawVLine(img *image.RGBA, x, y0, y1 int, c color.Color) {
	for y := y0; y <= y1; y++ {
		img.Set(x, y, c)
	}
}

func drawLine(img *image.RGBA, x0, y0, x1, y1 int, c color.Color) {
	dx := absInt(x1 - x0)
	dy := -absInt(y1 - y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	err := dx + dy
	for {
		img.Set(x0, y0, c)
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}
