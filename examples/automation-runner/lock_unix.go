//go:build !windows

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func lockStateDirectory(directory string) (func() error, error) {
	file, err := os.OpenFile(filepath.Join(directory, "runner.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("runner state is already in use: %w", err)
	}
	return func() error { return file.Close() }, nil
}
