package beep

import (
	"math"
	"time"
)

// hqStopbandDB is the target stopband attenuation used when designing the
// anti-aliasing / anti-imaging low-pass filter. The Kaiser window is sized for
// this amount of attenuation, which leaves a comfortable margin over the 80 dB
// acceptance threshold.
const hqStopbandDB = 100.0

// The default pass band and stop band are expressed as fractions of the lower
// of the two Nyquist frequencies, so the same design works for any sample-rate
// pair rather than being hard-coded to 44.1/48 kHz. For 44.1<->48 kHz the lower
// Nyquist is 22.05 kHz, yielding a 16.1 kHz pass-band edge and a 20.1 kHz
// stop-band edge: the full audible range is flat, then a smooth transition
// reaches full attenuation by ~20 kHz, leaving everything that could alias or
// image suppressed by well over 80 dB.
const (
	hqPassbandFraction = 0.73
	hqStopbandFraction = 0.91
)

// ResampleHQ returns a high-quality, anti-aliased resampler converting the
// Streamer s from the old sample rate to the new sample rate.
//
// Unlike Resample, which interpolates with a short polynomial and offers no
// protection against aliasing, ResampleHQ uses a linear-phase polyphase FIR
// filter (a Kaiser-windowed sinc) designed from the two sample rates. The same
// deterministic coefficients are produced on every run, so converting the same
// input twice yields byte-identical output.
//
// Both conversion directions are supported, including the common
// 44100 Hz <-> 48000 Hz case. The design targets:
//
//   - pass-band ripple below 0.1 dB across the 0..16 kHz pass band,
//   - stop-band / alias attenuation above 80 dB by 20 kHz,
//   - THD+N below -90 dB for in-band tones.
//
// The filter introduces a fixed, integer-sample look-ahead delay. Use
// HQResampler.Delay (or HQResampler.DelayOutputSamples) to align the stream in
// the calling layer.
func ResampleHQ(old, new SampleRate, s Streamer) *HQResampler {
	return NewHQResampler(old, new, s, hqStopbandDB)
}

// NewHQResampler builds a polyphase resampler targeting the given stop-band
// attenuation in dB. The pass-band and stop-band edges are derived
// automatically from the lower Nyquist frequency, so the constructor works for
// any sample-rate pair. Most callers should use ResampleHQ, which supplies the
// audio-grade default attenuation.
func NewHQResampler(old, new SampleRate, s Streamer, stopbandDB float64) *HQResampler {
	if old <= 0 || new <= 0 {
		panic("resamplehq: sample rates must be positive")
	}
	up, down := rationalRatio(int(old), int(new))
	lowerNyquist := math.Min(float64(old), float64(new)) / 2
	passbandEdgeHz := lowerNyquist * hqPassbandFraction
	stopbandEdgeHz := lowerNyquist * hqStopbandFraction
	design := designHQFilter(up, down, float64(old), float64(new),
		passbandEdgeHz, stopbandEdgeHz, stopbandDB)

	return &HQResampler{
		s:        s,
		oldRate:  old,
		newRate:  new,
		up:       up,
		down:     down,
		phases:   design.phases,
		taps:     design.taps,
		delayIn:  design.delayIn,
		delaySec: float64(design.delayIn) / float64(old),
		ring:     make([][2]float64, design.taps*2+64),
	}
}

// HQResampler is a Streamer produced by ResampleHQ. It resamples using a fixed
// polyphase FIR kernel and reports its own algorithmic delay.
type HQResampler struct {
	s       Streamer
	oldRate SampleRate
	newRate SampleRate

	up, down int // rational resampling ratio, up/down = newRate/oldRate
	phases   [][]float64
	taps     int // number of coefficients evaluated per output sample
	delayIn  int // group delay expressed in input samples (integer look-ahead)
	delaySec float64

	// ring holds the most recent input samples. ringPos is the absolute input
	// index of ring[0]; ringFill is how many entries, starting at ring[0], are
	// valid (older entries are dropped as the window advances).
	ring     [][2]float64
	ringPos  int
	ringFill int

	drained bool // whether the input stream has returned !ok
	count   int  // number of output samples already produced
}

