// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import "golang.org/x/sys/windows"

// diskSpace is the free and total bytes of the disk dir is on; ok is false
// when they cannot be read
func diskSpace(dir string) (free, total int64, ok bool) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, 0, false
	}
	var avail, all, allFree uint64
	if windows.GetDiskFreeSpaceEx(p, &avail, &all, &allFree) != nil {
		return 0, 0, false
	}
	return int64(avail), int64(all), true
}
