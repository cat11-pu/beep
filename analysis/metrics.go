package main

import "math"

// Metrics holds the objective measurements for one rendered signal.
type Metrics struct {
	Name string

	PeakDBFS     float64   // maximum absolute sample
	RMSDBFS      float64   // full-track RMS
	CrestFactor  float64   // Peak/RMS in dB, a coarse dynamic-range indicator
	ClipCount    int       // samples at or beyond full scale
	ClipRatePct  float64   // clipped samples as a percentage
	DRShortTerm  float64   // dynamic range from short-term loudness spread (dB)
	LUFSApprox   float64   // K-weighted integrated loudness approximation
	BandEnergyDB []float64 // per-band energy relative to the loudest band, dB
}

// bandEdges define the frequency bands used for the spectral energy comparison, covering
// sub-bass through brilliance.
var bandEdges = []struct {
	name string
	lo   float64
	hi   float64
}{
	{"sub-bass 20-60Hz", 20, 60},
	{"bass 60-250Hz", 60, 250},
	{"low-mid 250-500Hz", 250, 500},
	{"mid 500-2kHz", 500, 2000},
	{"upper-mid 2-4kHz", 2000, 4000},
	{"presence 4-8kHz", 4000, 8000},
	{"brilliance 8-16kHz", 8000, 16000},
}

func measure(samples [][2]float64, name string) Metrics {
	m := Metrics{Name: name, BandEnergyDB: make([]float64, len(bandEdges))}

	var sumSq float64
	peak := 0.0
	for _, s := range samples {
		for _, v := range s {
			ax := math.Abs(v)
			if ax > peak {
				peak = ax
			}
			if ax >= 1.0 {
				m.ClipCount++
			}
			sumSq += v * v
		}
	}
	n := float64(len(samples) * 2)
	rms := math.Sqrt(sumSq / n)
	m.PeakDBFS = toDB(peak)
	m.RMSDBFS = toDB(rms)
	m.CrestFactor = m.PeakDBFS - m.RMSDBFS
	m.ClipRatePct = 100 * float64(m.ClipCount) / n
	m.DRShortTerm = shortTermDynamicRange(samples)
	m.LUFSApprox = integratedLoudness(samples)

	bandEnergy := spectralBandEnergy(samples)
	maxE := 0.0
	for _, e := range bandEnergy {
		if e > maxE {
			maxE = e
		}
	}
	for i, e := range bandEnergy {
		if maxE > 0 {
			m.BandEnergyDB[i] = toDB(e / maxE)
		} else {
			m.BandEnergyDB[i] = -200
		}
	}
	return m
}

// shortTermDynamicRange estimates dynamic range from 400 ms RMS windows: the spread between a
// loud percentile (95th) and a quiet percentile (10th) of window loudness. A larger value means
// more dynamics were retained.
func shortTermDynamicRange(samples [][2]float64) float64 {
	window := sampleRate * 400 / 1000
	var levels []float64
	for start := 0; start+window <= len(samples); start += window / 2 {
		var sumSq float64
		for i := start; i < start+window; i++ {
			sumSq += samples[i][0]*samples[i][0] + samples[i][1]*samples[i][1]
		}
		rms := math.Sqrt(sumSq / float64(window*2))
		if rms > 1e-6 {
			levels = append(levels, toDB(rms))
		}
	}
	if len(levels) < 4 {
		return 0
	}
	sorted := append([]float64(nil), levels...)
	sortFloat64s(sorted)
	lo := percentile(sorted, 10)
	hi := percentile(sorted, 95)
	return hi - lo
}

