// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os/exec"
	"sync"
	"time"
)

// interfaceKinds names each interface's kind from macOS's own list of its
// hardware ports (networksetup -listallhardwareports): "Wi-Fi" is wifi,
// "iPhone USB" and Bluetooth are a phone's connection, "Ethernet",
// "USB 10/100/1000 LAN", "Thunderbolt Ethernet" are wired. An interface
// not on the list (a VPN's utun, Internet Sharing's bridge100, awdl) is
// other, which nothing opens to.
func interfaceKinds() map[string]string {
	darwinKinds.Lock()
	defer darwinKinds.Unlock()
	if time.Since(darwinKinds.at) < 5*time.Second && darwinKinds.m != nil {
		return darwinKinds.m
	}
	out, err := exec.Command("/usr/sbin/networksetup", "-listallhardwareports").Output()
	m := map[string]string{}
	if err == nil {
		m = parseHardwarePorts(string(out))
	}
	darwinKinds.m, darwinKinds.at = m, time.Now()
	return m
}

var darwinKinds struct {
	sync.Mutex
	m  map[string]string
	at time.Time
}
