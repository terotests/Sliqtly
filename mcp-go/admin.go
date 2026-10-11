// SPDX-License-Identifier: AGPL-3.0-or-later

// The owner's dashboard on sliqtly.com: the page is /main/admin (not linked
// anywhere, web/admin.html), its numbers come from
//
//	GET /main/admin/api/stats?days=30
//
// with the page's Firebase ID token as "Authorization: Bearer …". Only a
// Google account whose verified email is in SLIQTLY_ADMIN_EMAILS gets an
// answer; the path being unlisted is not the protection. The service runs
// without the route when the list is empty, and a server of one's own
// (local.go) never has it.
//
// What it tells, per UTC day: visitors and page loads (stats/<day>,
// rgr/Stats.rgr), presentations made (shares.created; owner "mcp" is an
// assistant without sign-in), new and active signed-in people (Firebase
// Auth), and the Cloud bill from the billing export in BigQuery
// (SLIQTLY_BILLING_TABLE). Counts only: no email, name or id goes out.
// adminReport is the model; the sources (firebase.go) only read.
//
// The licenses (editor.go) are the one list with people in it:
//
//	GET  /main/admin/api/licenses            every licenses/{uid}
//	POST /main/admin/api/license {uid, maxDocs, plan?}
//
// the second sets how many presentations an account may edit in the cloud
// (-1: no limit). It changes only an existing license (made at the
// account's first visit to /editor), and never takes presentations away.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	adminPath     = "/main/admin/api/"
	adminKeep     = time.Minute // a report is kept this long per instance
	adminMaxDays  = 90
	adminRefsShow = 10
)

// a presentation as the dashboard counts it
type adminShare struct {
	Created time.Time
	Owner   string // "mcp": an assistant without sign-in
}

// a signed-in person as the dashboard counts them
type adminUser struct {
	Created, Active time.Time // Active: last sign-in or token refresh
}

// one line of the billing export summed: a day, a service, its net cost
type adminCost struct {
	Day, Service, Currency string
	Cost                   float64 // cost and credits, so after free tiers
}

// errBillingOff: no billing table is set (SLIQTLY_BILLING_TABLE)
var errBillingOff = errors.New("billing export not set")

// what the dashboard reads; firebase.go has the real one
type adminSource interface {
	StatsDays(ctx context.Context, days []string) (map[string]Doc, error)
	SharesSince(ctx context.Context, since time.Time) ([]adminShare, error)
	ShareCount(ctx context.Context) (int64, error)
	Users(ctx context.Context) ([]adminUser, error)
	Costs(ctx context.Context, since time.Time) ([]adminCost, error)
	// every licenses/{uid}: the ids and the documents, in the same order
	Licenses(ctx context.Context) ([]string, []Doc, error)
}

type adminConfig struct {
	Emails []string // lower case
	Source adminSource

	mu    sync.Mutex
	kept  map[int]adminCached
	limit func(who string) string
}

type adminCached struct {
	at  time.Time
	out []byte
}

func newAdminConfig(emails []string, src adminSource) *adminConfig {
	list := []string{}
	for _, e := range emails {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			list = append(list, e)
		}
	}
	if len(list) == 0 || src == nil {
		return nil
	}
	return &adminConfig{Emails: list, Source: src, kept: map[int]adminCached{}, limit: rateLimiter(60, 10*time.Minute)}
}

