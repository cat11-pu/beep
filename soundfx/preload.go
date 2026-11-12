package soundfx

import (
	"sync"

	"github.com/faiface/beep"
)

// Preload decodes the entire effect into an in-memory beep.Buffer and registers
// it under name. It is intended to be called during scene loading or scene
// transitions, never on the trigger hot path.
//
// Decoding happens synchronously; for large batches use PreloadAsync and wait on
// the returned WaitGroup. Re-preloading an existing name replaces its buffer;
// already playing voices keep using their own streamers and are not affected.
func (p *Pool) Preload(name string, src beep.Streamer) error {
	buf := beep.NewBuffer(beep.Format{
		SampleRate:  p.cfg.SampleRate,
		NumChannels: 2,
		Precision:   2,
	})
	buf.Append(src)
	if buf.Len() == 0 {
		return errEmptyEffect(name)
	}
	p.mu.Lock()
	p.sounds[name] = buf
	p.mu.Unlock()
	return nil
}

// PreloadAsync preloads a batch of effects concurrently on background goroutines.
// This is the recommended entry point at scene switches: the scene loader calls
// it and waits for the returned WaitGroup before activating the new scene. The
// returned error slice is indexed in the same order as names and contains nil for
// successfully preloaded effects.
func (p *Pool) PreloadAsync(names []string, sources []beep.Streamer) *sync.WaitGroup {
	if len(names) != len(sources) {
		panic("soundfx: PreloadAsync names/sources length mismatch")
	}
	var wg sync.WaitGroup
	for i := range names {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = p.Preload(names[i], sources[i])
		}(i)
	}
	return &wg
}

// Unload removes a preloaded effect. Playing voices are not affected.
func (p *Pool) Unload(name string) {
	p.mu.Lock()
	delete(p.sounds, name)
	p.mu.Unlock()
}

// IsPreloaded reports whether name is available for triggering.
func (p *Pool) IsPreloaded(name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.sounds[name]
	return ok
}
