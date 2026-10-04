// SPDX-License-Identifier: AGPL-3.0-or-later

// Telling a cable from Wi-Fi and a phone (netaccess.go). Both readers are
// here, not behind build tags, so either can be tested anywhere.

package main

import (
	"os"
	"path/filepath"
	"strings"
)

// parseHardwarePorts reads networksetup -listallhardwareports:
//
//	Hardware Port: Wi-Fi
//	Device: en0
func parseHardwarePorts(out string) map[string]string {
	m := map[string]string{}
	port := ""
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Hardware Port:"):
			port = strings.TrimSpace(strings.TrimPrefix(line, "Hardware Port:"))
		case strings.HasPrefix(line, "Device:") && port != "":
			dev := strings.TrimSpace(strings.TrimPrefix(line, "Device:"))
			if dev != "" {
				m[dev] = portKind(port)
			}
			port = ""
		}
	}
	return m
}

func portKind(port string) string {
	p := strings.ToLower(port)
	switch {
	case strings.Contains(p, "wi-fi") || strings.Contains(p, "airport") || strings.Contains(p, "wlan"):
		return "wifi"
	case strings.Contains(p, "iphone") || strings.Contains(p, "ipad") || strings.Contains(p, "bluetooth") ||
		strings.Contains(p, "modem") || strings.Contains(p, "cellular") || strings.Contains(p, "android"):
		return "cellular"
	case strings.Contains(p, "ethernet") || strings.Contains(p, "lan") || strings.Contains(p, "thunderbolt"):
		return "wired"
	}
	return "other"
}

// drivers of a phone shared over USB, and of mobile broadband modems
var cellularDrivers = map[string]bool{
	"rndis_host": true, "ipheth": true, "qmi_wwan": true, "cdc_mbim": true,
	"cdc_ncm": true, "huawei_cdc_ncm": true, "option": true, "sierra_net": true,
}

// sysfsKinds reads Linux's /sys/class/net: wireless/ or phy80211 is Wi-Fi,
// a wwan* name or a phone's or modem's driver is cellular, an Ethernet
// interface with a device behind it is wired, and one without (docker0,
// veth, bridges, tun) is virtual.
func sysfsKinds(root string, readDir func(string) ([]os.DirEntry, error)) map[string]string {
	m := map[string]string{}
	entries, err := readDir(root)
	if err != nil {
		return m
	}
	for _, e := range entries {
		name := e.Name()
		dir := filepath.Join(root, name)
		exists := func(p string) bool { _, err := os.Stat(filepath.Join(dir, p)); return err == nil }
		uevent, _ := os.ReadFile(filepath.Join(dir, "uevent"))
		driver := ""
		if d, err := os.Readlink(filepath.Join(dir, "device", "driver")); err == nil {
			driver = filepath.Base(d)
		}
		typ, _ := os.ReadFile(filepath.Join(dir, "type"))
		switch {
		case name == "lo":
			m[name] = "loopback"
		case exists("wireless") || exists("phy80211") || strings.Contains(string(uevent), "DEVTYPE=wlan"):
			m[name] = "wifi"
		case strings.HasPrefix(name, "wwan") || strings.Contains(string(uevent), "DEVTYPE=wwan") || cellularDrivers[driver]:
			m[name] = "cellular"
		case !exists("device"):
			m[name] = "virtual"
		case strings.TrimSpace(string(typ)) == "1":
			m[name] = "wired"
		default:
			m[name] = "other"
		}
	}
	return m
}
