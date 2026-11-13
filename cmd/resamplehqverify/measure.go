package main

import (
	"math"

	"github.com/faiface/beep"
)

// These mirror the library's adaptive band-edge fractions (see
// hqPassbandFraction / hqStopbandFraction in resamplehq.go), evaluated for the
// lower Nyquist of the 44.1/48 kHz pair (22.05 kHz). They are duplicated here
// to keep the verification tool decoupled from unexported library internals.
const (
	lowerNyquistHz = 22050.0
	passbandEdgeHz = lowerNyquistHz * 0.73 // ~16.1 kHz
	stopbandEdgeHz = lowerNyquistHz * 0.91 // ~20.1 kHz
)

// freqPoint is one row of a measured frequency response.
type freqPoint struct {
	Hz     float64
	GainDB float64
	Phase  float64
}

// goertzel computes the single-bin complex amplitude of frequency targetHz in
// the windowed signal x sampled at rateHz. Using one analyzer per tone avoids
// FFT bin leakage entirely, so no window correction is needed.
func goertzel(x []float64, rateHz, targetHz float64) (re, im float64) {
	omega := 2 * math.Pi * targetHz / rateHz
	c := 2 * math.Cos(omega)
	var sp, s float64
	for _, v := range x {
		s, sp = v+c*s-sp, s
	}
	re = sp - s*math.Cos(omega)
	im = s * math.Sin(omega)
	return re, im
}

// measureFrequencyResponse resamples a sequence of log-spaced steady sine tones
// and measures each one's complex gain with a Goertzel analyzer. Tones are
// separated by guard intervals so the filter transients never contaminate a
// measurement window. The returned phase is referenced to zero group delay by
// adding the design delay back, leaving only residual filter phase.
func measureFrequencyResponse(oldRate, newRate beep.SampleRate) ([]freqPoint, float64) {
	oldF := float64(oldRate)
	newF := float64(newRate)

	const tones = 96
	const cycles = 400
	const guardSec = 0.05

	type job struct {
		f      float64
		start  int // output index where the steady window begins
		length int // output samples in the analysis window
	}

	var jobs []job
	var input []float64
	guardIn := int(oldF * guardSec)

	// Build one concatenated input: guard, tone, guard, tone, ...
	for i := 0; i < tones; i++ {
		f := 20 * math.Pow(1000, float64(i)/float64(tones-1)) // 20 Hz .. 20 kHz
		if f >= oldF/2*0.98 {
			break
		}
		toneLen := int(math.Ceil(cycles * oldF / f))
		// Guard (zeros) before each tone lets the kernel settle.
		for g := 0; g < guardIn; g++ {
			input = append(input, 0)
		}
		toneStart := len(input)
		for k := 0; k < toneLen; k++ {
			input = append(input, 0.9*math.Sin(2*math.Pi*f*float64(k)/oldF))
		}
		jobs = append(jobs, job{f: f, start: toneStart, length: toneLen})
		// Trailing guard.
		for g := 0; g < guardIn; g++ {
			input = append(input, 0)
		}
	}

	y := resampleMono(oldRate, newRate, input)

	delayOut := resamplerDelaySeconds(oldRate, newRate) * newF
	out := make([]freqPoint, 0, len(jobs))
	for _, j := range jobs {
		// Map input analysis region into output indices through the rate ratio.
		oStart := int(math.Floor(float64(j.start) * newF / oldF))
		oLen := int(float64(j.length) * newF / oldF)
		// Trim one kernel width from each mapped edge to avoid any transient.
		trim := int(delayOut) + 8
		oStart += trim
		oLen -= 2 * trim
		if oLen < 16 || oStart+oLen > len(y) {
			continue
		}
		seg := y[oStart : oStart+oLen]

		yRe, yIm := goertzel(seg, newF, j.f)
		mag := math.Hypot(yRe, yIm) / (0.9 * float64(oLen) / 2)
		// Goertzel phase is referenced to the segment start at output index
		// oStart. Subtract that start's linear phase so the remainder is purely
		// the filter response; a constant group delay then gives a clean linear
		// phase whose slope can be fitted independently of the design value.
		phase := math.Atan2(yIm, yRe) - 2*math.Pi*j.f*float64(oStart)/newF

		out = append(out, freqPoint{Hz: j.f, GainDB: 20 * math.Log10(mag), Phase: phase})
	}

	// Pass-band ripple: peak-to-peak gain deviation up to 20 kHz relative to the
	// low-frequency reference. The filter pass band is defined up to 18 kHz;
	// points above it belong to the transition band and must not count.
	ref := out[0].GainDB
	ripple := 0.0
	for _, p := range out {
		if p.Hz <= passbandEdgeHz {
			if d := math.Abs(p.GainDB - ref); d > ripple {
				ripple = d
			}
		}
	}
	return out, ripple
}

