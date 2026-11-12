// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows

package main

import (
	"fmt"
	"io"
)

// serviceCmd is the Windows service's (service_windows.go); systemd runs
// the Linux package (sliqtly.service), Homebrew the Mac's
func serviceCmd(_ []string, _, errOut io.Writer) int {
	fmt.Fprintln(errOut, "sliqtly-server service: for Windows; on Linux the package's sliqtly.service runs the server (systemctl)")
	return 2
}
