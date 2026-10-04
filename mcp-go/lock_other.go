// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !unix

package main

// no advisory lock here: the folder is not guarded against a second server
func lockFolder(string) (func(), error) { return func() {}, nil }
