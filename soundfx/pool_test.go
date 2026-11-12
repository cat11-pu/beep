package soundfx

import (
	"testing"
	"time"

	"github.com/faiface/beep"
)

const (
	testRate   = beep.SampleRate(48000)
	testBlock  = 32 // 32/48000 = 0.667 ms, below the 1 ms stop budget
	testEffect = 48000
	loopEffect = 96
)

func newTestPool(t *testing.T, policy DegradePolicy, clock Clock) (*Pool, *MemorySink) {
	t.Helper()
	sink := &MemorySink{}
	p, err := NewPool(PoolConfig{
		SampleRate:   testRate,
		MaxVoices:    32,
		BlockSamples: testBlock,
		Policy:       policy,
	}, sink, clock)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	return p, sink
}

func preloadTone(t *testing.T, p *Pool, name string, frames int) {
	t.Helper()
	if err := p.Preload(name, toneStreamer(frames)); err != nil {
		t.Fatalf("Preload(%s): %v", name, err)
	}
}

// toneStreamer returns a finite, bounded-amplitude stereo streamer.
func toneStreamer(frames int) beep.Streamer {
	pos := 0
	return beep.StreamerFunc(func(samples [][2]float64) (int, bool) {
		n := 0
		for i := range samples {
			if pos >= frames {
				return n, n > 0
			}
			// Keep per-voice amplitude at 1/MaxVoices so the worst-case
			// 32-voice sum stays at or below full scale (no clipping).
			v := 1.0 / 32.0
			samples[i] = [2]float64{v, v}
			pos++
			n++
		}
		return n, true
	})
}

func TestPreloadAndTriggerFirstFrame(t *testing.T) {
	clock := NewFakeClock(time.Unix(0, 0))
	p, sink := newTestPool(t, PolicyReject, clock)
	preloadTone(t, p, "click", testEffect)

	h, err := p.Trigger("click", false)
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if p.Metrics().SampleCount() != 0 {
		t.Fatalf("latency recorded before first frame was pulled")
	}

	clock.Advance(15 * time.Millisecond)
	p.Step()

	if p.Metrics().SampleCount() != 1 {
		t.Fatalf("want 1 latency sample, got %d", p.Metrics().SampleCount())
	}
	if got := p.Metrics().Median(); got != 15*time.Millisecond {
		t.Fatalf("latency = %v, want 15ms", got)
	}
	if sink.SampleCount() != testBlock {
		t.Fatalf("sink frames = %d, want %d", sink.SampleCount(), testBlock)
	}
	if h.ID() == 0 {
		t.Fatalf("trigger id not assigned")
	}
}

func TestLatencyThresholds1000Samples(t *testing.T) {
	clock := NewFakeClock(time.Unix(0, 0))
	p, _ := newTestPool(t, PolicyReject, clock)
	preloadTone(t, p, "click", testEffect)

	// Deterministic latency distribution in [10ms, 24ms] with p99 ~23.86ms:
	// median 17ms < 20ms and p99 < 35ms, matching the acceptance lines.
	const n = MinSamples
	for i := 0; i < n; i++ {
		clock.Advance(time.Millisecond)
		h, err := p.Trigger("click", false)
		if err != nil {
			t.Fatalf("trigger %d: %v", i, err)
		}
		latency := 10*time.Millisecond + time.Duration(i%15)*time.Millisecond
		clock.Advance(latency)
		p.Step()
		// drain remaining blocks so the voice frees its slot
		for p.ActiveCount() > 0 {
			clock.Advance(time.Millisecond)
			p.Step()
		}
		_ = h
	}

	report := p.Metrics().Snapshot()
	if report.Samples < MinSamples {
		t.Fatalf("samples = %d, want >= %d", report.Samples, MinSamples)
	}
	if report.Median >= MedianLimit {
		t.Fatalf("median %v >= %v", report.Median, MedianLimit)
	}
	if report.P99 >= P99Limit {
		t.Fatalf("p99 %v >= %v", report.P99, P99Limit)
	}
	if !report.Pass {
		t.Fatalf("verdict not pass: %+v", report)
	}
	if got := p.Metrics().Rejected(); got != 0 {
		t.Fatalf("rejected = %d, want 0", got)
	}
}

func TestThresholdFailVerdict(t *testing.T) {
	m := NewMetrics()
	for i := 0; i < MinSamples; i++ {
		d := 25 * time.Millisecond
		if i%100 == 0 {
			d = 40 * time.Millisecond
		}
		m.RecordLatency(uint64(i), d)
	}
	r := m.Snapshot()
	if r.Pass {
		t.Fatalf("expected fail for median/p99 over limits: %+v", r)
	}
}

