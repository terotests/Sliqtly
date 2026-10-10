// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

const testForm = `---
form: Palaute
results: live
---

## Kuinka hyödyllinen? {#hyoty type=scale min=1 max=5 required}

## Aihe {#aihe type=choice required}
- Live-data
- Muu {other}

## Mitä muuta? {#muuta type=text visible="aihe = 'muu'"}
`

// a deck with forms/palaute.form.md on a server of one's own
func formServer(t *testing.T, form string) (base, deck string, ls *localServer) {
	t.Helper()
	srv, ls := startV1(t, "", nil)
	a := v1Do(t, "POST", srv.URL+"/api/shares", map[string]any{"name": "Kysely", "deck": "# Kysely"}).want(t, 201)
	deck, _ = a.body["id"].(string)
	req, _ := http.NewRequest("PUT", srv.URL+"/api/files/shares/"+deck+"/forms/palaute.form.md", strings.NewReader(form))
	req.Header.Set("Content-Type", "text/markdown")
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("put form: %v %v", err, res.Status)
	}
	res.Body.Close()
	return srv.URL, deck, ls
}

func newLink(t *testing.T, base, deck, kind string, count int) []map[string]any {
	t.Helper()
	a := v1Do(t, "POST", base+"/api/forms/links", map[string]any{"deck": deck, "file": "forms/palaute.form.md", "kind": kind, "count": count}).want(t, 200)
	raw, _ := a.body["links"].([]any)
	out := make([]map[string]any, len(raw))
	for i, x := range raw {
		out[i] = x.(map[string]any)
	}
	return out
}

type formClient struct {
	t *testing.T
	c *http.Client
}

func newFormClient(t *testing.T) formClient {
	jar, _ := cookiejar.New(nil)
	return formClient{t, &http.Client{Jar: jar}}
}

