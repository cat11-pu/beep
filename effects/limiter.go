package effects

import (
	"math"

	"github.com/faiface/beep"
)

// LimiterConfig configures a Limiter. All levels are expressed in dBFS (decibels relative to
// full scale, where 0 dBFS equals the sample value 1.0) and all time constants in milliseconds.
//
// The zero value is not usable; always start from DefaultLimiterConfig so that every field gets a
// sensible, out-of-the-box value and then override only the parameters you need to tune.
type LimiterConfig struct {
	// SampleRate is the sample rate of the wrapped Streamer in Hz. It is required.
	SampleRate int

	// TargetLoudness is the long-term loudness the leveler aligns the signal to, in dBFS.
	// The leveler uses a slow RMS detector, so it rides the overall level instead of
	// reacting to individual transients. -16 dBFS matches the requested default.
	TargetLoudness float64

	// PeakCeiling is the maximum sample value the output is allowed to reach, in dBFS.
	// -1 dBFS leaves 1 dB of headroom for downstream codecs and converters.
	PeakCeiling float64

	// LevelerAttack and LevelerRelease are the time constants of the leveler's gain
	// smoothing. The long release deliberately keeps the dynamics intact: short peaks do
	// not cause the gain to be pumped down.
	LevelerAttack  float64
	LevelerRelease float64

	// LimiterThreshold is the level at which the peak limiter starts to act, in dBFS.
	// Set slightly below the ceiling so the soft knee has room to reach the ceiling
	// without hard clipping.
	LimiterThreshold float64

	// LimiterAttack is the look-ahead / detection time constant. Incoming samples are
	// delayed by this amount, which lets the gain reduction arrive in time and keeps the
	// transient shape instead of flattening its top.
	LimiterAttack float64

	// LimiterRelease is the time constant with which gain reduction is removed after the
	// peak is gone. A medium release recovers between impacts without pumping.
	LimiterRelease float64

	// Knee is the soft-knee width in dB around the limiter threshold. A wider knee
	// rounds the onset of gain reduction and is more transparent.
	Knee float64
}

// DefaultLimiterConfig returns the recommended configuration: loudness aligned to -16 dBFS,
// peaks bounded at -1 dBFS, slow leveler and a transparent look-ahead limiter. The values are
// chosen so that up to 32 simultaneously playing channels stay under the ceiling while keeping
// transients and dynamic range.
func DefaultLimiterConfig(sampleRate int) LimiterConfig {
	return LimiterConfig{
		SampleRate:       sampleRate,
		TargetLoudness:   -16,
		PeakCeiling:      -1,
		LevelerAttack:    80,
		LevelerRelease:   400,
		LimiterThreshold: -6,
		LimiterAttack:    2,
		LimiterRelease:   50,
		Knee:             6,
	}
}

// Validate reports an error if the configuration contains values the Limiter cannot work with.
func (c LimiterConfig) Validate() error {
	switch {
	case c.SampleRate <= 0:
		return errLimiter("limiter: SampleRate must be positive")
	case c.TargetLoudness > 0 || c.TargetLoudness < -120:
		return errLimiter("limiter: TargetLoudness must be between -120 and 0 dBFS")
	case c.PeakCeiling > 0 || c.PeakCeiling < -120:
		return errLimiter("limiter: PeakCeiling must be between -120 and 0 dBFS")
	case c.LimiterThreshold > 0 || c.LimiterThreshold < -120:
		return errLimiter("limiter: LimiterThreshold must be between -120 and 0 dBFS")
	case c.LimiterThreshold >= c.PeakCeiling:
		return errLimiter("limiter: LimiterThreshold must be below PeakCeiling")
	case c.LevelerAttack <= 0 || c.LevelerRelease <= 0:
		return errLimiter("limiter: LevelerAttack and LevelerRelease must be positive")
	case c.LimiterAttack <= 0 || c.LimiterRelease <= 0:
		return errLimiter("limiter: LimiterAttack and LimiterRelease must be positive")
	case c.Knee < 0 || c.Knee > 24:
		return errLimiter("limiter: Knee must be between 0 and 24 dB")
	}
	return nil
}

type limiterError string

func (e limiterError) Error() string { return string(e) }

func errLimiter(msg string) error { return limiterError(msg) }

