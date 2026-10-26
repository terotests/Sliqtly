// SPDX-License-Identifier: AGPL-3.0-or-later

// sliqtly.com's decks in a GitHub repository of the person's own
// (git-storage PILVI.md; the research deck "GitHub-kirjautuminen ja oma
// Markdown-repo", phases G1–G4). Optional, beside Google's sign-in, one
// repository per account.
//
// The repository has the folder server's format (gitstore.go): a deck in
// decks/<id>/ (deck.md, deck.json, theme.css, its files), a topic in
// rooms/<id>.json, README.md, sliqtly.json. A repository moves between the
// cloud and a server of one's own as it is.
//
// Firestore stays where the editor writes: the browser saves there
// directly and the rooms edit live there. sync (below) puts the account's
// own decks into the repository and takes in what was changed on GitHub
// since the last sync:
//
//  1. The branch moved on GitHub since the commit this account last synced
//     (someone pushed, edited on github.com, or an assistant committed):
//     each deck whose files changed there is merged into Firestore line by
//     line against that commit (RangerDiff's diff3, as two assistants'
//     edits are merged). What only GitHub changed is taken; what both
//     changed on the same lines is not: the deck keeps Sliqtly's text and
//     GitHub's is kept beside it as a conflict to choose from
//     (/editor/git). A deck new on GitHub becomes the account's deck. A
//     deck removed on GitHub is not removed here.
//  2. Then the decks as Firestore has them are committed over the branch
//     (only the files that differ are sent), the branch moved forward only,
//     never forced. A branch that moved meanwhile is read again.
//
// sync runs on "Save version" (the version's message is the commit's), on
// the editor's quiet after changes, on GitHub's push webhook, on "Back up
// now" and on a scheduled sweep. One sync per account at a time, across
// the server's instances (a lease on the account's document).
//
// What goes in is what may be published with the decks; who owns them,
// invitations, chat, form answers, links' keys stay in Firestore, since
// Git keeps every version for good. A public repository is written only
// when the person said so for it (Public): `private` is read before every
// commit, and a repository made public on GitHub later stops the syncs
// until the person says it may be.

package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"mime"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/terotests/sliqtly/mcp-go/rgit"
)

const (
	// gitLinkCol holds git_links/{uid}, the account's GitHub and its
	// repository; gitUserCol git_users/{github id} → the account it signs in.
	// No Firestore rule matches either: only the server reads and writes them.
	gitLinkCol = "git_links"
	gitUserCol = "git_users"
	// GitHub warns about files over 50 MB and refuses ones over 100 MB
	cloudGitMaxFile = 50 << 20
	// how long a sync may hold the account
	cloudGitLease = 3 * time.Minute
	// the most decks one sync puts in (the rest wait for the next)
	cloudGitMaxDecks = 2000
)

// cloudGit is the service; nil when the server has no GitHub App.
type cloudGit struct {
	app    *githubApp
	db     DB
	bucket Bucket
	// the site's address (https://sliqtly.com): where GitHub sends people back
	base string
	// the push webhook's secret, and the scheduled sweep's key ("" none)
	hookSecret, sweepKey string
	// mint: a Firebase custom token for the account uid, made when it is
	// new (gitSignIn); nil: no signing in with GitHub, only connecting
	mint func(ctx context.Context, uid string, u ghUser) (string, error)
	now  func() time.Time
	logf func(format string, args ...any)
}

func newCloudGit(app *githubApp, db DB, bucket Bucket, base string) *cloudGit {
	return &cloudGit{app: app, db: db, bucket: bucket, base: strings.TrimRight(base, "/"), now: time.Now, logf: log.Printf}
}

// gitLink is git_links/{uid}.
type gitLink struct {
	uid          string
	GitHubID     int64
	Login        string
	Installation int64
	RepoID       int64
	Repo         string // owner/name
	Branch       string
	// the commit this account last synced with ("" none yet)
	Head   string
	Synced time.Time
	Error  string
	Lease  string
	// a deck's file in the repository → what it was made from (path, size,
	// version), so a picture is read from Storage only when it changed
	FileKeys map[string]string
	// the decks the repository has from this account: only these are
	// removed from it when they are gone here
	Decks []string
	// decks GitHub changed on the same lines as Sliqtly: id → {sha, at, md, css}
	Conflicts map[string]any
	// the installations and repositories the person may pick, as GitHub
	// listed them at the last sign-in
	Choices []any
	// a repository being made ("" none)
	Want string
	// the person said the repository may be public
	Public bool
}

