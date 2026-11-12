# Spectrum analyzer benchmark report

- Sample rate: 48000 Hz, log bands: 32, range: 20-20000 Hz, window: Hann, hop: 50%
- Frames per scenario: 200; machine-generated, see accuracy.csv/timing.csv/memory.csv
- 60 FPS frame budget: 16.67 ms; one-third budget: 5.56 ms

## Precision versus naive-DFT reference

| Window | Peak freq max error (Hz) | Peak freq mean error (Hz) | Reference max error (Hz) | Band energy max rel. error | Band energy mean rel. error |
|---|---|---|---|---|---|
| 1024 | 0.2447 | 0.2211 | 18.1250 | 1.52e-09 | 2.22e-11 |
| 2048 | 0.0945 | 0.0557 | 7.6875 | 3.24e-09 | 3.19e-11 |

## Per-frame timing

| Window | Implementation | p50 (ms) | p99 (ms) | p99 % of frame budget |
|---|---|---|---|---|
| 1024 | production radix-2 FFT | 0.0090 | 0.0160 | 0.10% |
| 1024 | reference naive DFT | 4.7860 | 4.8180 | 28.91% |
| 2048 | production radix-2 FFT | 0.0200 | 0.0310 | 0.19% |
| 2048 | reference naive DFT | 19.1420 | 19.3920 | 116.35% |

## Steady-state memory

| Window | Allocs/frame | Bytes/frame | HeapAlloc first checkpoint (B) | HeapAlloc last checkpoint (B) |
|---|---|---|---|---|
| 1024 | 0 | 0 | 818192 | 818192 |
| 2048 | 0 | 0 | 1434528 | 1434528 |

Memory curve chart: [memory_curve.svg](memory_curve.svg)

Raw data: [accuracy.csv](accuracy.csv), [timing.csv](timing.csv), [memory.csv](memory.csv)
