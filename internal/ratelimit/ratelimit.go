// Package ratelimit slows down PIN guessing: an address that fails too often within a window
// is blocked for a fixed time.
package ratelimit

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type entry struct {
	fails        int
	windowStart  time.Time
	blockedUntil time.Time
	last         time.Time
}

// Limiter is safe for concurrent use.
type Limiter struct {
	max           int
	window, block time.Duration
	now           func() time.Time

	mu        sync.Mutex
	m         map[string]*entry
	lastSweep time.Time
}

// New allows max failures per window, then blocks the key for block.
func New(max int, window, block time.Duration) *Limiter {
	return &Limiter{max: max, window: window, block: block, now: time.Now, m: map[string]*entry{}}
}

// Allow reports whether key may try now, and if not, how long until it may.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.m[key]
	if !ok {
		return true, 0
	}
	if wait := e.blockedUntil.Sub(l.now()); wait > 0 {
		return false, wait
	}
	return true, 0
}

// Fail records a wrong attempt by key and reports whether it started a block.
func (l *Limiter) Fail(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweep(now)
	e, ok := l.m[key]
	if !ok {
		e = &entry{}
		l.m[key] = e
	}
	if now.Sub(e.windowStart) > l.window {
		e.fails, e.windowStart = 0, now
	}
	e.last = now
	e.fails++
	if e.fails < l.max {
		return false
	}
	e.blockedUntil = now.Add(l.block)
	e.fails = 0
	return true
}

// Success forgets the failures of key.
func (l *Limiter) Success(key string) {
	l.mu.Lock()
	delete(l.m, key)
	l.mu.Unlock()
}

// sweep drops entries idle for longer than the window; called with mu held.
func (l *Limiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < time.Minute {
		return
	}
	l.lastSweep = now
	for k, e := range l.m {
		if now.Sub(e.last) > l.window && now.After(e.blockedUntil) {
			delete(l.m, k)
		}
	}
}

func (l *Limiter) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.m)
}

// ClientIP is the address to limit: the one nginx reports in X-Forwarded-For when the request
// came from nginx on loopback, the peer address otherwise (a direct client could forge the header).
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
		}
	}
	return host
}
