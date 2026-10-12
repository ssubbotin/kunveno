package quality

import (
	"testing"
	"time"
)

func TestGrade(t *testing.T) {
	ms := time.Millisecond
	cases := []struct {
		name string
		s    Sample
		want string
	}{
		{"clean", Sample{RTT: 30 * ms, Jitter: 5 * ms}, Good},
		{"some loss up", Sample{UpLoss: 0.02, RTT: 30 * ms}, Fair},
		{"some loss down", Sample{DownLoss: 0.02, RTT: 30 * ms}, Fair},
		{"slow", Sample{RTT: 250 * ms}, Fair},
		{"tcp-like jitter stays good", Sample{Jitter: 27 * ms}, Good},
		{"jittery", Sample{Jitter: 45 * ms}, Fair},
		{"heavy loss", Sample{UpLoss: 0.08}, Poor},
		{"very slow", Sample{RTT: 500 * ms}, Poor},
		{"very jittery", Sample{Jitter: 90 * ms}, Poor},
	}
	for _, c := range cases {
		if got := Grade(c.s); got != c.want {
			t.Errorf("%s: Grade = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestTrackerDropsAtOnceAndRecoversSlowly(t *testing.T) {
	var tr Tracker
	good := Sample{RTT: 20 * time.Millisecond}
	poor := Sample{UpLoss: 0.1}
	if g, changed := tr.Update(good); g != Good || !changed {
		t.Fatalf("first sample: %q changed=%v", g, changed)
	}
	if g, changed := tr.Update(poor); g != Poor || !changed {
		t.Fatalf("drop: %q changed=%v", g, changed)
	}
	if g, changed := tr.Update(good); g != Poor || changed {
		t.Fatalf("one good sample must not recover yet: %q changed=%v", g, changed)
	}
	if g, changed := tr.Update(good); g != Good || !changed {
		t.Fatalf("second good sample recovers: %q changed=%v", g, changed)
	}
	if _, changed := tr.Update(good); changed {
		t.Fatal("steady state reported as a change")
	}
}
