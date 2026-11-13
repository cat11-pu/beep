// Command resamplehqverify runs the acceptance measurements for the
// anti-aliased HQ resampler in both 44.1 kHz <-> 48 kHz directions.
//
// It writes, into the output directory (default artifacts):
//
//   - freq_response_<dir>.csv / .png : measured magnitude response,
//   - noise_spectrum_<dir>.csv / .png : white-noise output PSD,
//   - resamplehq_report.csv : one summary row per direction plus a round-trip.
//
// All excitation signals use fixed seeds, so repeated runs are reproducible.
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strconv"

	"github.com/faiface/beep"
)

type direction struct {
	name    string
	oldRate beep.SampleRate
	newRate beep.SampleRate
}

func main() {
	outDir := flag.String("out", "artifacts", "directory for CSV/PNG outputs")
	flag.Parse()
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fatal(err)
	}

	directions := []direction{
		{"44100_to_48000", 44100, 48000},
		{"48000_to_44100", 48000, 44100},
	}

	var report [][]string
	report = append(report, []string{
		"direction", "delay_input_samples", "delay_seconds", "delay_output_samples",
		"passband_ripple_db", "stopband_atten_db", "thdn_1khz_db", "thdn_997hz_db",
		"stopband_gain_worst_db", "inband_flatness_db", "impulse_delay_residual_seconds",
	})

	for _, d := range directions {
		fmt.Printf("== measuring %s ==\n", d.name)

		r := beep.ResampleHQ(d.oldRate, d.newRate, &monoStreamer{x: make([]float64, 16)})
		delayIn := r.DelayInputSamples()
		delaySec := r.Delay().Seconds()
		delayOut := r.DelayOutputSamples()

		freq, ripple := measureFrequencyResponse(d.oldRate, d.newRate)
		thdn1k, _ := measureTHDN(d.oldRate, d.newRate, 1000)
		thdn997, _ := measureTHDN(d.oldRate, d.newRate, 997)
		_, flatness, psd, df := measureNoise(d.oldRate, d.newRate)
		stopWorst, stopDetails := measureStopband(d.oldRate, d.newRate)
		// Independent cross-check: the impulse-response centroid must coincide
		// with the time-mapped input position, i.e. zero residual offset once the
		// design delay is accounted for.
		delayOffsetOut := measureImpulseDelay(d.oldRate, d.newRate)
		delayOffsetSec := delayOffsetOut / float64(d.newRate)

		// Stop-band attenuation is the worst rejection measured on injected
		// out-of-band tones; stopWorst is already a negative dB gain.
		stopAtten := -stopWorst

		writeFreqCSV(filepath.Join(*outDir, "freq_response_"+d.name+".csv"), freq)
		writeFreqPlot(filepath.Join(*outDir, "freq_response_"+d.name+".png"), freq)
		writeStopbandCSV(filepath.Join(*outDir, "stopband_"+d.name+".csv"), stopDetails)
		writePSDCSV(filepath.Join(*outDir, "noise_spectrum_"+d.name+".csv"), psd, df)
		writePSDPlot(filepath.Join(*outDir, "noise_spectrum_"+d.name+".png"), psd, df)
		sweepPSD, sweepDF := sweepSpectrum(d.oldRate, d.newRate)
		writePSDCSV(filepath.Join(*outDir, "sweep_spectrum_"+d.name+".csv"), sweepPSD, sweepDF)
		writeSweepPlot(filepath.Join(*outDir, "sweep_spectrum_"+d.name+".png"), sweepPSD, sweepDF)

		report = append(report, []string{
			d.name,
			strconv.Itoa(delayIn),
			strconv.FormatFloat(delaySec, 'f', 9, 64),
			strconv.FormatFloat(delayOut, 'f', 3, 64),
			strconv.FormatFloat(ripple, 'f', 5, 64),
			strconv.FormatFloat(stopAtten, 'f', 2, 64),
			strconv.FormatFloat(thdn1k, 'f', 2, 64),
			strconv.FormatFloat(thdn997, 'f', 2, 64),
			strconv.FormatFloat(stopWorst, 'f', 2, 64),
			strconv.FormatFloat(flatness, 'f', 3, 64),
			strconv.FormatFloat(delayOffsetSec, 'f', 9, 64),
		})

		fmt.Printf("  delay: %d input samples = %.6f ms (%.3f output samples)\n",
			delayIn, delaySec*1000, delayOut)
		fmt.Printf("  pass-band ripple: %.4f dB | stop-band atten: %.2f dB\n", ripple, stopAtten)
		fmt.Printf("  THD+N 1kHz: %.2f dB | 997Hz: %.2f dB\n", thdn1k, thdn997)
		fmt.Printf("  stop-band gain (worst): %.2f dB | in-band flatness: %.3f dB\n", stopWorst, flatness)
		fmt.Printf("  impulse-centroid delay residual: %.6f ms (%.4f output samples, want ~0)\n",
			delayOffsetSec*1000, delayOffsetOut)
	}

	// Round-trip 44.1 -> 48 -> 44.1 THD+N on a 1 kHz tone.
	rt := roundTripTHDN()
	report = append(report, []string{"roundtrip_44100_48000_44100", "thdn_1khz_db",
		strconv.FormatFloat(rt, 'f', 2, 64)})
	fmt.Printf("== round-trip 44.1->48->44.1 THD+N: %.2f dB ==\n", rt)

	if err := writeReport(filepath.Join(*outDir, "resamplehq_report.csv"), report); err != nil {
		fatal(err)
	}

	// Reproducibility guard: resample the same signal twice and require equality.
	if err := assertReproducible(); err != nil {
		fatal(err)
	}
	fmt.Println("reproducibility: identical output across repeated conversions OK")
	fmt.Println("all artifacts written to", *outDir)
}

