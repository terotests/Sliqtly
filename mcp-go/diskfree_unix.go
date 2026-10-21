// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package main

import "syscall"

// diskSpace is the free and total bytes of the disk dir is on; ok is false
// when they cannot be read
func diskSpace(dir string) (free, total int64, ok bool) {
	var st syscall.Statfs_t
	if syscall.Statfs(dir, &st) != nil {
		return 0, 0, false
	}
	return int64(st.Bavail) * int64(st.Bsize), int64(st.Blocks) * int64(st.Bsize), true
}