// Stream implements the Streamer interface.
func (r *HQResampler) Stream(samples [][2]float64) (n int, ok bool) {
	for len(samples) > 0 {
		// Absolute input index of the underlying time instant and the polyphase
		// branch selected for this output sample.
		scaled := r.count * r.down
		center := scaled / r.up
		phase := scaled % r.up

		// The kernel spans taps input samples, centered on center: the oldest
		// coefficient multiplies input index center-delayIn and the newest
		// center+(taps-1-delayIn).
		needHead := center + (r.taps - 1 - r.delayIn)
		if !r.pullThrough(needHead) {
			return n, n > 0
		}

		coeffs := r.phases[phase]
		start := center - r.delayIn
		var left, right float64
		for k, h := range coeffs {
			idx := start + k
			slot := idx - r.ringPos
			if slot < 0 || slot >= r.ringFill {
				continue // beyond the available input: treated as zero padding
			}
			frame := r.ring[slot]
			left += h * frame[0]
			right += h * frame[1]
		}
		samples[0][0] = left
		samples[0][1] = right
		samples = samples[1:]
		n++
		r.count++
	}
	return n, true
}

// pullThrough makes sure every input index up to and including head is present
// in the ring buffer. It returns false once the input is drained and head lies
// past the last available sample.
func (r *HQResampler) pullThrough(head int) bool {
	for {
		if head < r.ringPos+r.ringFill {
			return true
		}
		if r.drained {
			return false
		}

		// Read into the free tail of the ring, growing if the window is full.
		if r.ringFill >= len(r.ring) {
			r.compact()
			if r.ringFill >= len(r.ring) {
				grown := make([][2]float64, len(r.ring)*2)
				copy(grown, r.ring[:r.ringFill])
				r.ring = grown
			}
		}
		m, ok := r.s.Stream(r.ring[r.ringFill : r.ringFill+1])
		if m == 1 {
			r.ringFill++
		}
		if !ok {
			r.drained = true
			if head < r.ringPos+r.ringFill {
				return true
			}
			return false
		}
	}
}

// compact drops input samples that can never be needed again and slides the
// remaining window to the start of the ring.
func (r *HQResampler) compact() {
	// Keep one full kernel width of history; anything older is unreachable.
	keepFrom := r.ringPos
	if r.ringFill > r.taps {
		drop := r.ringFill - r.taps
		keepFrom += drop
		copy(r.ring, r.ring[drop:r.ringFill])
		r.ringFill -= drop
		for i := r.ringFill; i < len(r.ring); i++ {
			r.ring[i] = [2]float64{}
		}
	}
	r.ringPos = keepFrom
}

// Err propagates the underlying Streamer's error.
func (r *HQResampler) Err() error {
	return r.s.Err()
}

// OldRate returns the source sample rate.
func (r *HQResampler) OldRate() SampleRate { return r.oldRate }

// NewRate returns the destination sample rate.
func (r *HQResampler) NewRate() SampleRate { return r.newRate }

// Delay returns the algorithmic group delay: the resampled stream is shifted in
// time by this duration relative to the input, because the FIR kernel looks
// ahead by DelayInputSamples input samples.
func (r *HQResampler) Delay() time.Duration {
	return r.oldRate.D(r.delayIn)
}

// DelayInputSamples returns the group delay measured in input samples. It is an
// integer because the kernel is linear phase and symmetric.
func (r *HQResampler) DelayInputSamples() int { return r.delayIn }

// DelayOutputSamples returns the fractional group delay measured in output
// samples. This is the value to subtract from an output-side position when
// aligning resampled audio against other streams at the new sample rate.
func (r *HQResampler) DelayOutputSamples() float64 {
	return r.delaySec * float64(r.newRate)
}

// hqFilterDesign holds a finalized polyphase kernel.
type hqFilterDesign struct {
	phases  [][]float64 // phases[p] has taps coefficients for polyphase branch p
	taps    int
	delayIn int
}

