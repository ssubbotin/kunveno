package botclient

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Rooms keeps a helper in every room the server lists on a control endpoint: GET returns a JSON
// array of room slugs (/internal/echo, /internal/notes, /internal/recording; loopback only). Run
// polls the endpoint and runs Serve in a goroutine of its own for every listed room that has none;
// Stay is the usual body of Serve: join, and rejoin after a drop while the room still wants the
// helper.
//
// Set the fields before Run and do not change them afterwards.
type Rooms struct {
	Control string        // the control endpoint
	Client  *http.Client  // for the polls; nil means one with a 3 s timeout
	Poll    time.Duration // between polls
	Retry   time.Duration // Stay: the wait before a rejoin
	Log     *slog.Logger

	// Serve keeps the helper in one room until it is done there; the room can start again at a later
	// poll after Serve returns. ctx ends when Run's context ends, and also when the room leaves the
	// list if CancelUnlisted is set.
	Serve func(ctx context.Context, slug string)
	// CancelUnlisted ends a room's context as soon as a poll no longer lists it (the echo). Without
	// it a room ends on its own, typically when the server tells the helper to stop, and Wanted says
	// whether the room still asks for it.
	CancelUnlisted bool
	// Listed, when set, sees every successful poll's answer before any room starts and may delete
	// slugs from it: a deleted slug is neither started nor Wanted.
	Listed func(ctx context.Context, want map[string]bool)
	// OnRejoin, when set, runs before each rejoin in Stay.
	OnRejoin func(slug string)

	mu      sync.Mutex
	want    map[string]bool
	running map[string]context.CancelFunc
	wg      sync.WaitGroup
}

// Run polls the control endpoint until ctx ends, then waits for every room's Serve to return.
func (r *Rooms) Run(ctx context.Context) {
	defer r.wg.Wait()
	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	tick := time.NewTicker(r.Poll)
	defer tick.Stop()
	for {
		if want, err := ListRooms(ctx, client, r.Control); err != nil {
			if ctx.Err() == nil {
				r.Log.Warn("control poll", "err", err)
			}
		} else {
			r.Update(ctx, want)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// Update applies one answer of the control endpoint: it starts the listed rooms that have no
// goroutine and, with CancelUnlisted, ends the rooms no longer listed. Run calls it after every
// successful poll; call it directly only while Run is not running, as tests do.
func (r *Rooms) Update(ctx context.Context, want map[string]bool) {
	if r.Listed != nil {
		r.Listed(ctx, want)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.want = want
	if r.running == nil {
		r.running = map[string]context.CancelFunc{}
	}
	if r.CancelUnlisted {
		for slug, cancel := range r.running {
			if !want[slug] {
				cancel()
			}
		}
	}
	if ctx.Err() != nil {
		return
	}
	for slug := range want {
		if r.running[slug] != nil {
			continue
		}
		rctx, cancel := context.WithCancel(ctx)
		r.running[slug] = cancel
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			defer cancel()
			r.Serve(rctx, slug)
			r.mu.Lock()
			delete(r.running, slug)
			r.mu.Unlock()
		}()
	}
}

// Wanted reports whether the last poll listed the room.
func (r *Rooms) Wanted(slug string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.want[slug]
}

// Running reports whether the room's Serve is running.
func (r *Rooms) Running(slug string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running[slug] != nil
}

// Stay runs session in the room until it reports the helper is done there (the server's stop) or
// ctx ends. After any other return it rejoins once Retry has passed, unless the room no longer
// wants the helper.
func (r *Rooms) Stay(ctx context.Context, slug string, log *slog.Logger, session func(ctx context.Context) (done bool, err error)) {
	for {
		done, err := session(ctx)
		if done || ctx.Err() != nil {
			return
		}
		if !r.Wanted(slug) {
			log.Info("the room no longer wants the helper", "err", err)
			return
		}
		log.Warn("disconnected; rejoining", "err", err, "in", r.Retry.String())
		select {
		case <-ctx.Done():
			return
		case <-time.After(r.Retry):
		}
		if r.OnRejoin != nil {
			r.OnRejoin(slug)
		}
	}
}

// ListRooms asks a control endpoint which rooms want the helper.
func ListRooms(ctx context.Context, client *http.Client, url string) (map[string]bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %s", res.Status)
	}
	var slugs []string
	if err := json.NewDecoder(res.Body).Decode(&slugs); err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(slugs))
	for _, s := range slugs {
		out[s] = true
	}
	return out, nil
}
