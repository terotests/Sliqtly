// SPDX-License-Identifier: AGPL-3.0-or-later

// The pages and calls of sliqtly.com's GitHub repositories (cloudgit.go).
//
//	GET  /editor/github/login?mode=in|link|create&name=&next=&nonce=
//	     to GitHub's sign-in: in signs in with GitHub (beside Google's),
//	     link connects GitHub to the signed-in account, create also makes a
//	     private repository
//	GET  /editor/github/callback   GitHub comes back here (the App's callback URL)
//	GET  /editor/github/setup      after installing the App (its setup URL)
//	POST /editor/github/hook       GitHub's push webhook (signed with the secret)
//	POST /editor/github/sweep      a sync of every account (X-Sliqtly-Key)
//	GET  /editor/git               the page: GitHub, the repository, conflicts
//
//	GET  /editor/api/git                    the account's state
//	POST /editor/api/git/connect            {installation, repo, public?}: the repository to keep the decks in (public: one public on GitHub may be)
//	POST /editor/api/git/save               {deck?, message}: a commit now (Save version)
//	POST /editor/api/git/sync               {}: the same without a message (quiet, Back up now)
//	POST /editor/api/git/resolve            {deck, take: github|sliqtly}
//	POST /editor/api/git/public             the repository may be public (it is on GitHub)
//	POST /editor/api/git/disconnect         no repository any more (GitHub stays signed in)
//	GET  /editor/api/history/{deck}[/…]     the deck's commits (cloudgithistory.go)
//
// GitHub's sign-in comes back to this server, not to the page: the state
// it carries is signed (HMAC with the App's secret) and says what was asked,
// for whom and until when. Signing in with GitHub also needs the nonce the
// login page kept for itself (sessionStorage): a sign-in started elsewhere
// is not finished in this browser.

package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

//go:embed assets/editor-git.html
var editorGitHTML string

const (
	githubPagePath = editorPath + "/github/"
	gitPagePath    = editorPath + "/git"
	// how long a sign-in on GitHub may take
	githubStateLife = 15 * time.Minute
)

var gitRepoName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)

// --- the signed state

type githubState struct {
	Mode  string `json:"m"`           // in, link, create
	UID   string `json:"u,omitempty"` // link and create: the account asking
	Name  string `json:"r,omitempty"` // create: the repository's name
	Next  string `json:"n,omitempty"` // in: the page to go to
	Nonce string `json:"o,omitempty"` // in: sha256 of the login page's nonce
	Until int64  `json:"t"`
}

func (c *cloudGit) sign(s githubState) string {
	b, _ := json.Marshal(s)
	body := base64.RawURLEncoding.EncodeToString(b)
	mac := hmac.New(sha256.New, []byte("sliqtly github state\x00"+c.app.ClientSecret))
	mac.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (c *cloudGit) unsign(v string) (githubState, error) {
	var s githubState
	body, sig, ok := strings.Cut(v, ".")
	if !ok {
		return s, errors.New("bad state")
	}
	mac := hmac.New(sha256.New, []byte("sliqtly github state\x00"+c.app.ClientSecret))
	mac.Write([]byte(body))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(want)) {
		return s, errors.New("bad state")
	}
	b, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil || json.Unmarshal(b, &s) != nil {
		return s, errors.New("bad state")
	}
	if c.now().UnixMilli() > s.Until {
		return s, errors.New("the sign-in took too long; start it again")
	}
	return s, nil
}

func (c *cloudGit) callback() string { return c.base + githubPagePath + "callback" }

// a page of this site to go to after signing in (never another site's)
func safeNext(p string) string {
	if !strings.HasPrefix(p, editorPath+"/") && p != editorPath || strings.HasPrefix(p, "//") || strings.ContainsAny(p, "\\\r\n") {
		return editorPath + "/"
	}
	return p
}

// --- the pages

