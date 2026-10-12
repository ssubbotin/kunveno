package ratelimit

import (
	"net/http/httptest"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }
func newTest() (*Limiter, *clock) {
	c := &clock{t: time.Unix(1000, 0)}
	l := New(5, 10*time.Minute, time.Minute)
	l.now = c.now
	return l, c
}

func TestBlocksForAFixedMinute(t *testing.T) {
	l, c := newTest()
	for i := 0; i < 4; i++ {
		l.Fail("ip")
	}
	if ok, _ := l.Allow("ip"); !ok {
		t.Fatal("blocked before the fifth failure")
	}
	l.Fail("ip")
	ok, wait := l.Allow("ip")
	if ok || wait != time.Minute {
		t.Fatalf("after 5 failures: ok=%v wait=%v, want blocked for 1m", ok, wait)
	}
	if ok, _ := l.Allow("other"); !ok {
		t.Fatal("another address is blocked too")
	}
	c.add(time.Minute)
	if ok, _ := l.Allow("ip"); !ok {
		t.Fatal("still blocked after the block expired")
	}
	for i := 0; i < 5; i++ {
		l.Fail("ip")
	}
	if _, wait := l.Allow("ip"); wait != time.Minute {
		t.Fatalf("second block = %v, want the same 1m (no doubling)", wait)
	}
}

func TestSuccessAndQuietTimeForgive(t *testing.T) {
	l, c := newTest()
	for i := 0; i < 4; i++ {
		l.Fail("ip")
	}
	l.Success("ip")
	l.Fail("ip")
	if ok, _ := l.Allow("ip"); !ok {
		t.Fatal("success did not reset the count")
	}
	for i := 0; i < 4; i++ {
		l.Fail("ip")
	}
	c.add(11 * time.Minute) // the window passed
	l.Fail("ip")
	if ok, _ := l.Allow("ip"); !ok {
		t.Fatal("old failures still counted after the window")
	}
}

func TestForgetsIdleEntries(t *testing.T) {
	l, c := newTest()
	l.Fail("a")
	c.add(11 * time.Minute)
	l.Fail("b") // triggers a sweep
	if n := l.size(); n != 1 {
		t.Fatalf("entries = %d, want 1", n)
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/ws", nil)
	r.RemoteAddr = "127.0.0.1:5000"
	r.Header.Set("X-Forwarded-For", "10.10.10.7")
	if got := ClientIP(r); got != "10.10.10.7" {
		t.Fatalf("behind nginx: %q", got)
	}
	r.RemoteAddr = "10.10.10.9:5000" // not from nginx: the header could be forged
	if got := ClientIP(r); got != "10.10.10.9" {
		t.Fatalf("direct: %q", got)
	}
}