// integratedLoudness approximates EBU R128 integrated loudness using a simplified K-weighting
// (a high-shelf and a low-pass stage) followed by mean-square gating. It is labeled an
// approximation because it omits the full absolute-gate calibration of a certified meter.
func integratedLoudness(samples [][2]float64) float64 {
	// K-weighting stage 1: high-shelf ~+4 dB above ~1.5 kHz, implemented as a one-pole
	// difference emphasizing highs.
	coef := 1 - math.Exp(-2*math.Pi*1500/sampleRate)
	hpL, hpR := 0.0, 0.0
	var gated []float64
	block := sampleRate / 10 // 100 ms blocks
	var blockSq float64
	blockN := 0
	for _, s := range samples {
		hpL += coef * (s[0] - hpL)
		hpR += coef * (s[1] - hpR)
		kL := s[0] + 1.25*hpL
		kR := s[1] + 1.25*hpR
		blockSq += (kL*kL + kR*kR) / 2
		blockN++
		if blockN == block {
			gated = append(gated, blockSq/float64(blockN))
			blockSq, blockN = 0, 0
		}
	}
	if len(gated) == 0 {
		return -200
	}
	// absolute gate at -70 LUFS-ish
	var sum, count float64
	for _, g := range gated {
		if toDB(math.Sqrt(g)) > -70 {
			sum += g
			count++
		}
	}
	if count == 0 {
		return -200
	}
	return -0.691 + 10*math.Log10(sum/count)
}

// spectralBandEnergy averages magnitude-squared FFT energy across Hann-windowed blocks and sums
// the bins falling inside each configured frequency band.
func spectralBandEnergy(samples [][2]float64) []float64 {
	fftN := 8192
	hop := fftN / 2
	window := make([]float64, fftN)
	for i := range window {
		window[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(fftN-1)))
	}
	bandEnergy := make([]float64, len(bandEdges))
	blocks := 0
	for start := 0; start+fftN <= len(samples); start += hop {
		re := make([]float64, fftN)
		im := make([]float64, fftN)
		for i := 0; i < fftN; i++ {
			// mono downmix for the spectrum
			mono := 0.5 * (samples[start+i][0] + samples[start+i][1])
			re[i] = mono * window[i]
		}
		fft(re, im)
		for b, edge := range bandEdges {
			loBin := int(edge.lo / (sampleRate / float64(fftN)))
			hiBin := int(math.Ceil(edge.hi / (sampleRate / float64(fftN))))
			if hiBin > fftN/2 {
				hiBin = fftN / 2
			}
			var e float64
			for k := loBin; k < hiBin; k++ {
				e += re[k]*re[k] + im[k]*im[k]
			}
			bandEnergy[b] += e
		}
		blocks++
	}
	if blocks > 0 {
		for i := range bandEnergy {
			bandEnergy[i] /= float64(blocks)
		}
	}
	return bandEnergy
}

// fft performs an iterative radix-2 in-place Cooley-Tukey transform.
func fft(re, im []float64) {
	n := len(re)
	// bit reversal
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
		ang := -2 * math.Pi / float64(length)
		wRe, wIm := math.Cos(ang), math.Sin(ang)
		for i := 0; i < n; i += length {
			curRe, curIm := 1.0, 0.0
			for k := 0; k < length/2; k++ {
				aRe, aIm := re[i+k], im[i+k]
				bRe := curRe*re[i+k+length/2] - curIm*im[i+k+length/2]
				bIm := curRe*im[i+k+length/2] + curIm*re[i+k+length/2]
				re[i+k] = aRe + bRe
				im[i+k] = aIm + bIm
				re[i+k+length/2] = aRe - bRe
				im[i+k+length/2] = aIm - bIm
				curRe, curIm = curRe*wRe-curIm*wIm, curRe*wIm+curIm*wRe
			}
		}
	}
}

func toDB(x float64) float64 {
	if x <= 1e-12 {
		return -240
	}
	return 20 * math.Log10(x)
}

func sortFloat64s(a []float64) {
	// insertion-free standard quicksort via sort package avoided to keep imports minimal;
	// use simple shell sort, adequate for a few dozen 400 ms windows.
	for gap := len(a) / 2; gap > 0; gap /= 2 {
		for i := gap; i < len(a); i++ {
			v := a[i]
			j := i
			for ; j >= gap && a[j-gap] > v; j -= gap {
				a[j] = a[j-gap]
			}
			a[j] = v
		}
	}
}

func percentile(sorted []float64, p float64) float64 {
	idx := int(p / 100 * float64(len(sorted)-1))
	return sorted[idx]
}
