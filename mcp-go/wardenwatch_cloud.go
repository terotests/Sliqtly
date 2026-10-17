//go:build !nocloud

// SPDX-License-Identifier: AGPL-3.0-or-later

// The deal watcher's scheduled run is called by GitHub Actions
// (.github/workflows/warden-watch.yml) with a Google ID token of the
// service account the deploy runs as (WARDEN_CRON_SA, set by Deploy MCP
// Go), for the run's own address: no other secret is shared.

package main

import (
	"context"
	"os"
	"strings"

	"google.golang.org/api/idtoken"
)

const wardenCronAudience = "https://sliqtly.com/apps/warden/api/run_watches"

func wardenCronAuth() func(ctx context.Context, token string) bool {
	sa := strings.ToLower(strings.TrimSpace(os.Getenv("WARDEN_CRON_SA")))
	if sa == "" {
		return nil
	}
	return func(ctx context.Context, token string) bool {
		if token == "" {
			return false
		}
		p, err := idtoken.Validate(ctx, token, wardenCronAudience)
		if err != nil {
			return false
		}
		email, _ := p.Claims["email"].(string)
		verified, _ := p.Claims["email_verified"].(bool)
		return verified && strings.ToLower(email) == sa
	}
}