func (c *cloudGit) link(ctx context.Context, uid string) (*gitLink, Doc, error) {
	d, err := c.db.Get(ctx, gitLinkCol, uid)
	if err != nil {
		return nil, nil, err
	}
	l := &gitLink{uid: uid, FileKeys: map[string]string{}, Conflicts: map[string]any{}}
	if d == nil {
		return l, nil, nil
	}
	gh, _ := d["github"].(map[string]any)
	l.GitHubID = docInt(gh["id"])
	l.Login, _ = gh["login"].(string)
	l.Installation = docInt(d["installation"])
	l.RepoID = docInt(d["repoId"])
	l.Repo, _ = d["repo"].(string)
	l.Branch, _ = d["branch"].(string)
	l.Head, _ = d["head"].(string)
	l.Synced = docTime(d["synced"])
	l.Error, _ = d["error"].(string)
	l.Lease, _ = d["lease"].(string)
	l.Want, _ = d["want"].(string)
	l.Public, _ = d["public"].(bool)
	if m, ok := d["fileKeys"].(map[string]any); ok {
		for k, v := range m {
			if s, ok := v.(string); ok {
				l.FileKeys[k] = s
			}
		}
	}
	for _, v := range asList(d["decks"]) {
		if s, ok := v.(string); ok {
			l.Decks = append(l.Decks, s)
		}
	}
	if m, ok := d["conflicts"].(map[string]any); ok {
		l.Conflicts = m
	}
	l.Choices = asList(d["choices"])
	return l, d, nil
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

// connected: the account has a repository to keep its decks in
func (l *gitLink) connected() bool { return l.Repo != "" && l.Installation != 0 && l.RepoID != 0 }

// --- the lease

// hold takes the account for one sync: false when another holds it
func (c *cloudGit) hold(ctx context.Context, uid string) (string, bool, error) {
	l, _, err := c.link(ctx, uid)
	if err != nil {
		return "", false, err
	}
	if l.Lease != "" {
		if _, until, _ := strings.Cut(l.Lease, "|"); until != "" {
			if ms, err := strconv.ParseInt(until, 10, 64); err == nil && c.now().UnixMilli() < ms {
				return "", false, nil
			}
		}
	}
	b := make([]byte, 8)
	rand.Read(b)
	mine := hex.EncodeToString(b) + "|" + strconv.FormatInt(c.now().Add(cloudGitLease).UnixMilli(), 10)
	ok, err := c.db.UpdateIf(ctx, gitLinkCol, uid, "lease", l.Lease, Doc{"lease": mine})
	return mine, ok, err
}

func (c *cloudGit) release(uid, lease string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.db.UpdateIf(ctx, gitLinkCol, uid, "lease", lease, Doc{"lease": ""}); err != nil {
		c.logf("git: %s: lease not let go: %v", uid, err)
	}
}

// --- sync

var (
	errGitNotConnected = errors.New("no GitHub repository is connected: connect one in File → Git and GitHub")
	errGitBusy         = errors.New("a sync of this account is running; try again in a moment")
)

// errGitStop is a reason syncing stops until the person acts (said on the page).
type errGitStop struct{ why string }

func (e errGitStop) Error() string { return e.why }

// gitSyncResult is what one sync did.
type gitSyncResult struct {
	Commit    string   `json:"commit"`           // the new commit, "" when nothing was to commit
	Head      string   `json:"head"`             // the branch now
	Imported  []string `json:"imported"`         // decks changed from GitHub
	Created   []string `json:"created"`          // decks made from GitHub
	Conflicts []string `json:"conflicts"`        // decks GitHub and Sliqtly changed on the same lines
	TooBig    []string `json:"tooBig,omitempty"` // files left out for their size
}

// sync the account's decks with its repository (see the top of the file);
// message: the commit's ("" one is made of what changed), author: whose
// change it was ("" the account's GitHub login)
func (c *cloudGit) sync(ctx context.Context, uid, message, author string) (*gitSyncResult, error) {
	l, _, err := c.link(ctx, uid)
	if err != nil {
		return nil, err
	}
	if !l.connected() {
		return nil, errGitNotConnected
	}
	lease, ok, err := c.hold(ctx, uid)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errGitBusy
	}
	defer c.release(uid, lease)
	// as it is now that this sync holds it
	if l, _, err = c.link(ctx, uid); err != nil {
		return nil, err
	}
	var res *gitSyncResult
	for try := 0; try < 3; try++ {
		res, err = c.syncOnce(ctx, l, message, author)
		if ghStatus(err) != 422 && ghStatus(err) != 409 {
			break
		}
		// the branch moved meanwhile: read again
		if l, _, err = c.link(ctx, uid); err != nil {
			break
		}
	}
	note := Doc{"synced": c.now(), "error": ""}
	if err != nil {
		note = Doc{"error": err.Error(), "errorAt": c.now()}
		c.logf("git: %s: %v", uid, err)
	}
	if uerr := c.db.Update(ctx, gitLinkCol, uid, note); uerr != nil {
		c.logf("git: %s: not noted: %v", uid, uerr)
	}
	return res, err
}

