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
}

type limiterState struct {
	failures    int
	lockedUntil time.Time
}

func NewLoginLimiter(maxFailures, lockSeconds int) *LoginLimiter {
	return &LoginLimiter{
		state:       make(map[string]*limiterState),
		maxFailures: maxFailures,
		lockFor:     time.Duration(lockSeconds) * time.Second,
	}
}

// Locked reports whether key is currently locked out.
func (l *LoginLimiter) Locked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	st, ok := l.state[key]
	if !ok {
		return false
	}
	if time.Now().Before(st.lockedUntil) {
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

	st, ok := l.state[key]
	if !ok {
		st = &limiterState{}
		l.state[key] = st
	}
	st.failures++
	if st.failures >= l.maxFailures {
		st.lockedUntil = time.Now().Add(l.lockFor)
	}
}

// Reset clears failures for key after a successful login.
func (l *LoginLimiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.state, key)
}
