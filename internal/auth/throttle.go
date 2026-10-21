package auth

import (
	"sync"
	"time"
)

// Throttle blocks a key (e.g. "ip|username") after too many failures within a window.
type Throttle struct {
	mu       sync.Mutex
	entries  map[string]*entry
	max      int
	window   time.Duration
	lockout  time.Duration
	Now      func() time.Time
	lastSwep time.Time
}

type entry struct {
	fails       int
	first       time.Time
	lockedUntil time.Time
}

func NewThrottle(max int, window, lockout time.Duration) *Throttle {
	return &Throttle{entries: map[string]*entry{}, max: max, window: window, lockout: lockout, Now: time.Now}
}

// Blocked reports whether key is locked and for how long.
func (t *Throttle) Blocked(key string) (bool, time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.Now()
	t.sweep(now)
	if e, ok := t.entries[key]; ok && now.Before(e.lockedUntil) {
		return true, e.lockedUntil.Sub(now)
	}
	return false, 0
}

func (t *Throttle) Fail(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.Now()
	e := t.entries[key]
	if e == nil || now.Sub(e.first) > t.window {
		e = &entry{first: now}
		t.entries[key] = e
	}
	e.fails++
	if e.fails >= t.max {
		e.lockedUntil = now.Add(t.lockout)
	}
}

func (t *Throttle) Reset(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.entries, key)
}

// sweep drops stale entries so memory cannot grow without bound. Caller holds the lock.
func (t *Throttle) sweep(now time.Time) {
	if now.Sub(t.lastSwep) < time.Minute {
		return
	}
	t.lastSwep = now
	for k, e := range t.entries {
		if now.Sub(e.first) > t.window && now.After(e.lockedUntil) {
			delete(t.entries, k)
		}
	}
}