func (c *cloudGit) syncOnce(ctx context.Context, l *gitLink, message, author string) (*gitSyncResult, error) {
	res := &gitSyncResult{Imported: []string{}, Created: []string{}, Conflicts: []string{}}
	r, err := c.app.at(ctx, l.Installation, l.RepoID, l.Repo)
	if err != nil {
		return nil, gitAccessErr(err, l)
	}
	info, err := r.info(ctx)
	if err != nil {
		return nil, gitAccessErr(err, l)
	}
	if !info.Private && !l.Public {
		return nil, errGitStop{publicStop(info.FullName)}
	}
	if info.FullName != "" && info.FullName != l.Repo {
		// renamed or moved on GitHub: the id is the same repository
		l.Repo, r.name = info.FullName, info.FullName
		c.db.Update(ctx, gitLinkCol, l.uid, Doc{"repo": info.FullName})
	}
	branch := nonEmpty(l.Branch, nonEmpty(info.DefaultBranch, "main"))
	head, err := r.head(ctx, branch)
	if err != nil {
		return nil, err
	}
	if head == "" {
		// an empty repository: the Git Data API writes only one with a commit
		if head, err = c.firstCommit(ctx, r, branch); err != nil {
			return nil, err
		}
	}
	hc, err := r.commit(ctx, head)
	if err != nil {
		return nil, err
	}
	remote, err := r.files(ctx, hc.Tree.SHA)
	if err != nil {
		return nil, err
	}
	// 1. what GitHub changed since the last sync
	if head != l.Head {
		if err := c.takeIn(ctx, l, r, head, remote, res); err != nil {
			return nil, err
		}
	}
	// 2. the decks as they are here
	snap, err := c.snapshot(ctx, l, r, remote)
	if err != nil {
		return nil, err
	}
	res.TooBig = snap.tooBig
	var entries []ghEntry
	var before, after []rgit.Entry
	for p, sha := range remote {
		if gitOwnedPath(p) {
			before = append(before, rgit.Entry{Path: p, ID: sha})
		}
	}
	for p, f := range snap.files {
		after = append(after, rgit.Entry{Path: p, ID: f.sha})
		if remote[p] == f.sha {
			continue
		}
		sha := f.sha
		if f.data != nil {
			got, err := r.putBlob(ctx, f.data)
			if err != nil {
				return nil, err
			}
			if got != f.sha {
				return nil, fmt.Errorf("GitHub stored %s as %s, not %s", p, got, f.sha)
			}
		}
		entries = append(entries, ghEntry{Path: p, Mode: "100644", Type: "blob", SHA: &sha})
	}
	kept := map[string]bool{}
	for _, id := range snap.decks {
		kept[id] = true
	}
	had := map[string]bool{}
	for _, id := range l.Decks {
		had[id] = true
	}
	for p := range remote {
		if _, ok := snap.files[p]; ok || !gitOwnedPath(p) {
			continue
		}
		if snap.left[p] {
			continue // too big now: what the repository has stays
		}
		deck := gitDeckOf(p)
		switch {
		case deck != "" && !kept[deck] && !had[deck]:
			continue // not a deck of this account's: left as it is
		case deck == "" && !strings.HasPrefix(p, "rooms/"):
			continue
		}
		entries = append(entries, ghEntry{Path: p, Mode: "100644", Type: "blob", SHA: nil})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	done := Doc{"head": head, "fileKeys": stringDoc(snap.keys), "decks": anyList(snap.decks), "branch": branch}
	if len(entries) == 0 {
		res.Head = head
		return res, c.db.Update(ctx, gitLinkCol, l.uid, done)
	}
	sort.Slice(before, func(i, j int) bool { return before[i].Path < before[j].Path })
	sort.Slice(after, func(i, j int) bool { return after[i].Path < after[j].Path })
	desc := gitChanges(before, after).describe(&gitSnapshot{Decks: snap.names, Rooms: snap.rooms})
	msg := desc
	if strings.TrimSpace(message) != "" {
		msg = strings.TrimRight(message, "\n") + "\n\n" + desc
	}
	tree, err := r.putTree(ctx, hc.Tree.SHA, entries)
	if err != nil {
		return nil, err
	}
	who := ghPerson{Name: nonEmpty(author, l.Login), Email: gitNoreply(l), Date: c.now()}
	commit, err := r.putCommit(ctx, strings.TrimRight(msg, "\n")+"\n", tree, []string{head}, who)
	if err != nil {
		return nil, err
	}
	if err := r.moveBranch(ctx, branch, commit, true); err != nil {
		return nil, err
	}
	done["head"] = commit
	res.Commit, res.Head = commit, commit
	return res, c.db.Update(ctx, gitLinkCol, l.uid, done)
}

// what a repository public on GitHub, not said to be, stops with
func publicStop(repo string) string {
	return fmt.Sprintf("%s is public on GitHub: nothing is written to it until you say it may be public (every presentation, picture and old version in it is readable by anyone). Allow it on the page, or make the repository private.", repo)
}

// the GitHub address commits carry (no one's real address goes in the history)
func gitNoreply(l *gitLink) string {
	if l.GitHubID != 0 && l.Login != "" {
		return fmt.Sprintf("%d+%s@users.noreply.github.com", l.GitHubID, l.Login)
	}
	return "sliqtly@users.noreply.github.com"
}

// what GitHub's refusal means to the person
func gitAccessErr(err error, l *gitLink) error {
	switch ghStatus(err) {
	case 401, 403, 404:
		return errGitStop{fmt.Sprintf("Sliqtly's GitHub App cannot reach %s any more (uninstalled, or the repository taken out of it). Connect the repository again in File → Git and GitHub.", l.Repo)}
	}
	return err
}

// the paths of the repository Sliqtly writes; the rest is the person's own
func gitOwnedPath(p string) bool {
	return strings.HasPrefix(p, "decks/") || strings.HasPrefix(p, "rooms/") || p == "README.md" || p == "sliqtly.json" || p == ".gitattributes"
}

// decks/<id>/… → id
func gitDeckOf(p string) string {
	rest, ok := strings.CutPrefix(p, "decks/")
	if !ok {
		return ""
	}
	id, _, ok := strings.Cut(rest, "/")
	if !ok {
		return ""
	}
	return id
}

func stringDoc(m map[string]string) Doc {
	d := Doc{}
	for k, v := range m {
		d[k] = v
	}
	return d
}

func anyList(l []string) []any {
	out := make([]any, len(l))
	for i, s := range l {
		out[i] = s
	}
	return out
}

// firstCommit writes README.md through the contents API, which (unlike the
// Git Data API) writes an empty repository → the branch's commit
func (c *cloudGit) firstCommit(ctx context.Context, r *ghRepoAt, branch string) (string, error) {
	var out struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	body := map[string]any{"message": "Sliqtly: start", "content": base64.StdEncoding.EncodeToString([]byte("# Sliqtly decks\n")), "branch": branch}
	if err := r.call(ctx, "PUT", "/contents/README.md", body, &out); err != nil {
		return "", err
	}
	return out.Commit.SHA, nil
}

// --- the decks as files

type gitWant struct {
	sha  string
	data []byte // nil: the repository has these bytes already
}

type cloudSnap struct {
	files  map[string]gitWant
	keys   map[string]string // file → what it was made from
	decks  []string
	names  map[string]string // deck → name
	rooms  map[string]string // room → title
	tooBig []string
	left   map[string]bool // files left out for their size
}

// snapshot: the account's own decks and topics as the repository's files
func (c *cloudGit) snapshot(ctx context.Context, l *gitLink, r *ghRepoAt, remote map[string]string) (*cloudSnap, error) {
	s := &cloudSnap{files: map[string]gitWant{}, keys: map[string]string{}, names: map[string]string{}, rooms: map[string]string{}, left: map[string]bool{}}
	put := func(p string, b []byte) {
		id := rgit.ID("blob", b)
		w := gitWant{sha: id}
		if remote[p] != id {
			w.data = b
		}
		s.files[p] = w
	}
	docs, ids, err := c.db.WhereEq(ctx, "shares", "owner", l.uid)
	if err != nil {
		return nil, err
	}
	// the topics and folders the account keeps its decks in (layout.go)
	place := map[string][2]string{}
	if ld, err := c.db.Get(ctx, layoutCol, l.uid); err == nil && ld != nil {
		lay := layoutOf(ld)
		for k, f := range lay.facts {
			if f.V == nil {
				continue
			}
			kind, id, _ := strings.Cut(k, "/")
			switch kind {
			case "room":
				if !gitSafeID(id) {
					continue
				}
				var room map[string]any
				if json.Unmarshal([]byte(*f.V), &room) != nil {
					continue
				}
				title, _ := room["title"].(string)
				if title == "" {
					title, _ = room["name"].(string)
				}
				s.rooms[id] = title
				out := pick(room, "title", "name", "kind", "archived", "created")
				if fv, ok := lay.facts["folders/"+id]; ok && fv.V != nil {
					var folders []any
					if json.Unmarshal([]byte(*fv.V), &folders) == nil {
						out["folders"] = folders
					}
				}
				put("rooms/"+id+".json", gitJSON(out, nil))
			case "deck":
				p := place[id]
				p[0] = *f.V
				place[id] = p
			case "filed":
				p := place[id]
				p[1] = *f.V
				place[id] = p
			}
		}
	}
	order := make([]int, len(ids))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return ids[order[a]] < ids[order[b]] })
	for _, i := range order {
		id, d := ids[i], docs[i]
		if !gitSafeID(id) || d == nil {
			continue
		}
		if d["branch"] != nil {
			continue // a topic branch's copy (cloudtopicgit.go): its own branch keeps it
		}
		if len(s.decks) >= cloudGitMaxDecks {
			break
		}
		doc := Doc{}
		for k, v := range d {
			doc[k] = v
		}
		// the page names a deck "cloud:<id>" in its facts (layout.go pageDeck)
		p, ok := place[pageDeck(id)]
		if !ok {
			p, ok = place[id]
		}
		if ok {
			if p[0] != "" {
				doc["room"] = p[0]
			}
			if p[1] != "" {
				doc["folder"] = p[1]
			}
		}
		name, _ := doc["name"].(string)
		s.names[id] = name
		s.decks = append(s.decks, id)
		for _, f := range deckRepoFiles(id, doc) {
			put(f.path, f.data)
		}
		for _, x := range asList(doc["files"]) {
			f, _ := x.(map[string]any)
			fp, _ := f["path"].(string)
			if !gitSafeFile(fp) || deckMeta[fp] {
				continue
			}
			p := "decks/" + id + "/" + fp
			size := docInt(f["size"])
			if size > cloudGitMaxFile {
				s.tooBig = append(s.tooBig, fmt.Sprintf("%s (%s)", p, sizeText(size)))
				s.left[p] = true
				continue
			}
			key := fmt.Sprintf("%d %v %v", size, f["sha"], f["url"])
			if sha := remote[p]; sha != "" && l.FileKeys[p] == key {
				s.files[p] = gitWant{sha: sha}
				s.keys[p] = key
				continue
			}
			b, err := c.bucket.Read(ctx, "shares/"+id+"/"+fp, cloudGitMaxFile+1)
			if err != nil {
				c.logf("git: %s: %s not read: %v", l.uid, p, err)
				if sha := remote[p]; sha != "" {
					s.files[p] = gitWant{sha: sha} // what the repository has stays
				}
				continue
			}
			if int64(len(b)) > cloudGitMaxFile {
				s.tooBig = append(s.tooBig, fmt.Sprintf("%s (%s)", p, sizeText(int64(len(b)))))
				s.left[p] = true
				continue
			}
			put(p, b)
			s.keys[p] = key
		}
	}
	for _, f := range append(gitFormatFiles(), gitText{"README.md", gitReadme(&gitSnapshot{Decks: s.names, Rooms: s.rooms})}) {
		put(f.path, f.data)
	}
	return s, nil
}

