package splice

import (
	"math"
)

// Codec models a perceptual-style audio codec whose bitstream framing introduces a
// fixed encoder delay on encode and a matching decoder delay on decode, exactly like
// an MP3 encoder (1152 samples of lookahead plus a 576-sample filter bank delay).
//
// The codec is intentionally lossless in amplitude so that length/phase defects show
// up deterministically; only the delay behaviour of a real codec is simulated.
type Codec struct {
	// EncoderDelay is the number of silent/padding samples the encoder prepends to the
	// coded stream. The decoder emits the same number before the first real sample.
	EncoderDelay int

	// DecoderPadding is the number of padding samples the decoder appends after the
	// real content (e.g. MP3 end padding to complete the final granule).
	DecoderPadding int
}

// MP3LikeDelay returns the canonical MP3 delay profile: 1152 encoder delay samples
// plus 576 samples of end padding.
func MP3LikeDelay() Codec {
	return Codec{EncoderDelay: 1152, DecoderPadding: 576}
}

// Encode codes one segment. The returned bitstream is EncoderDelay samples longer at
// the front; its tail also carries the end padding that the decoder will emit.
func (c Codec) Encode(seg [][2]float64) [][2]float64 {
	out := make([][2]float64, 0, c.EncoderDelay+len(seg)+c.DecoderPadding)
	silence := [2]float64{}
	for i := 0; i < c.EncoderDelay; i++ {
		out = append(out, silence)
	}
	out = append(out, seg...)
	for i := 0; i < c.DecoderPadding; i++ {
		out = append(out, silence)
	}
	return out
}

// Decode reverses Encode, emitting the full delayed/padded PCM stream. Callers that
// need the original segment must trim EncoderDelay samples from the head.
func (c Codec) Decode(bitstream [][2]float64) [][2]float64 {
	out := make([][2]float64, len(bitstream))
	copy(out, bitstream)
	return out
}

// TrimEncoderDelay removes the encoder delay from the head of a decoded stream and
// returns the real content. It is the delay-compensation step required by the spec.
func (c Codec) TrimEncoderDelay(decoded [][2]float64) [][2]float64 {
	if c.EncoderDelay >= len(decoded) {
		return nil
	}
	return decoded[c.EncoderDelay:]
}

// approxEqual compares two sample streams within a fixed tolerance. The simulated
// codec is amplitude-lossless, but the helper is kept for future lossy codecs.
func approxEqual(a, b [][2]float64, tol float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if math.Abs(a[i][0]-b[i][0]) > tol || math.Abs(a[i][1]-b[i][1]) > tol {
			return false
		}
	}
	return true
}
