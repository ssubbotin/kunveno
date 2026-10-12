// Package player plays a media file into a room in a loop: it transcodes the file once to VP8
// and Opus, then sends the frames in real time, restarting video and audio together.
package player

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

// Sample is one encoded frame and how long it lasts.
type Sample struct {
	Data     []byte
	Duration time.Duration
}

// Source yields samples in order and io.EOF at the end.
type Source interface {
	Next() (Sample, error)
	Close() error
}

// Opener starts a source from the beginning.
type Opener func() (Source, error)

// Loop plays video and audio (either may be nil) round after round until ctx ends. Each
// source is paced by the durations of its samples; a new round starts when both have finished,
// so they cannot drift apart over many repeats. onRound (may be nil) is told each finished round.
func Loop(ctx context.Context, video, audio Opener, writeVideo, writeAudio func(Sample) error, onRound func(int)) error {
	for round := 1; ; round++ {
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		play := func(open Opener, write func(Sample) error) {
			defer wg.Done()
			if err := playOnce(ctx, open, write); err != nil {
				errs <- err
			}
		}
		if video != nil {
			wg.Add(1)
			go play(video, writeVideo)
		}
		if audio != nil {
			wg.Add(1)
			go play(audio, writeAudio)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			return err
		}
		if onRound != nil {
			onRound(round)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

func playOnce(ctx context.Context, open Opener, write func(Sample) error) error {
	src, err := open()
	if err != nil {
		return err
	}
	defer src.Close()
	start, elapsed := time.Now(), time.Duration(0)
	for {
		s, err := src.Next()
		if errors.Is(err, io.EOF) {
			// Hold until the last sample's time is up, so the next round starts on schedule.
			return sleepUntil(ctx, start.Add(elapsed))
		}
		if err != nil {
			return err
		}
		if err := sleepUntil(ctx, start.Add(elapsed)); err != nil {
			return err
		}
		if err := write(s); err != nil {
			return err
		}
		elapsed += s.Duration
	}
}

func sleepUntil(ctx context.Context, t time.Time) error {
	d := time.Until(t)
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
