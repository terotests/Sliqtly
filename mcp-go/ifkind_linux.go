// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import "os"

func interfaceKinds() map[string]string {
	return sysfsKinds("/sys/class/net", os.ReadDir)
}
