// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !linux && !darwin

package main

// no way to tell a cable from Wi-Fi here: every interface is "other", and
// "wired" opens nothing beyond this computer
func interfaceKinds() map[string]string { return map[string]string{} }
