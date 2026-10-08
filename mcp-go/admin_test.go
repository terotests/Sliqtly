// SPDX-License-Identifier: AGPL-3.0-or-later

// The owner's dashboard (admin.go): the report from the sources, and who
// gets an answer from /main/admin/api.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeAdmin struct {
	stats   map[string]Doc
	shares  []adminShare
	users   []adminUser
	costs   []adminCost
	costErr error
	reads   int
}

func (f *fakeAdmin) StatsDays(_ context.Context, days []string) (map[string]Doc, error) {
	f.reads++
	return f.stats, nil
}
func (f *fakeAdmin) SharesSince(_ context.Context, since time.Time) ([]adminShare, error) {
	out := []adminShare{}
	for _, s := range f.shares {
		if !s.Created.Before(since) {
			out = append(out, s)
		}
	}
	return out, nil
}
func (f *fakeAdmin) ShareCount(context.Context) (int64, error) {
	return int64(len(f.shares)) + 100, nil
}
func (f *fakeAdmin) Users(context.Context) ([]adminUser, error) {
	if f.users == nil {
		return nil, errors.New("PERMISSION_DENIED")
	}
	return f.users, nil
}
func (f *fakeAdmin) Costs(context.Context, time.Time) ([]adminCost, error) {
	return f.costs, f.costErr
}

var adminNow = time.Date(2026, 10, 8, 15, 0, 0, 0, time.UTC)

func at(day string, hour int) time.Time {
	t, _ := time.Parse("2006-01-02", day)
	return t.Add(time.Duration(hour) * time.Hour)
}

func TestAdminDays(t *testing.T) {
	eq(t, adminDays(adminNow, 3), []string{"2026-10-06", "2026-10-07", "2026-10-08"})
	// 23:30 in Helsinki is still the same UTC day
	eq(t, adminDays(time.Date(2026, 10, 9, 1, 30, 0, 0, time.FixedZone("EEST", 3*3600)), 1), []string{"2026-10-08"})
}

func TestAdminReport(t *testing.T) {
	stats := map[string]Doc{
		"2026-10-07": {"visitors": int64(5), "views": int64(9), "pages": map[string]any{"view": int64(7), "edit": int64(2)}, "devices": map[string]any{"mobile": int64(2)}, "refs": map[string]any{"google.com": int64(3), "x.com": int64(1)}},
		"2026-10-08": {"visitors": int64(2), "views": int64(2), "refs": map[string]any{"google.com": int64(1)}},
		"2026-09-01": {"visitors": int64(99)}, // out of range
	}
	shares := []adminShare{
		{at("2026-10-07", 1), "mcp"}, {at("2026-10-07", 2), "u1"}, {at("2026-10-07", 3), "u1"}, {at("2026-10-08", 1), "u2"},
		{at("2026-10-01", 1), "u3"}, // before the range
	}
	users := []adminUser{
		{Created: at("2026-10-07", 5), Active: at("2026-10-08", 9)},
		{Created: at("2026-01-01", 0), Active: at("2026-10-08", 1)},
		{Created: at("2026-01-01", 0)},
	}
	costs := []adminCost{
		{"2026-09-10", "Cloud Run", "EUR", 1.004}, // last month
		{"2026-10-02", "Cloud Run", "EUR", 0.5},
		{"2026-10-07", "Cloud Run", "EUR", 0.25},
		{"2026-10-07", "Cloud Firestore", "EUR", 0.333},
		{"2026-10-08", "Cloud Run", "EUR", 0},
	}
	r := buildAdminReport(adminNow, 2, stats, nil, shares, nil, 140, nil, users, nil, costs, nil)

	eq(t, r.Days, []string{"2026-10-07", "2026-10-08"})
	eq(t, r.Visitors.Rows[0], adminVisitDay{"2026-10-07", 5, 9, 0, 7, 2, 2})
	eq(t, []int64{r.Visitors.Visitors, r.Visitors.Views}, []int64{7, 11})
	eq(t, r.Visitors.Refs, []adminNamed{{"google.com", 4}, {"x.com", 1}})

	eq(t, r.Decks.Rows, []adminDeckDay{{"2026-10-07", 3, 2, 1, 1}, {"2026-10-08", 1, 1, 0, 1}})
	eq(t, []int{r.Decks.Total, r.Decks.Anonymous, r.Decks.People}, []int{4, 1, 2})
	eq(t, r.Decks.AllTime, int64(140))

	eq(t, r.Users.Rows, []adminUserDay{{"2026-10-07", 1, 0}, {"2026-10-08", 0, 2}})
	eq(t, []int{r.Users.Total, r.Users.New, r.Users.Active}, []int{3, 1, 2})

	eq(t, r.Billing.Currency, "EUR")
	eq(t, r.Billing.Rows, []adminCostDay{{"2026-10-07", 0.58}, {"2026-10-08", 0}})
	eq(t, []float64{r.Billing.Range, r.Billing.Month, r.Billing.LastMonth}, []float64{0.58, 1.08, 1})
	eq(t, r.Billing.Services, []adminNamed{{"Cloud Run", 0.75}, {"Cloud Firestore", 0.33}})
	eq(t, r.Billing.Latest, "2026-10-07")
	eq(t, r.Billing.Off, false)
}