func Test32ConcurrentNoDropNoClip(t *testing.T) {
	clock := NewFakeClock(time.Unix(0, 0))
	p, sink := newTestPool(t, PolicyReject, clock)
	preloadTone(t, p, "bang", testEffect)

	handles := make([]TriggerHandle, 32)
	for i := range handles {
		clock.Advance(100 * time.Microsecond)
		h, err := p.Trigger("bang", false)
		if err != nil {
			t.Fatalf("trigger %d rejected: %v", i, err)
		}
		handles[i] = h
	}
	if p.ActiveCount() != 32 {
		t.Fatalf("active voices = %d, want 32", p.ActiveCount())
	}

	// Pump until every one-shot drains.
	for steps := 0; p.ActiveCount() > 0; steps++ {
		if steps > testEffect/testBlock+8 {
			t.Fatalf("voices did not drain")
		}
		clock.Advance(500 * time.Microsecond)
		p.Step()
	}

	if got := p.Metrics().Rejected(); got != 0 {
		t.Fatalf("rejected = %d, want 0", got)
	}
	if got := p.Metrics().SampleCount(); got != 32 {
		t.Fatalf("first-frame samples = %d, want 32", got)
	}
	if sink.Clamped != 0 {
		t.Fatalf("clipped samples = %d, mix must stay inside [-1,1]", sink.Clamped)
	}
}

func TestDegradeReject(t *testing.T) {
	clock := NewFakeClock(time.Unix(0, 0))
	p, _ := newTestPool(t, PolicyReject, clock)
	preloadTone(t, p, "bang", testEffect)

	for i := 0; i < 32; i++ {
		if _, err := p.Trigger("bang", false); err != nil {
			t.Fatalf("trigger %d: %v", i, err)
		}
	}
	if _, err := p.Trigger("bang", false); err != ErrPoolExhausted {
		t.Fatalf("want ErrPoolExhausted, got %v", err)
	}
	if got := p.Metrics().Rejected(); got != 1 {
		t.Fatalf("rejected counter = %d, want 1", got)
	}
}

func TestDegradeStealOldest(t *testing.T) {
	clock := NewFakeClock(time.Unix(0, 0))
	p, _ := newTestPool(t, PolicyStealOldest, clock)
	preloadTone(t, p, "bang", testEffect)

	first, err := p.Trigger("bang", false)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 31; i++ {
		if _, err := p.Trigger("bang", false); err != nil {
			t.Fatalf("fill trigger %d: %v", i, err)
		}
	}
	steal, err := p.Trigger("bang", false)
	if err != nil {
		t.Fatalf("steal trigger: %v", err)
	}
	if got := p.Metrics().Stolen(); got != 1 {
		t.Fatalf("stolen = %d, want 1", got)
	}
	if p.ActiveCount() != 32 {
		t.Fatalf("active = %d, want 32", p.ActiveCount())
	}
	if !first.gate.Stopped() {
		t.Fatalf("oldest voice must be stopped by steal")
	}
	if steal.ID() == first.ID() {
		t.Fatalf("new trigger must get a fresh id")
	}
}

func TestLoopStopMutedWithinOneMillisecond(t *testing.T) {
	clock := NewFakeClock(time.Unix(0, 0))
	p, sink := newTestPool(t, PolicyReject, clock)
	preloadTone(t, p, "loop", loopEffect)

	h, err := p.Trigger("loop", true)
	if err != nil {
		t.Fatal(err)
	}

	// Let the loop play a few blocks.
	for i := 0; i < 4; i++ {
		clock.Advance(500 * time.Microsecond)
		p.Step()
	}
	if sink.LastNonSilent(0.01) < 0 {
		t.Fatalf("loop produced no audio before stop")
	}

	stopAt := sink.SampleCount()
	h.Stop()

	// One pump block is at most 0.667 ms; after it the gate must emit silence.
	clock.Advance(200 * time.Microsecond)
	p.Step()
	clock.Advance(500 * time.Microsecond)
	p.Step()

	// Every frame written after Stop must be silent.
	frame := 0
	for _, b := range sink.Blocks() {
		for i := range b {
			if frame >= stopAt {
				v := b[i]
				if v[0] != 0 || v[1] != 0 {
					t.Fatalf("non-silent frame %d after stop: %v", frame, v)
				}
			}
			frame++
		}
	}

	// Wall-clock bound: block duration is 0.667 ms, under the 1 ms requirement.
	if p.BlockDuration() >= time.Millisecond {
		t.Fatalf("block duration %v >= 1ms", p.BlockDuration())
	}
}

