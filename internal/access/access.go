// Package access checks, at start, that a service can use the files and directories it was given,
// and names the fix when it cannot. The kunveno images run as user 10001, and in the Docker stack
// ./data, config.yaml, media/ and backgrounds/ come from the host, so a file or directory that user
// cannot use is the usual first-start failure.
package access

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
)

// ID is the service's uid:gid, as the hints name it.
func ID() string { return strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()) }

// Writable makes sure dir exists (created 0700, as the stores do) and the service can create files
// in it.
func Writable(dir string) error {
	err := os.MkdirAll(dir, 0o700)
	var f *os.File
	if err == nil {
		f, err = os.CreateTemp(dir, ".kunveno-write-check-*")
	}
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			id := ID()
			return fmt.Errorf("%s is not writable by this service (it runs as %s): in the Docker stack run "+
				"sudo chown -R %s data in the stack directory; otherwise sudo chown -R %s %s", dir, id, id, id, dir)
		}
		return err
	}
	f.Close()
	return os.Remove(f.Name())
}

// Readable makes sure the service can read the file at path (a media file, a background).
func Readable(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return fmt.Errorf("%s is not readable by this service (it runs as %s): sudo chmod 644 it; "+
				"files in media/ and backgrounds/ must be readable by others", path, ID())
		}
		return err
	}
	return f.Close()
}
