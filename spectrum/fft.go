package spectrum

import (
	"math"
	"math/bits"
)

// fftPlan is a precomputed in-place radix-2 decimation-in-time FFT.
//
// The bit-reversal permutation and all twiddle factors are computed once when
// the plan is created; transform itself performs no heap allocations.
type fftPlan struct {
	n       int
	rev     []int
	twiddle []complex128 // exp(-2*pi*i*k/n) for k in [0, n/2)
}

func newFFTPlan(n int) *fftPlan {
	if n < 2 || n&(n-1) != 0 {
		panic("spectrum: fft size must be a power of two")
	}
	p := &fftPlan{
		n:       n,
		rev:     make([]int, n),
		twiddle: make([]complex128, n/2),
	}
	log2n := bits.Len(uint(n)) - 1
	for i := 1; i < n; i++ {
		p.rev[i] = int(bits.Reverse32(uint32(i)) >> (32 - uint(log2n)))
	}
	for k := 0; k < n/2; k++ {
		angle := -2 * math.Pi * float64(k) / float64(n)
		p.twiddle[k] = complex(math.Cos(angle), math.Sin(angle))
	}
	return p
}

// transform computes the DFT of a in place. len(a) must equal the plan size.
func (p *fftPlan) transform(a []complex128) {
	n := p.n
	for i := 1; i < n; i++ {
		j := p.rev[i]
		if j > i {
			a[i], a[j] = a[j], a[i]
		}
	}
	for length := 2; length <= n; length <<= 1 {
		half := length >> 1
		step := n / length
		for start := 0; start < n; start += length {
			left := a[start : start+half]
			right := a[start+half : start+length]
			for k := 0; k < half; k++ {
				t := p.twiddle[k*step] * right[k]
				u := left[k]
				left[k] = u + t
				right[k] = u - t
			}
		}
	}
}