// a deck's file path as a repository's path may be
func gitSafeFile(p string) bool {
	if p == "" || len(p) > 300 || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") || strings.Contains(p, "\x00") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." || strings.HasPrefix(part, ".collab") || strings.EqualFold(part, ".git") {
			return false
		}
	}
	return true
}

// --- what GitHub changed

// takeIn merges what the branch changed since l.Head (head is the branch
// now, remote its files) into the account's decks
func (c *cloudGit) takeIn(ctx context.Context, l *gitLink, r *ghRepoAt, head string, remote map[string]string, res *gitSyncResult) error {
	base := map[string]string{}
	if l.Head != "" {
		bc, err := r.commit(ctx, l.Head)
		switch {
		case ghStatus(err) == 404 || ghStatus(err) == 422:
			// the commit is gone (history rewritten): every deck is compared
			// with nothing, so none is taken over Sliqtly's text
		case err != nil:
			return err
		default:
			if base, err = r.files(ctx, bc.Tree.SHA); err != nil {
				return err
			}
		}
	}
	// the decks whose files differ
	changed := map[string]bool{}
	for p, sha := range remote {
		if d := gitDeckOf(p); d != "" && base[p] != sha {
			changed[d] = true
		}
	}
	for p := range base {
		if d := gitDeckOf(p); d != "" {
			if _, ok := remote[p]; !ok {
				changed[d] = true
			}
		}
	}
	ids := make([]string, 0, len(changed))
	for id := range changed {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if !shareID.MatchString(id) {
			continue
		}
		if err := c.takeDeck(ctx, l, r, id, head, base, remote, res); err != nil {
			return fmt.Errorf("deck %s from GitHub: %w", id, err)
		}
	}
	return nil
}