func TestAdminReportErrors(t *testing.T) {
	r := buildAdminReport(adminNow, 1, nil, errors.New("stats down"), nil, nil, 0, nil, nil, errors.New("no auth role"), nil, errBillingOff)
	eq(t, r.Visitors.Error, "stats down")
	eq(t, r.Users.Error, "no auth role")
	eq(t, r.Billing.Off, true)
	eq(t, r.Billing.Error, "")
	eq(t, len(r.Visitors.Rows), 1)
	r = buildAdminReport(adminNow, 1, nil, nil, nil, nil, 0, nil, nil, nil, nil, errors.New("BigQuery: Access Denied"))
	eq(t, []any{r.Billing.Off, r.Billing.Error}, []any{false, "BigQuery: Access Denied"})
}

func TestBillingRows(t *testing.T) {
	rows, err := parseBillingRows(200, []byte(`{"jobComplete":true,"rows":[{"f":[{"v":"2026-10-07"},{"v":"Cloud Run"},{"v":"EUR"},{"v":"0.125"}]}]}`))
	eq(t, err, nil)
	eq(t, rows, []adminCost{{"2026-10-07", "Cloud Run", "EUR", 0.125}})
	_, err = parseBillingRows(403, []byte(`{"error":{"code":403,"message":"Access Denied: Table x"}}`))
	eq(t, err.Error(), "BigQuery: Access Denied: Table x")
	_, err = parseBillingRows(200, []byte(`{"jobComplete":false}`))
	match(t, err.Error(), "did not finish")
	if !billingTable.MatchString("sliqtly.billing_export.gcp_billing_export_v1_0123AB_CDEF") {
		t.Fatal("a real table name was refused")
	}
	for _, bad := range []string{"", "sliqtly.billing", "sliqtly.a.b` WHERE 1=1 --", "x.a.b"} {
		if billingTable.MatchString(bad) {
			t.Fatalf("%q was taken as a table", bad)
		}
	}
	match(t, billingQuery("p-1234.d.t"), "FROM `p-1234.d.t` WHERE usage_start_time >= @since")
}

func adminServer(t *testing.T, src adminSource) (string, func()) {
	e := testEnv(nil, nil)
	e.Now = func() time.Time { return adminNow }
	e.VerifyIDToken = func(_ context.Context, tok string) (*IDToken, error) {
		switch tok {
		case "tero":
			return &IDToken{UID: "u1", Email: "Teroktolonen@gmail.com", Verified: true}, nil
		case "unverified":
			return &IDToken{UID: "u2", Email: "teroktolonen@gmail.com"}, nil
		case "other":
			return &IDToken{UID: "u3", Email: "someone@example.com", Verified: true}, nil
		}
		return nil, fmt.Errorf("bad token")
	}
	e.Admin = newAdminConfig([]string{" teroktolonen@gmail.com "}, src)
	srv := httptest.NewServer(NewApp(e))
	return srv.URL, srv.Close
}

func adminGet(t *testing.T, url, token string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("%s: Cache-Control %q", url, res.Header.Get("Cache-Control"))
	}
	out := map[string]any{}
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func TestAdminAccess(t *testing.T) {
	src := &fakeAdmin{stats: map[string]Doc{"2026-10-08": {"visitors": int64(4)}}, shares: []adminShare{{at("2026-10-08", 1), "mcp"}}, costErr: errBillingOff}
	base, stop := adminServer(t, src)
	defer stop()
	api := base + "/main/admin/api/stats?days=7"

	for _, c := range []struct {
		token string
		want  int
	}{{"", 401}, {"expired", 401}, {"unverified", 403}, {"other", 403}} {
		code, body := adminGet(t, api, c.token)
		eq(t, code, c.want, c.token)
		if _, has := body["visitors"]; has {
			t.Fatalf("%q got numbers", c.token)
		}
	}
	code, me := adminGet(t, base+"/main/admin/api/me", "tero")
	eq(t, []any{code, me["ok"]}, []any{200, true})

	code, body := adminGet(t, api, "tero")
	eq(t, code, 200)
	eq(t, len(body["days"].([]any)), 7)
	eq(t, body["visitors"].(map[string]any)["visitors"], 4.0)
	eq(t, body["decks"].(map[string]any)["allTime"], 101.0)
	eq(t, body["billing"].(map[string]any)["off"], true)
	match(t, body["users"].(map[string]any)["error"].(string), "PERMISSION_DENIED")
	if strings.Contains(fmt.Sprint(body), "u1") {
		t.Fatal("an id went out")
	}

	// kept a minute per instance, unless asked fresh
	adminGet(t, api, "tero")
	eq(t, src.reads, 1)
	adminGet(t, api+"&fresh=1", "tero")
	eq(t, src.reads, 2)

	code, _ = adminGet(t, base+"/main/admin/api/other", "tero")
	eq(t, code, 404)
}

func TestAdminOffWithoutList(t *testing.T) {
	if newAdminConfig(nil, &fakeAdmin{}) != nil || newAdminConfig([]string{" ", ""}, &fakeAdmin{}) != nil {
		t.Fatal("an empty list made a dashboard")
	}
	e := testEnv(nil, nil)
	e.VerifyIDToken = func(context.Context, string) (*IDToken, error) { return &IDToken{Email: "a@b.c", Verified: true}, nil }
	srv := httptest.NewServer(NewApp(e))
	defer srv.Close()
	code, _ := adminGet(t, srv.URL+"/main/admin/api/stats", "x")
	eq(t, code, 404)
}
