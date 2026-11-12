package splice

import (
	"math"
	"strings"
	"testing"
)

func TestAcceptance_AllFiveChecksPass(t *testing.T) {
	codec, first, second := Fixture()

	result, err := Splice(codec, first, second)
	if err != nil {
		t.Fatalf("Splice returned error: %v", err)
	}

	report := Run(codec, first, second, result, 100)
	for _, c := range report.Checks {
		if !c.Pass {
			t.Errorf("check %q failed: %s", c.Name, c.Detail)
		} else {
			t.Logf("check %q passed: %s", c.Name, c.Detail)
		}
	}
	if !report.Passed() {
		t.Fatal("acceptance report must pass when delay is compensated")
	}
}

func TestLengthFormula(t *testing.T) {
	codec, first, second := Fixture()
	result, err := Splice(codec, first, second)
	if err != nil {
		t.Fatalf("Splice returned error: %v", err)
	}

	want := len(first.Original) + len(second.Original)
	if got := len(result.Samples); got != want {
		t.Fatalf("length = %d, want %d", got, want)
	}

	// The raw decoded concat is longer by two encoder delays plus two paddings.
	rawLen := len(first.Decoded) + len(second.Decoded)
	if rawLen-want != 2*(codec.EncoderDelay+codec.DecoderPadding) {
		t.Fatalf("uncompensated overhead = %d, want %d",
			rawLen-want, 2*(codec.EncoderDelay+codec.DecoderPadding))
	}
}

func TestDecodedLengthMatchesOriginal(t *testing.T) {
	codec, first, second := Fixture()
	result, err := Splice(codec, first, second)
	if err != nil {
		t.Fatalf("Splice returned error: %v", err)
	}

	originalTotal := len(first.Original) + len(second.Original)
	if len(result.Samples) != originalTotal {
		t.Fatalf("decoded/compensated length %d != original %d",
			len(result.Samples), originalTotal)
	}
}

// TestRejectUncompensatedSplice proves the checks actually catch defects: if the
// encoder delay is left in place, the length check, blank-junction check and delay
// compensation check must all fail.
func TestRejectUncompensatedSplice(t *testing.T) {
	codec, first, second := Fixture()

	raw := make([][2]float64, 0, len(first.Decoded)+len(second.Decoded))
	raw = append(raw, first.Decoded...)
	spliceIdx := len(raw)
	raw = append(raw, second.Decoded...)

	bad := Result{
		Samples:     raw,
		SpliceIndex: spliceIdx,
		FirstLen:    len(first.Original),
		SecondLen:   len(second.Original),
	}

	report := Run(codec, first, second, bad, 100)
	failing := map[string]bool{}
	for _, c := range report.Checks {
		if !c.Pass {
			failing[c.Name] = true
		}
	}
	for _, name := range []string{"length", "no-blank-junction", "delay-compensation"} {
		if !failing[name] {
			t.Errorf("check %q unexpectedly passed on uncompensated splice", name)
		}
	}
	if report.Passed() {
		t.Fatal("uncompensated splice must not be accepted")
	}
}

// TestRejectJunctionDiscontinuity injects an out-of-range jump at the splice point and
// verifies the first-difference continuity check rejects it.
func TestRejectJunctionDiscontinuity(t *testing.T) {
	codec, first, second := Fixture()
	result, err := Splice(codec, first, second)
	if err != nil {
		t.Fatalf("Splice returned error: %v", err)
	}

	result.Samples[result.SpliceIndex] = [2]float64{5.0, 5.0}
	c := CheckContinuity(result)
	if c.Pass {
		t.Fatal("continuity check must reject an out-of-range junction step")
	}
}

// TestRejectBlankJunction zeroes the sample just after the splice point and verifies
// the blank-sample check catches it.
func TestRejectBlankJunction(t *testing.T) {
	codec, first, second := Fixture()
	result, err := Splice(codec, first, second)
	if err != nil {
		t.Fatalf("Splice returned error: %v", err)
	}

	result.Samples[result.SpliceIndex] = [2]float64{}
	c := CheckNoBlankAtJunction(result)
	if c.Pass {
		t.Fatal("blank-junction check must reject zeroed sample at splice point")
	}
}

// TestPhaseDriftDetected verifies that a per-loop phase offset accumulates and is
// detected by the 100-iteration loop check.
func TestPhaseDriftDetected(t *testing.T) {
	codec, first, second := Fixture()
	result, err := Splice(codec, first, second)
	if err != nil {
		t.Fatalf("Splice returned error: %v", err)
	}

	// Shift every sample of the second segment slightly; the wrap now carries a
	// repeated offset that accumulates over iterations.
	drift := 0.01
	for i := result.SpliceIndex; i < len(result.Samples); i++ {
		result.Samples[i][0] += drift
		result.Samples[i][1] += drift
	}
	c := CheckLoopPhaseDrift(result, FixtureFreq, SampleRate, 100)
	if c.Pass {
		t.Fatal("loop phase check must detect accumulated drift")
	}
}

func TestPlotRendered(t *testing.T) {
	codec, first, second := Fixture()
	result, err := Splice(codec, first, second)
	if err != nil {
		t.Fatalf("Splice returned error: %v", err)
	}

	var sb strings.Builder
	if err := WriteWaveformPlot(&sb, result, first, second, PlotConfig{Window: 200}); err != nil {
		t.Fatalf("plot render error: %v", err)
	}
	if !strings.Contains(sb.String(), "<svg") || !strings.Contains(sb.String(), "splice @") {
		t.Fatal("rendered SVG missing expected markers")
	}
}

func TestSpliceIndexAndAmplitude(t *testing.T) {
	codec, first, second := Fixture()
	result, err := Splice(codec, first, second)
	if err != nil {
		t.Fatalf("Splice returned error: %v", err)
	}
	if result.SpliceIndex != len(first.Original) {
		t.Fatalf("splice index = %d, want %d", result.SpliceIndex, len(first.Original))
	}
	for i, s := range result.Samples {
		if math.IsNaN(s[0]) || math.IsNaN(s[1]) {
			t.Fatalf("NaN at sample %d", i)
		}
	}
}
