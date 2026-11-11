package auth

import (
	"sync"
	"time"
)

// LoginLimiter tracks consecutive login failures per "username+IP" key, in
// memory (single-instance only, see design.md Risks). Once a key reaches
// maxFailures, further attempts are rejected for lockSeconds regardless of
// whether the credentials supplied are correct.
type LoginLimiter struct {
	mu          sync.Mutex
	state       map[string]*limiterState
	maxFailures int
	lockFor     time.Duration
	now         func() time.Time
}

type limiterState struct {
	failures    int
	lockedUntil time.Time
}

func NewLoginLimiter(maxFailures, lockSeconds int) *LoginLimiter {
	return newLoginLimiter(maxFailures, lockSeconds, time.Now)
}

// newLoginLimiter takes the clock explicitly so tests can cross the lock
// boundary without sleeping for the real duration.
func newLoginLimiter(maxFailures, lockSeconds int, now func() time.Time) *LoginLimiter {
	return &LoginLimiter{
		state:       make(map[string]*limiterState),
		maxFailures: maxFailures,
		lockFor:     time.Duration(lockSeconds) * time.Second,
		now:         now,
	}
}

// Locked reports whether key is currently locked out.
func (l *LoginLimiter) Locked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	st, ok := l.state[key]
	if !ok {
		return false
	}
	if now.Before(st.lockedUntil) {
		return true
	}
	// Lock window has passed: forget prior failures so the key starts fresh.
	if !st.lockedUntil.IsZero() {
		delete(l.state, key)
	}
	return false
}

// RecordFailure increments the failure count for key and locks it once the
// threshold is reached.
func (l *LoginLimiter) RecordFailure(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	st, ok := l.state[key]
	if !ok {
		st = &limiterState{}
		l.state[key] = st
	} else if !st.lockedUntil.IsZero() && !now.Before(st.lockedUntil) {
		// The previous lock has expired; a new attempt counts from zero even
		// if the caller did not ask Locked first.
		st.failures = 0
		st.lockedUntil = time.Time{}
	}
	st.failures++
	if st.failures >= l.maxFailures {
		st.lockedUntil = now.Add(l.lockFor)
	}
}

// Reset clears failures for key after a successful login.
func (l *LoginLimiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.state, key)
}
