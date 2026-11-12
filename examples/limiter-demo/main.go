// limiter-demo shows how to place the soft limiter after a multi-channel mix so that summing
// many loud voices cannot clip. It runs offline and writes a WAV file, so it does not need an
// audio device.
package main

import (
	"log"
	"os"

	"github.com/faiface/beep"
	"github.com/faiface/beep/effects"
	"github.com/faiface/beep/generators"
	"github.com/faiface/beep/wav"
)

const sampleRate = 48000

func main() {
	sr := beep.SampleRate(sampleRate)

	// Build 32 full-scale-ish voices. Summing them directly would peak well above full scale;
	// each voice is limited to a fixed duration so the demo renders a finite WAV.
	voices := make([]beep.Streamer, 32)
	for i := range voices {
		tone, err := generators.SineTone(sr, 110+float64(i)*15)
		if err != nil {
			log.Fatal(err)
		}
		voices[i] = beep.Take(int(sr)*2, &effects.Volume{
			Streamer: tone,
			Base:     2,
			Volume:   -1, // roughly half amplitude per voice
		})
	}

	// Raw mix, then the out-of-the-box soft limiter: -16 dBFS loudness alignment and a hard
	// -1 dBFS peak ceiling while keeping transients and dynamics.
	mixed := beep.Mix(voices...)
	limited := effects.SoftLimiter(mixed, sampleRate)

	out, err := os.Create("limited_demo.wav")
	if err != nil {
		log.Fatal(err)
	}
	defer out.Close()

	format := beep.Format{SampleRate: sr, NumChannels: 2, Precision: 2}
	if err := wav.Encode(out, limited, format); err != nil {
		log.Fatal(err)
	}
	log.Println("wrote limited_demo.wav")
}