// a text file of the deck at one side ("" and false when it has none)
func (c *cloudGit) textAt(ctx context.Context, r *ghRepoAt, files map[string]string, p string) (string, bool, error) {
	sha, ok := files[p]
	if !ok {
		return "", false, nil
	}
	b, err := r.blob(ctx, sha)
	return string(b), true, err
}

func (c *cloudGit) takeDeck(ctx context.Context, l *gitLink, r *ghRepoAt, id, head string, base, remote map[string]string, res *gitSyncResult) error {
	dir := "decks/" + id + "/"
	theirsMD, inRemote, err := c.textAt(ctx, r, remote, dir+"deck.md")
	if err != nil {
		return err
	}
	if !inRemote {
		return nil // removed on GitHub: kept here (see the top of the file)
	}
	baseMD, _, err := c.textAt(ctx, r, base, dir+"deck.md")
	if err != nil {
		return err
	}
	theirsCSS, _, err := c.textAt(ctx, r, remote, dir+"theme.css")
	if err != nil {
		return err
	}
	baseCSS, _, err := c.textAt(ctx, r, base, dir+"theme.css")
	if err != nil {
		return err
	}
	var theirsMeta, baseMeta map[string]any
	if s, ok, err := c.textAt(ctx, r, remote, dir+"deck.json"); err != nil {
		return err
	} else if ok {
		json.Unmarshal([]byte(s), &theirsMeta)
	}
	if s, ok, err := c.textAt(ctx, r, base, dir+"deck.json"); err != nil {
		return err
	} else if ok {
		json.Unmarshal([]byte(s), &baseMeta)
	}
	cur, err := c.db.Get(ctx, "shares", id)
	if err != nil {
		return err
	}
	if cur == nil {
		return c.makeDeck(ctx, l, r, id, theirsMD, theirsCSS, theirsMeta, remote, res)
	}
	if owner, _ := cur["owner"].(string); owner != l.uid {
		c.logf("git: %s: deck %s in the repository is another account's: left out", l.uid, id)
		return nil
	}
	oursMD, _ := cur["md"].(string)
	oursCSS, _ := cur["css"].(string)
	md, mdClash := merge3Text(baseMD, oursMD, theirsMD)
	css, cssClash := merge3Text(baseCSS, oursCSS, theirsCSS)
	patch := Doc{}
	if !mdClash && md != oursMD {
		patch["md"] = md
	}
	if !cssClash && css != oursCSS {
		patch["css"] = css
	}
	// a name or theme changed on GitHub only
	for _, k := range []string{"name", "theme"} {
		t, _ := theirsMeta[k].(string)
		b, _ := baseMeta[k].(string)
		o, _ := cur[k].(string)
		if t != "" && t != b && o == b && t != o {
			patch[k] = t
		}
	}
	files, err := c.takeFiles(ctx, r, id, cur, base, remote)
	if err != nil {
		return err
	}
	if files != nil {
		patch["files"] = files
	}
	if mdClash || cssClash {
		res.Conflicts = append(res.Conflicts, id)
		c.db.Update(ctx, gitLinkCol, l.uid, Doc{"conflicts": mergeDoc(l.Conflicts, id, Doc{"sha": head, "at": c.now(), "md": theirsMD, "css": theirsCSS, "mdClash": mdClash, "cssClash": cssClash})})
	}
	if len(patch) == 0 {
		return nil
	}
	patch["updated"] = c.db.ServerTime()
	ok, err := c.db.UpdateIf(ctx, "shares", id, "md", oursMD, patch)
	if err != nil {
		return err
	}
	if !ok {
		// typed in meanwhile: the next sync merges again from the same base
		return fmt.Errorf("the deck changed while it was merged; it is taken in on the next sync")
	}
	res.Imported = append(res.Imported, id)
	return nil
}

