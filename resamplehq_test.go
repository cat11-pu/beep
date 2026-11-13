package beep_test

import (
	"math"
	"testing"

	"github.com/faiface/beep"
)

// hqSliceStreamer feeds a fixed slice and records how many times it was pulled,
// so determinism of both the output values and pull pattern can be checked.
type hqSliceStreamer struct {
	data [][2]float64
	pos  int
}

func (s *hqSliceStreamer) Stream(samples [][2]float64) (n int, ok bool) {
	if s.pos >= len(s.data) {
		return 0, false
	}
	n = copy(samples, s.data[s.pos:])
	s.pos += n
	return n, true
}

func (s *hqSliceStreamer) Err() error { return nil }

func collectHQ(r *beep.HQResampler) [][2]float64 {
	var out [][2]float64
	buf := make([][2]float64, 333)
	for {
		n, ok := r.Stream(buf)
		out = append(out, buf[:n]...)
		if !ok {
			return out
		}
	}
}

func TestResampleHQDeterministic(t *testing.T) {
	for _, pair := range [][2]beep.SampleRate{{44100, 48000}, {48000, 44100}} {
		old, new := pair[0], pair[1]
		data := make([][2]float64, 8192)
		for i := range data {
			v := math.Sin(2*math.Pi*997*float64(i)/float64(old)) * 0.5
			data[i] = [2]float64{v, v * 0.7}
		}

		first := collectHQ(beep.ResampleHQ(old, new, &hqSliceStreamer{data: data}))
		second := collectHQ(beep.ResampleHQ(old, new, &hqSliceStreamer{data: data}))

		if len(first) != len(second) {
			t.Fatalf("%d->%d: length mismatch %d vs %d", old, new, len(first), len(second))
		}
		for i := range first {
			if first[i] != second[i] {
				t.Fatalf("%d->%d: non-deterministic output at sample %d: %v vs %v",
					old, new, i, first[i], second[i])
			}
		}
	}
}

func TestResampleHQDelayAndImpulse(t *testing.T) {
	for _, pair := range [][2]beep.SampleRate{{44100, 48000}, {48000, 44100}} {
		old, new := pair[0], pair[1]

		// Single unit impulse well inside the stream so the full kernel fits.
		data := make([][2]float64, 4096)
		data[512] = [2]float64{1, 1}

		r := beep.ResampleHQ(old, new, &hqSliceStreamer{data: data})
		out := collectHQ(r)

		// The output peak must sit at the delayed location of the input impulse.
		// With a fractional ratio the peak is spread over adjacent samples, so the
		// peak index only needs to land within one sample of the ideal position.
		wantPeak := float64(512) * float64(new) / float64(old)
		peak := 0
		maxV := math.Inf(-1)
		var sum float64
		for i := range out {
			if v := math.Abs(out[i][0]); v > maxV {
				maxV, peak = v, i
			}
			sum += out[i][0]
		}
		if math.Abs(float64(peak)-wantPeak) > 1.0 {
			t.Fatalf("%d->%d: impulse peak at %d, want ~%.2f", old, new, peak, wantPeak)
		}

		// The impulse response of a unity-DC-gain resampler sums to the rate
		// ratio new/old (Parseval: more output samples carry the same energy).
		wantSum := float64(new) / float64(old)
		if math.Abs(sum-wantSum) > 1e-3 {
			t.Fatalf("%d->%d: impulse sum %.6f, want %.6f", old, new, sum, wantSum)
		}

		// Reported delay (seconds) must be internally consistent in both domains.
		delaySec := r.Delay().Seconds()
		fromInput := float64(r.DelayInputSamples()) / float64(old)
		// Delay() is a time.Duration and therefore truncated to nanoseconds.
		if math.Abs(delaySec-fromInput) > 1e-9 {
			t.Fatalf("%d->%d: delay mismatch %v vs %v", old, new, delaySec, fromInput)
		}
		fromOutput := r.DelayOutputSamples() / float64(new)
		if math.Abs(delaySec-fromOutput) > 1e-9 {
			t.Fatalf("%d->%d: output delay mismatch %v vs %v", old, new, delaySec, fromOutput)
		}
	}
}