func (a *adminConfig) allowed(t *IDToken) bool {
	if t == nil || !t.Verified {
		return false
	}
	email := strings.ToLower(t.Email)
	for _, e := range a.Emails {
		if e == email {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- report --

type adminVisitDay struct {
	Day      string `json:"day"`
	Visitors int64  `json:"visitors"`
	Views    int64  `json:"views"`
	Editor   int64  `json:"editor"`
	View     int64  `json:"view"`
	Edit     int64  `json:"edit"`
	Mobile   int64  `json:"mobile"`
}

type adminDeckDay struct {
	Day       string `json:"day"`
	Decks     int    `json:"decks"`
	SignedIn  int    `json:"signedIn"`
	Anonymous int    `json:"anonymous"`
	People    int    `json:"people"`
}

type adminUserDay struct {
	Day    string `json:"day"`
	New    int    `json:"new"`
	Active int    `json:"active"`
}

type adminCostDay struct {
	Day  string  `json:"day"`
	Cost float64 `json:"cost"`
}

type adminNamed struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

type adminSection struct {
	Error string `json:"error,omitempty"`
}

type adminReport struct {
	Generated string   `json:"generated"`
	Days      []string `json:"days"` // oldest first
	Visitors  struct {
		adminSection
		Rows     []adminVisitDay `json:"rows"`
		Visitors int64           `json:"visitors"`
		Views    int64           `json:"views"`
		Refs     []adminNamed    `json:"refs"`
	} `json:"visitors"`
	Decks struct {
		adminSection
		Rows      []adminDeckDay `json:"rows"`
		Total     int            `json:"total"`
		Anonymous int            `json:"anonymous"`
		People    int            `json:"people"`
		AllTime   int64          `json:"allTime"`
	} `json:"decks"`
	Users struct {
		adminSection
		Rows   []adminUserDay `json:"rows"`
		Total  int            `json:"total"`
		New    int            `json:"new"`
		Active int            `json:"active"` // active at least once in the range
	} `json:"users"`
	Billing struct {
		adminSection
		Off       bool           `json:"off,omitempty"`
		Currency  string         `json:"currency"`
		Rows      []adminCostDay `json:"rows"`
		Range     float64        `json:"range"`
		Month     float64        `json:"month"`     // this calendar month so far
		LastMonth float64        `json:"lastMonth"` // the whole previous month
		Services  []adminNamed   `json:"services"`  // this month, most first
		Latest    string         `json:"latest"`    // the last day with costs
	} `json:"billing"`
}

// the days of the report, oldest first: today (UTC) and days-1 before it
func adminDays(now time.Time, days int) []string {
	out := make([]string, days)
	for i := 0; i < days; i++ {
		out[days-1-i] = now.UTC().AddDate(0, 0, -i).Format("2006-01-02")
	}
	return out
}

func dayOf(t time.Time) string { return t.UTC().Format("2006-01-02") }

// a count in a Firestore document: int64 there, float64 from JSON
func docInt(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}

func docMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// the first of the month t is in, and of the month before (UTC)
func monthStarts(t time.Time) (time.Time, time.Time) {
	t = t.UTC()
	this := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	return this, this.AddDate(0, -1, 0)
}

func topNamed(m map[string]float64, n int) []adminNamed {
	out := []adminNamed{}
	for k, v := range m {
		out = append(out, adminNamed{k, v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Value != out[j].Value {
			return out[i].Value > out[j].Value
		}
		return out[i].Name < out[j].Name
	})
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

func round2(v float64) float64 {
	if v < 0 {
		return -round2(-v)
	}
	return float64(int64(v*100+0.5)) / 100
}

// The report from what the sources gave; a source that failed leaves its
// section empty with the reason in error.
func buildAdminReport(now time.Time, days int, stats map[string]Doc, statsErr error,
	shares []adminShare, sharesErr error, allTime int64, countErr error,
	users []adminUser, usersErr error, costs []adminCost, costsErr error) *adminReport {
	r := &adminReport{Generated: now.UTC().Format(time.RFC3339), Days: adminDays(now, days)}
	in := map[string]bool{}
	for _, d := range r.Days {
		in[d] = true
	}

	// visitors
	r.Visitors.Error = errText(statsErr)
	r.Visitors.Rows = []adminVisitDay{}
	refs := map[string]float64{}
	for _, d := range r.Days {
		doc := stats[d]
		row := adminVisitDay{Day: d}
		if doc != nil {
			pages, devices := docMap(doc["pages"]), docMap(doc["devices"])
			row.Visitors, row.Views = docInt(doc["visitors"]), docInt(doc["views"])
			row.Editor, row.View, row.Edit = docInt(pages["editor"]), docInt(pages["view"]), docInt(pages["edit"])
			row.Mobile = docInt(devices["mobile"])
			for k, v := range docMap(doc["refs"]) {
				refs[k] += float64(docInt(v))
			}
		}
		r.Visitors.Visitors += row.Visitors
		r.Visitors.Views += row.Views
		r.Visitors.Rows = append(r.Visitors.Rows, row)
	}
	r.Visitors.Refs = topNamed(refs, adminRefsShow)

	// presentations made
	r.Decks.Error = errText(sharesErr)
	if countErr != nil && sharesErr == nil {
		r.Decks.Error = "all-time count: " + countErr.Error()
	}
	r.Decks.AllTime = allTime
	byDay := map[string]*adminDeckDay{}
	people := map[string]map[string]bool{}
	everyone := map[string]bool{}
	for _, s := range shares {
		d := dayOf(s.Created)
		if !in[d] {
			continue
		}
		row := byDay[d]
		if row == nil {
			row = &adminDeckDay{Day: d}
			byDay[d], people[d] = row, map[string]bool{}
		}
		row.Decks++
		r.Decks.Total++
		if s.Owner == "mcp" || s.Owner == "" {
			row.Anonymous++
			r.Decks.Anonymous++
		} else {
			row.SignedIn++
			people[d][s.Owner] = true
			everyone[s.Owner] = true
		}
	}
	r.Decks.Rows = []adminDeckDay{}
	for _, d := range r.Days {
		row := adminDeckDay{Day: d}
		if p := byDay[d]; p != nil {
			row = *p
			row.People = len(people[d])
		}
		r.Decks.Rows = append(r.Decks.Rows, row)
	}
	r.Decks.People = len(everyone)

	// signed-in people
	r.Users.Error = errText(usersErr)
	made, seen := map[string]int{}, map[string]int{}
	for _, u := range users {
		r.Users.Total++
		if d := dayOf(u.Created); in[d] {
			made[d]++
			r.Users.New++
		}
		if !u.Active.IsZero() {
			if d := dayOf(u.Active); in[d] {
				seen[d]++
				r.Users.Active++
			}
		}
	}
	r.Users.Rows = []adminUserDay{}
	for _, d := range r.Days {
		r.Users.Rows = append(r.Users.Rows, adminUserDay{d, made[d], seen[d]})
	}

	// the bill
	switch {
	case errors.Is(costsErr, errBillingOff):
		r.Billing.Off = true
	case costsErr != nil:
		r.Billing.Error = costsErr.Error()
	}
	thisMonth, lastMonth := monthStarts(now)
	m0, m1 := dayOf(thisMonth), dayOf(lastMonth)
	perDay, services := map[string]float64{}, map[string]float64{}
	for _, c := range costs {
		if r.Billing.Currency == "" {
			r.Billing.Currency = c.Currency
		}
		if c.Day > r.Billing.Latest && c.Cost != 0 {
			r.Billing.Latest = c.Day
		}
		if in[c.Day] {
			perDay[c.Day] += c.Cost
			r.Billing.Range += c.Cost
		}
		switch {
		case c.Day >= m0:
			r.Billing.Month += c.Cost
			services[c.Service] += c.Cost
		case c.Day >= m1:
			r.Billing.LastMonth += c.Cost
		}
	}
	r.Billing.Rows = []adminCostDay{}
	for _, d := range r.Days {
		r.Billing.Rows = append(r.Billing.Rows, adminCostDay{d, round2(perDay[d])})
	}
	r.Billing.Range, r.Billing.Month, r.Billing.LastMonth = round2(r.Billing.Range), round2(r.Billing.Month), round2(r.Billing.LastMonth)
	r.Billing.Services = topNamed(services, 0)
	for i := range r.Billing.Services {
		r.Billing.Services[i].Value = round2(r.Billing.Services[i].Value)
	}
	return r
}

// every source read at once, each with its own error
func (a *adminConfig) report(ctx context.Context, now time.Time, days int) *adminReport {
	list := adminDays(now, days)
	_, since := monthStarts(now)
	if first, _ := time.Parse("2006-01-02", list[0]); first.Before(since) {
		since = first
	}
	var (
		wg                                          sync.WaitGroup
		stats                                       map[string]Doc
		shares                                      []adminShare
		users                                       []adminUser
		costs                                       []adminCost
		allTime                                     int64
		statsErr, sharesErr, countErr, usersErr, ce error
	)
	run := func(f func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f()
		}()
	}
	start, _ := time.Parse("2006-01-02", list[0])
	run(func() { stats, statsErr = a.Source.StatsDays(ctx, list) })
	run(func() { shares, sharesErr = a.Source.SharesSince(ctx, start) })
	run(func() { allTime, countErr = a.Source.ShareCount(ctx) })
	run(func() { users, usersErr = a.Source.Users(ctx) })
	run(func() { costs, ce = a.Source.Costs(ctx, since) })
	wg.Wait()
	for _, e := range []error{statsErr, sharesErr, countErr, usersErr} {
		if e != nil {
			log.Printf("admin: %v", e)
		}
	}
	if ce != nil && !errors.Is(ce, errBillingOff) {
		log.Printf("admin billing: %v", ce)
	}
	return buildAdminReport(now, days, stats, statsErr, shares, sharesErr, allTime, countErr, users, usersErr, costs, ce)
}

// ----------------------------------------------------------------- route --

func adminError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	b, _ := json.Marshal(map[string]string{"error": msg})
	w.Write(b)
}

// GET /main/admin/api/stats and /main/admin/api/me (who is signed in, and
// whether the account may see the numbers)
func serveAdmin(env *Env, w http.ResponseWriter, r *http.Request) {
	a := env.Admin
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Robots-Tag", "noindex")
	if a == nil || env.VerifyIDToken == nil {
		http.NotFound(w, r)
		return
	}
	p := strings.TrimPrefix(r.URL.Path, adminPath)
	method, known := map[string]string{"stats": "GET", "me": "GET", "licenses": "GET", "license": "POST"}[p]
	if !known || (strings.HasPrefix(p, "license") && env.DB == nil) {
		http.NotFound(w, r)
		return
	}
	if r.Method != method {
		adminError(w, 405, method+" only")
		return
	}
	if why := a.limit(clientIP(r)); why != "" {
		adminError(w, 429, "Too many requests; try again in a few minutes.")
		return
	}
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") || strings.TrimSpace(h[7:]) == "" {
		adminError(w, 401, "Sign in with Google.")
		return
	}
	t, err := env.VerifyIDToken(r.Context(), strings.TrimSpace(h[7:]))
	if err != nil || t == nil {
		adminError(w, 401, "The sign-in has expired; sign in again.")
		return
	}
	if !a.allowed(t) {
		log.Printf("admin: refused a signed-in account")
		adminError(w, 403, "This account has no access.")
		return
	}
	if p == "me" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		b, _ := json.Marshal(map[string]any{"email": t.Email, "ok": true})
		w.Write(b)
		return
	}
	if p == "licenses" || p == "license" {
		serveAdminLicenses(env, w, r, p == "license")
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days < 1 {
		days = 30
	}
	if days > adminMaxDays {
		days = adminMaxDays
	}
	now := time.Now()
	if env.Now != nil {
		now = env.Now()
	}
	fresh := r.URL.Query().Get("fresh") == "1"
	a.mu.Lock()
	c, ok := a.kept[days]
	a.mu.Unlock()
	if !ok || fresh || now.Sub(c.at) > adminKeep {
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		rep := a.report(ctx, now, days)
		cancel()
		b, _ := json.Marshal(rep)
		c = adminCached{now, b}
		a.mu.Lock()
		a.kept[days] = c
		a.mu.Unlock()
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	io.WriteString(w, string(c.out))
}

// --------------------------------------------------------------- licenses --

const adminMaxQuota = 10000

// a license as the owner's list shows it
type adminLicense struct {
	UID       string `json:"uid"`
	Email     string `json:"email"`
	Name      string `json:"name,omitempty"`
	Plan      string `json:"plan"`
	MaxDocs   int    `json:"maxDocs"` // < 0: no limit
	Used      int    `json:"used"`    // presentations it covers
	EditUntil string `json:"editUntil,omitempty"`
	Seen      string `json:"seen,omitempty"`
	// in SLIQTLY_ADMIN_EMAILS: no limit whatever the document says
	Admin bool `json:"admin,omitempty"`
}

// The licenses as the list shows them, the last seen first. admins: lower
// case, as adminConfig keeps them.
func buildAdminLicenses(ids []string, docs []Doc, admins []string) []adminLicense {
	out := []adminLicense{}
	for i, d := range docs {
		if i >= len(ids) || d == nil {
			continue
		}
		email, _ := d["email"].(string)
		name, _ := d["name"].(string)
		admin := email != "" && slices.Contains(admins, strings.ToLower(email))
		l := licenseOf(d, admin)
		row := adminLicense{UID: ids[i], Email: email, Name: name, Plan: l.Plan, MaxDocs: l.MaxDocs, Used: len(l.Docs), Admin: admin}
		if !l.EditUntil.IsZero() {
			row.EditUntil = l.EditUntil.UTC().Format(time.RFC3339)
		}
		if t := docTime(d["seen"]); !t.IsZero() {
			row.Seen = t.UTC().Format(time.RFC3339)
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Seen != out[j].Seen {
			return out[i].Seen > out[j].Seen
		}
		return strings.ToLower(out[i].Email) < strings.ToLower(out[j].Email)
	})
	return out
}

var (
	adminUID  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	adminPlan = regexp.MustCompile(`^[a-z0-9][a-z0-9 _-]{0,31}$`)
)

// The owner's change to a license, {uid, maxDocs, plan?}: the account and
// the fields to write, or why not. "admin" is not a plan one gives: it
// comes from SLIQTLY_ADMIN_EMAILS.
func parseLicenseEdit(b []byte) (string, Doc, error) {
	var in struct {
		UID     string  `json:"uid"`
		MaxDocs *int    `json:"maxDocs"`
		Plan    *string `json:"plan"`
	}
	if err := json.Unmarshal(b, &in); err != nil {
		return "", nil, errors.New("not JSON")
	}
	if !adminUID.MatchString(in.UID) {
		return "", nil, errors.New("no such account")
	}
	if in.MaxDocs == nil || *in.MaxDocs < -1 || *in.MaxDocs > adminMaxQuota {
		return "", nil, fmt.Errorf("presentations: a number from 0 to %d, or -1 for no limit", adminMaxQuota)
	}
	set := Doc{"maxDocs": int64(*in.MaxDocs)}
	if in.Plan != nil {
		plan := strings.ToLower(strings.TrimSpace(*in.Plan))
		if !adminPlan.MatchString(plan) || plan == "admin" {
			return "", nil, errors.New("plan: lower-case letters, digits, space, - or _ (not admin)")
		}
		set["plan"] = plan
	}
	return in.UID, set, nil
}

// GET licenses: the list; POST license: one changed, and its new row
func serveAdminLicenses(env *Env, w http.ResponseWriter, r *http.Request, edit bool) {
	a := env.Admin
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if !edit {
		ids, docs, err := a.Source.Licenses(ctx)
		if err != nil {
			log.Printf("admin licenses: %v", err)
			adminError(w, 502, "The licenses could not be read: "+errText(err))
			return
		}
		b, _ := json.Marshal(map[string]any{"licenses": buildAdminLicenses(ids, docs, a.Emails)})
		w.Write(b)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	if err != nil {
		adminError(w, 400, "The change is too long.")
		return
	}
	uid, set, err := parseLicenseEdit(body)
	if err != nil {
		adminError(w, 400, err.Error())
		return
	}
	d, err := env.DB.Get(ctx, licenseCol, uid)
	if err != nil {
		adminError(w, 502, "The license could not be read: "+errText(err))
		return
	}
	if d == nil {
		adminError(w, 404, "That account has no license yet; it gets one at its first visit to the editor.")
		return
	}
	// a new rev: a claim in flight (editor.go) reads the license again
	set["rev"] = newShareID()
	if err := env.DB.Update(ctx, licenseCol, uid, set); err != nil {
		adminError(w, 502, "The license was not changed: "+errText(err))
		return
	}
	for k, v := range set {
		d[k] = v
	}
	log.Printf("admin: license of %s set to %v", uid, set["maxDocs"])
	b, _ := json.Marshal(map[string]any{"license": buildAdminLicenses([]string{uid}, []Doc{d}, a.Emails)[0]})
	w.Write(b)
}

// ---------------------------------------------------------------- billing --

// project.dataset.table, nothing else: it goes into the query's text
var billingTable = regexp.MustCompile(`^[a-z][a-z0-9-]{4,61}[a-z0-9]\.[A-Za-z0-9_]{1,300}\.[A-Za-z0-9_]{1,300}$`)

// The export's cost per day and service since `since`, credits (free tiers,
// promotions) taken off.
func billingQuery(table string) string {
	return "SELECT FORMAT_DATE('%F', DATE(usage_start_time)) AS day, service.description AS service, currency, " +
		"SUM(cost) + SUM(IFNULL((SELECT SUM(c.amount) FROM UNNEST(credits) c), 0)) AS net " +
		"FROM `" + table + "` WHERE usage_start_time >= @since " +
		"GROUP BY day, service, currency ORDER BY day"
}

// BigQuery's jobs.query answer as rows: {"rows":[{"f":[{"v":…},…]}]}
func parseBillingRows(status int, b []byte) ([]adminCost, error) {
	var ans struct {
		JobComplete bool `json:"jobComplete"`
		Rows        []struct {
			F []struct {
				V any `json:"v"`
			} `json:"f"`
		} `json:"rows"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(b, &ans); err != nil {
		return nil, fmt.Errorf("BigQuery answered %d", status)
	}
	if status != 200 {
		msg := ans.Error.Message
		if msg == "" {
			msg = http.StatusText(status)
		}
		return nil, fmt.Errorf("BigQuery: %s", msg)
	}
	if !ans.JobComplete {
		return nil, errors.New("BigQuery did not finish in time; reload")
	}
	out := []adminCost{}
	for _, r := range ans.Rows {
		if len(r.F) < 4 {
			continue
		}
		s := func(i int) string { v, _ := r.F[i].V.(string); return v }
		cost, _ := strconv.ParseFloat(s(3), 64)
		out = append(out, adminCost{Day: s(0), Service: s(1), Currency: s(2), Cost: cost})
	}
	return out, nil
}