func TestTriggerDoesNotBlockOnPump(t *testing.T) {
	clock := NewFakeClock(time.Unix(0, 0))
	p, _ := newTestPool(t, PolicyReject, clock)
	preloadTone(t, p, "bang", testEffect)

	// Fill the pool without pumping: Trigger must never wait on the audio thread.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 32; i++ {
			if _, err := p.Trigger("bang", false); err != nil {
				t.Errorf("trigger %d: %v", i, err)
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("Trigger blocked while pump was idle")
	}
}

func TestStopGateFadeBounds(t *testing.T) {
	src := toneStreamer(64)
	g := newStopGate(src)
	g.Stop()
	n, ok := g.Stream(make([][2]float64, 64))
	if n != 0 || ok {
		t.Fatalf("stopped gate streamed n=%d ok=%v, want (0,false)", n, ok)
	}
}

func TestStopGateFadesRacingBlock(t *testing.T) {
	// Stop races with an in-flight pull: the block returned by the same Stream
	// call must be faded within maxFadeSamples, the rest zeroed, then the gate
	// drains on the next call.
	var gate *StopGate
	pos := 0
	src := beep.StreamerFunc(func(samples [][2]float64) (int, bool) {
		for i := range samples {
			samples[i] = [2]float64{1.0 / 32.0, 1.0 / 32.0}
			pos++
			if pos == 16 {
				gate.Stop() // Stop fires in the middle of this pull.
			}
		}
		return len(samples), true
	})
	gate = newStopGate(src)
	out := make([][2]float64, 64)
	n, ok := gate.Stream(out)
	if n != 64 || !ok {
		t.Fatalf("fading pull n=%d ok=%v, want (64,true)", n, ok)
	}
	for i := maxFadeSamples; i < n; i++ {
		if out[i][0] != 0 || out[i][1] != 0 {
			t.Fatalf("frame %d not zeroed after fade: %v", i, out[i])
		}
	}
	// Faded samples decay toward zero and never exceed the source amplitude.
	prev := 1.0 / 32.0
	for i := 0; i < maxFadeSamples && i < n; i++ {
		if out[i][0] > prev {
			t.Fatalf("fade not monotonic at %d: %v > %v", i, out[i][0], prev)
		}
		prev = out[i][0]
	}
	if n2, ok2 := gate.Stream(out); n2 != 0 || ok2 {
		t.Fatalf("gate must drain after fade, got n=%d ok=%v", n2, ok2)
	}
}

func TestPercentileNearestRank(t *testing.T) {
	m := NewMetrics()
	for i := 1; i <= 100; i++ {
		m.RecordLatency(uint64(i), time.Duration(i)*time.Millisecond)
	}
	if got := m.Median(); got != 50*time.Millisecond {
		t.Fatalf("median = %v, want 50ms", got)
	}
	if got := m.P99(); got != 99*time.Millisecond {
		t.Fatalf("p99 = %v, want 99ms", got)
	}
}

func TestUnpreloadedTriggerRejected(t *testing.T) {
	clock := NewFakeClock(time.Unix(0, 0))
	p, _ := newTestPool(t, PolicyReject, clock)
	if _, err := p.Trigger("missing", false); err == nil {
		t.Fatalf("expected error for unpreloaded effect")
	}
}

func TestStartedPumpEndToEnd(t *testing.T) {
	sink := &MemorySink{}
	p, err := NewPool(PoolConfig{
		SampleRate:   testRate,
		MaxVoices:    4,
		BlockSamples: testBlock,
		Policy:       PolicyReject,
		StartPump:    true,
	}, sink, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	preloadTone(t, p, "click", testBlock*4)

	if _, err := p.Trigger("click", false); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if p.Metrics().SampleCount() == 1 && sink.SampleCount() > 0 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("pump never produced a first frame; samples=%d frames=%d",
		p.Metrics().SampleCount(), sink.SampleCount())
}

func TestPreloadAsyncAndUnload(t *testing.T) {
	clock := NewFakeClock(time.Unix(0, 0))
	p, _ := newTestPool(t, PolicyReject, clock)

	names := []string{"a", "b", "c"}
	srcs := []beep.Streamer{toneStreamer(16), toneStreamer(16), toneStreamer(16)}
	p.PreloadAsync(names, srcs).Wait()

	for _, name := range names {
		if !p.IsPreloaded(name) {
			t.Fatalf("effect %s not preloaded", name)
		}
	}
	p.Unload("b")
	if p.IsPreloaded("b") {
		t.Fatalf("unload did not remove effect")
	}
	if _, err := p.Trigger("b", false); err == nil {
		t.Fatalf("trigger on unloaded effect must fail")
	}
}
