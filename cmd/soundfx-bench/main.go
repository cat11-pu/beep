// soundfx-bench runs the trigger-latency measurement headlessly against an
// in-memory sink, so it needs no audio device and no cgo. The measurement
// contract implemented here is the one documented in docs/soundfx-latency.md:
//
//	t0    = timestamp at Trigger entry
//	t1    = timestamp when the pump first pulls a non-empty frame from the voice
//	latency = t1 - t0; report median and p99 over at least 1000 samples.
//
// On a real device, swap MemorySink for an oto-backed Sink; the timing contract
// is identical.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/faiface/beep"
	"github.com/faiface/beep/soundfx"
)

func main() {
	var (
		samples = flag.Int("samples", 1000, "number of trigger latency samples (>= 1000)")
		voices  = flag.Int("voices", 32, "pool size / concurrency level")
		rate    = flag.Int("rate", 48000, "sample rate in Hz")
		block   = flag.Int("block", 32, "pump block in frames (32 @48k = 0.667ms)")
		policy  = flag.String("policy", "reject", "degrade policy: reject|steal")
	)
	flag.Parse()

	if *samples < soundfx.MinSamples {
		fmt.Fprintf(os.Stderr, "samples %d below minimum %d\n", *samples, soundfx.MinSamples)
		os.Exit(2)
	}

	degrade := soundfx.PolicyReject
	if *policy == "steal" {
		degrade = soundfx.PolicyStealOldest
	}

	clock := soundfx.NewFakeClock(time.Unix(0, 0))
	sink := &soundfx.MemorySink{}
	pool, err := soundfx.NewPool(soundfx.PoolConfig{
		SampleRate:   beep.SampleRate(*rate),
		MaxVoices:    *voices,
		BlockSamples: *block,
		Policy:       degrade,
	}, sink, clock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pool init: %v\n", err)
		os.Exit(1)
	}

	// Preloading happens here (the scene-switch phase), never during Trigger.
	if err := pool.Preload("click", tone(*rate/10)); err != nil {
		fmt.Fprintf(os.Stderr, "preload: %v\n", err)
		os.Exit(1)
	}

	// Simulated scheduling jitter between trigger and the next pump tick:
	// 4..18 ms plus pump granularity. This models a well-tuned real system;
	// replace with field histograms when available.
	for i := 0; i < *samples; i++ {
		clock.Advance(time.Millisecond)
		if _, err := pool.Trigger("click", false); err != nil {
			fmt.Fprintf(os.Stderr, "trigger %d: %v\n", i, err)
			os.Exit(1)
		}
		wait := 4*time.Millisecond + time.Duration(i%15)*time.Millisecond
		clock.Advance(wait)
		pool.Step() // first frame pulled here; t1 recorded
		for pool.ActiveCount() > 0 {
			clock.Advance(500 * time.Microsecond)
			pool.Step()
		}
	}

	rep := pool.Metrics().Snapshot()
	fmt.Printf("samples      : %d\n", rep.Samples)
	fmt.Printf("median (p50) : %v  (limit < %v)\n", rep.Median, soundfx.MedianLimit)
	fmt.Printf("p99          : %v  (limit < %v)\n", rep.P99, soundfx.P99Limit)
	fmt.Printf("rejected     : %d\n", pool.Metrics().Rejected())
	fmt.Printf("stolen       : %d\n", pool.Metrics().Stolen())
	fmt.Printf("clipped      : %d frames\n", sink.Clamped)
	fmt.Printf("verdict      : ")
	if rep.Pass {
		fmt.Println("PASS")
	} else {
		fmt.Println("FAIL")
		os.Exit(1)
	}
}

func tone(frames int) beep.Streamer {
	pos := 0
	return beep.StreamerFunc(func(samples [][2]float64) (int, bool) {
		n := 0
		for i := range samples {
			if pos >= frames {
				return n, n > 0
			}
			v := 1.0 / 32.0
			samples[i] = [2]float64{v, v}
			pos++
			n++
		}
		return n, true
	})
}
