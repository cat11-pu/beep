// splice-acceptance runs the five-check audio concatenation acceptance suite and
// writes the verdict plus the before/after waveform comparison evidence (SVG).
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/faiface/beep/acceptance/splice"
)

func main() {
	out := flag.String("out", "splice-evidence.svg", "path of the waveform comparison SVG")
	loops := flag.Int("loops", 100, "loop iterations for the phase-drift check")
	flag.Parse()

	codec, first, second := splice.Fixture()
	result, err := splice.Splice(codec, first, second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "splice failed: %v\n", err)
		os.Exit(2)
	}

	report := splice.Run(codec, first, second, result, *loops)

	fmt.Println("Audio splice acceptance report")
	fmt.Println("==============================")
	for i, c := range report.Checks {
		verdict := "PASS"
		if !c.Pass {
			verdict = "FAIL"
		}
		fmt.Printf("%d. [%-4s] %s\n    %s\n", i+1, verdict, c.Name, c.Detail)
	}
	fmt.Println("==============================")
	if report.Passed() {
		fmt.Println("OVERALL: PASS (all 5 checks passed)")
	} else {
		fmt.Println("OVERALL: FAIL")
	}

	f, err := os.Create(*out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create evidence file: %v\n", err)
		os.Exit(2)
	}
	defer f.Close()
	if err := splice.WriteWaveformPlot(f, result, first, second, splice.PlotConfig{Window: 1400}); err != nil {
		fmt.Fprintf(os.Stderr, "render evidence plot: %v\n", err)
		os.Exit(2)
	}
	fmt.Printf("Evidence waveform comparison written to %s\n", *out)

	if !report.Passed() {
		os.Exit(1)
	}
}
