package splice

import (
	"fmt"
	"math"
)

// Check outcomes are collected into a Report. All five checks must pass for the
// concatenation to be accepted.
type Check struct {
	Name   string
	Pass   bool
	Detail string
}

// Report is the full acceptance verdict.
type Report struct {
	Checks []Check
}

// Passed reports whether every check passed.
func (r Report) Passed() bool {
	for _, c := range r.Checks {
		if !c.Pass {
			return false
		}
	}
	return true
}

// Thresholds used by the acceptance checks. They are defined as named constants so the
// "normal range" is explicit rather than magic numbers.
const (
	// diffMargin is how much the junction first-difference may exceed the worst
	// first-difference observed inside either segment.
	diffMargin = 1e-9

	// blankEpsilon is the amplitude below which a sample is considered blank.
	blankEpsilon = 1e-9

	// phaseTolerance is the maximum accumulated phase drift (radians) allowed after
	// 100 loop iterations.
	phaseTolerance = 1e-6
)

// CheckContinuity verifies waveform continuity at the splice point using the first
// difference: the step between the last sample of segment one and the first sample of
// segment two must stay within the normal step range of the source material.
func CheckContinuity(r Result) Check {
	idx := r.SpliceIndex
	if idx <= 0 || idx >= len(r.Samples) {
		return Check{Name: "continuity", Pass: false, Detail: "splice index out of range"}
	}

	maxNormal := 0.0
	for i := 1; i < len(r.Samples); i++ {
		// The junction pair itself is excluded when establishing the normal range.
		if i == idx {
			continue
		}
		if d := sampleDiff(r.Samples[i-1], r.Samples[i]); d > maxNormal {
			maxNormal = d
		}
	}

	junction := sampleDiff(r.Samples[idx-1], r.Samples[idx])
	limit := maxNormal + diffMargin
	pass := junction <= limit
	return Check{
		Name:   "continuity",
		Pass:   pass,
		Detail: fmt.Sprintf("junction first-difference %.6f, normal-range limit %.6f", junction, limit),
	}
}

// CheckNoBlankAtJunction fails if either sample adjacent to the splice point is blank,
// which would indicate dropped/zeroed samples at the junction.
func CheckNoBlankAtJunction(r Result) Check {
	idx := r.SpliceIndex
	left := r.Samples[idx-1]
	right := r.Samples[idx]
	leftBlank := isBlank(left)
	rightBlank := isBlank(right)
	pass := !leftBlank && !rightBlank
	return Check{
		Name:   "no-blank-junction",
		Pass:   pass,
		Detail: fmt.Sprintf("left amp %.6f, right amp %.6f", maxAmp(left), maxAmp(right)),
	}
}

// CheckLength verifies that the total sample count equals the two original segment
// lengths minus the encoder-delay compensation. Because delay/padding samples are
// trimmed from every coded segment, the expected total is simply len(a)+len(b); the
// formula below makes the compensation term explicit.
func CheckLength(r Result, codec Codec) Check {
	expected := r.FirstLen + r.SecondLen
	compensation := 2 * codec.EncoderDelay
	rawExpected := expected + compensation
	got := len(r.Samples)
	pass := got == expected
	return Check{
		Name: "length",
		Pass: pass,
		Detail: fmt.Sprintf(
			"got %d samples, want %d (= %d raw - %d delay compensation)",
			got, expected, rawExpected, compensation,
		),
	}
}

