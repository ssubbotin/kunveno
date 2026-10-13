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

// The grade follows the median of the last three samples: a problem shows once it holds for two
// of them (~6 s), and a single spike in one 3-second window does not move the indicator.
func TestTrackerFollowsTheMedianOfThreeSamples(t *testing.T) {
	good := Sample{RTT: 20 * time.Millisecond}
	poor := Sample{UpLoss: 0.11}
	var tr Tracker
	steps := []struct {
		s       Sample
		want    string
		changed bool
	}{
		{good, Good, true},  // first sample sets the grade
		{poor, Good, false}, // one bad sample of two: not yet
		{poor, Poor, true},  // two of three
		{good, Poor, false}, // still two of three bad
		{good, Good, true},  // two of three good again
	}
	for i, st := range steps {
		g, changed := tr.Update(st.s)
		if g != st.want || changed != st.changed {
			t.Fatalf("step %d: %q changed=%v, want %q changed=%v", i, g, changed, st.want, st.changed)
		}
	}
}

func TestTrackerIgnoresASingleSpike(t *testing.T) {
	good := Sample{RTT: 20 * time.Millisecond}
	var tr Tracker
	tr.Update(good)
	tr.Update(good)
	for i, s := range []Sample{{UpLoss: 0.18}, good, {DownLoss: 0.17}, good} {
		if g, changed := tr.Update(s); g != Good || changed {
			t.Fatalf("spike %d moved the grade: %q changed=%v", i, g, changed)
		}
	}
}
