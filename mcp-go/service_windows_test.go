// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"testing"
)

func TestDiskSpaceWindows(t *testing.T) {
	free, total, ok := diskSpace(t.TempDir())
	if !ok || free <= 0 || total < free {
		t.Fatalf("free %d, total %d, ok %v", free, total, ok)
	}
	if _, _, ok := diskSpace(`Q:\no\such\folder`); ok {
		t.Fatal("a disk that is not there has space")
	}
}

func TestInterfaceKindsWindows(t *testing.T) {
	kinds := interfaceKinds()
	if len(kinds) == 0 {
		t.Fatal("no interfaces")
	}
	t.Log(kinds)
	for name, k := range kinds {
		if k == "" {
			t.Fatalf("%s has no kind", name)
		}
	}
}

func TestCommandLine(t *testing.T) {
	eq(t, commandLine(`C:\Program Files\Sliqtly\sliqtly-server.exe`, []string{"service", "run", "-config", `D:\my settings\s.env`}),
		`"C:\Program Files\Sliqtly\sliqtly-server.exe" service run -config "D:\my settings\s.env"`)
}

func TestServiceRunOutsideTheManager(t *testing.T) {
	if code := serviceCmd([]string{"run"}, os.Stdout, os.Stderr); code != 2 {
		t.Fatalf("service run in a test: %d", code)
	}
}
