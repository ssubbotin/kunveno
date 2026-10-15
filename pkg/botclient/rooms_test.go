package botclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// control is a fake control endpoint whose list the test sets.
type control struct {
	mu    sync.Mutex
	slugs []string
	fail  bool
}

func (c *control) set(slugs ...string) { c.mu.Lock(); c.slugs, c.fail = slugs, false; c.mu.Unlock() }

func (c *control) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(append([]string{}, c.slugs...))
}

// served records which rooms run and lets the test end them.
type served struct {
	mu     sync.Mutex
	starts map[string]int
	live   map[string]chan struct{} // closed by the test to end the room
}

func newServed() *served {
	return &served{starts: map[string]int{}, live: map[string]chan struct{}{}}
}

func (s *served) serve(ctx context.Context, slug string) {
	end := make(chan struct{})
	s.mu.Lock()
	s.starts[slug]++
	s.live[slug] = end
	s.mu.Unlock()
	select {
	case <-ctx.Done():
	case <-end:
	}
}

func (s *served) count(slug string) int { s.mu.Lock(); defer s.mu.Unlock(); return s.starts[slug] }

func (s *served) end(slug string) { s.mu.Lock(); close(s.live[slug]); s.mu.Unlock() }

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func start(t *testing.T, r *Rooms) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	return func() { cancel(); <-done }
}

func TestRoomsStartEachListedRoomOnce(t *testing.T) {
	c, s := &control{}, newServed()
	c.set("ops")
	srv := httptest.NewServer(c)
	defer srv.Close()
	r := &Rooms{Control: srv.URL, Poll: 10 * time.Millisecond, Log: quiet, Serve: s.serve}
	stop := start(t, r)
	defer stop()
	waitUntil(t, "ops to start", func() bool { return r.Running("ops") })
	time.Sleep(50 * time.Millisecond) // several polls
	if n := s.count("ops"); n != 1 || !r.Wanted("ops") {
		t.Fatalf("ops started %d times, wanted %v", n, r.Wanted("ops"))
	}
	c.set("dev")
	waitUntil(t, "dev to start", func() bool { return r.Running("dev") })
	if !r.Running("ops") || r.Wanted("ops") {
		t.Fatal("an unlisted room was ended without CancelUnlisted, or is still wanted")
	}
	s.end("ops") // the room ends on its own (the server's stop)
	waitUntil(t, "ops to end", func() bool { return !r.Running("ops") })
	c.set("ops", "dev")
	waitUntil(t, "ops to start again", func() bool { return s.count("ops") == 2 })
}

func TestRoomsCancelUnlisted(t *testing.T) {
	c, s := &control{}, newServed()
	c.set("ops")
	srv := httptest.NewServer(c)
	defer srv.Close()
	r := &Rooms{Control: srv.URL, Poll: 10 * time.Millisecond, Log: quiet, Serve: s.serve, CancelUnlisted: true}
	stop := start(t, r)
	defer stop()
	waitUntil(t, "ops to start", func() bool { return r.Running("ops") })
	c.set()
	waitUntil(t, "ops to be cancelled", func() bool { return !r.Running("ops") })
	c.set("ops")
	waitUntil(t, "ops to start again", func() bool { return s.count("ops") == 2 })
}

func TestRoomsKeepTheLastListOnAFailedPoll(t *testing.T) {
	c, s := &control{}, newServed()
	c.set("ops")
	srv := httptest.NewServer(c)
	defer srv.Close()
	r := &Rooms{Control: srv.URL, Poll: 10 * time.Millisecond, Log: quiet, Serve: s.serve, CancelUnlisted: true}
	stop := start(t, r)
	defer stop()
	waitUntil(t, "ops to start", func() bool { return r.Running("ops") })
	c.mu.Lock()
	c.fail = true
	c.mu.Unlock()
	time.Sleep(50 * time.Millisecond)
	if !r.Running("ops") || !r.Wanted("ops") {
		t.Fatal("a failed poll ended the room")
	}
}

func TestRoomsListedFilters(t *testing.T) {
	s := newServed()
	r := &Rooms{Log: quiet, Serve: s.serve, Listed: func(_ context.Context, want map[string]bool) { delete(want, "../x") }}
	ctx, cancel := context.WithCancel(context.Background())
	r.Update(ctx, map[string]bool{"../x": true, "ops": true})
	waitUntil(t, "ops to start", func() bool { return r.Running("ops") })
	if r.Running("../x") || r.Wanted("../x") {
		t.Fatal("a filtered slug started or is wanted")
	}
	cancel()
	r.wg.Wait()
}

func TestRoomsRunWaitsForEveryRoom(t *testing.T) {
	c := &control{}
	c.set("ops", "dev")
	srv := httptest.NewServer(c)
	defer srv.Close()
	var ended atomic.Int32
	r := &Rooms{Control: srv.URL, Poll: 10 * time.Millisecond, Log: quiet, Serve: func(ctx context.Context, _ string) {
		<-ctx.Done()
		time.Sleep(20 * time.Millisecond) // finishing work after the shutdown
		ended.Add(1)
	}}
	stop := start(t, r)
	waitUntil(t, "both rooms to start", func() bool { return r.Running("ops") && r.Running("dev") })
	stop()
	if n := ended.Load(); n != 2 {
		t.Fatalf("Run returned with %d of 2 rooms finished", n)
	}
	r.Update(context.Background(), map[string]bool{}) // safe once Run has returned
}

func TestStayRejoinsWhileWanted(t *testing.T) {
	var rejoins atomic.Int32
	r := &Rooms{Retry: time.Millisecond, Log: quiet, OnRejoin: func(string) { rejoins.Add(1) }}
	r.want = map[string]bool{"ops": true}
	sessions := 0
	r.Stay(context.Background(), "ops", quiet, func(context.Context) (bool, error) {
		sessions++
		if sessions == 3 {
			return true, nil // the server's stop
		}
		return false, errors.New("socket closed")
	})
	if sessions != 3 || rejoins.Load() != 2 {
		t.Fatalf("%d sessions, %d rejoins", sessions, rejoins.Load())
	}
}

func TestStayLeavesARoomNoLongerWanted(t *testing.T) {
	r := &Rooms{Retry: time.Millisecond, Log: quiet}
	r.want = map[string]bool{"ops": true}
	sessions := 0
	r.Stay(context.Background(), "ops", quiet, func(context.Context) (bool, error) {
		sessions++
		r.mu.Lock()
		r.want = map[string]bool{}
		r.mu.Unlock()
		return false, errors.New("socket closed")
	})
	if sessions != 1 {
		t.Fatalf("%d sessions", sessions)
	}
}

func TestStayEndsWithTheContext(t *testing.T) {
	r := &Rooms{Retry: time.Hour, Log: quiet}
	r.want = map[string]bool{"ops": true}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Stay(ctx, "ops", quiet, func(context.Context) (bool, error) { return false, errors.New("socket closed") })
		close(done)
	}()
	time.Sleep(10 * time.Millisecond) // in the retry wait
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stay did not end with its context")
	}
}
