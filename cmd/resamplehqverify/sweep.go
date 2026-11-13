package main

import (
	"github.com/faiface/beep"
)

// sweepSpectrum drives an exponential swept sine (20 Hz .. ~0.97 Nyquist)
// through the resampler and returns the output power spectrum. It is the
// qualitative "sweep test" artifact: a single excitation that traverses the
// whole frequency range, making the flat pass band, transition roll-off and
// stop-band floor visible in one view. The precise numeric figures (ripple,
// stop-band gain, THD+N) come from the dedicated steady-tone measurements.
func sweepSpectrum(oldRate, newRate beep.SampleRate) (psd []float64, df float64) {
	oldF := float64(oldRate)
	newF := float64(newRate)
	sweep, _ := logSweep(oldF, 6.0, 20.0, oldF/2*0.97)
	y := resampleMono(oldRate, newRate, sweep)

	nfft := 4096
	psd = welchPSD(y, nfft)
	df = newF / float64(nfft)
	return psd, df
}
