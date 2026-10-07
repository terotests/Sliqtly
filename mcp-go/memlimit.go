package main

import (
	"log"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
)

// Go does not see a container's memory limit: its heap grows to twice the
// live data before it collects, and a burst of renders and exports in a
// 512 MB container got it killed by the kernel. With the limit known, the
// collector works harder near it instead (GOMEMLIMIT, when set, decides).
func applyMemoryLimit() {
	if os.Getenv("GOMEMLIMIT") != "" {
		return
	}
	if limit, ok := cgroupMemoryLimit(os.ReadFile); ok {
		soft := limit / 10 * 8
		debug.SetMemoryLimit(soft)
		log.Printf("memory limit %d MB: the heap is kept under %d MB", limit>>20, soft>>20)
	}
}

// the limit of the cgroup this process runs in (v2, else v1); false when
// there is none
func cgroupMemoryLimit(read func(string) ([]byte, error)) (int64, bool) {
	for _, p := range []string{"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory/memory.limit_in_bytes"} {
		b, err := read(p)
		if err != nil {
			continue
		}
		v, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
		// "max", or v1's "no limit" (a number near 2^63)
		if err != nil || v <= 0 || v >= 1<<50 {
			return 0, false
		}
		return v, true
	}
	return 0, false
}
