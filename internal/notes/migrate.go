package notes

import (
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// migrate moves the files of the layout that kept only each room's last meeting into the layout of
// one directory per room and one file set per meeting:
//
//	<slug>.jsonl                  -> <slug>/<meeting>.jsonl          (meeting: the header's start)
//	<slug>.<end>.summary.md       -> <slug>/<meeting>.<end>.summary.md (meeting: its first line)
//	<slug>.<end>.summary.deleted  -> <slug>/<meeting>.<end>.summary.deleted, next to its notes only
//
// A file that cannot be read or moved is logged and left where it was; leftover temporary files go.
// Running it again finds nothing to move.
func migrate(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type moved struct{ meeting, end int64 }
	notes := map[string]moved{}
	for _, e := range entries {
		name := e.Name()
		slug, ok := strings.CutSuffix(name, ".jsonl")
		if e.IsDir() || !ok || !slugRe.MatchString(slug) {
			continue
		}
		path := filepath.Join(dir, name)
		lines, err := readLines(path)
		if err != nil || len(lines) == 0 || lines[0].Type != KindHeader {
			slog.Warn("notes file of the old layout not moved", "file", path, "err", err)
			continue
		}
		meeting := lines[0].At
		target := filepath.Join(dir, slug, strconv.FormatInt(meeting, 10)+".jsonl")
		if moveInto(path, target) {
			notes[slug] = moved{meeting: meeting, end: lineTime(lines[len(lines)-1])}
		}
	}
	for _, e := range entries {
		name, path := e.Name(), filepath.Join(dir, e.Name())
		switch {
		case e.IsDir():
		case strings.HasSuffix(name, ".tmp"):
			_ = os.Remove(path)
		case strings.HasSuffix(name, summarySuffix):
			slug, end, ok := splitTop(name, summarySuffix)
			meta, err := readMeta(path)
			if !ok || err != nil {
				slog.Warn("summary file of the old layout not moved", "file", path, "err", err)
				continue
			}
			moveInto(path, filepath.Join(dir, slug, strconv.FormatInt(meta.Start, 10)+"."+strconv.FormatInt(end, 10)+summarySuffix))
		case strings.HasSuffix(name, deletedSuffix):
			slug, end, ok := splitTop(name, deletedSuffix)
			if n, has := notes[slug]; ok && has && n.end == end {
				moveInto(path, filepath.Join(dir, slug, strconv.FormatInt(n.meeting, 10)+"."+strconv.FormatInt(end, 10)+deletedSuffix))
			} else {
				_ = os.Remove(path)
			}
		}
	}
}

// splitTop reads <slug>.<end><suffix>.
func splitTop(name, suffix string) (string, int64, bool) {
	slug, endText, _ := strings.Cut(strings.TrimSuffix(name, suffix), ".")
	end, err := strconv.ParseInt(endText, 10, 64)
	return slug, end, err == nil && slugRe.MatchString(slug)
}

// moveInto renames path to target, creating target's directory; a target that already exists is
// left alone (a migration cut off earlier) and so is path.
func moveInto(path, target string) bool {
	if _, err := os.Stat(target); err == nil {
		slog.Warn("old-layout file left in place: its new place is taken", "file", path, "target", target)
		return false
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		slog.Warn("cannot move an old-layout file", "file", path, "err", err)
		return false
	}
	if err := os.Rename(path, target); err != nil {
		slog.Warn("cannot move an old-layout file", "file", path, "err", err)
		return false
	}
	slog.Info("moved to the meeting history layout", "file", path, "to", target)
	return true
}
