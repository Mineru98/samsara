package domain

import (
	"sync"
	"time"
)

// Clock abstracts time so lease, retry and freshness logic is deterministic in
// tests (05 §6.2). Production uses SystemClock.
type Clock interface {
	Now() time.Time
}

// SystemClock reads the wall clock.
type SystemClock struct{}

// Now implements Clock.
func (SystemClock) Now() time.Time { return time.Now().UTC() }

// FakeClock only moves when told to. It can also move backwards to simulate
// wall-clock regressions (POL-13).
type FakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewFakeClock starts a fake clock at t.
func NewFakeClock(t time.Time) *FakeClock { return &FakeClock{now: t.UTC()} }

// Now implements Clock.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock by d (which may be negative).
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// Set jumps to t.
func (c *FakeClock) Set(t time.Time) {
	c.mu.Lock()
	c.now = t.UTC()
	c.mu.Unlock()
}

// Millis converts a time to the unix-millisecond integer stored in SQLite.
func Millis(t time.Time) int64 { return t.UnixMilli() }

// FromMillis converts stored milliseconds back to UTC time.
func FromMillis(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

// RFC3339 formats a stored millisecond value for the wire; zero means absent.
func RFC3339(ms int64) string {
	if ms == 0 {
		return ""
	}
	return FromMillis(ms).Format(time.RFC3339Nano)
}
