// SPDX-License-Identifier: AGPL-3.0-or-later

// A settings file of KEY=value lines (sliqtly.env), read by the server
// itself where nothing reads it for it: systemd and Homebrew hand the
// settings over as the environment, the Windows service manager has no
// such file (winservice_windows.go).

package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// parseEnvFile reads KEY=value lines; a line starting with # and an empty
// line are skipped, a value in matching quotes loses them. A line without
// = or with a key that is not a name is an error, with its line number.
func parseEnvFile(r io.Reader) ([][2]string, error) {
	var out [][2]string
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if n == 1 {
			line = strings.TrimPrefix(line, "\ufeff") // Notepad's byte order mark
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || !envName(key) {
			return nil, fmt.Errorf("line %d: not KEY=value: %q", n, line)
		}
		val = strings.TrimSpace(val)
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
			val = val[1 : len(val)-1]
		}
		out = append(out, [2]string{key, val})
	}
	return out, sc.Err()
}

func envName(s string) bool {
	if s == "" || s[0] >= '0' && s[0] <= '9' {
		return false
	}
	for _, c := range s {
		if !(c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// loadEnvFile sets the file's settings in the environment; one the
// environment has already wins
func loadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	pairs, err := parseEnvFile(f)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	for _, p := range pairs {
		if _, set := os.LookupEnv(p[0]); !set {
			os.Setenv(p[0], p[1])
		}
	}
	return nil
}
