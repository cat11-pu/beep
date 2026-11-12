package spectrum_test

import (
	"fmt"
	"math"

	"github.com/faiface/beep/spectrum"
)

// ExampleNewAnalyzer analyzes a 1 kHz tone with the default configuration and
// prints the measured dominant frequency.
func ExampleNewAnalyzer() {
	const (
		sampleRate = 48000
		windowSize = 2048
	)
	analyzer, err := spectrum.NewAnalyzer(spectrum.DefaultConfig(sampleRate, windowSize))
	if err != nil {
		panic(err)
	}

	// Reuse the output slice: the whole processing path stays allocation-free.
	bands := make([]float64, analyzer.Bands())
	for i := 0; i < windowSize*2; i++ {
		// 1007.8125 Hz lands exactly on FFT bin 43 at 48 kHz / 2048.
		sample := 0.8 * math.Sin(2*math.Pi*1007.8125*float64(i)/sampleRate)
		if analyzer.Push(sample) {
			analyzer.BandEnergies(bands)
		}
	}
	fmt.Printf("frames=%d peak=%.1f Hz bands=%d\n",
		analyzer.FrameCount(), analyzer.PeakFrequency(), analyzer.Bands())
	// Output: frames=3 peak=1007.8 Hz bands=32
}
