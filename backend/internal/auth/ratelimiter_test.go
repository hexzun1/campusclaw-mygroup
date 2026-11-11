package auth

import (
	"testing"
	"time"
)

func TestLoginLimiterThresholdAndLockBoundary(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	l := newLoginLimiter(5, 300, clock)
	const key = "alice@198.51.100.10"

	for i := 1; i <= 4; i++ {
		l.RecordFailure(key)
		if l.Locked(key) {
			t.Fatalf("locked after %d failures, want threshold 5", i)
		}
	}
	l.RecordFailure(key)
	if !l.Locked(key) {
		t.Fatal("not locked after 5 failures")
	}

	now = now.Add(299 * time.Second)
	if !l.Locked(key) {
		t.Fatal("not locked at the 299th second")
	}

	now = now.Add(time.Second) // exactly 300 seconds after the lock began
	if l.Locked(key) {
		t.Fatal("still locked at the 300th second, want the lock to expire")
	}

	// The expired counter starts from zero rather than continuing the old one.
	l.RecordFailure(key)
	if l.Locked(key) {
		t.Fatal("one failure after expiry re-locked the key, want a fresh count")
	}
}

func TestLoginLimiterResetOnlyClearsOneKey(t *testing.T) {
	l := NewLoginLimiter(5, 300)
	for i := 0; i < 4; i++ {
		l.RecordFailure("alice@C1")
		l.RecordFailure("alice@C2")
	}

	l.Reset("alice@C1")
	l.RecordFailure("alice@C1")
	l.RecordFailure("alice@C2")

	if l.Locked("alice@C1") {
		t.Fatal("C1 locked, want its successful login to clear the count")
	}
	if !l.Locked("alice@C2") {
		t.Fatal("C2 not locked, want 4+1 failures to reach the threshold")
	}
}
