package api

import (
	"testing"
	"time"
)

// newTestGuard returns a guard with a controllable clock anchored at a fixed
// instant, plus a function to advance that clock.
func newTestGuard(t *testing.T, maxAttempts int, window, lockout time.Duration) (*loginGuard, func(time.Duration)) {
	t.Helper()
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	g := newLoginGuard(maxAttempts, window, lockout)
	g.now = func() time.Time { return clock }
	advance := func(d time.Duration) { clock = clock.Add(d) }
	return g, advance
}

func TestLoginGuard_LocksAfterMaxAttempts(t *testing.T) {
	g, _ := newTestGuard(t, 3, 15*time.Minute, 15*time.Minute)
	const ip = "203.0.113.7"

	for i := range 2 {
		if locked := g.recordFailure(ip); locked {
			t.Fatalf("attempt %d: locked too early", i+1)
		}
		if locked, _ := g.retryAfter(ip); locked {
			t.Fatalf("attempt %d: retryAfter reported locked too early", i+1)
		}
	}

	if locked := g.recordFailure(ip); !locked {
		t.Fatal("3rd failure should trip the lockout")
	}
	locked, wait := g.retryAfter(ip)
	if !locked {
		t.Fatal("expected key to be locked after reaching maxAttempts")
	}
	if wait <= 0 || wait > 15*time.Minute {
		t.Fatalf("unexpected retry-after %v", wait)
	}
}

func TestLoginGuard_UnlocksAfterLockout(t *testing.T) {
	g, advance := newTestGuard(t, 2, 15*time.Minute, 10*time.Minute)
	const ip = "203.0.113.8"

	g.recordFailure(ip)
	g.recordFailure(ip) // locked now
	if locked, _ := g.retryAfter(ip); !locked {
		t.Fatal("expected lock")
	}

	advance(10*time.Minute + time.Second)
	if locked, _ := g.retryAfter(ip); locked {
		t.Fatal("lock should have expired")
	}

	// A failure after the lock expired starts a fresh allowance rather than
	// re-locking on the first try.
	if locked := g.recordFailure(ip); locked {
		t.Fatal("first failure after expiry must not immediately re-lock")
	}
}

func TestLoginGuard_SuccessResets(t *testing.T) {
	g, _ := newTestGuard(t, 3, 15*time.Minute, 15*time.Minute)
	const ip = "203.0.113.9"

	g.recordFailure(ip)
	g.recordFailure(ip)
	g.recordSuccess(ip)

	// Counter is back to zero: it should take the full maxAttempts again.
	if locked := g.recordFailure(ip); locked {
		t.Fatal("locked after a single failure following success reset")
	}
	if locked := g.recordFailure(ip); locked {
		t.Fatal("locked after two failures; success should have reset the streak")
	}
	if locked := g.recordFailure(ip); !locked {
		t.Fatal("expected lock on the third post-reset failure")
	}
}

func TestLoginGuard_WindowExpiryResetsStreak(t *testing.T) {
	g, advance := newTestGuard(t, 3, 5*time.Minute, 15*time.Minute)
	const ip = "203.0.113.10"

	g.recordFailure(ip)
	g.recordFailure(ip)

	// Let the counting window lapse; the old failures are forgotten.
	advance(5*time.Minute + time.Second)

	g.recordFailure(ip) // counts as the 1st failure of a new window
	if locked, _ := g.retryAfter(ip); locked {
		t.Fatal("stale failures should not count toward the lock")
	}
}

func TestLoginGuard_PerKeyIsolation(t *testing.T) {
	g, _ := newTestGuard(t, 2, 15*time.Minute, 15*time.Minute)

	g.recordFailure("198.51.100.1")
	g.recordFailure("198.51.100.1") // locks only this IP

	if locked, _ := g.retryAfter("198.51.100.1"); !locked {
		t.Fatal("first IP should be locked")
	}
	if locked, _ := g.retryAfter("198.51.100.2"); locked {
		t.Fatal("second IP must not be affected by the first IP's failures")
	}
}

func TestLoginGuard_DisabledWhenMaxAttemptsNonPositive(t *testing.T) {
	g, _ := newTestGuard(t, 0, 15*time.Minute, 15*time.Minute)
	const ip = "203.0.113.11"

	for range 100 {
		if locked := g.recordFailure(ip); locked {
			t.Fatal("disabled guard must never lock")
		}
	}
	if locked, _ := g.retryAfter(ip); locked {
		t.Fatal("disabled guard must never report locked")
	}
}

func TestCeilSeconds(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want time.Duration
	}{
		{0, 0},
		{-time.Second, 0},
		{time.Millisecond, time.Second},
		{time.Second, time.Second},
		{time.Second + time.Millisecond, 2 * time.Second},
		{90 * time.Second, 90 * time.Second},
	}
	for _, tc := range cases {
		if got := ceilSeconds(tc.in); got != tc.want {
			t.Errorf("ceilSeconds(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
