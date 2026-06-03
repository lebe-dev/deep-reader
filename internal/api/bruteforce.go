package api

import (
	"sync"
	"time"
)

// loginGuard is an in-memory, per-key brute-force throttle for the login
// endpoint. It counts *consecutive failed* login attempts per key (the client
// IP) within a rolling window and, once the count reaches maxAttempts, locks the
// key out for lockout. A successful login clears the key.
//
// Design notes:
//   - State lives in process memory (a map under a mutex), mirroring the
//     existing ingest rate limiter. It resets on restart, which is acceptable
//     for a single-user, self-hosted service: an attacker cannot restart the
//     server, and a restart only ever *relaxes* a lockout for a legitimate user.
//   - Only failures are counted, so a legitimate user signing in repeatedly
//     (e.g. across devices) never trips the lock; the success path resets it.
//   - maxAttempts <= 0 disables the guard entirely (every check passes). This is
//     both a kill-switch (LOGIN_MAX_ATTEMPTS=0) and the safe behaviour for the
//     zero-value config used in some tests.
//   - now is injectable so the time-dependent logic is testable without sleeps.
type loginGuard struct {
	maxAttempts int
	window      time.Duration
	lockout     time.Duration
	now         func() time.Time

	mu        sync.Mutex
	entries   map[string]*attemptEntry
	lastSweep time.Time
}

// attemptEntry tracks the failure streak for a single key.
type attemptEntry struct {
	// failures is the number of failed attempts in the current window.
	failures int
	// windowStart is when the current counting window began (the first failure
	// of the streak). Failures older than the window are forgotten.
	windowStart time.Time
	// lockedUntil is the instant the key becomes usable again; zero when not
	// locked.
	lockedUntil time.Time
}

// newLoginGuard builds a guard from the resolved configuration. A non-positive
// maxAttempts yields a disabled guard.
func newLoginGuard(maxAttempts int, window, lockout time.Duration) *loginGuard {
	return &loginGuard{
		maxAttempts: maxAttempts,
		window:      window,
		lockout:     lockout,
		now:         time.Now,
		entries:     make(map[string]*attemptEntry),
	}
}

// enabled reports whether the guard enforces anything.
func (g *loginGuard) enabled() bool { return g != nil && g.maxAttempts > 0 }

// retryAfter reports whether key is currently locked out and, if so, how long
// until it may try again (rounded up to the next whole second so the
// Retry-After header is never 0 for a still-active lock).
func (g *loginGuard) retryAfter(key string) (locked bool, wait time.Duration) {
	if !g.enabled() {
		return false, 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	e := g.entries[key]
	if e == nil || e.lockedUntil.IsZero() {
		return false, 0
	}
	now := g.now()
	if now.Before(e.lockedUntil) {
		return true, ceilSeconds(e.lockedUntil.Sub(now))
	}
	return false, 0
}

// recordFailure registers one failed login for key and returns whether that
// failure tripped (or kept) the lockout. The window resets when the previous
// streak has aged out or a prior lockout has already expired, so each lockout
// period grants a fresh allowance of attempts.
func (g *loginGuard) recordFailure(key string) (lockedNow bool) {
	if !g.enabled() {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	now := g.now()
	g.sweepLocked(now)

	e := g.entries[key]
	if e == nil || now.Sub(e.windowStart) > g.window || (!e.lockedUntil.IsZero() && !now.Before(e.lockedUntil)) {
		e = &attemptEntry{windowStart: now}
		g.entries[key] = e
	}
	e.failures++
	if e.failures >= g.maxAttempts {
		e.lockedUntil = now.Add(g.lockout)
		return true
	}
	return false
}

// recordSuccess clears any failure state for key after a successful login.
func (g *loginGuard) recordSuccess(key string) {
	if !g.enabled() {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.entries, key)
}

// sweepLocked drops entries that are neither locked nor within their counting
// window, bounding memory under a spray of distinct source IPs. It runs at most
// once per window and assumes g.mu is held.
func (g *loginGuard) sweepLocked(now time.Time) {
	if now.Sub(g.lastSweep) < g.window {
		return
	}
	g.lastSweep = now
	for key, e := range g.entries {
		expiredLock := e.lockedUntil.IsZero() || !now.Before(e.lockedUntil)
		agedOut := now.Sub(e.windowStart) > g.window
		if expiredLock && agedOut {
			delete(g.entries, key)
		}
	}
}

// ceilSeconds rounds a positive duration up to the next whole second.
func ceilSeconds(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return ((d + time.Second - 1) / time.Second) * time.Second
}
