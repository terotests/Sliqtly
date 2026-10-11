//go:build !nocloud

// SPDX-License-Identifier: AGPL-3.0-or-later

// Reading a Figma file put on sliqtly.com is the work of a Cloud Run job
// (cloudshared.go): the service starts an execution of it for the one file
// through the Cloud Run Admin API, as its own service account, which the
// job lets run it with other arguments (roles/run.jobsExecutorWithOverrides,
// .github/workflows/deploy-mcp-go.yml).

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"

	"golang.org/x/oauth2/google"
)

var cloudJobName = regexp.MustCompile(`^projects/[a-z0-9-]+/locations/[a-z0-9-]+/jobs/[a-z0-9-]+$`)

// a start for cloudShared that runs job (projects/…/locations/…/jobs/…) as
// `figma-index <uid> <id>`; nil without a job
func cloudJobStarter(job string) func(ctx context.Context, uid, id string) error {
	if !cloudJobName.MatchString(job) {
		return nil
	}
	return func(ctx context.Context, uid, id string) error {
		client, err := google.DefaultClient(ctx, "https://www.googleapis.com/auth/cloud-platform")
		if err != nil {
			return err
		}
		body, _ := json.Marshal(map[string]any{"overrides": map[string]any{
			"containerOverrides": []any{map[string]any{"args": []string{"figma-index", uid, id}}},
		}})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://run.googleapis.com/v2/"+job+":run", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		if err != nil {
			return err
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			b, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
			return fmt.Errorf("%s: %s", res.Status, bytes.TrimSpace(b))
		}
		return nil
	}
}