// rationalRatio reduces newRate/oldRate to coprime integers up/down.
func rationalRatio(oldRate, newRate int) (up, down int) {
	g := gcd(oldRate, newRate)
	return newRate / g, oldRate / g
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// designHQFilter builds the polyphase FIR kernel.
//
// up/down is the reduced ratio newRate/oldRate. The shared high-rate grid runs
// at gridRate = up*oldHz = down*newHz samples per second; one grid step is
// 1/up of an input sample. A single prototype low-pass on that grid performs
// both the anti-imaging (interpolation) and anti-aliasing (decimation) work.
//
// passbandEdgeHz and stopbandEdgeHz bound the flat pass band and the point where
// full attenuation is reached. The transition band between them sets the
// required kernel length for the requested stop-band attenuation.
func designHQFilter(up, down int, oldHz, newHz, passbandEdgeHz, stopbandEdgeHz, stopbandDB float64) hqFilterDesign {
	gridRate := float64(up) * oldHz // == float64(down) * newHz

	// Digital frequencies in cycles per grid sample, Nyquist at 0.5.
	if stopbandEdgeHz >= math.Min(oldHz, newHz)/2 {
		panic("resamplehq: stop-band edge must be below the lower Nyquist frequency")
	}
	fStop := stopbandEdgeHz / gridRate
	fPass := passbandEdgeHz / gridRate
	transitionWidth := fStop - fPass
	if transitionWidth <= 0 {
		panic("resamplehq: pass-band edge must be below the lower Nyquist frequency")
	}
	fCutoff := (fPass + fStop) / 2
	deltaOmega := 2 * math.Pi * transitionWidth

	beta := kaiserBeta(stopbandDB)

	// Kaiser order estimate on the grid. The prototype length is forced to
	// 2*up*P+1: an odd, up-symmetric length whose center is an integer number P
	// of input samples, giving exact linear phase and integer group delay.
	order := (stopbandDB - 7.95) / (2.285 * deltaOmega)
	needHalf := (int(math.Ceil(order)) + 1) / 2
	delayIn := (needHalf + up - 1) / up // round up to a whole input sample
	taps := 2*delayIn + 1
	halfGrid := delayIn * up
	protoLen := 2*halfGrid + 1

	// Build the single windowed-sinc prototype on the high-rate grid. Applying
	// one window to the whole prototype is what guarantees the Kaiser stop-band
	// behavior; per-branch windowing would distort it.
	window := kaiserWindow(protoLen, beta)
	prototype := make([]float64, protoLen)
	for n := 0; n < protoLen; n++ {
		d := float64(n - halfGrid)
		var sinc float64
		if d == 0 {
			sinc = 2 * fCutoff
		} else {
			sinc = math.Sin(2*math.Pi*fCutoff*d) / (math.Pi * d)
		}
		prototype[n] = sinc * window[n]
	}

	// One global DC normalization keeps the prototype (and every symmetric
	// branch) linear phase. The interpolation bank inserts up-1 zeros, so unity
	// DC gain requires the prototype sum to equal up.
	var protoSum float64
	for _, h := range prototype {
		protoSum += h
	}
	gain := float64(up) / protoSum
	for n := range prototype {
		prototype[n] *= gain
	}

	// Polyphase extraction: branch p coefficient k is the prototype sample at
	// grid index halfGrid + up*(k-delayIn) - p, matching the Stream-time
	// convention gridOff = up*(k-delayIn) - p. Out-of-range positions are
	// genuine zeros, so branches are padded to equal length and stay symmetric.
	phases := make([][]float64, up)
	for p := 0; p < up; p++ {
		branch := make([]float64, taps)
		for k := 0; k < taps; k++ {
			// Matches the Stream convention gridOff = up*(k-delayIn) - p.
			idx := halfGrid + up*(k-delayIn) - p
			if idx >= 0 && idx < protoLen {
				branch[k] = prototype[idx]
			}
		}
		phases[p] = branch
	}

	return hqFilterDesign{
		phases:  phases,
		taps:    taps,
		delayIn: delayIn,
	}
}

// kaiserBeta returns the Kaiser shape parameter for a desired stop-band
// attenuation in dB (Kaiser & Schafer formula).
func kaiserBeta(stopbandDB float64) float64 {
	switch {
	case stopbandDB > 50:
		return 0.1102 * (stopbandDB - 8.7)
	case stopbandDB >= 21:
		return 0.5842*math.Pow(stopbandDB-21, 0.8) + 0.0788*(stopbandDB-21)
	default:
		return 0
	}
}

// kaiserWindow returns w(n) for n in [0, length-1], a symmetric Kaiser window
// with shape parameter beta.
func kaiserWindow(length int, beta float64) []float64 {
	w := make([]float64, length)
	if length == 1 {
		w[0] = 1
		return w
	}
	denom := besselI0(beta)
	m := float64(length - 1)
	for n := 0; n < length; n++ {
		x := 2*float64(n)/m - 1
		arg := beta * math.Sqrt(math.Max(0, 1-x*x))
		w[n] = besselI0(arg) / denom
	}
	return w
}

// besselI0 evaluates the 0th-order modified Bessel function of the first kind
// via its convergent power series.
func besselI0(x float64) float64 {
	sum := 1.0
	term := 1.0
	y := x * x / 4
	for k := 1; k < 64; k++ {
		term *= y / float64(k*k)
		sum += term
		if term < 1e-20*sum {
			break
		}
	}
	return sum
}
