package main

import "math"

// fft performs an in-place iterative radix-2 Cooley-Tukey FFT. len(re) must be
// a power of two. It is self-contained (no external dependency) and operates on
// float64 for the precision the acceptance measurements require.
func fft(re, im []float64) {
	n := len(re)
	if n&(n-1) != 0 {
		panic("fft: length must be a power of two")
	}
	// Bit-reversal permutation.
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for length := 2; length <= n; length <<= 1 {
		half := length >> 1
		ang := -2 * math.Pi / float64(length)
		wRe := math.Cos(ang)
		wIm := math.Sin(ang)
		for start := 0; start < n; start += length {
			cRe, cIm := 1.0, 0.0
			for k := 0; k < half; k++ {
				a := start + k
				b := a + half
				tRe := cRe*re[b] - cIm*im[b]
				tIm := cRe*im[b] + cIm*re[b]
				re[b] = re[a] - tRe
				im[b] = im[a] - tIm
				re[a] += tRe
				im[a] += tIm
				cRe, cIm = cRe*wRe-cIm*wIm, cRe*wIm+cIm*wRe
			}
		}
	}
}

// nextPow2 returns the smallest power of two >= n.
func nextPow2(n int) int {
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

// hannWindow returns a periodic Hann window of length n.
func hannWindow(n int) []float64 {
	w := make([]float64, n)
	for i := range w {
		w[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(n)))
	}
	return w
}

// blackmanHarrisWindow returns a 4-term Blackman-Harris window. Its peak
// sidelobe is about -93 dB, which keeps spectral leakage low enough for the
// -90 dB THD+N acceptance measurement.
func blackmanHarrisWindow(n int) []float64 {
	const (
		a0 = 0.35875
		a1 = 0.48829
		a2 = 0.14128
		a3 = 0.01168
	)
	w := make([]float64, n)
	for i := range w {
		t := 2 * math.Pi * float64(i) / float64(n-1)
		w[i] = a0 - a1*math.Cos(t) + a2*math.Cos(2*t) - a3*math.Cos(3*t)
	}
	return w
}

// welchPSD estimates the one-sided power spectral density of x using Hann-windowed
// segments of length nfft with 50% overlap. Output bins are normalized to a
// total signal power of sum(PSD) and expressed on a linear (power) scale.
func welchPSD(x []float64, nfft int) []float64 {
	win := hannWindow(nfft)
	var winPower float64
	for _, v := range win {
		winPower += v * v
	}
	psd := make([]float64, nfft/2+1)
	hop := nfft / 2
	segments := 0
	for start := 0; start+nfft <= len(x); start += hop {
		re := make([]float64, nfft)
		im := make([]float64, nfft)
		for i := 0; i < nfft; i++ {
			re[i] = x[start+i] * win[i]
		}
		fft(re, im)
		for k := 0; k <= nfft/2; k++ {
			p := re[k]*re[k] + im[k]*im[k]
			if k > 0 && k < nfft/2 {
				p *= 2 // one-sided spectrum
			}
			psd[k] += p
		}
		segments++
	}
	norm := float64(segments) * winPower
	for k := range psd {
		psd[k] /= norm
	}
	return psd
}

// bandPower integrates PSD power over [fLo, fHi] given the bin spacing df.
func bandPower(psd []float64, df, fLo, fHi float64) float64 {
	lo := int(math.Floor(fLo / df))
	hi := int(math.Ceil(fHi / df))
	if lo < 0 {
		lo = 0
	}
	if hi > len(psd)-1 {
		hi = len(psd) - 1
	}
	var p float64
	for k := lo; k <= hi; k++ {
		p += psd[k]
	}
	return p * df
}

func toDB(p float64) float64 {
	if p <= 0 {
		return -300
	}
	return 10 * math.Log10(p)
}
