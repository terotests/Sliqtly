// SPDX-License-Identifier: AGPL-3.0-or-later

// Sliqtly's GitHub App on sliqtly.com (cloudgit.go): signing in with
// GitHub, and the one repository of each account its decks are kept in.
//
// The App is one registration on GitHub (docs/github-app.md). With it the
// server
//
//   - signs a person in: GitHub's user authorization (the web flow) gives a
//     user token for a few minutes of this server's time, used for who they
//     are, their installations and, when asked, a new repository. It is not
//     kept.
//   - writes the repository: an installation token (an hour), made from the
//     App's private key, for that one repository only: Contents read and
//     write, Metadata read.
//
// The Git Data API (blobs, trees, commits, refs) makes the commits: Cloud
// Run keeps no repository on disk, and only what changed is sent.

package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// githubApp is the App's registration and a client for GitHub's API.
type githubApp struct {
	ID           string // the App's id (iss of its JWT)
	ClientID     string
	ClientSecret string
	Slug         string // github.com/apps/<slug>
	key          *rsa.PrivateKey
	// api.github.com and github.com; the tests' server otherwise
	API, Web string
	client   *http.Client
	now      func() time.Time

	mu     sync.Mutex
	tokens map[string]appToken // installation/repository → token
}

type appToken struct {
	token string
	until time.Time
}

// newGithubApp: the App from its settings; the key is the .pem GitHub gave
// (PKCS#1 or PKCS#8)
func newGithubApp(id, clientID, clientSecret, slug, keyPEM string) (*githubApp, error) {
	if id == "" || clientID == "" || clientSecret == "" || slug == "" || keyPEM == "" {
		return nil, errors.New("the GitHub App needs its id, client id, client secret, slug and private key")
	}
	blk, _ := pem.Decode([]byte(keyPEM))
	if blk == nil {
		// one line in an environment variable: the .pem file base64-encoded
		if b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(keyPEM)); err == nil {
			blk, _ = pem.Decode(b)
		}
	}
	if blk == nil {
		return nil, errors.New("the GitHub App's private key is not PEM")
	}
	var key *rsa.PrivateKey
	if k, err := x509.ParsePKCS1PrivateKey(blk.Bytes); err == nil {
		key = k
	} else if k8, err := x509.ParsePKCS8PrivateKey(blk.Bytes); err == nil {
		rk, ok := k8.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("the GitHub App's private key is not RSA")
		}
		key = rk
	} else {
		return nil, fmt.Errorf("the GitHub App's private key: %w", err)
	}
	return &githubApp{ID: id, ClientID: clientID, ClientSecret: clientSecret, Slug: slug, key: key,
		API: "https://api.github.com", Web: "https://github.com",
		client: &http.Client{Timeout: 30 * time.Second}, now: time.Now, tokens: map[string]appToken{}}, nil
}

// the App's own JWT (RS256, 9 minutes; GitHub takes at most 10)
func (a *githubApp) jwt() (string, error) {
	now := a.now()
	enc := base64.RawURLEncoding
	head := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{"iat": now.Add(-60 * time.Second).Unix(), "exp": now.Add(9 * time.Minute).Unix(), "iss": a.ID})
	body := head + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(body))
	sig, err := rsa.SignPKCS1v15(rand.Reader, a.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return body + "." + enc.EncodeToString(sig), nil
}

// ghError is GitHub's answer when it is not 2xx.
type ghError struct {
	Status  int
	Message string
}

func (e *ghError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("GitHub: %d %s", e.Status, http.StatusText(e.Status))
	}
	return fmt.Sprintf("GitHub: %d %s", e.Status, e.Message)
}

func ghStatus(err error) int {
	var e *ghError
	if errors.As(err, &e) {
		return e.Status
	}
	return 0
}

// call sends one request to the API with the token ("Bearer" for every
// kind) and reads its JSON answer into out (nil: none wanted)
func (a *githubApp) call(ctx context.Context, token, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.API+path, body)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "Sliqtly")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		var m struct {
			Message string `json:"message"`
		}
		json.Unmarshal(b, &m)
		return &ghError{Status: res.StatusCode, Message: m.Message}
	}
	if out == nil || len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, out)
}

// --- signing in (the user's token)

// authorizeURL: where the browser goes to sign in, coming back to
// redirect with the code and state
func (a *githubApp) authorizeURL(redirect, state string) string {
	q := url.Values{"client_id": {a.ClientID}, "redirect_uri": {redirect}, "state": {state}, "allow_signup": {"true"}}
	return a.Web + "/login/oauth/authorize?" + q.Encode()
}