// Limiter is a transparent, stereo-linked dynamics processor placed after a Mixer or Mix. It
// chains two stages:
//   - a slow loudness leveler that aligns the long-term RMS level to TargetLoudness without
//     chasing transients, and
//   - a look-ahead soft-knee peak limiter that guarantees no sample exceeds PeakCeiling, with a
//     final soft-clip as a safety net.
//
// Because processing is stereo-linked (both channels share the same gain), the stereo image is
// preserved. Limiter propagates the wrapped Streamer's errors.
type Limiter struct {
	Streamer beep.Streamer
	Config   LimiterConfig

	// leveler state
	levelRMS     float64 // long-window mean-square estimate
	levelGain    float64
	levelAtkCoef float64
	levelRelCoef float64
	rmsCoef      float64
	targetLevel  float64

	// peak limiter state
	delay      [][2]float64 // look-ahead ring buffer for the delayed audio
	gainDelay  []float64    // matching ring buffer; gain computed when each sample entered
	delayPos   int
	envelope   float64 // smoothed peak envelope (linear)
	limAtkCoef float64
	limRelCoef float64
	ceiling    float64
	knee       float64

	// telemetry, updated per Stream call
	maxGainReduction  float64 // dB, peak limiter only
	lastGainReduction float64 // dB applied to the most recently output sample
}

// NewLimiter creates a Limiter wrapping s with the given configuration. It panics if the
// configuration is invalid, matching the constructor style of the rest of the package.
func NewLimiter(s beep.Streamer, cfg LimiterConfig) *Limiter {
	if err := cfg.Validate(); err != nil {
		panic(err)
	}
	sampleRate := float64(cfg.SampleRate)
	lookAhead := int(math.Ceil(cfg.LimiterAttack / 1000 * sampleRate))
	if lookAhead < 1 {
		lookAhead = 1
	}
	return &Limiter{
		Streamer:     s,
		Config:       cfg,
		levelGain:    1,
		levelAtkCoef: timeConstant(cfg.LevelerAttack, sampleRate),
		levelRelCoef: timeConstant(cfg.LevelerRelease, sampleRate),
		rmsCoef:      timeConstant(400, sampleRate),
		targetLevel:  dbToLinear(cfg.TargetLoudness),
		delay:        make([][2]float64, lookAhead),
		gainDelay:    ones(lookAhead),
		limAtkCoef:   timeConstant(cfg.LimiterAttack, sampleRate),
		limRelCoef:   timeConstant(cfg.LimiterRelease, sampleRate),
		ceiling:      dbToLinear(cfg.PeakCeiling),
		knee:         cfg.Knee,
	}
}

// SoftLimiter builds a Limiter with the DefaultLimiterConfig for sampleRate. It is the
// out-of-the-box constructor for the common case of capping a multi-channel mix.
func SoftLimiter(s beep.Streamer, sampleRate int) *Limiter {
	return NewLimiter(s, DefaultLimiterConfig(sampleRate))
}

// MaxGainReduction returns the largest peak-limiter gain reduction applied so far, in dB. It is
// intended for diagnostics and tuning (for example logging how hard the limiter is working).
func (l *Limiter) MaxGainReduction() float64 {
	return l.maxGainReduction
}

// GainReduction returns the peak-limiter gain reduction applied to the most recently output
// sample, in dB. 0 means no limiting. It is intended for metering or plotting limiter activity
// without altering the signal path.
func (l *Limiter) GainReduction() float64 {
	return l.lastGainReduction
}

// Stream processes the wrapped Streamer through the leveler and the look-ahead peak limiter.
func (l *Limiter) Stream(samples [][2]float64) (n int, ok bool) {
	n, ok = l.Streamer.Stream(samples)
	for i := range samples[:n] {
		in := samples[i]

		leveled := l.applyLeveler(in)
		limited := l.applyPeakLimiter(leveled)

		samples[i][0] = softClip(limited[0], l.ceiling)
		samples[i][1] = softClip(limited[1], l.ceiling)
	}
	return n, ok
}

// Err propagates the wrapped Streamer's errors.
func (l *Limiter) Err() error {
	return l.Streamer.Err()
}

// applyLeveler aligns the slow RMS loudness toward the target. Both channels are measured
// together and the gain is smoothed with asymmetric attack/release coefficients.
func (l *Limiter) applyLeveler(in [2]float64) [2]float64 {
	power := 0.5 * (in[0]*in[0] + in[1]*in[1])
	l.levelRMS = l.rmsCoef*power + (1-l.rmsCoef)*l.levelRMS

	rms := math.Sqrt(l.levelRMS)
	desired := 1.0
	// Noise gate: below -70 dBFS there is effectively no program material, so freeze the
	// current gain instead of chasing silence. Without this, a loud transient after a quiet
	// passage would be hit with a huge, stale correction before the long window catches up.
	if rms > dbToLinear(-70) {
		desired = clamp(l.targetLevel/rms, 0, 8)
	}

	coef := l.levelRelCoef
	if desired < l.levelGain {
		// gain is being reduced: use the (faster) attack constant
		coef = l.levelAtkCoef
	}
	l.levelGain += coef * (desired - l.levelGain)

	return [2]float64{in[0] * l.levelGain, in[1] * l.levelGain}
}

