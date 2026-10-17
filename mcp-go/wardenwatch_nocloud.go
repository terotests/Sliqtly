//go:build nocloud

// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import "context"

// no scheduled run without the cloud
func wardenCronAuth() func(ctx context.Context, token string) bool { return nil }
