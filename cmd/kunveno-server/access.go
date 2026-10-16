package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"

	"github.com/ssubbotin/kunveno/internal/config"
)

// The images run as user 10001. The stack bind-mounts ./data and config.yaml from the host, so their
// owner is whoever unpacked the stack; the checks below turn a permission error at start into the
// command that fixes it.

func uidgid() string { return strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()) }

// loadConfig is config.Load, with the fix named when the file is not readable.
func loadConfig(path string) (*config.Config, error) {
	cfg, err := config.Load(path)
	if errors.Is(err, fs.ErrPermission) {
		return nil, fmt.Errorf("%w (the server runs as %s: in the stack directory run sudo chgrp %d config.yaml && chmod 640 config.yaml)",
			err, uidgid(), os.Getgid())
	}
	return cfg, err
}

// writable checks that the server can create files in dir, where it keeps rooms, notes, recordings
// and meeting summaries.
func writable(dir string) error {
	err := os.MkdirAll(dir, 0o755)
	var f *os.File
	if err == nil {
		f, err = os.CreateTemp(dir, ".kunveno-write-check-*")
	}
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return fmt.Errorf("%s is not writable by the server (it runs as %s): in the stack directory run sudo chown -R %s data",
				dir, uidgid(), uidgid())
		}
		return err
	}
	f.Close()
	return os.Remove(f.Name())
}
