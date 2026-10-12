package player

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"
)

type fake struct {
	name string
	n, i int
	dur  time.Duration
}

func (f *fake) Next() (Sample, error) {
	if f.i == f.n {
		return Sample{}, io.EOF
	}
	f.i++
	return Sample{Data: []byte{byte(f.i)}, Duration: f.dur}, nil
}
func (f *fake) Close() error { return nil }

func TestLoopRepeatsBothStreamsTogether(t *testing.T) {
	var mu sync.Mutex
	var log []string
	rec := func(tag string) func(Sample) error {
		return func(s Sample) error {
			mu.Lock()
			log = append(log, tag+string('0'+s.Data[0]))
			mu.Unlock()
			return nil
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	rounds := 0
	err := Loop(ctx,
		func() (Source, error) { return &fake{name: "v", n: 3, dur: 2 * time.Millisecond}, nil },
		func() (Source, error) { return &fake{name: "a", n: 1, dur: 6 * time.Millisecond}, nil },
		rec("v"), rec("a"),
		func(r int) {
			rounds = r
			if r == 3 {
				cancel()
			}
		})
	if err != nil && err != context.Canceled {
		t.Fatal(err)
	}
	if rounds != 3 {
		t.Fatalf("rounds = %d", rounds)
	}
	mu.Lock()
	defer mu.Unlock()
	// Each round writes v1 v2 v3 and a1; a round starts only after both streams of the previous one ended.
	var v, a int
	for _, e := range log {
		switch e[0] {
		case 'v':
			v++
		case 'a':
			a++
		}
	}
	if v != 9 || a != 3 {
		t.Fatalf("video %d audio %d samples over 3 rounds: %v", v, a, log)
	}
}

func TestLoopPacesInRealTime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	start := time.Now()
	_ = Loop(ctx,
		func() (Source, error) { return &fake{n: 5, dur: 10 * time.Millisecond}, nil },
		nil,
		func(Sample) error { return nil }, nil,
		func(int) { cancel() })
	if d := time.Since(start); d < 45*time.Millisecond {
		t.Fatalf("5 samples of 10 ms played in %v: not paced", d)
	}
}
