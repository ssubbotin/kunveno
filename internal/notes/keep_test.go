package notes

import (
	"testing"
	"time"
)

func TestParseKeep(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"90m": 90 * time.Minute,
		"12h": 12 * time.Hour,
		"1d":  24 * time.Hour,
		"2d":  48 * time.Hour,
	} {
		got, err := ParseKeep(in)
		if err != nil || got != want {
			t.Errorf("ParseKeep(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"2d12h", "1w", "0d", "1.5h", "", "d", "-1d", "1D", " 1d", "01d", "100000d"} {
		if _, err := ParseKeep(in); err == nil {
			t.Errorf("ParseKeep(%q) accepted", in)
		}
	}
}
