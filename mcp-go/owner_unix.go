// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// sameOwner gives what root made in dir to dir's own owner: `backup` run
// by root (sudo) on the folder of a server that runs as its own user
// (the .deb's sliqtly.service) must leave nothing that user cannot write.
func sameOwner(dir string) {
	if os.Geteuid() != 0 {
		return
	}
	info, err := os.Stat(dir)
	if err != nil {
		return
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid == 0 {
		return
	}
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if i, err := d.Info(); err == nil {
			if s, ok := i.Sys().(*syscall.Stat_t); ok && (s.Uid != st.Uid || s.Gid != st.Gid) {
				os.Lchown(p, int(st.Uid), int(st.Gid))
			}
		}
		return nil
	})
}