func mergeDoc(m map[string]any, k string, v any) Doc {
	out := Doc{}
	for a, b := range m {
		out[a] = b
	}
	out[k] = v
	return out
}

// merge3Text: what base became on both sides, line by line; clash when
// both changed the same lines (then mine is returned as it is)
func merge3Text(base, mine, theirs string) (string, bool) {
	switch {
	case theirs == base || theirs == mine:
		return mine, false
	case mine == base:
		return theirs, false
	}
	m := RdText_static_merge3(base, mine, theirs)
	if m.conflicts > 0 {
		return mine, true
	}
	return m.text("mine"), false
}

// takeFiles: the deck's file list with the pictures and data GitHub added
// or changed (written to Storage); nil when none. A file removed on GitHub
// stays.
func (c *cloudGit) takeFiles(ctx context.Context, r *ghRepoAt, id string, cur Doc, base, remote map[string]string) ([]any, error) {
	dir := "decks/" + id + "/"
	list := asList(cur["files"])
	at := map[string]int{}
	for i, x := range list {
		f, _ := x.(map[string]any)
		if p, _ := f["path"].(string); p != "" {
			at[p] = i
		}
	}
	var out []any
	paths := make([]string, 0)
	for p := range remote {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		fp, ok := strings.CutPrefix(p, dir)
		if !ok || deckMeta[fp] || !gitSafeFile(fp) || remote[p] == base[p] {
			continue
		}
		b, err := r.blob(ctx, remote[p])
		if err != nil {
			return nil, err
		}
		if int64(len(b)) > cloudGitMaxFile {
			continue
		}
		typ := mime.TypeByExtension(path.Ext(fp))
		if typ == "" {
			typ = "application/octet-stream"
		}
		token := newUUID()
		name := "shares/" + id + "/" + fp
		if err := c.bucket.Save(ctx, name, typ, b, map[string]string{"firebaseStorageDownloadTokens": token}); err != nil {
			return nil, err
		}
		f := map[string]any{"path": fp, "type": typ, "size": int64(len(b)), "url": storageURL(c.bucket.Name(), name, token)}
		if out == nil {
			out = append([]any{}, list...)
		}
		if i, ok := at[fp]; ok {
			out[i] = f
		} else {
			at[fp] = len(out)
			out = append(out, f)
		}
	}
	return out, nil
}

