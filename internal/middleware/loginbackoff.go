package middleware

import (
	"sort"
	"sync"
	"time"
)

// Per-(IP, account) exponential backoff for failed logins.
//
// This REPLACES a global per-email lockout (10 failures / 15 min, keyed on the
// email alone) that could be abused as a targeted denial-of-service: an
// attacker who knew a victim's email could burn the whole per-account budget
// with bad passwords and lock the real owner out of their own account.
//
// Keying on (client IP + email) instead means an attacker on one IP can only
// throttle that IP's own attempts against the account — the legitimate owner,
// arriving from a different IP, is never affected. A correct password also
// clears the counter immediately, so a user who simply fat-fingered their
// password is never stuck. Distributed credential-stuffing remains bounded by
// the per-IP route limit (RateLimit) and bcrypt's cost.
//
// NOTE: this relies on RealClientIP being the true client address, which in
// turn requires a correct --trusted-proxies configuration. Behind an untrusted
// proxy every request shares one IP and the (IP, account) granularity collapses
// to per-account — the same limitation every other per-IP control here shares.

const (
	loginFailFree    = 5                // failures allowed before any backoff
	loginBackoffBase = 5 * time.Second  // first backoff window past the free allowance
	loginBackoffMax  = 15 * time.Minute // cap on a single backoff window
	loginFailTTL     = 30 * time.Minute // idle period after which a key resets
	maxBackoffKeys   = 100_000          // memory bound under IP-rotation
)

type backoffEntry struct {
	fails        int
	blockedUntil time.Time
	updated      time.Time
}

var (
	backoffMu  sync.Mutex
	backoffMap = map[string]*backoffEntry{}
)

// LoginBlocked reports whether failed-attempt backoff is currently in effect
// for key. retryAfter is the remaining wait (0 when not blocked).
func LoginBlocked(key string) (bool, time.Duration) {
	now := time.Now()
	backoffMu.Lock()
	defer backoffMu.Unlock()
	e := backoffMap[key]
	if e == nil {
		return false, 0
	}
	if now.Sub(e.updated) > loginFailTTL {
		delete(backoffMap, key)
		return false, 0
	}
	if now.Before(e.blockedUntil) {
		return true, e.blockedUntil.Sub(now)
	}
	return false, 0
}

// RecordLoginFailure counts a failed login for key and, once past the free
// allowance, opens an exponentially growing backoff window.
func RecordLoginFailure(key string) {
	now := time.Now()
	backoffMu.Lock()
	defer backoffMu.Unlock()

	if len(backoffMap) > maxBackoffKeys {
		evictOldestBackoff()
	}

	e := backoffMap[key]
	if e == nil || now.Sub(e.updated) > loginFailTTL {
		e = &backoffEntry{}
		backoffMap[key] = e
	}
	e.fails++
	e.updated = now

	if e.fails > loginFailFree {
		shift := uint(e.fails - loginFailFree - 1)
		d := loginBackoffMax
		if shift <= 7 { // 5s<<7 = 640s < 900s cap; beyond that, clamp (also avoids overflow)
			d = loginBackoffBase << shift
			if d > loginBackoffMax {
				d = loginBackoffMax
			}
		}
		e.blockedUntil = now.Add(d)
	}
}

// ClearLoginFailures resets state for key after a successful authentication.
func ClearLoginFailures(key string) {
	backoffMu.Lock()
	delete(backoffMap, key)
	backoffMu.Unlock()
}

// cleanupLoginBackoff drops entries whose last activity is older than the TTL.
// Called from StartRateLimitCleanup's ticker.
func cleanupLoginBackoff() {
	cutoff := time.Now().Add(-loginFailTTL)
	backoffMu.Lock()
	for k, e := range backoffMap {
		if e.updated.Before(cutoff) {
			delete(backoffMap, k)
		}
	}
	backoffMu.Unlock()
}

// evictOldestBackoff removes ~10% of the least-recently-updated keys when the
// map exceeds maxBackoffKeys. Caller must hold backoffMu.
func evictOldestBackoff() {
	type entry struct {
		key  string
		last time.Time
	}
	all := make([]entry, 0, len(backoffMap))
	for k, e := range backoffMap {
		all = append(all, entry{k, e.updated})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].last.Before(all[j].last) })
	toEvict := len(all) / 10
	if toEvict < 1 {
		toEvict = 1
	}
	for i := 0; i < toEvict; i++ {
		delete(backoffMap, all[i].key)
	}
}
