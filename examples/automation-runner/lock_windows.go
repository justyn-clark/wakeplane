package main

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func lockStateDirectory(directory string) (func() error, error) {
	file, err := os.OpenFile(filepath.Join(directory, "runner.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	var overlapped windows.Overlapped
	if err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("runner state is already in use: %w", err)
	}
	return func() error { return file.Close() }, nil
}
