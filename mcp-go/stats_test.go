// SPDX-License-Identifier: AGPL-3.0-or-later

// Visitor counts (rgr/Stats.rgr) and the /api/hit route, with Firestore
// replaced by the fake that applies increments.

package main

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

const (
	UA    = "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/605.1.15 Safari/605.1.15"
	PHONE = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) Mobile/15E148 Safari/604.1"
)

func TestVisitOf(t *testing.T) {
	v := Stats_static_visitOf(J_static_parse(`{"p":"editor","r":"www.Google.com"}`), UA)
	eq(t, []any{v.ok, v.page, v.device, v.ref}, []any{true, "editor", "desktop", "google.com"})
	v = Stats_static_visitOf(J_static_parse(`{"p":"view"}`), PHONE)
	eq(t, []any{v.ok, v.page, v.device, v.ref}, []any{true, "view", "mobile", ""})
	eq(t, Stats_static_visitOf(J_static_parse(`{"p":"view","r":"sliqtly.com"}`), UA).ref, "")
	eq(t, Stats_static_visitOf(J_static_parse(`{"p":"view","r":"a b/c"}`), UA).ref, "")
	for _, c := range []struct{ body, ua string }{
		{`{"p":"admin"}`, UA}, {`{}`, UA}, {`{"p":"editor"}`, "Googlebot/2.1"}, {`{"p":"editor"}`, ""},
	} {
		if Stats_static_visitOf(J_static_parse(c.body), c.ua).ok {
			t.Fatalf("%s %q was counted", c.body, c.ua)
		}
	}
}

type hits struct {
	f     fb
	url   string
	clock time.Time
	close func()
}

func startHits(t *testing.T) *hits {
	h := &hits{f: fakeFirebase(), clock: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)}
	e := testEnv(&h.f, nil)
	e.BaseURL = "https://sliqtly.com"
	e.Now = func() time.Time { return h.clock }
	srv := httptest.NewServer(NewApp(e))
	h.url, h.close = srv.URL+"/api/hit", srv.Close
	return h
}

func (h *hits) post(t *testing.T, origin, ip, ua, body string) {
	t.Helper()
	req, _ := http.NewRequest("POST", h.url, strings.NewReader(body))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("user-agent", ua)
	req.Header.Set("x-forwarded-for", ip+", 10.0.0.1")
	if origin != "" {
		req.Header.Set("origin", origin)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 204 {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestVisitorCountedOnceADayEveryLoadAView(t *testing.T) {
	h := startHits(t)
	defer h.close()
	const site = "https://sliqtly.com"
	h.post(t, site, "1.1.1.1", UA, `{"p":"editor","r":"news.ycombinator.com"}`)
	h.post(t, site, "1.1.1.1", UA, `{"p":"view","r":"news.ycombinator.com"}`)
	h.post(t, "https://sliqtly.web.app", "2.2.2.2", PHONE, `{"p":"view"}`)
	h.post(t, site, "3.3.3.3", "curl/8", `{"p":"view"}`)
	// not from the site: answered alike, not counted
	h.post(t, "https://evil.example", "4.4.4.4", UA, `{"p":"editor"}`)
	h.post(t, "", "5.5.5.5", UA, `{"p":"editor"}`)
	eq(t, h.f.db.doc("stats/2026-10-03"), Doc{
		"views": 3, "visitors": 2,
		"pages":   Doc{"editor": 1, "view": 2},
		"devices": Doc{"desktop": 1, "mobile": 1},
		"refs":    Doc{"news.ycombinator.com": 1},
	})
	// nothing that names a visitor: the seen marks are hashes with an expiry
	seen := 0
	for k, d := range h.f.db.data {
		if strings.HasPrefix(k, "stats_seen/") {
			seen++
			if !regexp.MustCompile(`^stats_seen/[0-9a-f]{64}$`).MatchString(k) || len(d) != 1 || d["expires"] == nil {
				t.Fatalf("%s: %v", k, d)
			}
		}
	}
	eq(t, seen, 2)
	salt := h.f.db.doc("stats_salt/2026-10-03")
	exp, _ := salt["expires"].(string)
	if !strings.HasPrefix(exp, "2026-10-05T12:00:00") || len(salt["salt"].(string)) != 64 {
		t.Fatalf("salt %v", salt)
	}
}

func TestNewDayNewSaltSameVisitorCountsAgain(t *testing.T) {
	h := startHits(t)
	defer h.close()
	h.post(t, "https://sliqtly.com", "1.1.1.1", UA, `{"p":"editor"}`)
	h.clock = h.clock.Add(24 * time.Hour)
	h.post(t, "https://sliqtly.com", "1.1.1.1", UA, `{"p":"editor"}`)
	eq(t, h.f.db.doc("stats/2026-10-03")["visitors"], 1)
	eq(t, h.f.db.doc("stats/2026-10-04")["visitors"], 1)
	if h.f.db.doc("stats_salt/2026-10-03")["salt"] == h.f.db.doc("stats_salt/2026-10-04")["salt"] {
		t.Fatal("the same salt on two days")
	}
}

// a list's bullet is drawn muted on purpose, like its numbers
func TestBulletsAreNotJudgedAsText(t *testing.T) {
	for _, s := range []string{"•", "◦", "▪", "–", "→", "1.", "b)"} {
		if Contrast_static_readable(s) {
			t.Fatalf("%q judged as text", s)
		}
	}
	for _, s := range []string{"Long", "Ääni", "7", "€5"} {
		if !Contrast_static_readable(s) {
			t.Fatalf("%q not judged", s)
		}
	}
}
