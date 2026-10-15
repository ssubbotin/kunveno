package sfu

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/pion/logging"
)

// pionRepeatEvery is how often one peer may log the same pion message; the rest are counted and
// reported with the next one. A broken client can make pion log ten times a second.
const pionRepeatEvery = 10 * time.Second

// pionLogs routes the pion errors of one peer connection into the server log, tagged with the
// participant, so a message like "mux: failed to read from packetio.Buffer" names its source.
// Like pion's default logger, it keeps errors only.
type pionLogs struct {
	log *slog.Logger
	now func() time.Time

	mu   sync.Mutex
	seen map[string]*pionRepeat
}

type pionRepeat struct {
	last       time.Time
	suppressed int
}

func newPionLogs(log *slog.Logger) *pionLogs {
	return &pionLogs{log: log, now: time.Now, seen: map[string]*pionRepeat{}}
}

func (f *pionLogs) NewLogger(scope string) logging.LeveledLogger {
	return pionLogger{f: f, scope: scope}
}

func (f *pionLogs) error(scope, msg string) {
	now := f.now()
	key := scope + "\x00" + msg
	f.mu.Lock()
	r := f.seen[key]
	if r == nil {
		r = &pionRepeat{}
		f.seen[key] = r
	} else if now.Sub(r.last) < pionRepeatEvery {
		r.suppressed++
		f.mu.Unlock()
		return
	}
	suppressed := r.suppressed
	r.last, r.suppressed = now, 0
	f.mu.Unlock()
	if suppressed > 0 {
		f.log.Error(msg, "scope", scope, "repeated", suppressed)
	} else {
		f.log.Error(msg, "scope", scope)
	}
}

type pionLogger struct {
	f     *pionLogs
	scope string
}

func (pionLogger) Trace(string)          {}
func (pionLogger) Tracef(string, ...any) {}
func (pionLogger) Debug(string)          {}
func (pionLogger) Debugf(string, ...any) {}
func (pionLogger) Info(string)           {}
func (pionLogger) Infof(string, ...any)  {}
func (pionLogger) Warn(string)           {}
func (pionLogger) Warnf(string, ...any)  {}
func (l pionLogger) Error(msg string)    { l.f.error(l.scope, msg) }
func (l pionLogger) Errorf(format string, args ...any) {
	l.f.error(l.scope, fmt.Sprintf(format, args...))
}
