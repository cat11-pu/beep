package spectrum

import "math"

// Window selects the window function applied to every analysis frame before
// the DFT. All windows are generated in their periodic form (length N).
type Window int

const (
	// WindowHann is the default window. It offers a good trade-off between
	// frequency resolution and spectral leakage and is the window assumed by
	// the Grandke peak-frequency interpolator.
	WindowHann Window = iota
	// WindowHamming has a lower first side lobe than Hann but higher leakage
	// far from the peak.
	WindowHamming
	// WindowBlackman provides stronger side-lobe suppression at the cost of a
	// wider main lobe.
	WindowBlackman
	// WindowNuttall is a four-term window with very low side lobes.
	WindowNuttall
	// WindowRectangular applies no weighting.
	WindowRectangular
)

func (w Window) valid() bool {
	return w >= WindowHann && w <= WindowRectangular
}

// String returns the human-readable name of the window.
func (w Window) String() string {
	switch w {
	case WindowHann:
		return "hann"
	case WindowHamming:
		return "hamming"
	case WindowBlackman:
		return "blackman"
	case WindowNuttall:
		return "nuttall"
	case WindowRectangular:
		return "rectangular"
	default:
		return "unknown"
	}
}

// windowTable holds a precomputed window together with its gain statistics, so
// that per-frame processing never computes trigonometric functions.
type windowTable struct {
	values []float64
	s1     float64 // sum of weights, coherent-gain statistics
	s2     float64 // sum of squared weights, used for power correction
}

func newWindowTable(w Window, n int) windowTable {
	t := windowTable{values: make([]float64, n)}
	step := 2 * math.Pi / float64(n)
	for i := 0; i < n; i++ {
		x := step * float64(i)
		switch w {
		case WindowHann:
			t.values[i] = 0.5 - 0.5*math.Cos(x)
		case WindowHamming:
			t.values[i] = 0.54 - 0.46*math.Cos(x)
		case WindowBlackman:
			t.values[i] = 0.42 - 0.5*math.Cos(x) + 0.08*math.Cos(2*x)
		case WindowNuttall:
			t.values[i] = 0.355768 - 0.487396*math.Cos(x) +
				0.144232*math.Cos(2*x) - 0.012604*math.Cos(3*x)
		case WindowRectangular:
			t.values[i] = 1
		}
		t.s1 += t.values[i]
		t.s2 += t.values[i] * t.values[i]
	}
	return t
}
