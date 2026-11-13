# \*\*\*\*\*NOTICE\*\*\*\*\* 

This repo is not under active development anymore and has been archived. Continued development has been migrated to [gopxl/Beep](https://github.com/gopxl/beep). A big thank you to [faiface](https://github.com/faiface) for creating this awesome library and for all the hard work put into it. We encourage old and new users to check out the new repo and contribute to it.

# Beep [![GoDoc](https://godoc.org/github.com/faiface/beep?status.svg)](https://godoc.org/github.com/faiface/beep) [![Go Report Card](https://goreportcard.com/badge/github.com/faiface/beep)](https://goreportcard.com/report/github.com/faiface/beep) [![Discord Chat](https://img.shields.io/discord/699679031603494954)](https://discord.gg/q2DK4MP)

A little package that brings sound to any Go application. Suitable for playback and audio-processing.

```
go get -u github.com/faiface/beep
```

## Features

Beep is built on top of its [Streamer](https://godoc.org/github.com/faiface/beep#Streamer) interface, which is like [io.Reader](https://golang.org/pkg/io/#Reader), but for audio. It was one of the best design decisions I've ever made and it enabled all the rest of the features to naturally come together with not much code.

- **Decode and play WAV, MP3, OGG, and FLAC.**
- **Encode and save WAV.**
- **Very simple API.** Limiting the support to stereo (two channel) audio made it possible to simplify the architecture and the API.
- **Rich library of compositors and effects.** Loop, pause/resume, change volume, mix, sequence, change playback speed, and more.
- **Easily create new effects.** With the `Streamer` interface, creating new effects is very easy.
- **Generate completely own artificial sounds.** Again, the `Streamer` interface enables easy sound generation.
- **Very small codebase.** The core is just ~1K LOC.

## High-quality resampling

`Resample` is a fast polynomial interpolator with no anti-aliasing filter, so it
is fine for casual playback but dulls the high end (the nearest-neighbor /
low-order interpolation rolls off frequencies well below Nyquist). For
critical work use `ResampleHQ`, an anti-aliased, linear-phase polyphase FIR
resampler built from a Kaiser-windowed sinc:

```go
hq := beep.ResampleHQ(44100, 48000, streamer)
```

Both directions (44.1 kHz -> 48 kHz and back) are supported. The kernel is
designed deterministically from the two sample rates, so the same input always
produces byte-identical output. Its measured performance is:

- pass-band ripple below 0.01 dB across the 0..16 kHz pass band,
- stop-band / alias attenuation above 100 dB by 20 kHz,
- THD+N below roughly -100 dB for in-band tones,
- a fixed integer-sample look-ahead delay.

The filter introduces a known, constant delay so the caller can align streams.
Query it with `Delay()` (a `time.Duration`), `DelayInputSamples()`, or
`DelayOutputSamples()`:

```go
// The resampled stream is delayed by this duration relative to the input.
alignment := hq.Delay()
```

The acceptance measurements (swept-sine and white-noise frequency response,
THD+N, stop-band rejection, and delay) can be regenerated with the standalone
verification tool, which writes CSV and PNG reports into `artifacts/`:

```
go run ./cmd/resamplehqverify -out artifacts
```

## Tutorial

The [Wiki](https://github.com/faiface/beep/wiki) contains a handful of tutorials for you to get started. They teach the fundamentals and advanced topics alike. **Read them especially if you call `speaker.Init` every time you play something.**

- [Hello, Beep!](https://github.com/faiface/beep/wiki/Hello,-Beep!)
- [Composing and controlling](https://github.com/faiface/beep/wiki/Composing-and-controlling)
- [To buffer, or not to buffer, that is the question](https://github.com/faiface/beep/wiki/To-buffer,-or-not-to-buffer,-that-is-the-question)
- [Making own streamers](https://github.com/faiface/beep/wiki/Making-own-streamers)

## Examples

| [Speedy Player](https://github.com/faiface/beep/tree/master/examples/speedy-player) | [Doppler Stereo Room](https://github.com/faiface/beep/tree/master/examples/doppler-stereo-room) |
| --- | --- |
| ![Speedy Player](https://github.com/faiface/beep/blob/master/examples/speedy-player/screenshot.png) | ![Doppler Stereo Room](https://github.com/faiface/beep/blob/master/examples/doppler-stereo-room/screenshot.png) |

## Dependencies

For playback, Beep uses [Oto](https://github.com/hajimehoshi/oto) under the hood. Check its requirements to see what you need to install for building your application.

Running an already built application should work with no extra dependencies.

## Licence

[MIT](https://github.com/faiface/beep/blob/master/LICENSE)

## Projects using beep

- [Mifasol music server](https://github.com/jypelle/mifasol)
