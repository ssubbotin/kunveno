// Package notes keeps meeting transcripts: the on-disk store of each room's last meeting, its
// Markdown rendering and expiry. The phrases come from the notes bot of the kunveno-notes add-on.
package notes

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// At most five digits: 99999d still fits a time.Duration.
var keepRe = regexp.MustCompile(`^([1-9][0-9]{0,4})([mhd])$`)

var keepUnits = map[string]time.Duration{"m": time.Minute, "h": time.Hour, "d": 24 * time.Hour}

// ParseKeep reads notes.keep: a positive whole number and exactly one unit, m, h or d ("90m",
// "12h", "1d", "2d"). Anything else ("2d12h", "1w", "0d", "1.5h", "") is an error.
func ParseKeep(s string) (time.Duration, error) {
	m := keepRe.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("%q is not a positive whole number followed by m, h or d (90m, 12h, 1d)", s)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, err
	}
	return time.Duration(n) * keepUnits[m[2]], nil
}
