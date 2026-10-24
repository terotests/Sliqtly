// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"net"

	"golang.org/x/sys/windows"
)

// interfaceKinds names each interface as Go's net package does (its
// friendly name, e.g. "Ethernet" or "Wi-Fi") with its kind
func interfaceKinds() map[string]string {
	m := map[string]string{}
	ifs, err := net.Interfaces()
	if err != nil {
		return m
	}
	for _, ifi := range ifs {
		row := windows.MibIfRow2{InterfaceIndex: uint32(ifi.Index)}
		if windows.GetIfEntry2Ex(windows.MibIfEntryNormalWithoutStatistics, &row) != nil {
			continue
		}
		// InterfaceAndOperStatusFlags: HardwareInterface is its lowest bit
		hardware := row.InterfaceAndOperStatusFlags&1 != 0
		m[ifi.Name] = windowsKind(row.Type, hardware, windows.UTF16ToString(row.Description[:]))
	}
	return m
}
