package soundfx

import (
	"sync/atomic"
	"time"

	"github.com/faiface/beep"
)

// firstFrameProbe records the end-of-latency timestamp: the moment the audio pump
// first pulls a non-empty sample block from this voice, i.e. when the first frame
// enters the output path. It records exactly one latency sample per trigger.
type firstFrameProbe struct {
	gate      beep.Streamer
	id        uint64
	triggerAt time.Time
	clock     Clock
	metrics   *Metrics
	recorded  uint32
	done      uint32
}

func newFirstFrameProbe(gate *StopGate, id uint64, triggerAt time.Time, clock Clock, metrics *Metrics) *firstFrameProbe {
	return &firstFrameProbe{
		gate:      gate,
		id:        id,
		triggerAt: triggerAt,
		clock:     clock,
		metrics:   metrics,
	}
}

// Stream implements beep.Streamer.
func (p *firstFrameProbe) Stream(samples [][2]float64) (n int, ok bool) {
	n, ok = p.gate.Stream(samples)
	if n > 0 && atomic.CompareAndSwapUint32(&p.recorded, 0, 1) {
		firstFrameAt := p.clock.Now()
		p.metrics.RecordLatency(p.id, firstFrameAt.Sub(p.triggerAt))
	}
	if !ok {
		atomic.StoreUint32(&p.done, 1)
	}
	return n, ok
}

func (p *firstFrameProbe) drained() bool { return atomic.LoadUint32(&p.done) == 1 }

// Err implements beep.Streamer.
func (p *firstFrameProbe) Err() error {
	return p.gate.Err()
}