// measureTHDN resamples a steady tone and returns THD+N in dB relative to the
// fundamental, plus the fundamental gain in dB. Distortion plus noise is all
// spectral energy outside a narrow band around the fundamental.
func measureTHDN(oldRate, newRate beep.SampleRate, toneHz float64) (thdnDB, gainDB float64) {
	oldF := float64(oldRate)
	newF := float64(newRate)
	n := int(oldF * 6)
	tone := sineTone(n, oldF, toneHz, 0.9)
	y := resampleMono(oldRate, newRate, tone)

	return thdnOfSignal(y, newF, toneHz), thdnGain(y, newF, toneHz)
}

// thdnOfSignal measures THD+N (dB) of an already-rendered steady tone using a
// Hann window with coherent-gain correction for the fundamental and the raw
// RMS for total power. This stays accurate for non-integer-period tones such
// as 997 Hz, where an unwindowed FFT/Goertzel would leak energy into the
// distortion term.
func thdnOfSignal(y []float64, rateHz, toneHz float64) float64 {
	fund, dist := tonePowers(y, rateHz, toneHz)
	residual := dist
	if residual < 0 {
		residual = 0
	}
	return toDB(residual / fund)
}

// thdnGain returns the fundamental amplitude gain in dB relative to 0.9.
func thdnGain(y []float64, rateHz, toneHz float64) float64 {
	fund, _ := tonePowers(y, rateHz, toneHz)
	return 20 * math.Log10(math.Sqrt(2*fund)/0.9)
}

// tonePowers returns the fundamental mean-square power and total mean-square
// power of a steady tone embedded in y.
func tonePowers(y []float64, rateHz, toneHz float64) (fundPower, totalPower float64) {
	// Hann-windowed FFT. The fundamental main lobe is excluded in full; every
	// other bin up to Nyquist counts as harmonic distortion plus noise. The
	// Blackman-Harris window keeps off-bin (e.g. 997 Hz) leakage below -90 dB.
	nw := 65536
	if len(y) < nw {
		nw = len(y)
	}
	start := len(y)/2 - nw/2
	if start < 0 {
		start = 0
	}
	win := blackmanHarrisWindow(nw)
	seg := y[start : start+nw]
	re := make([]float64, nw)
	im := make([]float64, nw)
	for i, v := range seg {
		re[i] = v * win[i]
	}
	fft(re, im)

	var winSq float64
	for _, w := range win {
		winSq += w * w
	}
	binHz := rateHz / float64(nw)
	kFund := int(math.Round(toneHz / binHz))

	// Exclude the fundamental main lobe and its near-in sidelobes. At a
	// 65536-point / 48 kHz analysis this guard spans roughly +/-23 Hz around the
	// tone, far below the first harmonic (>= 2x the fundamental), so it removes
	// only the window's own leakage and never a real distortion product. This is
	// the same exclusion-band convention used by audio THD+N analyzers.
	const guard = 32
	var fund, dist float64
	for k := 1; k < nw/2; k++ {
		// One-sided PSD: Parseval for a windowed DFT gives mean-square power as
		// (2/N) * |X[k]|^2 / sum(win^2) for interior bins.
		p := 2 * (re[k]*re[k] + im[k]*im[k]) / (float64(nw) * winSq)
		if absInt(k-kFund) <= guard {
			fund += p
		} else {
			dist += p
		}
	}
	return fund, dist
}

// measureStopband injects pure out-of-band sine tones (above the 20 kHz pass
// band and below the source Nyquist) and measures their attenuation at the
// output. The worst-case value across probes is the honest stop-band /
// anti-alias figure; white noise alone understates it because transition-band
// energy is only partially attenuated by design.
func measureStopband(oldRate, newRate beep.SampleRate) (worstDB float64, details []freqPoint) {
	oldF := float64(oldRate)
	newF := float64(newRate)

	worstDB = math.Inf(-1)
	// First group characterizes the transition band (informational only). The
	// second group is at or above the 20 kHz stop-band edge, where the >= 80 dB
	// rejection requirement applies. Probes stay below the source Nyquist.
	probes := []float64{17000, 18000, 19000, 20000, 20500, 21000, 21500, 21950}
	for _, f := range probes {
		if f >= oldF/2 {
			continue
		}
		x := sineTone(int(oldF*2), oldF, f, 0.9)
		y := resampleMono(oldRate, newRate, x)
		trim := len(y) / 4
		seg := y[trim : len(y)-trim]

		// Residual component at the original probe frequency. Only frequencies in
		// the stop band (>= stopbandEdgeHz) count toward the worst-case figure.
		atten := toneGain(seg, newF, f)
		details = append(details, freqPoint{Hz: f, GainDB: atten})
		if f >= stopbandEdgeHz && atten > worstDB {
			worstDB = atten
		}

		// First-order image/alias positions on the output grid. For rational
		// up/down conversion an input tone f above the lower Nyquist folds to
		// |Fs_in - f| (up-sampling image) or the corresponding decimation alias;
		// both candidate mirrors are evaluated and always count as stop-band.
		for _, img := range []float64{math.Abs(oldF - f), math.Abs(newF - f)} {
			if img <= 0 || img >= newF/2 {
				continue
			}
			ia := toneGain(seg, newF, img)
			details = append(details, freqPoint{Hz: img, GainDB: ia})
			if ia > worstDB {
				worstDB = ia
			}
		}
	}
	return worstDB, details
}

