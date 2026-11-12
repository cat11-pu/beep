package soundfx

import "time"

// run is the audio pump goroutine used when PoolConfig.StartPump is true. It
// pulls blocks at the audio cadence and pushes them to the Sink. The trigger path
// never waits on this goroutine.
func (p *Pool) run() {
	ticker := time.NewTicker(p.BlockDuration())
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			p.Step()
		case <-p.stopCh:
			return
		}
	}
}

// Close stops the internal pump goroutine. It is a no-op for manually driven
// pools (StartPump=false).
func (p *Pool) Close() {
	p.closeOnce.Do(func() { close(p.stopCh) })
}

// Step performs one pump tick: admit queued voices, mix one block, drop finished
// voices and write the block to the sink. It is normally called by the internal
// pump goroutine; headless tests and benchmarks call it directly to make timing
// deterministic.
func (p *Pool) Step() {
	p.drainPending()

	block := make([][2]float64, p.cfg.BlockSamples)
	p.mu.Lock()
	p.mixer.Stream(block)
	finished := make([]uint64, 0)
	for id, v := range p.voices {
		if v.gate.Stopped() || v.probe.drained() {
			finished = append(finished, id)
		}
	}
	for _, id := range finished {
		delete(p.voices, id)
		if id == p.oldestID {
			p.oldestID = p.nextOldestLocked()
		}
	}
	p.mu.Unlock()

	p.sink.WriteBlock(block)
}

// nextOldestLocked finds the lowest remaining voice id, which is the longest
// playing one (ids are assigned monotonically at trigger time).
func (p *Pool) nextOldestLocked() uint64 {
	var oldest uint64
	for id := range p.voices {
		if oldest == 0 || id < oldest {
			oldest = id
		}
	}
	return oldest
}
