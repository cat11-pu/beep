package spectrum

import "math"

// peakBiasTables holds the systematic fractional-bin bias of the three-point
// log-parabolic frequency interpolator for every non-rectangular window. Each
// table is sampled every 0.05 bin from -0.5 to +0.5 (21 entries); the curves
// are odd-symmetric and independent of frame size and sample rate. Subtracting
// the linearly interpolated bias leaves residuals well below 0.5 Hz for both
// supported frame sizes at 44.1/48 kHz. Rectangular windows are not corrected.
var peakBiasTables = map[Window]*[21]float64{
	WindowHann: {
		0, -0.004061691, -0.007886782, -0.011236892, -0.013869997,
		-0.015538384, -0.015986314, -0.014947232, -0.012140309, -0.007265979,
		0,
		0.007265979, 0.012140309, 0.014947232, 0.015986314,
		0.015538384, 0.013869997, 0.011236892, 0.007886782, 0.004061691,
		0,
	},
	WindowHamming: {
		0, -0.004085385, -0.007930931, -0.011295256, -0.013933715,
		-0.015596850, -0.016028880, -0.014966100, -0.012134932, -0.007249244,
		0,
		0.007249244, 0.012134932, 0.014966100, 0.016028880,
		0.015596850, 0.013933715, 0.011295256, 0.007930931, 0.004085385,
		0,
	},
	WindowBlackman: {
		0, -0.001705381, -0.003307785, -0.004704156, -0.005791284,
		-0.006465723, -0.006623713, -0.006161091, -0.004973204, -0.002954811,
		0,
		0.002954811, 0.004973204, 0.006161091, 0.006623713,
		0.006465723, 0.005791284, 0.004704156, 0.003307785, 0.001705381,
		0,
	},
	WindowNuttall: {
		0, -0.000808341, -0.001567742, -0.002229250, -0.002743894,
		-0.003062672, -0.003136540, -0.002916404, -0.002353105, -0.001397410,
		0,
		0.001397410, 0.002353105, 0.002916404, 0.003136540,
		0.003062672, 0.002743894, 0.002229250, 0.001567742, 0.000808341,
		0,
	},
}

// correctPeakBias removes the tabulated window-specific interpolation bias
// from a fractional bin offset using linear interpolation between entries.
func correctPeakBias(table *[21]float64, delta float64) float64 {
	if delta <= -0.5 || delta >= 0.5 {
		return delta
	}
	pos := (delta + 0.5) / 0.05
	idx := int(pos)
	if idx >= len(table)-1 {
		idx = len(table) - 2
	}
	frac := pos - float64(idx)
	bias := table[idx] + frac*(table[idx+1]-table[idx])
	return delta - bias
}

// peakDelta computes the fractional offset of the dominant tone relative to
// peakBin from the three surrounding bin magnitudes.
//
// All windows fit a parabola through the log magnitudes; non-rectangular
// windows additionally subtract their tabulated systematic bias
// (correctPeakBias), which brings single-tone error well below half a hertz.
func (a *Analyzer) peakDelta(peak int) float64 {
	alpha := a.mag[peak-1]
	beta := a.mag[peak]
	gamma := a.mag[peak+1]
	if alpha <= 0 || beta <= 0 || gamma <= 0 {
		return 0
	}
	la, lb, lg := math.Log(alpha), math.Log(beta), math.Log(gamma)
	denom := la - 2*lb + lg
	if denom == 0 {
		return 0
	}
	delta := 0.5 * (la - lg) / denom
	if delta > 1 {
		delta = 1
	} else if delta < -1 {
		delta = -1
	}
	if table, ok := peakBiasTables[a.cfg.Window]; ok && -0.5 < delta && delta < 0.5 {
		delta = correctPeakBias(table, delta)
	}
	return delta
}
