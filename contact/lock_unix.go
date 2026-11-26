//go:build unix

package main

import (
	"os"
	"syscall"
)

// lockFile takes an exclusive lock on path, waiting for other runs of the
// program to finish. The file is never deleted; the system drops the lock
// when the program exits, even if it crashes, so there are no stale locks.
func lockFile(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