// the Storage download URL of an object, with its token
func storageURL(bucket, name, token string) string {
	return "https://firebasestorage.googleapis.com/v0/b/" + bucket + "/o/" + strings.ReplaceAll(url.QueryEscape(name), "+", "%20") + "?alt=media&token=" + token
}

// makeDeck: a deck added on GitHub becomes the account's own, private
func (c *cloudGit) makeDeck(ctx context.Context, l *gitLink, r *ghRepoAt, id, md, css string, meta map[string]any, remote map[string]string, res *gitSyncResult) error {
	name, _ := meta["name"].(string)
	if strings.TrimSpace(name) == "" {
		name = deckTitle(md, id)
	}
	theme, _ := meta["theme"].(string)
	doc := Doc{"name": name, "md": md, "theme": theme, "owner": l.uid, "visibility": "private", "deck": "github", "source": "github",
		"files": []any{}, "created": c.db.ServerTime(), "updated": c.db.ServerTime()}
	if css != "" {
		doc["css"] = css
	} else {
		doc["css"] = nil
	}
	files, err := c.takeFiles(ctx, r, id, Doc{}, map[string]string{}, remote)
	if err != nil {
		return err
	}
	if files != nil {
		doc["files"] = files
	}
	had, err := c.db.Create(ctx, "shares", id, doc)
	if err != nil {
		return err
	}
	if had != nil {
		return nil // made meanwhile (or another account's id): not this one
	}
	res.Created = append(res.Created, id)
	return nil
}