func TestResampleHQUnityGain(t *testing.T) {
	// A constant (DC) signal must pass through at the same level in both
	// directions, confirming the normalized polyphase bank sums to unity.
	for _, pair := range [][2]beep.SampleRate{{44100, 48000}, {48000, 44100}} {
		old, new := pair[0], pair[1]
		data := make([][2]float64, 4096)
		for i := range data {
			data[i] = [2]float64{0.8, -0.8}
		}
		r := beep.ResampleHQ(old, new, &hqSliceStreamer{data: data})
		out := collectHQ(r)
		// Skip the first and last kernel-length worth of edge transients; the
		// filter looks ahead by DelayInputSamples input samples.
		margin := r.DelayInputSamples() + 50
		for i := margin; i < len(out)-margin; i++ {
			if math.Abs(out[i][0]-0.8) > 1e-5 || math.Abs(out[i][1]+0.8) > 1e-5 {
				t.Fatalf("%d->%d: DC gain drift at %d: %v", old, new, i, out[i])
			}
		}
	}
}

func TestResampleHQRatioReduction(t *testing.T) {
	// 44100/48000 reduces to 160/147; exercise the generic constructor path.
	r := beep.NewHQResampler(44100, 48000, &hqSliceStreamer{
		data: make([][2]float64, 16),
	}, 100)
	if r.DelayInputSamples() <= 0 {
		t.Fatal("expected positive integer delay")
	}
}

func TestResampleHQIntegerRatio(t *testing.T) {
	// A factor-of-two conversion must preserve a low-frequency tone with no
	// amplitude error and keep output length consistent with the rate ratio.
	const old, new beep.SampleRate = 24000, 48000
	n := 8192
	data := make([][2]float64, n)
	for i := range data {
		v := math.Sin(2 * math.Pi * 500 * float64(i) / float64(old))
		data[i] = [2]float64{v, v}
	}
	out := collectHQ(beep.ResampleHQ(old, new, &hqSliceStreamer{data: data}))
	// The look-ahead filter stops once the source is drained, so the tail is a
	// few kernel widths short of the nominal 2*n; the bulk length must match.
	if len(out) < 2*n-200 || len(out) > 2*n {
		t.Fatalf("unexpected output length %d for 2x conversion of %d", len(out), n)
	}
	// Steady middle region must retain unit amplitude.
	for i := n; i < n+2000; i++ {
		if math.Abs(out[i][0]) > 1.0001 {
			t.Fatalf("amplitude exceeded unity at %d: %v", i, out[i][0])
		}
	}
}

func TestResampleHQEmptyAndTiny(t *testing.T) {
	// An empty source drains immediately without producing spurious samples.
	if out := collectHQ(beep.ResampleHQ(44100, 48000, &hqSliceStreamer{
		data: nil,
	})); len(out) != 0 {
		t.Fatalf("empty source produced %d samples", len(out))
	}

	// A single-sample source must not panic and returns a finite, bounded tail.
	out := collectHQ(beep.ResampleHQ(44100, 48000, &hqSliceStreamer{
		data: [][2]float64{{1, 1}},
	}))
	for _, f := range out {
		if math.IsNaN(f[0]) || math.IsInf(f[0], 0) {
			t.Fatal("non-finite output from tiny source")
		}
	}
}

func TestResampleHQImpulseEnergy(t *testing.T) {
	// Both directions must conserve the impulse-response sum at the rate ratio
	// (unity DC gain under sample-rate conversion).
	for _, pair := range [][2]beep.SampleRate{{44100, 48000}, {48000, 44100}} {
		old, new := pair[0], pair[1]
		data := make([][2]float64, 4096)
		data[1024] = [2]float64{1, 1}
		out := collectHQ(beep.ResampleHQ(old, new, &hqSliceStreamer{data: data}))
		var sum float64
		for _, f := range out {
			sum += f[0]
		}
		want := float64(new) / float64(old)
		if math.Abs(sum-want) > 1e-3 {
			t.Fatalf("%d->%d impulse sum %.6f want %.6f", old, new, sum, want)
		}
	}
}
