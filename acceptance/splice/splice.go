// Package splice implements two-segment audio concatenation with codec encoder-delay
// compensation and provides the acceptance checks defined in ACCEPTANCE.md:
//
//  1. waveform continuity at the splice point, verified by first difference;
//  2. no blank samples at the splice point;
//  3. total length equals len(a)+len(b) minus the encoder-delay compensation;
//  4. encoder delay fully compensated, decoded length matches the original;
//  5. no phase drift at loop start/end over 100 iterations.
package splice

import "errors"

// Result holds a concatenated stream together with the metadata needed for acceptance.
type Result struct {
	// Samples is the delay-compensated concatenation of the two segments.
	Samples [][2]float64

	// SpliceIndex is the sample index where the second segment begins.
	SpliceIndex int

	// FirstLen is the number of real samples contributed by the first segment.
	FirstLen int

	// SecondLen is the number of real samples contributed by the second segment.
	SecondLen int
}

// Segment pairs an original (pre-encode) segment with its decoded PCM. The decoded
// stream still carries the codec encoder delay on its head and end padding on its
// tail; Splice performs the compensation.
type Segment struct {
	Original [][2]float64
	Decoded  [][2]float64
}

// Splice concatenates two coded segments.
//
// Compensation rule: the first segment is trimmed by the full encoder delay on its
// head, while the second segment is trimmed by the encoder delay on its head and by
// the first segment's end padding at the junction is avoided because padding belongs
// to each segment's own tail. Concretely each decoded segment is reduced to its real
// content by trimming EncoderDelay from the head, and the trailing padding of the
// first segment is dropped so that no gap is introduced between the two segments.
func Splice(codec Codec, first, second Segment) (Result, error) {
	if len(first.Original) == 0 || len(second.Original) == 0 {
		return Result{}, errors.New("splice: both segments must be non-empty")
	}

	firstReal, err := realContent(codec, first)
	if err != nil {
		return Result{}, err
	}
	secondReal, err := realContent(codec, second)
	if err != nil {
		return Result{}, err
	}

	joined := make([][2]float64, 0, len(firstReal)+len(secondReal))
	joined = append(joined, firstReal...)
	spliceIndex := len(joined)
	joined = append(joined, secondReal...)

	return Result{
		Samples:     joined,
		SpliceIndex: spliceIndex,
		FirstLen:    len(firstReal),
		SecondLen:   len(secondReal),
	}, nil
}

// realContent strips head delay and tail padding from a decoded segment, leaving the
// samples that correspond one-to-one to the original segment.
func realContent(codec Codec, seg Segment) ([][2]float64, error) {
	head := codec.EncoderDelay
	tail := head + codec.DecoderPadding
	if tail > len(seg.Decoded) {
		return nil, errors.New("splice: decoded stream shorter than codec delay+padding")
	}
	real := seg.Decoded[head : len(seg.Decoded)-codec.DecoderPadding]
	if len(real) != len(seg.Original) {
		return nil, errors.New("splice: compensated length does not match original segment")
	}
	return real, nil
}