// a deck's name from its first heading
func deckTitle(md, or string) string {
	for _, line := range strings.Split(md, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			if t := strings.TrimSpace(strings.TrimPrefix(line, "# ")); t != "" {
				return t
			}
		}
	}
	return or
}

// --- conflicts

// resolve a deck's conflict: take GitHub's text ("github") or keep
// Sliqtly's ("sliqtly"); the next sync commits what the deck has then
func (c *cloudGit) resolve(ctx context.Context, uid, deck, take string) error {
	l, _, err := c.link(ctx, uid)
	if err != nil {
		return err
	}
	cf, ok := l.Conflicts[deck].(map[string]any)
	if !ok {
		return errors.New("no such conflict")
	}
	if take == "github" {
		cur, err := c.db.Get(ctx, "shares", deck)
		if err != nil {
			return err
		}
		if cur == nil || cur["owner"] != uid {
			return errors.New("no such presentation")
		}
		patch := Doc{"updated": c.db.ServerTime()}
		if v, _ := cf["mdClash"].(bool); v {
			patch["md"], _ = cf["md"].(string)
		}
		if v, _ := cf["cssClash"].(bool); v {
			patch["css"], _ = cf["css"].(string)
		}
		if err := c.db.Update(ctx, "shares", deck, patch); err != nil {
			return err
		}
	} else if take != "sliqtly" {
		return errors.New("take is github or sliqtly")
	}
	rest := Doc{}
	for k, v := range l.Conflicts {
		if k != deck {
			rest[k] = v
		}
	}
	return c.db.Update(ctx, gitLinkCol, uid, Doc{"conflicts": rest})
}

// --- the sweep

// sweep syncs every connected account (a scheduled job calls it)
func (c *cloudGit) sweep(ctx context.Context) (int, error) {
	docs, ids, err := c.db.WhereEq(ctx, gitLinkCol, "enabled", true)
	if err != nil {
		return 0, err
	}
	n := 0
	for i, uid := range ids {
		if ctx.Err() != nil {
			break
		}
		if repo, _ := docs[i]["repo"].(string); repo == "" {
			continue
		}
		if _, err := c.sync(ctx, uid, "", ""); err == nil {
			n++
		}
	}
	return n, nil
}
