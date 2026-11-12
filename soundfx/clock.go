package soundfx

import "time"

// Clock abstracts time measurement so latency statistics can be recorded against
// the wall clock in production and against a controllable fake clock in
// deterministic tests.
type Clock interface {
	Now() time.Time
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

// FakeClock is a manually advanced clock for tests.
type FakeClock struct{ now time.Time }

// NewFakeClock creates a FakeClock seeded at t.
func NewFakeClock(t time.Time) *FakeClock { return &FakeClock{now: t} }

// Now returns the current fake time.
func (c *FakeClock) Now() time.Time {
	return c.now
}

// Advance moves the clock forward by d.
func (c *FakeClock) Advance(d time.Duration) {
	c.now = c.now.Add(d)
}
