package soundfx

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Thresholds from the measurement specification: median < 20 ms, p99 < 35 ms.
const (
	MedianLimit = 20 * time.Millisecond
	P99Limit    = 35 * time.Millisecond

	// MinSamples is the minimum number of latency samples required before the
	// pass/fail verdict is considered valid.
	MinSamples = 1000
)

// Metrics collects per-trigger latency samples and pool degradation counters.
type Metrics struct {
	mu       sync.Mutex
	latency  []time.Duration
	rejected uint64
	stolen   uint64
}

// NewMetrics creates an empty Metrics.
func NewMetrics() *Metrics {
	return &Metrics{latency: make([]time.Duration, 0, MinSamples)}
}

// RecordLatency stores one end-to-end trigger latency sample.
func (m *Metrics) RecordLatency(id uint64, d time.Duration) {
	m.mu.Lock()
	m.latency = append(m.latency, d)
	m.mu.Unlock()
}

// AddRejected counts a trigger refused by pool saturation.
func (m *Metrics) AddRejected() { atomic.AddUint64(&m.rejected, 1) }

// AddStolen counts a voice preempted to make room for a new trigger.
func (m *Metrics) AddStolen() { atomic.AddUint64(&m.stolen, 1) }

// Rejected returns the total number of rejected triggers.
func (m *Metrics) Rejected() uint64 { return atomic.LoadUint64(&m.rejected) }

// Stolen returns the total number of stolen voices.
func (m *Metrics) Stolen() uint64 { return atomic.LoadUint64(&m.stolen) }

// SampleCount returns the number of recorded latency samples.
func (m *Metrics) SampleCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.latency)
}

// Percentile returns the q-quantile (0..1) using the nearest-rank method, which
// is deterministic without interpolation and matches the way p50/p99 limits are
// specified in the requirement.
func (m *Metrics) Percentile(q float64) time.Duration {
	m.mu.Lock()
	sorted := append([]time.Duration(nil), m.latency...)
	m.mu.Unlock()
	if len(sorted) == 0 {
		return 0
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	rank := int(q*float64(len(sorted)) + 0.999999999) // ceil, nearest-rank
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

// Median returns the 50th percentile.
func (m *Metrics) Median() time.Duration { return m.Percentile(0.5) }

// P99 returns the 99th percentile.
func (m *Metrics) P99() time.Duration { return m.Percentile(0.99) }

// Report is a snapshot of the latency verdict.
type Report struct {
	Samples int
	Median  time.Duration
	P99     time.Duration
	Pass    bool
}

// Snapshot evaluates the pass criteria: at least MinSamples, median strictly below
// MedianLimit and p99 strictly below P99Limit.
func (m *Metrics) Snapshot() Report {
	r := Report{
		Samples: m.SampleCount(),
		Median:  m.Median(),
		P99:     m.P99(),
	}
	r.Pass = r.Samples >= MinSamples && r.Median < MedianLimit && r.P99 < P99Limit
	return r
}
