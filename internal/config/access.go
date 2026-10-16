package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
)

// The kunveno images run as user 10001. In the Docker stack ./data (/var/lib/kunveno),
// config.yaml, media/ and backgrounds/ come from the host, so a file or directory that user cannot
// use is the usual first-start failure; these checks turn it into the command that fixes it.

func uidgid() string { return strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()) }

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
			id := uidgid()
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
				"files in media/ and backgrounds/ must be readable by others", path, uidgid())
		}
		return err
	}
	return f.Close()
}

// readHint names the fix for a config file the service cannot read.
func readHint(err error) error {
	if !errors.Is(err, fs.ErrPermission) {
		return err
	}
	return fmt.Errorf("%w (this service runs as %s: in the stack directory run sudo chgrp %d config.yaml && sudo chmod 640 config.yaml)",
		err, uidgid(), os.Getgid())
}
