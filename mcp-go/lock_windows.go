// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockFolder holds an exclusive lock on path until release; a second
// process asking for it gets an error at once rather than waiting
func lockFolder(path string) (release func(), err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	h := windows.Handle(f.Fd())
	all := ^uint32(0)
	if err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, all, all, new(windows.Overlapped)); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		windows.UnlockFileEx(h, 0, all, all, new(windows.Overlapped))
		f.Close()
	}, nil
}
