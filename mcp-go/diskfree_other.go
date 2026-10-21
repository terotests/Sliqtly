// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !unix

package main

func diskSpace(string) (free, total int64, ok bool) { return 0, 0, false }
