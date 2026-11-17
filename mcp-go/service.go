// SPDX-License-Identifier: AGPL-3.0-or-later

// The Windows service's settings (service_windows.go), here without build
// tags so they are tested everywhere: what the service sets when its
// settings file leaves it out, as sliqtly.service does on Linux.

package main

import (
	"path/filepath"
	"strings"
)

// The service's name, in sc.exe, services.msc and its account
// NT SERVICE\Sliqtly
const serviceName = "Sliqtly"

// serviceDefaults is what the service sets on top of its settings file
// (lookup reads the environment after the file is read). home is
// %ProgramData%\Sliqtly: decks in data\, backups in backup\. An empty
// SLIQTLY_DATA is the default too; an empty SLIQTLY_BACKUP turns backups
// off, as in sliqtly.env on Linux. The service listens on the network
// unless told otherwise, as the Linux package's service does.
func serviceDefaults(home string, lookup func(string) (string, bool)) map[string]string {
	m := map[string]string{}
	if v, _ := lookup("SLIQTLY_DATA"); strings.TrimSpace(v) == "" {
		m["SLIQTLY_DATA"] = filepath.Join(home, "data")
	}
	if _, set := lookup("SLIQTLY_BACKUP"); !set {
		m["SLIQTLY_BACKUP"] = filepath.Join(home, "backup")
	}
	if v, _ := lookup("SLIQTLY_LISTEN"); v == "" {
		m["SLIQTLY_LISTEN"] = accessNetwork
	}
	return m
}

// serviceFolders are the folders the service writes, with the settings
// file's own data and backup folders when it names some: the installer
// makes them and lets the service's account change what is in them
func serviceFolders(home string, settings [][2]string) []string {
	got := map[string]string{}
	for _, p := range settings {
		got[p[0]] = p[1]
	}
	lookup := func(k string) (string, bool) { v, ok := got[k]; return v, ok }
	d := serviceDefaults(home, lookup)
	pick := func(k string) string {
		if v, ok := d[k]; ok {
			return v
		}
		return strings.TrimSpace(got[k])
	}
	out := []string{filepath.Join(home, "log")}
	for _, k := range []string{"SLIQTLY_DATA", "SLIQTLY_BACKUP"} {
		if v := pick(k); v != "" {
			out = append(out, v)
		}
	}
	return out
}