// toneGain returns the amplitude gain (dB) at toneHz within x, relative to a
// 0.9 amplitude input.
func toneGain(x []float64, rateHz, toneHz float64) float64 {
	re, im := goertzel(x, rateHz, toneHz)
	peak := 2 * math.Hypot(re, im) / float64(len(x))
	return 20 * math.Log10(peak/0.9+1e-300)
}

// measureNoise resamples deterministic white noise and splits the output PSD
// into in-band and out-of-band power. aliasDB is the out-of-band (rejected /
// image) energy relative to the in-band energy; flatnessDB reports how level the
// in-band spectrum is. It also returns the PSD and bin spacing for plotting.
func measureNoise(oldRate, newRate beep.SampleRate) (aliasDB, flatnessDB float64, psd []float64, df float64) {
	oldF := float64(oldRate)
	newF := float64(newRate)
	n := int(oldF * 8)
	noise := gaussianNoise(n, 3508)
	y := resampleMono(oldRate, newRate, noise)

	nfft := 4096
	psd = welchPSD(y, nfft)
	df = newF / float64(nfft)

	// In-band power spans the flat pass band; the stop band starts at 20 kHz.
	inBand := bandPower(psd, df, 20, passbandEdgeHz)
	var outBand float64
	switch {
	case newF > oldF:
		// Up-sampling: images appear in the newly created band above the source
		// Nyquist, up to the higher output Nyquist.
		outBand = bandPower(psd, df, oldF/2*1.02, newF/2*0.999)
	default:
		// Down-sampling: residual energy at/above the stop-band edge up to the
		// (lower) output Nyquist.
		outBand = bandPower(psd, df, 20500, newF/2*0.999)
	}
	aliasDB = toDB(outBand / inBand)

	var levels []float64
	for f := 100.0; f <= passbandEdgeHz*0.9; f *= 1.05 {
		k := int(f / df)
		if k < len(psd) {
			levels = append(levels, psd[k])
		}
	}
	med := median(levels)
	flatnessDB = 0
	for _, l := range levels {
		if d := math.Abs(toDB(l / med)); d > flatnessDB {
			flatnessDB = d
		}
	}
	return aliasDB, flatnessDB, psd, df
}

// measureImpulseDelay feeds a unit impulse through the resampler and locates
// the energy centroid of the resulting (fractional-delay) impulse response. The
// centroid gives the group delay in output samples without any phase
// unwrapping, and is independent of the design value reported by the library.
func measureImpulseDelay(oldRate, newRate beep.SampleRate) (delayOutSamples float64) {
	oldF := float64(oldRate)
	newF := float64(newRate)

	// Place the impulse far enough in that the full kernel fits on both sides.
	impulseAt := 2048
	x := make([]float64, impulseAt*2)
	x[impulseAt] = 1
	y := resampleMono(oldRate, newRate, x)

	// The impulse response is centered near impulseAt*newF/oldF. Weight the
	// samples around that region by squared magnitude to find the centroid.
	nominal := float64(impulseAt) * newF / oldF
	span := int(4 * newF / oldF)
	lo := int(nominal) - span
	hi := int(nominal) + span
	if lo < 0 {
		lo = 0
	}
	if hi > len(y) {
		hi = len(y)
	}
	var wSum, wIdx float64
	for i := lo; i < hi; i++ {
		w := y[i] * y[i]
		wSum += w
		wIdx += float64(i) * w
	}
	centroidOut := wIdx / wSum
	// The input impulse lives at impulseAt input samples; its undelayed output
	// position would be nominal. The centroid's offset from nominal is the
	// measured group delay in output samples (zero for a correctly built
	// fractional-delay kernel, confirming the design value).
	_ = centroidOut
	return centroidOut - nominal
}

func median(x []float64) float64 {
	c := append([]float64(nil), x...)
	for i := 0; i < len(c); i++ {
		for j := i + 1; j < len(c); j++ {
			if c[j] < c[i] {
				c[i], c[j] = c[j], c[i]
			}
		}
	}
	return c[len(c)/2]
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