// installURL: where a person picks the repositories the App may write
func (a *githubApp) installURL(state string) string {
	return a.Web + "/apps/" + url.PathEscape(a.Slug) + "/installations/new?state=" + url.QueryEscape(state)
}

// userToken trades the code from the sign-in for the user's token
func (a *githubApp) userToken(ctx context.Context, code, redirect string) (string, error) {
	form := url.Values{"client_id": {a.ClientID}, "client_secret": {a.ClientSecret}, "code": {code}, "redirect_uri": {redirect}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.Web+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := a.client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var out struct {
		Token string `json:"access_token"`
		Error string `json:"error"`
		Desc  string `json:"error_description"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&out); err != nil {
		return "", fmt.Errorf("GitHub's sign-in answered %s", res.Status)
	}
	if out.Token == "" {
		if out.Desc != "" {
			return "", errors.New("GitHub's sign-in: " + out.Desc)
		}
		return "", fmt.Errorf("GitHub's sign-in gave no token (%s)", nonEmpty(out.Error, res.Status))
	}
	return out.Token, nil
}

// ghUser is the person a user token is for.
type ghUser struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Name  string `json:"name"`
	// their primary address, when GitHub has verified it; "" otherwise
	Email string `json:"email"`
}

func (a *githubApp) user(ctx context.Context, token string) (ghUser, error) {
	var u ghUser
	if err := a.call(ctx, token, http.MethodGet, "/user", nil, &u); err != nil {
		return u, err
	}
	u.Email = ""
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	// the App asks for "Email addresses: read"; without it there is none
	if err := a.call(ctx, token, http.MethodGet, "/user/emails", nil, &emails); err == nil {
		for _, e := range emails {
			if e.Primary && e.Verified {
				u.Email = strings.ToLower(e.Email)
			}
		}
	}
	if u.ID == 0 || u.Login == "" {
		return u, errors.New("GitHub did not say who signed in")
	}
	return u, nil
}

// ghRepo is a repository as the API lists one.
type ghRepo struct {
	ID            int64  `json:"id"`
	FullName      string `json:"full_name"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
	HTMLURL       string `json:"html_url"`
	Owner         struct {
		Login string `json:"login"`
	} `json:"owner"`
}

// ghPlace is an installation of the App a person can use, with its repositories.
type ghPlace struct {
	Installation int64    `json:"installation"`
	Account      string   `json:"account"`
	All          bool     `json:"all"` // every repository of the account
	Repos        []ghRepo `json:"repos"`
}

// places: the App's installations the user may use and the repositories
// each lets it write
func (a *githubApp) places(ctx context.Context, token string) ([]ghPlace, error) {
	var inst struct {
		Installations []struct {
			ID      int64 `json:"id"`
			Account struct {
				Login string `json:"login"`
			} `json:"account"`
			Selection string `json:"repository_selection"`
		} `json:"installations"`
	}
	if err := a.call(ctx, token, http.MethodGet, "/user/installations?per_page=100", nil, &inst); err != nil {
		return nil, err
	}
	out := []ghPlace{}
	for _, in := range inst.Installations {
		p := ghPlace{Installation: in.ID, Account: in.Account.Login, All: in.Selection == "all", Repos: []ghRepo{}}
		for page := 1; page <= 5; page++ {
			var rs struct {
				Repos []ghRepo `json:"repositories"`
			}
			if err := a.call(ctx, token, http.MethodGet, fmt.Sprintf("/user/installations/%d/repositories?per_page=100&page=%d", in.ID, page), nil, &rs); err != nil {
				return nil, err
			}
			p.Repos = append(p.Repos, rs.Repos...)
			if len(rs.Repos) < 100 {
				break
			}
		}
		out = append(out, p)
	}
	return out, nil
}

// createRepo makes a private repository of the user's own, with a first
// commit (the Git Data API cannot write an empty repository)
func (a *githubApp) createRepo(ctx context.Context, token, name string) (ghRepo, error) {
	var r ghRepo
	err := a.call(ctx, token, http.MethodPost, "/user/repos", map[string]any{
		"name": name, "private": true, "auto_init": true,
		"description": "Presentations kept by Sliqtly", "has_issues": false, "has_wiki": false, "has_projects": false,
	}, &r)
	return r, err
}

// addToInstallation lets the installation write the repository (when it
// was installed for chosen repositories only)
func (a *githubApp) addToInstallation(ctx context.Context, token string, installation, repo int64) error {
	return a.call(ctx, token, http.MethodPut, fmt.Sprintf("/user/installations/%d/repositories/%d", installation, repo), nil, nil)
}

// --- writing the repository (the installation's token)

// repoToken: a token of the installation for that one repository, kept
// until a few minutes before it runs out
func (a *githubApp) repoToken(ctx context.Context, installation, repo int64) (string, error) {
	key := strconv.FormatInt(installation, 10) + "/" + strconv.FormatInt(repo, 10)
	a.mu.Lock()
	t, ok := a.tokens[key]
	a.mu.Unlock()
	if ok && a.now().Before(t.until) {
		return t.token, nil
	}
	j, err := a.jwt()
	if err != nil {
		return "", err
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	in := map[string]any{"repository_ids": []int64{repo}, "permissions": map[string]string{"contents": "write", "metadata": "read"}}
	if err := a.call(ctx, j, http.MethodPost, fmt.Sprintf("/app/installations/%d/access_tokens", installation), in, &out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", errors.New("GitHub gave the App no token")
	}
	until := out.ExpiresAt.Add(-5 * time.Minute)
	if out.ExpiresAt.IsZero() {
		until = a.now().Add(50 * time.Minute)
	}
	a.mu.Lock()
	a.tokens[key] = appToken{out.Token, until}
	if len(a.tokens) > 2000 {
		for k, v := range a.tokens {
			if a.now().After(v.until) {
				delete(a.tokens, k)
			}
		}
	}
	a.mu.Unlock()
	return out.Token, nil
}

// ghRepoAt is one repository and a token that writes it.
type ghRepoAt struct {
	app   *githubApp
	token string
	name  string // owner/name
}

func (a *githubApp) at(ctx context.Context, installation, repo int64, fullName string) (*ghRepoAt, error) {
	t, err := a.repoToken(ctx, installation, repo)
	if err != nil {
		return nil, err
	}
	return &ghRepoAt{app: a, token: t, name: fullName}, nil
}

func (r *ghRepoAt) path(p string) string {
	owner, name, _ := strings.Cut(r.name, "/")
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + p
}

func (r *ghRepoAt) call(ctx context.Context, method, p string, in, out any) error {
	return r.app.call(ctx, r.token, method, r.path(p), in, out)
}

func (r *ghRepoAt) info(ctx context.Context) (ghRepo, error) {
	var out ghRepo
	err := r.call(ctx, http.MethodGet, "", nil, &out)
	return out, err
}

// head: the branch's commit; "" when there is no such branch
func (r *ghRepoAt) head(ctx context.Context, branch string) (string, error) {
	var out struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	err := r.call(ctx, http.MethodGet, "/git/ref/heads/"+url.PathEscape(branch), nil, &out)
	if s := ghStatus(err); s == 404 || s == 409 {
		return "", nil
	}
	return out.Object.SHA, err
}

// ghCommit is a commit as the Git Data API gives it.
type ghCommit struct {
	SHA     string `json:"sha"`
	Message string `json:"message"`
	Tree    struct {
		SHA string `json:"sha"`
	} `json:"tree"`
	Parents []struct {
		SHA string `json:"sha"`
	} `json:"parents"`
	Author    ghPerson `json:"author"`
	Committer ghPerson `json:"committer"`
}

type ghPerson struct {
	Name  string    `json:"name"`
	Email string    `json:"email"`
	Date  time.Time `json:"date"`
}

func (r *ghRepoAt) commit(ctx context.Context, sha string) (ghCommit, error) {
	var c ghCommit
	err := r.call(ctx, http.MethodGet, "/git/commits/"+url.PathEscape(sha), nil, &c)
	return c, err
}

// ghEntry is one entry of a tree.
type ghEntry struct {
	Path string  `json:"path"`
	Mode string  `json:"mode"`
	Type string  `json:"type"`
	SHA  *string `json:"sha"` // nil: removed (when written)
	Size int64   `json:"size,omitempty"`
}

// files: every file of the tree, path → blob (recursive); an error when
// GitHub cut the listing short
func (r *ghRepoAt) files(ctx context.Context, tree string) (map[string]string, error) {
	var out struct {
		Tree      []ghEntry `json:"tree"`
		Truncated bool      `json:"truncated"`
	}
	if err := r.call(ctx, http.MethodGet, "/git/trees/"+url.PathEscape(tree)+"?recursive=1", nil, &out); err != nil {
		return nil, err
	}
	if out.Truncated {
		return nil, errors.New("the repository has more files than GitHub lists at once")
	}
	m := map[string]string{}
	for _, e := range out.Tree {
		if e.Type == "blob" && e.SHA != nil {
			m[e.Path] = *e.SHA
		}
	}
	return m, nil
}

// list: one tree's own entries (not recursive)
func (r *ghRepoAt) list(ctx context.Context, tree string) ([]ghEntry, error) {
	var out struct {
		Tree []ghEntry `json:"tree"`
	}
	err := r.call(ctx, http.MethodGet, "/git/trees/"+url.PathEscape(tree), nil, &out)
	return out.Tree, err
}

// subtree: the tree at dir ("decks/x") under tree, "" when there is none
func (r *ghRepoAt) subtree(ctx context.Context, tree, dir string) (string, error) {
	at := tree
	for _, part := range strings.Split(dir, "/") {
		es, err := r.list(ctx, at)
		if err != nil {
			return "", err
		}
		at = ""
		for _, e := range es {
			if e.Path == part && e.Type == "tree" && e.SHA != nil {
				at = *e.SHA
			}
		}
		if at == "" {
			return "", nil
		}
	}
	return at, nil
}

func (r *ghRepoAt) blob(ctx context.Context, sha string) ([]byte, error) {
	var out struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := r.call(ctx, http.MethodGet, "/git/blobs/"+url.PathEscape(sha), nil, &out); err != nil {
		return nil, err
	}
	if out.Encoding != "base64" {
		return []byte(out.Content), nil
	}
	return base64.StdEncoding.DecodeString(strings.ReplaceAll(out.Content, "\n", ""))
}

func (r *ghRepoAt) putBlob(ctx context.Context, b []byte) (string, error) {
	var out struct {
		SHA string `json:"sha"`
	}
	err := r.call(ctx, http.MethodPost, "/git/blobs", map[string]string{"content": base64.StdEncoding.EncodeToString(b), "encoding": "base64"}, &out)
	return out.SHA, err
}

func (r *ghRepoAt) putTree(ctx context.Context, base string, entries []ghEntry) (string, error) {
	in := map[string]any{"tree": entries}
	if base != "" {
		in["base_tree"] = base
	}
	var out struct {
		SHA string `json:"sha"`
	}
	err := r.call(ctx, http.MethodPost, "/git/trees", in, &out)
	return out.SHA, err
}

func (r *ghRepoAt) putCommit(ctx context.Context, message, tree string, parents []string, author ghPerson) (string, error) {
	in := map[string]any{"message": message, "tree": tree, "parents": parents}
	if author.Name != "" {
		in["author"] = map[string]any{"name": author.Name, "email": author.Email, "date": author.Date.UTC().Format(time.RFC3339)}
	}
	var out struct {
		SHA string `json:"sha"`
	}
	err := r.call(ctx, http.MethodPost, "/git/commits", in, &out)
	return out.SHA, err
}

// moveBranch: the branch to sha, only forward (never forced); made when
// there is none. A 422 says it moved meanwhile.
func (r *ghRepoAt) moveBranch(ctx context.Context, branch, sha string, exists bool) error {
	if !exists {
		return r.call(ctx, http.MethodPost, "/git/refs", map[string]string{"ref": "refs/heads/" + branch, "sha": sha}, nil)
	}
	return r.call(ctx, http.MethodPatch, "/git/refs/heads/"+url.PathEscape(branch), map[string]any{"sha": sha, "force": false}, nil)
}

// ghLogRow is a commit as the commits listing gives it.
type ghLogRow struct {
	SHA    string `json:"sha"`
	Commit struct {
		Message   string   `json:"message"`
		Author    ghPerson `json:"author"`
		Committer ghPerson `json:"committer"`
	} `json:"commit"`
	Parents []struct {
		SHA string `json:"sha"`
	} `json:"parents"`
}

// log: up to n commits of the branch that changed dir, newest first
func (r *ghRepoAt) log(ctx context.Context, branch, dir string, n int) ([]ghLogRow, error) {
	var out []ghLogRow
	q := url.Values{"sha": {branch}, "path": {dir}, "per_page": {strconv.Itoa(n)}}
	err := r.call(ctx, http.MethodGet, "/commits?"+q.Encode(), nil, &out)
	return out, err
}
