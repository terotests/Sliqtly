package main

import (
	"errors"
	"testing"
)

func TestCgroupMemoryLimit(t *testing.T) {
	files := func(m map[string]string) func(string) ([]byte, error) {
		return func(p string) ([]byte, error) {
			if v, ok := m[p]; ok {
				return []byte(v), nil
			}
			return nil, errors.New("no such file")
		}
	}
	for _, c := range []struct {
		name  string
		files map[string]string
		want  int64
		ok    bool
	}{
		{"v2 limit", map[string]string{"/sys/fs/cgroup/memory.max": "536870912\n"}, 536870912, true},
		{"v2 no limit", map[string]string{"/sys/fs/cgroup/memory.max": "max\n"}, 0, false},
		{"v1 limit", map[string]string{"/sys/fs/cgroup/memory/memory.limit_in_bytes": "268435456\n"}, 268435456, true},
		{"v1 no limit", map[string]string{"/sys/fs/cgroup/memory/memory.limit_in_bytes": "9223372036854771712\n"}, 0, false},
		{"no cgroup", map[string]string{}, 0, false},
	} {
		got, ok := cgroupMemoryLimit(files(c.files))
		if got != c.want || ok != c.ok {
			t.Errorf("%s: got %d %v, want %d %v", c.name, got, ok, c.want, c.ok)
		}
	}
}