// applyPeakLimiter is a feed-forward look-ahead limiter. The peak detector and gain computer run
// on the current input, while the audio passes through a look-ahead delay (LimiterAttack ms).
// The computed gain travels through a matching delay line, so a sample leaves the delay
// multiplied by exactly the gain derived from its own level, never from a later, unrelated
// sample. Instant attack makes the envelope track each incoming peak immediately; the release
// coefficient only governs how fast reduction is removed afterwards.
func (l *Limiter) applyPeakLimiter(in [2]float64) [2]float64 {
	pos := l.delayPos
	out := l.delay[pos]
	outGain := l.gainDelay[pos]
	l.delay[l.delayPos] = in

	peak := math.Max(math.Abs(in[0]), math.Abs(in[1]))

	// Asymmetric peak follower. While the input is rising it drives the envelope with a fast
	// attack coefficient (the look-ahead delay gives it time to converge); once the input
	// falls below the envelope, the envelope releases toward zero at its own fixed rate.
	// Crucially the release target is zero rather than the instantaneous peak, so the local
	// peaks of a decaying tone cannot re-pin it at the historical maximum. Any residual
	// overshoot the smoothed envelope misses is caught by the per-sample ceiling cap below.
	if peak > l.envelope {
		l.envelope += l.limAtkCoef * (peak - l.envelope)
	} else {
		l.envelope += l.limRelCoef * (0 - l.envelope)
	}

	gain := l.limiterGain(l.envelope)

	// Brick-wall guarantee derived from the CURRENT input, which is the sample this gain will
	// be applied to once it exits the delay line. This is the correct feed-forward cap.
	if peak*gain > l.ceiling {
		gain = l.ceiling / peak
	}
	l.gainDelay[pos] = gain
	l.delayPos = (pos + 1) % len(l.delay)

	if gr := -linearToDB(outGain); gr > l.maxGainReduction {
		l.maxGainReduction = gr
	}
	l.lastGainReduction = -linearToDB(outGain)

	return [2]float64{out[0] * outGain, out[1] * outGain}
}

// limiterGain implements the soft-knee gain computer. Below the knee region the gain is 1;
// inside the knee the reduction is a quadratic blend; above it the limiter acts with a
// ratio that pushes the output toward the ceiling instead of an infinite brick wall.
func (l *Limiter) limiterGain(level float64) float64 {
	if level < 1e-9 {
		return 1
	}
	levelDB := linearToDB(level)
	thresholdDB := l.Config.LimiterThreshold
	kneeStart := thresholdDB - l.knee/2
	kneeEnd := thresholdDB + l.knee/2

	var gainDB float64
	switch {
	case levelDB <= kneeStart:
		gainDB = 0
	case levelDB < kneeEnd:
		// quadratic soft knee: reduction grows smoothly from 0 to the hard-knee value
		intoKnee := levelDB - kneeStart
		gainDB = -(intoKnee * intoKnee) / (2 * l.knee)
	default:
		// 20:1 limiting above the knee, scaled so the ceiling is never exceeded
		over := levelDB - thresholdDB
		gainDB = -over * 19 / 20
	}

	gain := dbToLinear(gainDB)
	return gain
}

// softClip is the last safety net against true full-scale clipping. At or below the ceiling it
// is identity; above it a smooth saturation rounds the residual overshoot toward full scale
// instead of producing a flat hard-clipped plateau. The configured ceiling itself is enforced
// by the feed-forward gain cap in applyPeakLimiter; this stage only ensures that even an
// unexpected overshoot can never reach the +/-1 rails.
func softClip(x, ceiling float64) float64 {
	if math.IsNaN(x) {
		return 0
	}
	ax := math.Abs(x)
	if ax <= ceiling {
		return x
	}

	// Saturating curve on (ceiling, 1): continuous with slope 1 at the ceiling and asymptotic
	// to full scale. Overshoot is rounded rather than sliced off, and the output can never
	// reach the +/-1 rails.
	t := (ax - ceiling) / (1 - ceiling)
	saturated := ceiling + (1-ceiling)*t/(1+t)
	if x < 0 {
		return -saturated
	}
	return saturated
}

func timeConstant(ms, sampleRate float64) float64 {
	return 1 - math.Exp(-1/(ms/1000*sampleRate))
}

func dbToLinear(db float64) float64 {
	return math.Pow(10, db/20)
}

func linearToDB(x float64) float64 {
	if x <= 1e-12 {
		return -240
	}
	return 20 * math.Log10(x)
}

func clamp(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

func ones(n int) []float64 {
	s := make([]float64, n)
	for i := range s {
		s[i] = 1
	}
	return s
}
