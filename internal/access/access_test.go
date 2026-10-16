package access

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func me() string { return strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()) }

func TestWritableCreatesAndAcceptsAnOwnDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "notes")
	if err := Writable(dir); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(dir); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("created %v %v, want mode 0700 like the stores", st, err)
	}
}

// A directory the service cannot write (a bind mount Docker created as root, a deb install) is named
// with the fix.
func TestWritableNamesTheFix(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root writes anywhere")
	}
	dir := filepath.Join(t.TempDir(), "recordings")
	if err := os.Mkdir(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	err := Writable(dir)
	if err == nil {
		t.Fatal("a read-only directory passed")
	}
	for _, want := range []string{dir, "sudo chown -R " + me() + " " + dir} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

func TestReadableNamesTheFix(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads anywhere")
	}
	p := filepath.Join(t.TempDir(), "film.mp4")
	if err := os.WriteFile(p, []byte("x"), 0o000); err != nil {
		t.Fatal(err)
	}
	err := Readable(p)
	if err == nil || !strings.Contains(err.Error(), "chmod 644") || !strings.Contains(err.Error(), p) {
		t.Fatalf("error %v does not name the fix", err)
	}
	ok := filepath.Join(t.TempDir(), "ok.mp4")
	_ = os.WriteFile(ok, []byte("x"), 0o644)
	if err := Readable(ok); err != nil {
		t.Fatal(err)
	}
}