// CheckDelayCompensation verifies that after encode/decode and trimming, the recovered
// content is identical in length (and samples) to the original pre-encode audio.
func CheckDelayCompensation(codec Codec, first, second Segment, r Result) Check {
	firstRecovered := codec.TrimEncoderDelay(first.Decoded)
	firstRecovered = firstRecovered[:len(first.Original)]
	secondRecovered := codec.TrimEncoderDelay(second.Decoded)
	secondRecovered = secondRecovered[:len(second.Original)]

	lengthOK := len(firstRecovered) == len(first.Original) &&
		len(secondRecovered) == len(second.Original) &&
		len(r.Samples) == len(first.Original)+len(second.Original)
	samplesOK := approxEqual(firstRecovered, first.Original, 1e-12) &&
		approxEqual(secondRecovered, second.Original, 1e-12)
	// The spliced output must actually be the compensated content, not the raw
	// delay-bearing decoded stream.
	outputOK := len(r.Samples) == len(firstRecovered)+len(secondRecovered) &&
		approxEqual(r.Samples[:len(firstRecovered)], firstRecovered, 1e-12) &&
		approxEqual(r.Samples[len(firstRecovered):], secondRecovered, 1e-12)

	rawLen := len(first.Decoded) + len(second.Decoded)
	pass := lengthOK && samplesOK && outputOK
	return Check{
		Name: "delay-compensation",
		Pass: pass,
		Detail: fmt.Sprintf("compensated length %d == original length %d; raw decoded length %d",
			len(r.Samples), len(first.Original)+len(second.Original), rawLen),
	}
}

// CheckLoopPhaseDrift loops the concatenated stream 100 times and verifies that the
// phase at the loop boundary (end -> start) does not drift.
//
// The test stream is a sine whose segment boundaries are whole-period aligned, so the
// analytically expected phase at the end of each looped iteration is exactly the phase
// at the loop start. The check reconstructs the expected end sample from the tone
// frequency and compares it with the physically streamed end sample over every
// iteration. A per-iteration phase offset therefore accumulates and is caught.
func CheckLoopPhaseDrift(r Result, freqHz float64, sampleRate int, iterations int) Check {
	if iterations <= 0 {
		iterations = 100
	}
	s := r.Samples

	startPhase, ok := estimateStartPhase(s[0][0], s[1][0])
	if !ok {
		return Check{Name: "loop-phase-100", Pass: false, Detail: "cannot estimate start phase"}
	}
	phaseStep := 2 * math.Pi * freqHz / float64(sampleRate)

	maxDrift := 0.0
	for k := 1; k <= iterations; k++ {
		endPhase := startPhase + phaseStep*float64(k*len(s)-1)
		expected := math.Sin(endPhase)
		// Every loop replays the identical buffered stream, so the observed end sample
		// is always the last physical sample.
		observed := s[len(s)-1][0]
		if d := math.Abs(observed - expected); d > maxDrift {
			maxDrift = d
		}
	}

	pass := maxDrift <= phaseTolerance
	return Check{
		Name:   "loop-phase-100",
		Pass:   pass,
		Detail: fmt.Sprintf("max accumulated phase drift over %d loops: %.3e (tolerance %.1e)", iterations, maxDrift, phaseTolerance),
	}
}

// estimateStartPhase recovers the phase at sample 0 from the first two samples of a
// sine, using the sign of the first step to disambiguate arcsin.
func estimateStartPhase(v0, v1 float64) (float64, bool) {
	phase := math.Asin(clampUnit(v0))
	if v1 < v0 {
		phase = math.Pi - phase
	}
	if math.IsNaN(phase) {
		return 0, false
	}
	return phase, true
}

func clampUnit(v float64) float64 {
	if v > 1 {
		return 1
	}
	if v < -1 {
		return -1
	}
	return v
}

// Run executes all five checks and returns the full report.
func Run(codec Codec, first, second Segment, r Result, loopIterations int) Report {
	return Report{Checks: []Check{
		CheckContinuity(r),
		CheckNoBlankAtJunction(r),
		CheckLength(r, codec),
		CheckDelayCompensation(codec, first, second, r),
		CheckLoopPhaseDrift(r, FixtureFreq, SampleRate, loopIterations),
	}}
}

func sampleDiff(a, b [2]float64) float64 {
	dl := math.Abs(a[0] - b[0])
	dr := math.Abs(a[1] - b[1])
	if dr > dl {
		return dr
	}
	return dl
}

func isBlank(s [2]float64) bool {
	return maxAmp(s) < blankEpsilon
}

func maxAmp(s [2]float64) float64 {
	l := math.Abs(s[0])
	r := math.Abs(s[1])
	if r > l {
		return r
	}
	return l
}
