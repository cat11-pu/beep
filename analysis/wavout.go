package main

import (
	"os"

	"github.com/faiface/beep"
	"github.com/faiface/beep/wav"
)

// writeWAV encodes the float stream as 16-bit stereo WAV. The library's encoder hard-clips at
// full scale, which is exactly what we want to demonstrate for the raw overloaded mix.
func writeWAV(path string, samples [][2]float64) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	format := beep.Format{
		SampleRate:  beep.SampleRate(sampleRate),
		NumChannels: 2,
		Precision:   2,
	}
	return wav.Encode(f, &sliceSource{samples: samples}, format)
}