func roundTripTHDN() float64 {
	const rate = 44100
	tone := sineTone(rate*4, rate, 1000, 0.9)
	once := resampleMono(44100, 48000, tone)
	back := resampleMono(48000, 44100, once)

	return thdnOfSignal(back, rate, 1000)
}

func assertReproducible() error {
	tone := sineTone(8192, 44100, 997, 0.7)
	a := resampleMono(44100, 48000, tone)
	b := resampleMono(44100, 48000, tone)
	if len(a) != len(b) {
		return fmt.Errorf("reproducibility: length mismatch %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			return fmt.Errorf("reproducibility: sample %d differs", i)
		}
	}
	return nil
}

func writeFreqCSV(path string, points []freqPoint) {
	rows := [][]string{{"frequency_hz", "gain_db", "phase_rad"}}
	for _, p := range points {
		rows = append(rows, []string{
			strconv.FormatFloat(p.Hz, 'f', 3, 64),
			strconv.FormatFloat(p.GainDB, 'f', 6, 64),
			strconv.FormatFloat(p.Phase, 'f', 6, 64),
		})
	}
	mustWriteCSV(path, rows)
}

func writeFreqPlot(path string, points []freqPoint) {
	xs := make([]float64, len(points))
	ys := make([]float64, len(points))
	for i, p := range points {
		xs[i] = p.Hz
		ys[i] = p.GainDB
	}
	ref := map[float64]color.Color{
		0:    color.RGBA{0, 150, 0, 255},
		-0.1: color.RGBA{200, 120, 0, 255},
		-80:  color.RGBA{200, 0, 0, 255},
	}
	if err := plotCurve(path, "frequency response", xs, ys, true, -100, 5, ref); err != nil {
		fatal(err)
	}
}

func writeStopbandCSV(path string, points []freqPoint) {
	rows := [][]string{{"probe_frequency_hz", "output_gain_db"}}
	for _, p := range points {
		rows = append(rows, []string{
			strconv.FormatFloat(p.Hz, 'f', 1, 64),
			strconv.FormatFloat(p.GainDB, 'f', 3, 64),
		})
	}
	mustWriteCSV(path, rows)
}

func writePSDCSV(path string, psd []float64, df float64) {
	rows := [][]string{{"frequency_hz", "power_db"}}
	for k, p := range psd {
		rows = append(rows, []string{
			strconv.FormatFloat(float64(k)*df, 'f', 2, 64),
			strconv.FormatFloat(toDB(p), 'f', 4, 64),
		})
	}
	mustWriteCSV(path, rows)
}

func writePSDPlot(path string, psd []float64, df float64) {
	// Drop the DC bin (k==0): log10(0) is undefined on the logarithmic x-axis
	// and would corrupt the whole polyline's coordinate transform.
	xs := make([]float64, 0, len(psd)-1)
	ys := make([]float64, 0, len(psd)-1)
	for k := 1; k < len(psd); k++ {
		xs = append(xs, float64(k)*df)
		ys = append(ys, toDB(psd[k]))
	}
	ref := map[float64]color.Color{
		-80: color.RGBA{200, 0, 0, 255},
	}
	if err := plotCurve(path, "noise PSD", xs, ys, true, -140, 10, ref); err != nil {
		fatal(err)
	}
}

func writeSweepPlot(path string, psd []float64, df float64) {
	xs := make([]float64, 0, len(psd)-1)
	ys := make([]float64, 0, len(psd)-1)
	for k := 1; k < len(psd); k++ {
		xs = append(xs, float64(k)*df)
		ys = append(ys, toDB(psd[k]))
	}
	ref := map[float64]color.Color{
		-80: color.RGBA{200, 0, 0, 255},
	}
	if err := plotCurve(path, "sweep spectrum", xs, ys, true, -120, 10, ref); err != nil {
		fatal(err)
	}
}

func writeReport(path string, rows [][]string) error {
	return writeCSV(path, rows)
}

func mustWriteCSV(path string, rows [][]string) {
	if err := writeCSV(path, rows); err != nil {
		fatal(err)
	}
}

func writeCSV(path string, rows [][]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	if err := w.WriteAll(rows); err != nil {
		return err
	}
	w.Flush()
	return w.Error()
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "resamplehqverify:", err)
	os.Exit(1)
}