// serveGitHub answers /editor/github/… and /editor/git; false: not one of them
func serveGitHub(env *Env, w http.ResponseWriter, r *http.Request) bool {
	p := r.URL.Path
	if p != gitPagePath && p != topicPagePath && !strings.HasPrefix(p, githubPagePath) {
		return false
	}
	c, g := env.CloudGit, env.EditorGate
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	if c == nil || g == nil {
		http.Error(w, "GitHub is not set up on this server.", http.StatusNotFound)
		return true
	}
	if p == topicPagePath {
		serveTopicPage(env, w, r)
		return true
	}
	switch strings.TrimPrefix(p, githubPagePath) {
	case gitPagePath:
		if g.user(r) == nil {
			http.Redirect(w, r, editorPath+"/", http.StatusFound)
			return true
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'")
		io.WriteString(w, editorGitHTML)
	case "login":
		c.login(g, w, r)
	case "callback":
		c.returned(g, w, r)
	case "setup":
		// after installing the App: its repositories are listed again
		http.Redirect(w, r, githubPagePath+"login?mode=link", http.StatusFound)
	case "hook":
		c.hook(w, r)
	case "sweep":
		c.serveSweep(w, r)
	default:
		http.NotFound(w, r)
	}
	return true
}

func (c *cloudGit) login(g *editorGate, w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	s := githubState{Mode: q.Get("mode"), Until: c.now().Add(githubStateLife).UnixMilli()}
	switch s.Mode {
	case "in":
		if c.mint == nil {
			http.Error(w, "Signing in with GitHub is not set up on this server.", http.StatusNotFound)
			return
		}
		n := q.Get("nonce")
		if len(n) < 16 || len(n) > 128 {
			http.Error(w, "Start from the sign-in page.", http.StatusBadRequest)
			return
		}
		sum := sha256.Sum256([]byte(n))
		s.Nonce, s.Next = hex.EncodeToString(sum[:]), safeNext(q.Get("next"))
	case "link", "create":
		t := g.user(r)
		if t == nil {
			http.Redirect(w, r, editorPath+"/", http.StatusFound)
			return
		}
		s.UID = t.UID
		if s.Mode == "create" {
			s.Name = strings.TrimSpace(q.Get("name"))
			if !gitRepoName.MatchString(s.Name) || strings.HasPrefix(s.Name, ".") {
				http.Error(w, "A repository's name is letters, digits, - _ and . (at most 100).", http.StatusBadRequest)
				return
			}
		}
	default:
		http.Error(w, "mode is in, link or create", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, c.app.authorizeURL(c.callback(), c.sign(s)), http.StatusFound)
}

// returned: GitHub's sign-in came back with a code
func (c *cloudGit) returned(g *editorGate, w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		c.page(w, "GitHub", "GitHub did not sign you in: "+nonEmpty(q.Get("error_description"), e), gitPagePath)
		return
	}
	if q.Get("state") == "" {
		// GitHub sends the installation back without a state of ours when
		// the App asks for sign-in during installation
		http.Redirect(w, r, githubPagePath+"login?mode=link", http.StatusFound)
		return
	}
	s, err := c.unsign(q.Get("state"))
	if err != nil {
		c.page(w, "GitHub", err.Error(), gitPagePath)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	token, err := c.app.userToken(ctx, q.Get("code"), c.callback())
	if err != nil {
		c.page(w, "GitHub", err.Error(), gitPagePath)
		return
	}
	u, err := c.app.user(ctx, token)
	if err != nil {
		c.page(w, "GitHub", err.Error(), gitPagePath)
		return
	}
	switch s.Mode {
	case "in":
		uid, err := c.accountOf(ctx, u)
		if err != nil {
			c.page(w, "GitHub", err.Error(), editorPath+"/")
			return
		}
		ct, err := c.mint(ctx, uid, u)
		if err != nil {
			c.logf("git: sign-in of %s: %v", u.Login, err)
			c.page(w, "GitHub", "Signing in with GitHub did not go through; try again in a moment.", editorPath+"/")
			return
		}
		if err := c.noteGitHub(ctx, uid, u, token); err != nil {
			c.logf("git: %s: GitHub not noted: %v", uid, err)
		}
		c.signInPage(w, ct, s.Nonce, s.Next)
	case "link", "create":
		t := g.user(r)
		if t == nil || t.UID != s.UID {
			c.page(w, "GitHub", "You are signed in as someone else now. Open File → Git and GitHub again.", editorPath+"/")
			return
		}
		if err := c.linkUser(ctx, t.UID, u); err != nil {
			c.page(w, "GitHub", err.Error(), gitPagePath)
			return
		}
		if err := c.noteGitHub(ctx, t.UID, u, token); err != nil {
			c.page(w, "GitHub", err.Error(), gitPagePath)
			return
		}
		if s.Mode == "create" {
			to, err := c.makeRepo(ctx, t.UID, token, s.Name)
			if err != nil {
				c.page(w, "GitHub", err.Error(), gitPagePath)
				return
			}
			if to != "" {
				http.Redirect(w, r, to, http.StatusFound)
				return
			}
		}
		http.Redirect(w, r, gitPagePath, http.StatusFound)
	}
}

// accountOf: the Sliqtly account a GitHub account signs in. One connected
// to an account (Google's) signs that one in; else its own, "gh-<id>".
func (c *cloudGit) accountOf(ctx context.Context, u ghUser) (string, error) {
	d, err := c.db.Get(ctx, gitUserCol, strconv.FormatInt(u.ID, 10))
	if err != nil {
		return "", err
	}
	if uid, _ := d["uid"].(string); uid != "" {
		return uid, nil
	}
	uid := "gh-" + strconv.FormatInt(u.ID, 10)
	if _, err := c.db.Create(ctx, gitUserCol, strconv.FormatInt(u.ID, 10), Doc{"uid": uid, "login": u.Login, "created": c.db.ServerTime()}); err != nil {
		return "", err
	}
	return uid, nil
}

// linkUser connects the GitHub account to the signed-in account: refused
// when it signs in another account already
func (c *cloudGit) linkUser(ctx context.Context, uid string, u ghUser) error {
	key := strconv.FormatInt(u.ID, 10)
	had, err := c.db.Create(ctx, gitUserCol, key, Doc{"uid": uid, "login": u.Login, "created": c.db.ServerTime()})
	if err != nil {
		return err
	}
	if had != nil {
		if other, _ := had["uid"].(string); other != uid {
			return fmt.Errorf("The GitHub account %s is already connected to another Sliqtly account. Sign in with GitHub to use that one, or disconnect it there first.", u.Login)
		}
	}
	// an account connected to another GitHub before: that one no longer signs it in
	l, _, err := c.link(ctx, uid)
	if err == nil && l.GitHubID != 0 && l.GitHubID != u.ID {
		if d, err := c.db.Get(ctx, gitUserCol, strconv.FormatInt(l.GitHubID, 10)); err == nil && d != nil && d["uid"] == uid {
			c.db.Delete(ctx, gitUserCol, strconv.FormatInt(l.GitHubID, 10))
		}
	}
	return nil
}

// noteGitHub keeps who the account is on GitHub and the repositories it
// may pick (the user's token is not kept)
func (c *cloudGit) noteGitHub(ctx context.Context, uid string, u ghUser, token string) error {
	places, err := c.app.places(ctx, token)
	if err != nil {
		return err
	}
	choices := []any{}
	for _, p := range places {
		for _, rp := range p.Repos {
			choices = append(choices, map[string]any{"installation": p.Installation, "account": p.Account, "repo": rp.ID, "name": rp.FullName, "private": rp.Private})
			if len(choices) >= 300 {
				break
			}
		}
	}
	d := Doc{"github": map[string]any{"id": u.ID, "login": u.Login, "name": u.Name}, "choices": choices, "installations": placesDoc(places), "seen": c.db.ServerTime()}
	had, err := c.db.Create(ctx, gitLinkCol, uid, mergeDoc(d, "created", c.db.ServerTime()))
	if err != nil || had == nil {
		return err
	}
	if err := c.db.Update(ctx, gitLinkCol, uid, d); err != nil {
		return err
	}
	// a repository being made and now in the App's reach is connected
	l, _, err := c.link(ctx, uid)
	if err != nil || l.Want == "" || l.connected() {
		return err
	}
	for _, p := range places {
		for _, rp := range p.Repos {
			if strings.EqualFold(rp.FullName, l.Want) {
				return c.connect(ctx, uid, p.Installation, rp.ID, false)
			}
		}
	}
	return nil
}

func placesDoc(ps []ghPlace) []any {
	out := []any{}
	for _, p := range ps {
		out = append(out, map[string]any{"installation": p.Installation, "account": p.Account, "all": p.All})
	}
	return out
}

// makeRepo makes a private repository of the user's own and lets the App
// write it → where to go next ("" the page; GitHub's when the App must
// be installed on the repository there)
func (c *cloudGit) makeRepo(ctx context.Context, uid, token, name string) (string, error) {
	u, err := c.app.user(ctx, token)
	if err != nil {
		return "", err
	}
	repo, err := c.app.createRepo(ctx, token, name)
	if err != nil {
		if ghStatus(err) == 422 {
			return "", fmt.Errorf("You have a repository called %s already. Pick it from the list, or give another name.", name)
		}
		return "", fmt.Errorf("GitHub did not make the repository (%v). Make a private one at github.com/new, then pick it here.", err)
	}
	c.db.Update(ctx, gitLinkCol, uid, Doc{"want": repo.FullName})
	places, err := c.app.places(ctx, token)
	if err != nil {
		return "", err
	}
	for _, p := range places {
		if !strings.EqualFold(p.Account, u.Login) {
			continue
		}
		if !p.All {
			if err := c.app.addToInstallation(ctx, token, p.Installation, repo.ID); err != nil {
				c.logf("git: %s: %s not added to installation %d: %v", uid, repo.FullName, p.Installation, err)
				return c.app.installURL(""), nil
			}
		}
		return "", c.connect(ctx, uid, p.Installation, repo.ID, false)
	}
	// the App is not installed on the account yet: GitHub asks which repositories
	return c.app.installURL(""), nil
}

// connect: the repository the account's decks go to, one it was offered;
// the first sync follows
func (c *cloudGit) connect(ctx context.Context, uid string, installation, repoID int64, public bool) error {
	l, _, err := c.link(ctx, uid)
	if err != nil {
		return err
	}
	name := ""
	for _, x := range l.Choices {
		ch, _ := x.(map[string]any)
		if docInt(ch["installation"]) == installation && docInt(ch["repo"]) == repoID {
			name, _ = ch["name"].(string)
		}
	}
	if name == "" && strings.Contains(l.Want, "/") {
		name = l.Want
	}
	if name == "" {
		return errors.New("That repository is not one Sliqtly's GitHub App may write. Add it to the App on GitHub first.")
	}
	r, err := c.app.at(ctx, installation, repoID, name)
	if err != nil {
		return gitAccessErr(err, &gitLink{Repo: name})
	}
	info, err := r.info(ctx)
	if err != nil {
		return gitAccessErr(err, &gitLink{Repo: name})
	}
	if !info.Private && !public {
		return roomErr{fmt.Sprintf("%s is public: every presentation, picture and old version in it would be readable by anyone. Say that it may be, or pick a private one.", info.FullName)}
	}
	if l.connected() && l.RepoID == repoID {
		return c.db.Update(ctx, gitLinkCol, uid, Doc{"public": public})
	}
	// another repository than before: compared from its own start
	return c.db.Update(ctx, gitLinkCol, uid, Doc{"installation": installation, "repoId": repoID, "repo": info.FullName,
		"branch": nonEmpty(info.DefaultBranch, "main"), "head": "", "fileKeys": Doc{}, "decks": []any{}, "conflicts": Doc{},
		"enabled": true, "want": "", "error": "", "public": public, "connected": c.db.ServerTime()})
}

// a small page that says what happened and links on
func (c *cloudGit) page(w http.ResponseWriter, title, text, next string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>%s</title>
<style>body{font:16px/1.5 system-ui,sans-serif;max-width:520px;margin:12vh auto;padding:0 16px;color:#1d2233;background:#f6f7fb}@media(prefers-color-scheme:dark){body{color:#f4f4f5;background:#111113}a{color:#c8ff3c}}</style></head>
<body><h1>%s</h1><p>%s</p><p><a href="%s">Continue</a></p></body></html>`, html.EscapeString(title), html.EscapeString(title), html.EscapeString(text), html.EscapeString(next))
}

// signInPage finishes a sign-in with GitHub in the browser: the custom
// token signs Firebase in, then the editor's session is made as after
// Google's sign-in
func (c *cloudGit) signInPage(w http.ResponseWriter, token, nonce, next string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Referrer-Policy", "no-referrer")
	js, _ := json.Marshal(map[string]string{"token": token, "nonce": nonce, "next": next})
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><meta name="robots" content="noindex"><title>Sliqtly</title>
<style>body{font:16px/1.5 system-ui,sans-serif;max-width:520px;margin:12vh auto;padding:0 16px;color:#1d2233;background:#f6f7fb}@media(prefers-color-scheme:dark){body{color:#f4f4f5;background:#111113}}</style></head>
<body><p id="m">Signing in…</p><script>
(async () => {
  const s = %s;
  const say = (t) => { document.getElementById("m").textContent = t; };
  try {
    const n = sessionStorage.getItem("sliqtly:github-nonce") || "";
    sessionStorage.removeItem("sliqtly:github-nonce");
    const d = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(n));
    const hex = [...new Uint8Array(d)].map((b) => b.toString(16).padStart(2, "0")).join("");
    if (!n || hex !== s.nonce) { say("This sign-in was not started in this browser. Start it again from the sign-in page."); return; }
    const load = (src) => new Promise((ok, no) => { const e = document.createElement("script"); e.src = src; e.onload = ok; e.onerror = () => no(new Error("could not load " + src)); document.head.append(e); });
    const SDK = "https://www.gstatic.com/firebasejs/10.14.1/";
    await load(SDK + "firebase-app-compat.js");
    await load(SDK + "firebase-auth-compat.js");
    const cfg = await (await fetch("/__/firebase/init.json")).json();
    firebase.initializeApp({ ...cfg, authDomain: location.host });
    const cred = await firebase.auth().signInWithCustomToken(s.token);
    const idToken = await cred.user.getIdToken(true);
    const res = await fetch("/editor/api/session", { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ idToken }) });
    if (!res.ok) { say((await res.json().catch(() => ({}))).error || "Sign-in did not go through."); return; }
    location.replace(s.next);
  } catch (e) { say("Sign-in did not go through: " + (e && e.message || e)); }
})();
</script></body></html>`, strings.ReplaceAll(string(js), "</", "<\\/"))
}

// --- GitHub's webhook and the sweep

func (c *cloudGit) hook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || c.hookSecret == "" {
		http.Error(w, "no", http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 25<<20))
	if err != nil {
		http.Error(w, "bad body", 400)
		return
	}
	mac := hmac.New(sha256.New, []byte(c.hookSecret))
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(r.Header.Get("X-Hub-Signature-256")), []byte(want)) {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	switch r.Header.Get("X-GitHub-Event") {
	case "push":
	case "installation", "installation_repositories":
		c.uninstalled(r.Context(), body)
		w.WriteHeader(204)
		return
	default:
		w.WriteHeader(204)
		return
	}
	var ev struct {
		Ref        string `json:"ref"`
		After      string `json:"after"`
		Repository struct {
			ID int64 `json:"id"`
		} `json:"repository"`
	}
	if json.Unmarshal(body, &ev) != nil || ev.Repository.ID == 0 {
		http.Error(w, "bad event", 400)
		return
	}
	docs, ids, err := c.db.WhereEq(r.Context(), gitLinkCol, "repoId", ev.Repository.ID)
	if err != nil {
		http.Error(w, "try again", 503)
		return
	}
	// GitHub waits ten seconds for the answer; what is not done by then
	// is done by the next sync
	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Second)
	defer cancel()
	for i, uid := range ids {
		head, _ := docs[i]["head"].(string)
		branch, _ := docs[i]["branch"].(string)
		if ev.Ref != "refs/heads/"+nonEmpty(branch, "main") || ev.After == head || strings.Trim(ev.After, "0") == "" {
			continue // another branch, Sliqtly's own commit, or the branch removed
		}
		if _, err := c.sync(ctx, uid, "", ""); err != nil && !errors.Is(err, errGitBusy) {
			c.logf("git: %s: push from GitHub: %v", uid, err)
		}
	}
	// the topics kept in the repository (cloudtopicgit.go) follow it too
	c.topicPushed(ctx, ev.Repository.ID, ev.Ref, ev.After)
	w.WriteHeader(204)
}

