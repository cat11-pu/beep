package splice

import "math"

// Default sample rate for the acceptance fixtures, 48 kHz like a typical production
// pipeline.
const SampleRate = 48000

// FixtureFreq is the tone frequency used by Fixture. It divides SampleRate so the
// generated segments are whole-period aligned for the loop phase check.
const FixtureFreq = 480.0

// ToneParams describes one generated sine-tone segment.
type ToneParams struct {
	Freq    float64 // Hz
	Seconds float64
	Phase   float64 // radians at sample 0
}

// GenerateTone synthesizes a stereo sine segment. Both channels carry the same signal
// so every check can be validated on either channel.
func GenerateTone(p ToneParams) [][2]float64 {
	n := int(p.Seconds * SampleRate)
	out := make([][2]float64, n)
	for i := 0; i < n; i++ {
		v := math.Sin(p.Phase + 2*math.Pi*p.Freq*float64(i)/SampleRate)
		out[i] = [2]float64{v, v}
	}
	return out
}

// CodeRound runs one segment through the simulated codec and returns a Segment pairing
// the original samples with the delayed decoded PCM.
func CodeRound(codec Codec, original [][2]float64) Segment {
	bitstream := codec.Encode(original)
	return Segment{Original: original, Decoded: codec.Decode(bitstream)}
}

// Fixture builds the standard acceptance fixture: two continuous-phase sine segments
// coded with the MP3-like delay profile. The second segment starts at the exact phase
// at which the first ends, so a correct splice is waveform-continuous by construction.
func Fixture() (Codec, Segment, Segment) {
	codec := MP3LikeDelay()

	// FixtureFreq divides the 48 kHz sample rate (100 samples/period), so each segment is an
	// integer number of periods and the loop boundary end->start is seamless by
	// construction; any residual wrap offset therefore indicates real phase drift.
	const freq = FixtureFreq
	const seconds = 0.05
	// Start at a non-zero phase so neither segment boundary lands accidentally on a
	// zero crossing; the blank-sample check must not be defeated by fixture luck.
	const startPhase = math.Pi / 7

	first := GenerateTone(ToneParams{Freq: freq, Seconds: seconds, Phase: startPhase})
	n1 := len(first)
	continuationPhase := startPhase + 2*math.Pi*freq*float64(n1)/SampleRate
	second := GenerateTone(ToneParams{Freq: freq, Seconds: seconds, Phase: continuationPhase})

	return codec, CodeRound(codec, first), CodeRound(codec, second)
}
