package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/faiface/beep/effects"
)

func writeReport(path string, cfg effects.LimiterConfig, raw, lim Metrics) error {
	var b strings.Builder
	b.WriteString("# Soft Limiter Analysis Report\n\n")
	b.WriteString(fmt.Sprintf("Scenario: %d simultaneous explosion/impact voices, %.0f s at %d Hz, stereo float.\n\n",
		voices, durationS, sampleRate))

	b.WriteString("## Configuration (default, out-of-the-box)\n\n")
	b.WriteString("| Parameter | Value |\n|---|---|\n")
	b.WriteString(fmt.Sprintf("| Target loudness | %.1f dBFS |\n", cfg.TargetLoudness))
	b.WriteString(fmt.Sprintf("| Peak ceiling | %.1f dBFS |\n", cfg.PeakCeiling))
	b.WriteString(fmt.Sprintf("| Leveler attack / release | %.0f / %.0f ms |\n", cfg.LevelerAttack, cfg.LevelerRelease))
	b.WriteString(fmt.Sprintf("| Peak limiter threshold | %.1f dBFS |\n", cfg.LimiterThreshold))
	b.WriteString(fmt.Sprintf("| Look-ahead (attack) | %.1f ms |\n", cfg.LimiterAttack))
	b.WriteString(fmt.Sprintf("| Limiter release | %.0f ms |\n", cfg.LimiterRelease))
	b.WriteString(fmt.Sprintf("| Soft knee width | %.1f dB |\n\n", cfg.Knee))

	b.WriteString("## Objective metrics\n\n")
	b.WriteString("| Metric | Raw sum | Soft-limited |\n|---|---|---|\n")
	b.WriteString(fmt.Sprintf("| Sample peak | %.2f dBFS | %.2f dBFS |\n", raw.PeakDBFS, lim.PeakDBFS))
	b.WriteString(fmt.Sprintf("| Integrated loudness (K-weighted approx.) | %.2f LUFS-like | %.2f LUFS-like |\n", raw.LUFSApprox, lim.LUFSApprox))
	b.WriteString(fmt.Sprintf("| Full-track RMS | %.2f dBFS | %.2f dBFS |\n", raw.RMSDBFS, lim.RMSDBFS))
	b.WriteString(fmt.Sprintf("| Crest factor (peak − RMS) | %.2f dB | %.2f dB |\n", raw.CrestFactor, lim.CrestFactor))
	b.WriteString(fmt.Sprintf("| Short-term dynamic range (P95 − P10) | %.2f dB | %.2f dB |\n", raw.DRShortTerm, lim.DRShortTerm))
	b.WriteString(fmt.Sprintf("| Clipped samples (≥ ±1.0) | %d (%.3f%%) | %d (%.3f%%) |\n\n",
		raw.ClipCount, raw.ClipRatePct, lim.ClipCount, lim.ClipRatePct))

	b.WriteString("## Spectral band energy (dB relative to loudest band)\n\n")
	b.WriteString("| Band | Raw | Limited | Δ |\n|---|---|---|---|\n")
	for i, edge := range bandEdges {
		delta := lim.BandEnergyDB[i] - raw.BandEnergyDB[i]
		b.WriteString(fmt.Sprintf("| %s | %.1f dB | %.1f dB | %+.1f dB |\n",
			edge.name, raw.BandEnergyDB[i], lim.BandEnergyDB[i], delta))
	}

	b.WriteString("\n## Figures\n\n")
	b.WriteString("- `waveform_compare.svg` — raw vs limited waveform with the -1 dBFS ceiling and clip lines.\n")
	b.WriteString("- `band_energy.svg` — spectral energy distribution across seven bands.\n")
	b.WriteString("- `gain_reduction.svg` — remaining headroom envelope over time.\n\n")

	b.WriteString("## Listening review checklist (audio team)\n\n")
	b.WriteString("Compare `raw_mix.wav` against `limited_mix.wav` and confirm:\n\n")
	b.WriteString("- [ ] No hard-clipping distortion on the dense impact cluster near 1.0 s.\n")
	b.WriteString("- [ ] Explosion/collision transients still punch; attack is not softened or dulled.\n")
	b.WriteString("- [ ] No audible pumping or breathing during the 32-voice overlap.\n")
	b.WriteString("- [ ] Stereo image and pan positions are stable (gain is stereo-linked).\n")
	b.WriteString("- [ ] Quiet ambient bed is not raised into audible noise by the leveler.\n")
	b.WriteString("- [ ] Limiter release between impacts is inaudible (no tremolo on decays).\n")

	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func printSummary(raw, lim Metrics, outDir string) {
	fmt.Println("Soft limiter analysis complete.")
	fmt.Printf("  peak:     %.2f dBFS -> %.2f dBFS (ceiling -1.00 dBFS)\n", raw.PeakDBFS, lim.PeakDBFS)
	fmt.Printf("  clipping: %d samples (%.3f%%) -> %d samples\n", raw.ClipCount, raw.ClipRatePct, lim.ClipCount)
	fmt.Printf("  loudness: %.2f -> %.2f LUFS-like (target -16)\n", raw.LUFSApprox, lim.LUFSApprox)
	fmt.Printf("  DR(short): %.2f dB -> %.2f dB\n", raw.DRShortTerm, lim.DRShortTerm)
	fmt.Println("Output written to", outDir)
}
