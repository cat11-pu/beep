# spectrum-benchmark

Benchmark and reference-comparison harness for the
[`spectrum`](../../spectrum) real-time analyzer.

## What it does

For both supported window sizes (1024 and 2048) it:

1. Feeds identical frames to the production `spectrum.Analyzer` and to an
   independent naive O(n²) DFT reference implementation.
2. Measures dominant-frequency error against the true tone frequency and
   per-band energy error against the reference.
3. Times full-frame processing (p50/p99) and compares it with the 60 FPS frame
   budget (16.67 ms; one third = 5.56 ms).
4. Counts steady-state allocations per frame and samples the heap curve over
   12,000 frames.

## Run

```
go run . 
```

Reports are written to `report/`:

- `REPORT.md` — summarized tables.
- `accuracy.csv`, `timing.csv`, `memory.csv` — raw measurements.
- `memory_curve.svg` — heap/cumulative-allocation curve (flat means the
   processing path allocates nothing).

The naive DFT is intentionally slow (the 2048 case exceeds the frame budget),
which quantifies why the production path uses a precomputed radix-2 FFT.