func (f formClient) get(u string) (int, string) {
	f.t.Helper()
	res, err := f.c.Get(u)
	if err != nil {
		f.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

var sealedField = regexp.MustCompile(`name="s" value="([^"]*)"`)
var shownField = regexp.MustCompile(`name="shown" value="([^"]*)"`)

// post the page's form with these fields (s and shown from the page)
func (f formClient) post(u, page string, fields url.Values) (int, string) {
	f.t.Helper()
	if m := sealedField.FindStringSubmatch(page); m != nil && fields.Get("s") == "" {
		fields.Set("s", m[1])
	}
	if m := shownField.FindStringSubmatch(page); m != nil && fields.Get("shown") == "" {
		fields.Set("shown", m[1])
	}
	req, _ := http.NewRequest("POST", u, strings.NewReader(fields.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", u[:strings.Index(u[8:], "/")+8])
	res, err := f.c.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func TestFormAnswerFlow(t *testing.T) {
	base, deck, _ := formServer(t, testForm)
	l := newLink(t, base, deck, "token", 1)[0]
	u := base + l["path"].(string)
	match(t, l["path"].(string), `^/r/[a-z2-7]{26}$`)
	c := newFormClient(t)
	code, page := c.get(u)
	eq(t, code, 200)
	match(t, page, `name="q1" value="5"`)
	match(t, page, `name="shown" value="q1,q2"`)

	// nothing ties the page to the deck, the file or the form
	for _, leak := range []string{deck, "palaute", "form.md", "hyoty", "aihe"} {
		if strings.Contains(page, leak) {
			t.Fatalf("the page names %q", leak)
		}
	}
	// two loads share no sealed value
	_, again := c.get(u)
	eq(t, sealedField.FindStringSubmatch(page)[1] != sealedField.FindStringSubmatch(again)[1], true)

	// a missing answer comes back marked
	code, body := c.post(u, page, url.Values{"q2": {"live-data"}})
	eq(t, code, 422)
	match(t, body, `aria-describedby="e-q1"`)
	match(t, body, `value="live-data" checked`)

	// "muu" brings in q3: the page asks it before saving
	code, body = c.post(u, page, url.Values{"q1": {"4"}, "q2": {"muu"}, "q2.other": {"Hinta"}})
	eq(t, code, 422)
	match(t, body, `name="q3"`)
	match(t, body, `more questions`)
	code, body = c.post(u, body, url.Values{"q1": {"4"}, "q2": {"muu"}, "q2.other": {"Hinta"}, "q3": {"=SUM(A1)"}})
	eq(t, code, 200)
	match(t, body, `Thank you`)

	// once: soft (the default) keeps this browser from answering again
	code, body = c.get(u)
	eq(t, code, 409)
	match(t, body, `answered this questionnaire already`)

	// the owner's results and responses
	res := v1Do(t, "GET", base+"/api/forms/results?deck="+deck+"&file=forms/palaute.form.md", nil).want(t, 200)
	eq(t, res.body["total"], 1.0)
	resp := v1Do(t, "GET", base+"/api/forms/responses?deck="+deck+"&file=forms/palaute.form.md&format=csv", nil).want(t, 200)
	match(t, resp.raw, `response,time,version,hyoty,aihe,aihe.other,muuta`)
	match(t, resp.raw, `,4,muu,Hinta,'=SUM\(A1\)`)
}

func TestFormLinksAndRefusals(t *testing.T) {
	base, deck, _ := formServer(t, testForm)
	c := newFormClient(t)

	// a short code reads either case
	code := newLink(t, base, deck, "code", 1)[0]["path"].(string)
	match(t, code, `^/c/[A-HJKMNP-Z2-9]{8}$`)
	st, _ := c.get(base + strings.ToLower(code))
	eq(t, st, 200)
	// a code is not a token's address and the other way round
	tok := newLink(t, base, deck, "token", 1)[0]["path"].(string)
	st, _ = c.get(base + "/r/" + strings.ToLower(strings.Repeat("A", 26)))
	eq(t, st, 404)
	st, _ = c.get(base + "/t/" + tok[3:])
	eq(t, st, 404)

	// person links take one response each
	people := newLink(t, base, deck, "person", 3)
	eq(t, len(people), 3)
	p1 := base + people[0]["path"].(string)
	other := newFormClient(t)
	_, page := other.get(p1)
	st, _ = other.post(p1, page, url.Values{"q1": {"3"}, "q2": {"live-data"}})
	eq(t, st, 200)
	fresh := newFormClient(t)
	st, body := fresh.get(p1)
	eq(t, st, 409)
	match(t, body, `This link has been used`)

	// a revoked link is closed
	links := v1Do(t, "GET", base+"/api/forms/links?deck="+deck+"&file=forms/palaute.form.md", nil).want(t, 200)
	eq(t, len(links.body["links"].([]any)), 5)
	id := people[1]["id"].(string)
	v1Do(t, "POST", base+"/api/forms/revoke", map[string]any{"deck": deck, "id": id}).want(t, 200)
	st, _ = newFormClient(t).get(base + people[1]["path"].(string))
	eq(t, st, 410)

	// a page this server did not seal comes back, answers kept
	p2 := base + people[2]["path"].(string)
	st, body = newFormClient(t).post(p2, "", url.Values{"s": {"forged"}, "shown": {"q1,q2"}, "q1": {"2"}, "q2": {"live-data"}})
	eq(t, st, 422)
	match(t, body, `page was open for long`)
	match(t, body, `value="2" checked`)

	// a field by the form's own id is refused
	_, page = newFormClient(t).get(p2)
	st, _ = newFormClient(t).post(p2, page, url.Values{"q1": {"2"}, "q2": {"live-data"}, "hyoty": {"5"}})
	eq(t, st, 422)

	// the owner's API checks its input
	v1Do(t, "POST", base+"/api/forms/links", map[string]any{"deck": deck, "file": "../x.form.md", "kind": "token"}).want(t, 400)
	v1Do(t, "POST", base+"/api/forms/links", map[string]any{"deck": deck, "file": "forms/palaute.form.md", "kind": "admin"}).want(t, 400)
	v1Do(t, "POST", base+"/api/forms/links", map[string]any{"deck": deck, "file": "forms/none.form.md", "kind": "token"}).want(t, 404)
}

func TestFormResultsLink(t *testing.T) {
	base, deck, _ := formServer(t, testForm)
	tok := base + newLink(t, base, deck, "token", 1)[0]["path"].(string)
	results := base + newLink(t, base, deck, "results", 1)[0]["path"].(string)
	match(t, results, `/t/[a-z2-7]{26}$`)
	for i, v := range []string{"5", "5", "4", "5", "3"} {
		c := newFormClient(t)
		_, page := c.get(tok)
		st, body := c.post(tok, page, url.Values{"q1": {v}, "q2": {"live-data"}})
		if st != 200 {
			t.Fatalf("answer %d: %d %s", i, st, body)
		}
		a := v1Do(t, "GET", results, nil).want(t, 200)
		qs := a.body["questions"].([]any)
		rows := qs[0].(map[string]any)["rows"].([]any)
		// below five answers a viewer sees no counts
		eq(t, len(rows) > 0, i == 4)
	}
	a := v1Do(t, "GET", results, nil).want(t, 200)
	eq(t, a.header.Get("Access-Control-Allow-Origin"), "*")
	match(t, a.raw, `"value":"5","label":"5","count":3`)
	if strings.Contains(a.raw, deck) {
		t.Fatal("the results name the deck")
	}

	// results: owner keeps them from viewers
	base2, deck2, _ := formServer(t, strings.Replace(testForm, "results: live", "results: owner", 1))
	r2 := base2 + newLink(t, base2, deck2, "results", 1)[0]["path"].(string)
	v1Do(t, "GET", r2, nil).want(t, 403)
}

func TestFormLimitAndClose(t *testing.T) {
	base, deck, ls := formServer(t, strings.Replace(testForm, "results: live", "results: live\nlimit: 1\nonce: none", 1))
	tok := base + newLink(t, base, deck, "token", 1)[0]["path"].(string)
	c := newFormClient(t)
	_, page := c.get(tok)
	st, _ := c.post(tok, page, url.Values{"q1": {"5"}, "q2": {"live-data"}})
	eq(t, st, 200)
	_, page = c.get(tok)
	st, body := c.post(tok, page, url.Values{"q1": {"5"}, "q2": {"live-data"}})
	eq(t, st, 409)
	match(t, body, `all the answers it takes`)

	// a deck removed takes its questionnaires' rows with it
	v1Do(t, "DELETE", base+"/api/shares/"+deck, nil).want(t, 204)
	st, _ = newFormClient(t).get(tok)
	eq(t, st, 404)
	rows, _ := ls.env.Forms.Responses(t.Context(), deck, "forms/palaute.form.md")
	eq(t, len(rows), 0)

	// closes in the past
	base2, deck2, _ := formServer(t, strings.Replace(testForm, "results: live", "closes: 2020-01-01", 1))
	st, body = newFormClient(t).get(base2 + newLink(t, base2, deck2, "token", 1)[0]["path"].(string))
	eq(t, st, 403)
	match(t, body, `closed`)
}

// what the form file says reaches the page whole in Go too: a description
// after " — ", ä and ö
func TestFormPageText(t *testing.T) {
	base, deck, _ := formServer(t, strings.Replace(testForm, "- Live-data", "- Live-data — kaaviot taulukosta", 1))
	_, page := newFormClient(t).get(base + newLink(t, base, deck, "token", 1)[0]["path"].(string))
	match(t, page, `Live-data<small>kaaviot taulukosta</small>`)
	match(t, page, `Kuinka hyödyllinen\?`)
}