// the App uninstalled, or a repository taken out of it: those accounts say so
func (c *cloudGit) uninstalled(ctx context.Context, body []byte) {
	var ev struct {
		Action       string `json:"action"`
		Installation struct {
			ID int64 `json:"id"`
		} `json:"installation"`
		Removed []struct {
			ID int64 `json:"id"`
		} `json:"repositories_removed"`
	}
	if json.Unmarshal(body, &ev) != nil {
		return
	}
	gone := map[int64]bool{}
	for _, r := range ev.Removed {
		gone[r.ID] = true
	}
	if ev.Action != "deleted" && ev.Action != "suspend" && len(gone) == 0 {
		return
	}
	docs, ids, err := c.db.WhereEq(ctx, gitLinkCol, "installation", ev.Installation.ID)
	if err != nil {
		return
	}
	for i, uid := range ids {
		if ev.Action == "deleted" || ev.Action == "suspend" || gone[docInt(docs[i]["repoId"])] {
			c.db.Update(ctx, gitLinkCol, uid, Doc{"error": "Sliqtly's GitHub App can no longer write the repository (uninstalled or the repository taken out). Connect it again.", "enabled": false})
		}
	}
}

func (c *cloudGit) serveSweep(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || c.sweepKey == "" || !hmac.Equal([]byte(r.Header.Get("X-Sliqtly-Key")), []byte(c.sweepKey)) {
		http.Error(w, "no", http.StatusNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 50*time.Minute)
	defer cancel()
	n, err := c.sweep(ctx)
	if err != nil {
		editorJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	editorJSON(w, 200, map[string]int{"synced": n})
}

// --- the editor's calls

func serveGitAPI(env *Env, g *editorGate, w http.ResponseWriter, r *http.Request, op string) {
	c := env.CloudGit
	if c == nil {
		editorJSON(w, 404, map[string]any{"error": "GitHub is not set up on this server.", "enabled": false})
		return
	}
	t := g.user(r)
	if t == nil {
		editorJSON(w, 401, map[string]string{"error": "Sign in again.", "code": "signed-out"})
		return
	}
	if r.Method == http.MethodPost && !sameSite(env, r) {
		editorJSON(w, 403, map[string]string{"error": "Not allowed."})
		return
	}
	if op == "topic" || strings.HasPrefix(op, "topic/") {
		c.serveTopicAPI(t, w, r, strings.TrimPrefix(strings.TrimPrefix(op, "topic"), "/"))
		return
	}
	var in struct {
		Installation int64  `json:"installation"`
		Repo         int64  `json:"repo"`
		Deck         string `json:"deck"`
		Message      string `json:"message"`
		Take         string `json:"take"`
		Public       bool   `json:"public"`
	}
	if r.Method == http.MethodPost {
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
			editorJSON(w, 400, map[string]string{"error": "bad body"})
			return
		}
	}
	ctx := r.Context()
	fail := func(err error) {
		code := 500
		var stop errGitStop
		switch {
		case errors.Is(err, errGitNotConnected):
			code = 409
		case errors.Is(err, errGitBusy):
			code = 429
		case errors.As(err, &stop):
			code = 409
		}
		editorJSON(w, code, map[string]string{"error": err.Error()})
	}
	switch op {
	case "":
		if r.Method != http.MethodGet {
			editorJSON(w, 405, map[string]string{"error": "GET only"})
			return
		}
		st, err := c.status(ctx, t.UID)
		if err != nil {
			fail(err)
			return
		}
		editorJSON(w, 200, st)
	case "connect":
		if err := c.connect(ctx, t.UID, in.Installation, in.Repo, in.Public); err != nil {
			editorJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		res, err := c.sync(ctx, t.UID, "Sliqtly: decks from sliqtly.com", t.Name)
		if err != nil {
			fail(err)
			return
		}
		editorJSON(w, 200, res)
	case "save", "sync":
		if r.Method != http.MethodPost {
			editorJSON(w, 405, map[string]string{"error": "POST only"})
			return
		}
		// an invited editor's Save version goes to the owner's repository
		uid := t.UID
		if in.Deck != "" {
			d, err := c.db.Get(ctx, "shares", in.Deck)
			if err != nil || d == nil {
				editorJSON(w, 404, map[string]string{"error": "no such presentation"})
				return
			}
			owner, _ := d["owner"].(string)
			if owner != t.UID && !invitedTo(d, t) {
				editorJSON(w, 403, map[string]string{"error": "not yours"})
				return
			}
			uid = owner
		}
		msg := ""
		if op == "save" {
			msg = strings.TrimSpace(in.Message)
			if msg == "" {
				msg = "Save version"
			}
			if len(msg) > 2000 {
				msg = msg[:2000]
			}
		}
		// the account's repository, and the linked topic the deck is in
		l, _, err := c.link(ctx, uid)
		if err != nil {
			fail(err)
			return
		}
		var res *gitSyncResult
		if l.connected() {
			if res, err = c.sync(ctx, uid, msg, t.Name); err != nil {
				fail(err)
				return
			}
		}
		var tres *topicSyncResult
		if in.Deck != "" {
			if tres, err = c.syncDeckTopic(ctx, uid, in.Deck, msg, t.Name); err != nil && res == nil {
				fail(err)
				return
			}
		}
		switch {
		case res != nil:
			editorJSON(w, 200, res)
		case tres != nil:
			editorJSON(w, 200, tres)
		default:
			fail(errGitNotConnected)
		}
	case "resolve":
		if err := c.resolve(ctx, t.UID, in.Deck, in.Take); err != nil {
			editorJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		editorJSON(w, 200, map[string]bool{"ok": true})
	case "public":
		// the repository, public on GitHub, may be: the syncs go on
		if err := c.db.Update(ctx, gitLinkCol, t.UID, Doc{"public": true, "error": ""}); err != nil {
			fail(err)
			return
		}
		editorJSON(w, 200, map[string]bool{"ok": true})
	case "disconnect":
		if err := c.db.Update(ctx, gitLinkCol, t.UID, Doc{"installation": int64(0), "repoId": int64(0), "repo": "", "head": "", "enabled": false, "fileKeys": Doc{}, "decks": []any{}, "conflicts": Doc{}}); err != nil {
			fail(err)
			return
		}
		editorJSON(w, 200, map[string]bool{"ok": true})
	default:
		editorJSON(w, 404, map[string]string{"error": "no such call"})
	}
}

// status: what the page and the editor show
func (c *cloudGit) status(ctx context.Context, uid string) (map[string]any, error) {
	l, d, err := c.link(ctx, uid)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"enabled": l.connected(), "app": c.app.Slug, "install": c.app.installURL(""), "signIn": c.mint != nil}
	if l.Login != "" {
		out["github"] = map[string]any{"login": l.Login}
	}
	if l.connected() {
		out["repo"] = l.Repo
		out["url"] = "https://github.com/" + l.Repo
		out["branch"] = nonEmpty(l.Branch, "main")
		out["head"] = l.Head
		if !l.Synced.IsZero() {
			out["synced"] = l.Synced.UnixMilli()
		}
		out["public"] = l.Public
	}
	if l.Error != "" {
		out["error"] = l.Error
	}
	choices := l.Choices
	if choices == nil {
		choices = []any{}
	}
	out["choices"] = choices
	if d != nil {
		if v := asList(d["installations"]); v != nil {
			out["installations"] = v
		}
	}
	conflicts := []any{}
	for id, v := range l.Conflicts {
		cf, _ := v.(map[string]any)
		name := id
		if s, err := c.db.Get(ctx, "shares", id); err == nil && s != nil {
			name, _ = s["name"].(string)
		}
		conflicts = append(conflicts, map[string]any{"deck": id, "name": nonEmpty(name, id), "sha": cf["sha"], "at": millis(cf["at"])})
	}
	out["conflicts"] = conflicts
	topics, err := c.topicsOf(ctx, uid)
	if err != nil {
		return nil, err
	}
	out["topics"] = topics
	return out, nil
}

// githubLoginButton: the login page's GitHub button shown (the page hides it)
func githubLoginOn(html string, on bool) string {
	if !on {
		return html
	}
	return strings.Replace(html, `<button class="secondary" id="github" hidden>`, `<button class="secondary" id="github">`, 1)
}

// cloudGitFromEnv: the GitHub App sliqtly.com's environment names
// (docs/github-app.md); nil, nil when it names none
func cloudGitFromEnv(e *Env) (*cloudGit, error) {
	id := os.Getenv("SLIQTLY_GITHUB_APP_ID")
	if id == "" {
		return nil, nil
	}
	app, err := newGithubApp(id, os.Getenv("SLIQTLY_GITHUB_APP_CLIENT_ID"), os.Getenv("SLIQTLY_GITHUB_APP_CLIENT_SECRET"), os.Getenv("SLIQTLY_GITHUB_APP_SLUG"), os.Getenv("SLIQTLY_GITHUB_APP_KEY"))
	if err != nil {
		return nil, err
	}
	c := newCloudGit(app, e.DB, e.Bucket, e.BaseURL)
	c.hookSecret = os.Getenv("SLIQTLY_GITHUB_HOOK_SECRET")
	c.sweepKey = os.Getenv("SLIQTLY_GIT_SWEEP_KEY")
	if os.Getenv("SLIQTLY_GITHUB_SIGNIN") != "off" {
		c.mint = e.githubMint
	}
	return c, nil
}
