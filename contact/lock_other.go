//go:build !unix

package main

import "errors"

// The program runs on OpenBSD, and is tested in WSL (Linux). Other systems
// can build it but not handle a submission.
func lockFile(string) (func(), error) {
	return nil, errors.New("file locking is only supported on Unix systems")
}
