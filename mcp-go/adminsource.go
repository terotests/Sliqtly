//go:build !nocloud

// SPDX-License-Identifier: AGPL-3.0-or-later

// What the owner's dashboard (admin.go) reads in the cloud: Firestore's
// stats/<day> and shares, Firebase Auth's users, and the Cloud Billing
// export in BigQuery. The service account needs Cloud Datastore User
// (already), Firebase Authentication Viewer for the users, and for the bill
// BigQuery Job User on the project and BigQuery Data Viewer on the export's
// dataset.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"cloud.google.com/go/firestore"
	"firebase.google.com/go/v4/auth"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/iterator"
)

type cloudAdmin struct {
	fs      *firestore.Client
	auth    *auth.Client
	project string // the project BigQuery jobs run in
	table   string // project.dataset.table of the billing export; "": off
}

func (c *cloudAdmin) StatsDays(ctx context.Context, days []string) (map[string]Doc, error) {
	refs := make([]*firestore.DocumentRef, len(days))
	for i, d := range days {
		refs[i] = c.fs.Collection("stats").Doc(d)
	}
	snaps, err := c.fs.GetAll(ctx, refs)
	if err != nil {
		return nil, err
	}
	out := map[string]Doc{}
	for _, s := range snaps {
		if s.Exists() {
			out[s.Ref.ID] = s.Data()
		}
	}
	return out, nil
}

func (c *cloudAdmin) SharesSince(ctx context.Context, since time.Time) ([]adminShare, error) {
	it := c.fs.Collection("shares").Where("created", ">=", since).Select("created", "owner").Documents(ctx)
	defer it.Stop()
	out := []adminShare{}
	for {
		s, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		d := s.Data()
		t, _ := d["created"].(time.Time)
		owner, _ := d["owner"].(string)
		out = append(out, adminShare{t, owner})
	}
}

func (c *cloudAdmin) ShareCount(ctx context.Context) (int64, error) {
	res, err := c.fs.Collection("shares").NewAggregationQuery().WithCount("n").Get(ctx)
	if err != nil {
		return 0, err
	}
	return countOf(res["n"]), nil
}

// an aggregation's count: *firestorepb.Value or a plain number by version
func countOf(v any) int64 {
	switch n := v.(type) {
	case interface{ GetIntegerValue() int64 }:
		return n.GetIntegerValue()
	case int64:
		return n
	}
	return 0
}

func (c *cloudAdmin) Users(ctx context.Context) ([]adminUser, error) {
	it := c.auth.Users(ctx, "")
	out := []adminUser{}
	for {
		u, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		m := u.UserMetadata
		if m == nil {
			continue
		}
		last := m.LastRefreshTimestamp
		if m.LastLogInTimestamp > last {
			last = m.LastLogInTimestamp
		}
		a := adminUser{Created: time.UnixMilli(m.CreationTimestamp)}
		if last > 0 {
			a.Active = time.UnixMilli(last)
		}
		out = append(out, a)
	}
}

func (c *cloudAdmin) Costs(ctx context.Context, since time.Time) ([]adminCost, error) {
	if c.table == "" {
		return nil, errBillingOff
	}
	if !billingTable.MatchString(c.table) {
		return nil, fmt.Errorf("SLIQTLY_BILLING_TABLE is not project.dataset.table: %q", c.table)
	}
	client, err := google.DefaultClient(ctx, "https://www.googleapis.com/auth/bigquery.readonly")
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]any{
		"query":         billingQuery(c.table),
		"useLegacySql":  false,
		"timeoutMs":     30000,
		"parameterMode": "NAMED",
		"queryParameters": []any{map[string]any{
			"name":           "since",
			"parameterType":  map[string]string{"type": "TIMESTAMP"},
			"parameterValue": map[string]string{"value": since.UTC().Format("2006-01-02 15:04:05")},
		}},
	})
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://bigquery.googleapis.com/bigquery/v2/projects/"+c.project+"/queries", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	return parseBillingRows(res.StatusCode, b)
}
