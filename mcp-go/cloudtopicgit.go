// SPDX-License-Identifier: AGPL-3.0-or-later

// A topic of sliqtly.com in a GitHub repository, or in one folder of it,
// with branches: several presentations about one subject worked on apart
// from main and merged back (the design "Topic GitHub-repossa: haarat ja
// yhdistäminen", 2026-10-26). It uses the same GitHub App as the account's
// own repository (cloudgit.go), and is apart from it: a topic is linked to a
// repository the person picks, which can hold other things besides.
//
// In the repository, under the topic's folder ("" the repository's top),
// each presentation is readable on GitHub as it is:
//
//	<slug>.md              the presentation's Markdown
//	<slug>/deck.json       its Sliqtly id, name, theme and files
//	<slug>/theme.css       its own CSS, when it has one
//	<slug>/<file>          its pictures and data files
//
// The slug is made from the name when the presentation first goes in and
// stays (the name can change). Sliqtly writes only these paths; the rest of
// the repository is left as it is. A Markdown file put in the folder on
// GitHub becomes a presentation of the topic.
//
// Main: the topic's presentations, as Firestore has them, are synced with
// the main branch as the account's repository is (cloudgit.go sync): what
// GitHub changed since the last sync is merged in line by line, then the
// presentations are committed over the branch.
//
// A branch (sliqtly/<name> on GitHub) starts at main. A presentation opened
// in the branch gets a copy of its own (a share with "branch" set), filed in
// the topic's folder "⎇ <name>"; one not opened stays as main has it. A
// presentation made in that folder is new in the branch. Update from main
// merges what main changed into each copy (diff3); the same lines changed
// on both sides are a conflict, chosen from on the topic's page. Once none
// is left, the branch gets a merge commit with main, so that merging it is
// clean: a pull request is opened and merged (or, when the App may not open
// one, main is moved forward to the branch). Main's presentations then take
// the branch's text in through the next sync, and the copies go.

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/terotests/sliqtly/mcp-go/rgit"
	"github.com/terotests/sliqtly/mcp-go/store"
)

const (
	// topicLinkCol holds topic_links/{uid}~{room}; only the server reads
	// and writes it (no Firestore rule matches it)
	topicLinkCol = "topic_links"
	// the GitHub branches Sliqtly makes start with this
	topicRefPrefix = "sliqtly/"
	// the most branches a topic keeps open at once
	topicMaxBranches = 20
)

var (
	errTopicNotLinked = errors.New("the topic is not linked to a GitHub repository")
	errTopicBusy      = errors.New("the topic is being synced; try again in a moment")
	errNoBranch       = errors.New("no such branch")
	topicBranchName   = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N} ._-]{0,59}$`)
)

// topicLink is topic_links/{uid}~{room}.
type topicLink struct {
	id, uid, room string
	Installation  int64
	RepoID        int64
	Repo          string // owner/name
	Dir           string // the folder, "" the repository's top
	Main          string // the branch the topic's presentations are on
	Head          string // main's commit the last sync left
	// a presentation → its slug (the name of its file and folder)
	Slugs map[string]string
	// a presentation's file → what it was made from, as cloudgit.go keeps
	FileKeys map[string]string
	// main's presentations GitHub changed on the same lines: id → {sha, md, css, mdClash, cssClash, at}
	Conflicts map[string]any
	Branches  map[string]*topicBranch
	Lease     string
	Error     string
	Synced    time.Time
	// the person said the repository may be public
	Public bool
}

// topicBranch is one branch of a topic.
type topicBranch struct {
	Name   string
	Ref    string // the GitHub branch
	Base   string // main's commit the branch was last brought up to
	Head   string // the branch's commit the last sync left
	State  string // open, review (a pull request waits on GitHub), merged, dropped
	Folder string // its folder in the topic
	By     string
	PR     int64
	PRURL  string
	// main's presentation → its copy in the branch
	Copies map[string]topicCopy
	// a copy whose text and main's changed on the same lines: copy → {md, css, main, at}
	Conflicts map[string]any
	Created   int64
}

// topicCopy is a presentation's copy in a branch and the main commit whose
// text it was last merged with.
type topicCopy struct {
	Deck string
	Base string
}

func topicLinkID(uid, room string) string { return uid + "~" + room }

func (c *cloudGit) topicLink(ctx context.Context, uid, room string) (*topicLink, error) {
	id := topicLinkID(uid, room)
	d, err := c.db.Get(ctx, topicLinkCol, id)
	if err != nil {
		return nil, err
	}
	return topicLinkOf(id, uid, room, d), nil
}

func topicLinkOf(id, uid, room string, d Doc) *topicLink {
	l := &topicLink{id: id, uid: uid, room: room, Slugs: map[string]string{}, FileKeys: map[string]string{}, Conflicts: map[string]any{}, Branches: map[string]*topicBranch{}}
	if d == nil {
		return l
	}
	l.Installation = docInt(d["installation"])
	l.RepoID = docInt(d["repoId"])
	l.Repo, _ = d["repo"].(string)
	l.Dir, _ = d["dir"].(string)
	l.Main, _ = d["main"].(string)
	l.Head, _ = d["head"].(string)
	l.Lease, _ = d["lease"].(string)
	l.Error, _ = d["error"].(string)
	l.Synced = docTime(d["synced"])
	l.Public, _ = d["public"].(bool)
	l.Slugs = stringMap(d["slugs"])
	l.FileKeys = stringMap(d["fileKeys"])
	if m, ok := d["conflicts"].(map[string]any); ok {
		l.Conflicts = m
	}
	bs, _ := d["branches"].(map[string]any)
	for name, v := range bs {
		m, _ := v.(map[string]any)
		if m == nil {
			continue
		}
		b := &topicBranch{Name: name, Copies: map[string]topicCopy{}, Conflicts: map[string]any{}}
		b.Ref, _ = m["ref"].(string)
		b.Base, _ = m["base"].(string)
		b.Head, _ = m["head"].(string)
		b.State, _ = m["state"].(string)
		b.Folder, _ = m["folder"].(string)
		b.By, _ = m["by"].(string)
		b.PR = docInt(m["pr"])
		b.PRURL, _ = m["prUrl"].(string)
		b.Created = millis(m["created"])
		cs, _ := m["copies"].(map[string]any)
		for main, cv := range cs {
			cm, _ := cv.(map[string]any)
			deck, _ := cm["deck"].(string)
			base, _ := cm["base"].(string)
			if deck != "" {
				b.Copies[main] = topicCopy{deck, base}
			}
		}
		if cf, ok := m["conflicts"].(map[string]any); ok {
			b.Conflicts = cf
		}
		l.Branches[name] = b
	}
	return l
}

func stringMap(v any) map[string]string {
	out := map[string]string{}
	m, _ := v.(map[string]any)
	for k, x := range m {
		if s, ok := x.(string); ok {
			out[k] = s
		}
	}
	return out
}

func (b *topicBranch) doc() Doc {
	copies := Doc{}
	for main, cp := range b.Copies {
		copies[main] = Doc{"deck": cp.Deck, "base": cp.Base}
	}
	conflicts := Doc{}
	for k, v := range b.Conflicts {
		conflicts[k] = v
	}
	return Doc{"ref": b.Ref, "base": b.Base, "head": b.Head, "state": b.State, "folder": b.Folder, "by": b.By,
		"pr": b.PR, "prUrl": b.PRURL, "created": b.Created, "copies": copies, "conflicts": conflicts}
}

// save writes what a sync changed (the lease is not touched)
func (c *cloudGit) saveTopic(ctx context.Context, l *topicLink) error {
	bs := Doc{}
	for name, b := range l.Branches {
		bs[name] = b.doc()
	}
	conflicts := Doc{}
	for k, v := range l.Conflicts {
		conflicts[k] = v
	}
	return c.db.Update(ctx, topicLinkCol, l.id, Doc{"uid": l.uid, "room": l.room, "installation": l.Installation, "repoId": l.RepoID,
		"repo": l.Repo, "dir": l.Dir, "main": l.Main, "head": l.Head, "slugs": stringDoc(l.Slugs), "fileKeys": stringDoc(l.FileKeys),
		"conflicts": conflicts, "branches": bs})
}

func (l *topicLink) linked() bool { return l.Repo != "" && l.Installation != 0 && l.RepoID != 0 }

// the folder's prefix of a repository path: "" or "a/b/"
func (l *topicLink) prefix() string {
	if l.Dir == "" {
		return ""
	}
	return l.Dir + "/"
}

// topicDir: the folder as kept, "" for the repository's top; false when it
// cannot be one
func topicDir(s string) (string, bool) {
	s = strings.Trim(strings.TrimSpace(s), "/")
	if s == "" {
		return "", true
	}
	s = path.Clean(s)
	if !gitSafeFile(s) || strings.HasPrefix(s, ".") {
		return "", false
	}
	return s, true
}

// --- the lease (one sync of a topic at a time, across the instances)

func (c *cloudGit) holdTopic(ctx context.Context, id string) (string, bool, error) {
	d, err := c.db.Get(ctx, topicLinkCol, id)
	if err != nil {
		return "", false, err
	}
	was, _ := d["lease"].(string)
	if _, until, _ := strings.Cut(was, "|"); until != "" {
		if ms, err := strconv.ParseInt(until, 10, 64); err == nil && c.now().UnixMilli() < ms {
			return "", false, nil
		}
	}
	b := make([]byte, 8)
	rand.Read(b)
	mine := hex.EncodeToString(b) + "|" + strconv.FormatInt(c.now().Add(cloudGitLease).UnixMilli(), 10)
	ok, err := c.db.UpdateIf(ctx, topicLinkCol, id, "lease", was, Doc{"lease": mine})
	return mine, ok, err
}

func (c *cloudGit) releaseTopic(id, lease string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.db.UpdateIf(ctx, topicLinkCol, id, "lease", lease, Doc{"lease": ""}); err != nil {
		c.logf("git: topic %s: lease not let go: %v", id, err)
	}
}

// withTopic runs fn on the linked topic, holding it; the link is saved
// after fn (also when it failed: what was done stays noted)
func (c *cloudGit) withTopic(ctx context.Context, uid, room string, fn func(l *topicLink, r *ghRepoAt) error) error {
	id := topicLinkID(uid, room)
	l, err := c.topicLink(ctx, uid, room)
	if err != nil {
		return err
	}
	if !l.linked() {
		return errTopicNotLinked
	}
	lease, ok, err := c.holdTopic(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return errTopicBusy
	}
	defer c.releaseTopic(id, lease)
	if l, err = c.topicLink(ctx, uid, room); err != nil {
		return err
	}
	r, err := c.app.at(ctx, l.Installation, l.RepoID, l.Repo)
	if err != nil {
		return topicAccessErr(err, l)
	}
	info, err := r.info(ctx)
	if err != nil {
		return topicAccessErr(err, l)
	}
	if !info.Private && !l.Public {
		err = errGitStop{publicStop(info.FullName)}
	} else {
		if info.FullName != "" && info.FullName != l.Repo {
			l.Repo, r.name = info.FullName, info.FullName
		}
		if l.Main == "" {
			l.Main = nonEmpty(info.DefaultBranch, "main")
		}
		err = fn(l, r)
	}
	note := Doc{"synced": c.now(), "error": ""}
	if err != nil {
		note = Doc{"error": err.Error(), "errorAt": c.now()}
		c.logf("git: topic %s: %v", id, err)
	}
	if serr := c.saveTopic(ctx, l); serr != nil && err == nil {
		err = serr
	}
	c.db.Update(ctx, topicLinkCol, id, note)
	return err
}

func topicAccessErr(err error, l *topicLink) error {
	switch ghStatus(err) {
	case 401, 403, 404:
		return errGitStop{fmt.Sprintf("Sliqtly's GitHub App cannot reach %s any more (uninstalled, or the repository taken out of it). Add it to the App on GitHub, or link the topic again.", l.Repo)}
	}
	return err
}

// --- the topic's presentations

// topicDecks: the account's presentations in the topic, by id → share.
// main: on main (no branch set, not new in an open branch's folder);
// branch: that branch's copies and new presentations.
type topicDecks struct {
	main   map[string]Doc
	branch map[string]map[string]Doc // branch name → deck → share
	// a deck filed in a branch's folder that is not in it yet: deck → branch
	adopt map[string]string
	view  layoutView
}

func (c *cloudGit) topicDecks(ctx context.Context, l *topicLink) (*topicDecks, error) {
	docs, ids, err := c.db.WhereEq(ctx, "shares", "owner", l.uid)
	if err != nil {
		return nil, err
	}
	ld, err := c.db.Get(ctx, layoutCol, l.uid)
	if err != nil {
		return nil, err
	}
	v := viewOf(layoutOf(ld))
	folders := map[string]string{} // folder → open branch
	for name, b := range l.Branches {
		if b.open() && b.Folder != "" {
			folders[b.Folder] = name
		}
	}
	out := &topicDecks{main: map[string]Doc{}, branch: map[string]map[string]Doc{}, adopt: map[string]string{}, view: v}
	for i, d := range docs {
		id := ids[i]
		if d == nil || !gitSafeID(id) {
			continue
		}
		if br, _ := d["branch"].(map[string]any); br != nil {
			room, _ := br["room"].(string)
			name, _ := br["name"].(string)
			if room != l.room || l.Branches[name] == nil || !l.Branches[name].open() {
				continue
			}
			if out.branch[name] == nil {
				out.branch[name] = map[string]Doc{}
			}
			out.branch[name][id] = d
			continue
		}
		if v.roomOf(pageDeck(id)) != l.room {
			continue
		}
		if name, ok := folders[v.folderOf(pageDeck(id))]; ok {
			if _, onMain := l.Slugs[id]; !onMain {
				out.adopt[id] = name
				continue
			}
		}
		out.main[id] = d
	}
	return out, nil
}

func (b *topicBranch) open() bool { return b.State == "open" || b.State == "review" }

// slugFor: the deck's slug, made from its name the first time
func (l *topicLink) slugFor(id, name string, remote map[string]string) string {
	if s := l.Slugs[id]; s != "" {
		return s
	}
	taken := map[string]bool{}
	for _, s := range l.Slugs {
		taken[s] = true
	}
	base := topicSlug(name)
	s := base
	for n := 2; taken[s] || remote[l.prefix()+s+".md"] != ""; n++ {
		s = fmt.Sprintf("%s-%d", base, n)
	}
	l.Slugs[id] = s
	return s
}

// topicSlug: a name as a file name, lower case letters, digits and -
func topicSlug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch r {
		case 'ä', 'å', 'á', 'à', 'â':
			r = 'a'
		case 'ö', 'ó', 'ò', 'ô', 'ø':
			r = 'o'
		case 'ü', 'ú', 'ù', 'û':
			r = 'u'
		case 'é', 'è', 'ê', 'ë':
			r = 'e'
		}
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= 60 {
			break
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" || strings.EqualFold(s, "readme") {
		return "presentation"
	}
	return s
}

// the slug a repository path is of ("" none): <prefix><slug>.md or <prefix><slug>/…
func (l *topicLink) slugOf(p string) string {
	rest, ok := strings.CutPrefix(p, l.prefix())
	if !ok || strings.HasPrefix(rest, ".") {
		return ""
	}
	if s, ok := strings.CutSuffix(rest, ".md"); ok && !strings.Contains(s, "/") {
		if strings.EqualFold(s, "readme") {
			return ""
		}
		return s
	}
	if s, _, ok := strings.Cut(rest, "/"); ok {
		return s
	}
	return ""
}

// --- the files

// topicSnap is a side's presentations as the repository's files.
type topicSnap struct {
	files  map[string]gitWant
	keys   map[string]string
	slugs  map[string]bool // the slugs it holds
	names  map[string]string
	tooBig []string
	left   map[string]bool
}

// put a deck's files into s: store is the share whose Storage holds its
// files, meta the id deck.json names (main's, for a copy)
func (c *cloudGit) topicDeckFiles(ctx context.Context, l *topicLink, s *topicSnap, remote map[string]string, slug, store, meta string, doc Doc) {
	dir := l.prefix() + slug + "/"
	put := func(p string, b []byte) {
		id := rgit.ID("blob", b)
		w := gitWant{sha: id}
		if remote[p] != id {
			w.data = b
		}
		s.files[p] = w
	}
	s.slugs[slug] = true
	name, _ := doc["name"].(string)
	s.names[slug] = name
	md, _ := doc["md"].(string)
	put(l.prefix()+slug+".md", []byte(md))
	m := pick(doc, "name", "theme")
	m["id"] = meta
	put(dir+"deck.json", gitJSON(m, deckFiles(doc["files"])))
	if css, _ := doc["css"].(string); css != "" {
		put(dir+"theme.css", []byte(css))
	}
	for _, x := range asList(doc["files"]) {
		f, _ := x.(map[string]any)
		fp, _ := f["path"].(string)
		if !gitSafeFile(fp) || deckMeta[fp] {
			continue
		}
		p := dir + fp
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
		b, err := c.bucket.Read(ctx, "shares/"+store+"/"+fp, cloudGitMaxFile+1)
		if err != nil {
			c.logf("git: topic %s: %s not read: %v", l.id, p, err)
			if sha := remote[p]; sha != "" {
				s.files[p] = gitWant{sha: sha}
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

func newTopicSnap() *topicSnap {
	return &topicSnap{files: map[string]gitWant{}, keys: map[string]string{}, slugs: map[string]bool{}, names: map[string]string{}, left: map[string]bool{}}
}

// entries: what to write over remote so that the slugs in `owned` are as s
// has them; the files of an owned slug that s does not have go
func (l *topicLink) entries(ctx context.Context, r *ghRepoAt, s *topicSnap, remote map[string]string, owned map[string]bool) ([]ghEntry, error) {
	var out []ghEntry
	for p, f := range s.files {
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
		out = append(out, ghEntry{Path: p, Mode: "100644", Type: "blob", SHA: &sha})
	}
	for p := range remote {
		if _, ok := s.files[p]; ok || s.left[p] {
			continue
		}
		if slug := l.slugOf(p); slug != "" && owned[slug] {
			out = append(out, ghEntry{Path: p, Mode: "100644", Type: "blob", SHA: nil})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// changes: the commit message's lines for what entries do
func topicDescribe(l *topicLink, entries []ghEntry, names map[string]string) string {
	added, changed, removed := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, e := range entries {
		slug := l.slugOf(e.Path)
		if slug == "" {
			continue
		}
		switch {
		case e.SHA == nil && strings.HasSuffix(e.Path, ".md") && !strings.Contains(strings.TrimPrefix(e.Path, l.prefix()), "/"):
			removed[slug] = true
		default:
			changed[slug] = true
		}
	}
	for s := range removed {
		delete(changed, s)
	}
	var lines []string
	for _, group := range []struct {
		set  map[string]bool
		verb string
	}{{added, "added"}, {changed, "changed"}, {removed, "removed"}} {
		var list []string
		for s := range group.set {
			list = append(list, s)
		}
		sort.Strings(list)
		for _, s := range list {
			label := s
			if n := names[s]; n != "" {
				label = fmt.Sprintf("%q (%s)", n, s)
			}
			lines = append(lines, "- "+label+" "+group.verb)
		}
	}
	if len(lines) == 0 {
		return "Sliqtly: topic synced"
	}
	return plural(len(lines), "presentation", "presentations") + " changed\n\n" + strings.Join(lines, "\n")
}

// commitOver writes entries over the commit at head on branch → the new commit
func (c *cloudGit) commitOver(ctx context.Context, r *ghRepoAt, l *topicLink, branch, head, tree, msg, author string, parents []string, entries []ghEntry) (string, error) {
	t, err := r.putTree(ctx, tree, entries)
	if err != nil {
		return "", err
	}
	who := ghPerson{Name: nonEmpty(author, "Sliqtly"), Email: "sliqtly@users.noreply.github.com", Date: c.now()}
	if gl, _, err := c.link(ctx, l.uid); err == nil && gl.GitHubID != 0 {
		who.Email = gitNoreply(gl)
		who.Name = nonEmpty(author, gl.Login)
	}
	commit, err := r.putCommit(ctx, strings.TrimRight(msg, "\n")+"\n", t, parents, who)
	if err != nil {
		return "", err
	}
	if err := r.moveBranch(ctx, branch, commit, true); err != nil {
		return "", err
	}
	return commit, nil
}

// --- main

// topicSyncResult is what one sync or update did.
type topicSyncResult struct {
	Commit    string   `json:"commit"`
	Head      string   `json:"head"`
	Imported  []string `json:"imported"`
	Created   []string `json:"created"`
	Conflicts []string `json:"conflicts"`
	TooBig    []string `json:"tooBig,omitempty"`
	// merge: the pull request, and whether the branch is in main now
	PR     int64  `json:"pr,omitempty"`
	PRURL  string `json:"prUrl,omitempty"`
	Merged bool   `json:"merged,omitempty"`
}

func newTopicResult() *topicSyncResult {
	return &topicSyncResult{Imported: []string{}, Created: []string{}, Conflicts: []string{}}
}

// syncMain: the topic's presentations with the main branch
func (c *cloudGit) syncMain(ctx context.Context, l *topicLink, r *ghRepoAt, message, author string, res *topicSyncResult) error {
	for try := 0; ; try++ {
		err := c.syncMainOnce(ctx, l, r, message, author, res)
		if (ghStatus(err) != 422 && ghStatus(err) != 409) || try == 2 {
			return err
		}
	}
}

func (c *cloudGit) syncMainOnce(ctx context.Context, l *topicLink, r *ghRepoAt, message, author string, res *topicSyncResult) error {
	head, err := r.head(ctx, l.Main)
	if err != nil {
		return err
	}
	if head == "" {
		if head, err = c.firstCommit(ctx, r, l.Main); err != nil {
			return err
		}
	}
	hc, err := r.commit(ctx, head)
	if err != nil {
		return err
	}
	remote, err := r.files(ctx, hc.Tree.SHA)
	if err != nil {
		return err
	}
	if head != l.Head {
		if err := c.topicTakeIn(ctx, l, r, "", l.Head, head, remote, res); err != nil {
			return err
		}
	}
	decks, err := c.topicDecks(ctx, l)
	if err != nil {
		return err
	}
	s := newTopicSnap()
	ids := sortedKeys(decks.main)
	for _, id := range ids {
		d := decks.main[id]
		name, _ := d["name"].(string)
		c.topicDeckFiles(ctx, l, s, remote, l.slugFor(id, name, remote), id, id, d)
	}
	res.TooBig = s.tooBig
	// the slugs main had: a presentation gone from the topic goes from it
	owned := map[string]bool{}
	for id, slug := range l.Slugs {
		owned[slug] = true
		if _, ok := decks.main[id]; !ok && !l.inBranch(id) {
			delete(l.Slugs, id)
		}
	}
	for k, v := range s.keys {
		l.FileKeys[k] = v
	}
	entries, err := l.entries(ctx, r, s, remote, owned)
	if err != nil {
		return err
	}
	l.Head = head
	res.Head = head
	if len(entries) == 0 {
		return nil
	}
	msg := topicDescribe(l, entries, s.names)
	if strings.TrimSpace(message) != "" {
		msg = strings.TrimRight(message, "\n") + "\n\n" + msg
	}
	commit, err := c.commitOver(ctx, r, l, l.Main, head, hc.Tree.SHA, msg, author, []string{head}, entries)
	if err != nil {
		return err
	}
	l.Head, res.Commit, res.Head = commit, commit, commit
	return nil
}

// inBranch: a deck new in an open branch (its slug stays while it is)
func (l *topicLink) inBranch(id string) bool {
	for _, b := range l.Branches {
		if !b.open() {
			continue
		}
		for _, cp := range b.Copies {
			if cp.Deck == id {
				return true
			}
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// topicTakeIn merges what a side (branch "" main) changed on GitHub from
// the commit `from` to `head` (its files remote) into its presentations
func (c *cloudGit) topicTakeIn(ctx context.Context, l *topicLink, r *ghRepoAt, branch, from, head string, remote map[string]string, res *topicSyncResult) error {
	base := map[string]string{}
	if from != "" {
		bc, err := r.commit(ctx, from)
		switch {
		case ghStatus(err) == 404 || ghStatus(err) == 422:
		case err != nil:
			return err
		default:
			if base, err = r.files(ctx, bc.Tree.SHA); err != nil {
				return err
			}
		}
	}
	changed := map[string]bool{}
	for p, sha := range remote {
		if s := l.slugOf(p); s != "" && base[p] != sha {
			changed[s] = true
		}
	}
	for p := range base {
		if s := l.slugOf(p); s != "" {
			if _, ok := remote[p]; !ok {
				changed[s] = true
			}
		}
	}
	decks, err := c.topicDecks(ctx, l)
	if err != nil {
		return err
	}
	bySlug := map[string]string{}
	for id, slug := range l.Slugs {
		bySlug[slug] = id
	}
	for _, slug := range sortedKeys(changed) {
		if err := c.topicTakeSlug(ctx, l, r, decks, branch, slug, bySlug[slug], head, base, remote, res); err != nil {
			return fmt.Errorf("%s from GitHub: %w", slug, err)
		}
	}
	return nil
}

func (c *cloudGit) topicTakeSlug(ctx context.Context, l *topicLink, r *ghRepoAt, decks *topicDecks, branch, slug, known, head string, base, remote map[string]string, res *topicSyncResult) error {
	p := l.prefix()
	theirsMD, inRemote, err := c.textAt(ctx, r, remote, p+slug+".md")
	if err != nil {
		return err
	}
	if !inRemote {
		return nil // removed on GitHub: kept here
	}
	baseMD, _, err := c.textAt(ctx, r, base, p+slug+".md")
	if err != nil {
		return err
	}
	theirsCSS, _, err := c.textAt(ctx, r, remote, p+slug+"/theme.css")
	if err != nil {
		return err
	}
	baseCSS, _, err := c.textAt(ctx, r, base, p+slug+"/theme.css")
	if err != nil {
		return err
	}
	var meta, baseMeta map[string]any
	if s, ok, err := c.textAt(ctx, r, remote, p+slug+"/deck.json"); err != nil {
		return err
	} else if ok {
		json.Unmarshal([]byte(s), &meta)
	}
	if s, ok, err := c.textAt(ctx, r, base, p+slug+"/deck.json"); err != nil {
		return err
	} else if ok {
		json.Unmarshal([]byte(s), &baseMeta)
	}
	metaID, _ := meta["id"].(string)
	// which presentation of this side the slug is
	id := ""
	if branch == "" {
		switch {
		case known != "" && decks.main[known] != nil:
			id = known
		case metaID != "" && decks.main[metaID] != nil:
			id = metaID
		case metaID != "":
			// new in a branch that was merged: the presentation is main's now
			for name, ds := range decks.branch {
				if d := ds[metaID]; d != nil && !l.isCopy(name, metaID) {
					if err := c.promote(ctx, l, metaID); err != nil {
						return err
					}
					decks.main[metaID] = d
					id = metaID
				}
			}
		}
		if id == "" {
			made, err := c.topicMakeDeck(ctx, l, r, "", slug, theirsMD, theirsCSS, meta, remote, res)
			if err != nil || made == "" {
				return err
			}
			l.Slugs[made] = slug
			return nil
		}
	} else {
		b := l.Branches[branch]
		mainID := known
		if mainID == "" {
			mainID = metaID
		}
		if cp, ok := b.Copies[mainID]; ok {
			id = cp.Deck
		} else if d := decks.branch[branch][metaID]; d != nil {
			id = metaID
		} else if known != "" && decks.main[known] != nil {
			// changed on GitHub before it was opened here: its copy takes GitHub's text
			cp, err := c.topicCopy(ctx, l, b, known, decks.main[known])
			if err != nil {
				return err
			}
			id = cp.Deck
			if err := c.db.Update(ctx, "shares", id, Doc{"md": theirsMD}); err != nil {
				return err
			}
			res.Imported = append(res.Imported, id)
			return nil
		} else {
			made, err := c.topicMakeDeck(ctx, l, r, branch, slug, theirsMD, theirsCSS, meta, remote, res)
			if err != nil || made == "" {
				return err
			}
			l.Slugs[made] = slug
			return nil
		}
	}
	cur, err := c.db.Get(ctx, "shares", id)
	if err != nil {
		return err
	}
	if cur == nil {
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
	for _, k := range []string{"name", "theme"} {
		t, _ := meta[k].(string)
		b, _ := baseMeta[k].(string)
		o, _ := cur[k].(string)
		if t != "" && t != b && o == b && t != o {
			patch[k] = t
		}
	}
	files, err := c.topicTakeFiles(ctx, r, l, id, slug, cur, base, remote)
	if err != nil {
		return err
	}
	if files != nil {
		patch["files"] = files
	}
	if mdClash || cssClash {
		res.Conflicts = append(res.Conflicts, id)
		cf := Doc{"sha": head, "at": c.now(), "md": theirsMD, "css": theirsCSS, "mdClash": mdClash, "cssClash": cssClash, "slug": slug}
		if branch == "" {
			l.Conflicts[id] = cf
		} else {
			cf["github"] = true
			l.Branches[branch].Conflicts[id] = cf
		}
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
		return fmt.Errorf("the presentation changed while it was merged; it is taken in on the next sync")
	}
	res.Imported = append(res.Imported, id)
	return nil
}

func (l *topicLink) isCopy(branch, deck string) bool {
	for _, cp := range l.Branches[branch].Copies {
		if cp.Deck == deck {
			return true
		}
	}
	return false
}

// topicTakeFiles: as takeFiles (cloudgit.go), from the slug's folder
func (c *cloudGit) topicTakeFiles(ctx context.Context, r *ghRepoAt, l *topicLink, id, slug string, cur Doc, base, remote map[string]string) ([]any, error) {
	dir := l.prefix() + slug + "/"
	list := asList(cur["files"])
	at := map[string]int{}
	for i, x := range list {
		f, _ := x.(map[string]any)
		if p, _ := f["path"].(string); p != "" {
			at[p] = i
		}
	}
	var out []any
	for _, p := range sortedKeys(remote) {
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
		f, err := c.storeFile(ctx, id, fp, b)
		if err != nil {
			return nil, err
		}
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

// storeFile writes a presentation's file to Storage → its entry in files
func (c *cloudGit) storeFile(ctx context.Context, deck, fp string, b []byte) (map[string]any, error) {
	typ := mime.TypeByExtension(path.Ext(fp))
	if typ == "" {
		typ = "application/octet-stream"
	}
	token := newUUID()
	name := "shares/" + deck + "/" + fp
	if err := c.bucket.Save(ctx, name, typ, b, map[string]string{"firebaseStorageDownloadTokens": token}); err != nil {
		return nil, err
	}
	return map[string]any{"path": fp, "type": typ, "size": int64(len(b)), "url": storageURL(c.bucket.Name(), name, token)}, nil
}

// topicMakeDeck: a presentation added on GitHub becomes the account's, in
// the topic (branch "" on main, else new in that branch) → its id
func (c *cloudGit) topicMakeDeck(ctx context.Context, l *topicLink, r *ghRepoAt, branch, slug, md, css string, meta map[string]any, remote map[string]string, res *topicSyncResult) (string, error) {
	name, _ := meta["name"].(string)
	if strings.TrimSpace(name) == "" {
		name = deckTitle(md, slug)
	}
	theme, _ := meta["theme"].(string)
	id := newShareID()
	doc := Doc{"name": name, "md": md, "theme": theme, "owner": l.uid, "visibility": "private", "deck": "github", "source": "github",
		"files": []any{}, "created": c.db.ServerTime(), "updated": c.db.ServerTime()}
	if css != "" {
		doc["css"] = css
	} else {
		doc["css"] = nil
	}
	if branch != "" {
		doc["branch"] = Doc{"room": l.room, "name": branch, "of": ""}
	}
	files, err := c.topicTakeFiles(ctx, r, l, id, slug, Doc{}, map[string]string{}, remote)
	if err != nil {
		return "", err
	}
	if files != nil {
		doc["files"] = files
	}
	if had, err := c.db.Create(ctx, "shares", id, doc); err != nil || had != nil {
		return "", err
	}
	folder := ""
	if branch != "" {
		folder = l.Branches[branch].Folder
	}
	if err := c.placeDeck(ctx, l, id, folder, ""); err != nil {
		return "", err
	}
	res.Created = append(res.Created, id)
	return id, nil
}

// placeDeck puts the deck in the topic (in folder, "" at its top), with
// the story of `like` ("" a new draft's)
func (c *cloudGit) placeDeck(ctx context.Context, l *topicLink, deck, folder, like string) error {
	ls := &layoutStore{db: c.db, now: c.now}
	_, err := ls.change(ctx, l.uid, func(lay *layoutDoc, now int64) error {
		lay.set("deck/"+pageDeck(deck), l.room, now)
		if folder != "" {
			lay.set("filed/"+pageDeck(deck), folder, now)
		} else {
			lay.remove("filed/"+pageDeck(deck), now)
		}
		if like != "" {
			if s, ok := lay.get("story/" + pageDeck(like)); ok {
				lay.set("story/"+pageDeck(deck), s, now)
			}
		} else if _, ok := lay.get("story/" + pageDeck(deck)); !ok {
			st := store.StoryOf(nil)
			st.Phase = store.NewStoryPhase
			b, _ := json.Marshal(store.StoryDoc(st))
			lay.set("story/"+pageDeck(deck), string(b), now)
		}
		return nil
	})
	return err
}

// promote: a presentation new in a branch, now merged, is main's
func (c *cloudGit) promote(ctx context.Context, l *topicLink, id string) error {
	if err := c.db.Update(ctx, "shares", id, Doc{"branch": nil}); err != nil {
		return err
	}
	return c.placeDeck(ctx, l, id, "", id)
}

// --- branches

// newBranch makes a branch of the topic at main's commit
func (c *cloudGit) newBranch(ctx context.Context, l *topicLink, r *ghRepoAt, name, by string, res *topicSyncResult) (*topicBranch, error) {
	name = strings.Join(strings.Fields(name), " ")
	if !topicBranchName.MatchString(name) {
		return nil, roomErr{"a branch's name is letters, digits, spaces, . _ and - (at most 60)"}
	}
	if b := l.Branches[name]; b != nil && b.open() {
		return nil, roomErr{fmt.Sprintf("the topic has a branch %q already", name)}
	}
	open := 0
	for _, b := range l.Branches {
		if b.open() {
			open++
		}
	}
	if open >= topicMaxBranches {
		return nil, roomErr{fmt.Sprintf("a topic keeps at most %d branches open", topicMaxBranches)}
	}
	if err := c.syncMain(ctx, l, r, "", by, res); err != nil {
		return nil, err
	}
	ref := topicRefPrefix + topicSlug(name)
	for n := 2; ; n++ {
		at, err := r.head(ctx, ref)
		if err != nil {
			return nil, err
		}
		if at == "" {
			break
		}
		ref = fmt.Sprintf("%s%s-%d", topicRefPrefix, topicSlug(name), n)
	}
	if err := r.moveBranch(ctx, ref, l.Head, false); err != nil {
		return nil, err
	}
	folder, err := c.branchFolder(ctx, l, "⎇ "+name)
	if err != nil {
		return nil, err
	}
	b := &topicBranch{Name: name, Ref: ref, Base: l.Head, Head: l.Head, State: "open", Folder: folder, By: by,
		Created: c.now().UnixMilli(), Copies: map[string]topicCopy{}, Conflicts: map[string]any{}}
	l.Branches[name] = b
	return b, nil
}

// branchFolder: the topic's folder of that name (made when it has none) → its id
func (c *cloudGit) branchFolder(ctx context.Context, l *topicLink, name string) (string, error) {
	ls := &layoutStore{db: c.db, now: c.now}
	id := ""
	_, err := ls.change(ctx, l.uid, func(lay *layoutDoc, now int64) error {
		v := viewOf(*lay)
		next, fid, _, err := store.AddFolder(v.folders[l.room], name, store.NewFolderID)
		if err != nil {
			return err
		}
		id = fid
		b, _ := json.Marshal(store.FolderDocs(next))
		lay.set("folders/"+l.room, string(b), now)
		return nil
	})
	return id, err
}

// dropFolder removes the branch's folder; what is left in it goes to the topic's top
func (c *cloudGit) dropFolder(ctx context.Context, l *topicLink, folder string) error {
	ls := &layoutStore{db: c.db, now: c.now}
	_, err := ls.change(ctx, l.uid, func(lay *layoutDoc, now int64) error {
		v := viewOf(*lay)
		next, err := store.RemoveFolder(v.folders[l.room], folder)
		if err != nil {
			return nil // gone already
		}
		for deck, f := range v.filed {
			if f == folder {
				lay.remove("filed/"+deck, now)
			}
		}
		if len(next) == 0 {
			lay.remove("folders/"+l.room, now)
		} else {
			b, _ := json.Marshal(store.FolderDocs(next))
			lay.set("folders/"+l.room, string(b), now)
		}
		return nil
	})
	return err
}

// topicCopy: main's presentation's copy in the branch, made when it has none
func (c *cloudGit) topicCopy(ctx context.Context, l *topicLink, b *topicBranch, mainID string, main Doc) (topicCopy, error) {
	if cp, ok := b.Copies[mainID]; ok {
		return cp, nil
	}
	id := newShareID()
	doc := Doc{"owner": l.uid, "visibility": "private", "deck": "branch", "source": "branch", "files": []any{},
		"branch": Doc{"room": l.room, "name": b.Name, "of": mainID}, "created": c.db.ServerTime(), "updated": c.db.ServerTime()}
	for _, k := range []string{"name", "md", "css", "theme"} {
		doc[k] = main[k]
	}
	var files []any
	for _, x := range asList(main["files"]) {
		f, _ := x.(map[string]any)
		fp, _ := f["path"].(string)
		if !gitSafeFile(fp) {
			continue
		}
		data, err := c.bucket.Read(ctx, "shares/"+mainID+"/"+fp, cloudGitMaxFile+1)
		if err != nil {
			c.logf("git: topic %s: %s/%s not copied: %v", l.id, mainID, fp, err)
			continue
		}
		nf, err := c.storeFile(ctx, id, fp, data)
		if err != nil {
			return topicCopy{}, err
		}
		if t, _ := f["type"].(string); t != "" {
			nf["type"] = t
		}
		files = append(files, nf)
	}
	if files != nil {
		doc["files"] = files
	}
	if _, err := c.db.Create(ctx, "shares", id, doc); err != nil {
		return topicCopy{}, err
	}
	if err := c.placeDeck(ctx, l, id, b.Folder, mainID); err != nil {
		return topicCopy{}, err
	}
	// what main had when the copy was made is what it merges against
	cp := topicCopy{Deck: id, Base: l.Head}
	b.Copies[mainID] = cp
	return cp, nil
}

// syncBranch: the branch's copies and new presentations with its GitHub branch
func (c *cloudGit) syncBranch(ctx context.Context, l *topicLink, r *ghRepoAt, b *topicBranch, message, author string, res *topicSyncResult) error {
	for try := 0; ; try++ {
		err := c.syncBranchOnce(ctx, l, r, b, message, author, res)
		if (ghStatus(err) != 422 && ghStatus(err) != 409) || try == 2 {
			return err
		}
	}
}

func (c *cloudGit) syncBranchOnce(ctx context.Context, l *topicLink, r *ghRepoAt, b *topicBranch, message, author string, res *topicSyncResult) error {
	head, err := r.head(ctx, b.Ref)
	if err != nil {
		return err
	}
	if head == "" {
		return errGitStop{fmt.Sprintf("The branch %s was removed on GitHub. Drop the branch here, or make it again.", b.Ref)}
	}
	hc, err := r.commit(ctx, head)
	if err != nil {
		return err
	}
	remote, err := r.files(ctx, hc.Tree.SHA)
	if err != nil {
		return err
	}
	if head != b.Head {
		if err := c.topicTakeIn(ctx, l, r, b.Name, b.Head, head, remote, res); err != nil {
			return err
		}
	}
	s, owned, err := c.branchSnap(ctx, l, b, remote)
	if err != nil {
		return err
	}
	res.TooBig = s.tooBig
	entries, err := l.entries(ctx, r, s, remote, owned)
	if err != nil {
		return err
	}
	b.Head = head
	res.Head = head
	if len(entries) == 0 {
		return nil
	}
	msg := topicDescribe(l, entries, s.names)
	if strings.TrimSpace(message) != "" {
		msg = strings.TrimRight(message, "\n") + "\n\n" + msg
	}
	commit, err := c.commitOver(ctx, r, l, b.Ref, head, hc.Tree.SHA, msg, author, []string{head}, entries)
	if err != nil {
		return err
	}
	b.Head, res.Commit, res.Head = commit, commit, commit
	return nil
}

// branchSnap: the branch's own presentations as files, and the slugs they own
func (c *cloudGit) branchSnap(ctx context.Context, l *topicLink, b *topicBranch, remote map[string]string) (*topicSnap, map[string]bool, error) {
	decks, err := c.topicDecks(ctx, l)
	if err != nil {
		return nil, nil, err
	}
	// presentations put in the branch's folder are new in the branch
	for id, name := range decks.adopt {
		if name != b.Name {
			continue
		}
		mark := Doc{"room": l.room, "name": b.Name, "of": ""}
		if err := c.db.Update(ctx, "shares", id, Doc{"branch": mark}); err != nil {
			return nil, nil, err
		}
		d, err := c.db.Get(ctx, "shares", id)
		if err != nil || d == nil {
			continue
		}
		if decks.branch[b.Name] == nil {
			decks.branch[b.Name] = map[string]Doc{}
		}
		decks.branch[b.Name][id] = d
	}
	s := newTopicSnap()
	owned := map[string]bool{}
	copyOf := map[string]string{}
	for main, cp := range b.Copies {
		copyOf[cp.Deck] = main
	}
	mine := decks.branch[b.Name]
	for _, id := range sortedKeys(mine) {
		d := mine[id]
		name, _ := d["name"].(string)
		meta, storeID := id, id
		slug := ""
		if main, ok := copyOf[id]; ok {
			meta = main
			slug = l.Slugs[main]
		}
		if slug == "" {
			slug = l.slugFor(id, name, remote)
		}
		owned[slug] = true
		c.topicDeckFiles(ctx, l, s, remote, slug, storeID, meta, d)
	}
	for k, v := range s.keys {
		l.FileKeys[k] = v
	}
	return s, owned, nil
}

// updateBranch brings main's changes into the branch: each copy merged
// with what main has now; when no conflict is left, the branch gets a merge
// commit with main
func (c *cloudGit) updateBranch(ctx context.Context, l *topicLink, r *ghRepoAt, b *topicBranch, author string, res *topicSyncResult) error {
	if err := c.syncMain(ctx, l, r, "", author, res); err != nil {
		return err
	}
	if err := c.syncBranch(ctx, l, r, b, "", author, res); err != nil {
		return err
	}
	if len(b.Conflicts) > 0 {
		res.Conflicts = append(res.Conflicts, sortedKeys(b.Conflicts)...)
		return nil
	}
	mainHead := l.Head
	texts := map[string]map[string]string{} // commit → its files
	filesAt := func(sha string) (map[string]string, error) {
		if f, ok := texts[sha]; ok {
			return f, nil
		}
		cm, err := r.commit(ctx, sha)
		if err != nil {
			return nil, err
		}
		f, err := r.files(ctx, cm.Tree.SHA)
		texts[sha] = f
		return f, err
	}
	for _, mainID := range sortedKeys(b.Copies) {
		cp := b.Copies[mainID]
		if cp.Base == mainHead {
			continue
		}
		main, err := c.db.Get(ctx, "shares", mainID)
		if err != nil {
			return err
		}
		mine, err := c.db.Get(ctx, "shares", cp.Deck)
		if err != nil {
			return err
		}
		if mine == nil {
			delete(b.Copies, mainID)
			continue
		}
		if main == nil {
			continue // gone from main: the branch's copy stays and goes back in with the merge
		}
		baseFiles, err := filesAt(cp.Base)
		if err != nil {
			return err
		}
		slug := l.Slugs[mainID]
		baseMD, _, err := c.textAt(ctx, r, baseFiles, l.prefix()+slug+".md")
		if err != nil {
			return err
		}
		baseCSS, _, err := c.textAt(ctx, r, baseFiles, l.prefix()+slug+"/theme.css")
		if err != nil {
			return err
		}
		mainMD, _ := main["md"].(string)
		mainCSS, _ := main["css"].(string)
		oursMD, _ := mine["md"].(string)
		oursCSS, _ := mine["css"].(string)
		md, mdClash := merge3Text(baseMD, oursMD, mainMD)
		css, cssClash := merge3Text(baseCSS, oursCSS, mainCSS)
		if mdClash || cssClash {
			b.Conflicts[cp.Deck] = Doc{"md": mainMD, "css": mainCSS, "base": cp.Base, "main": mainHead, "mdClash": mdClash, "cssClash": cssClash, "at": c.now(), "slug": slug}
			res.Conflicts = append(res.Conflicts, cp.Deck)
			continue
		}
		patch := Doc{}
		if md != oursMD {
			patch["md"] = md
		}
		if css != oursCSS {
			patch["css"] = css
		}
		if len(patch) > 0 {
			patch["updated"] = c.db.ServerTime()
			ok, err := c.db.UpdateIf(ctx, "shares", cp.Deck, "md", oursMD, patch)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("a presentation of the branch changed while it was merged; update again")
			}
			res.Imported = append(res.Imported, cp.Deck)
		}
		b.Copies[mainID] = topicCopy{cp.Deck, mainHead}
	}
	if len(res.Conflicts) > 0 {
		return nil
	}
	return c.mergeMainIn(ctx, l, r, b, author, res)
}

// mergeMainIn: a commit on the branch whose parents are the branch and
// main, with main's files and the branch's own over them
func (c *cloudGit) mergeMainIn(ctx context.Context, l *topicLink, r *ghRepoAt, b *topicBranch, author string, res *topicSyncResult) error {
	head, err := r.head(ctx, b.Ref)
	if err != nil {
		return err
	}
	mainHead, err := r.head(ctx, l.Main)
	if err != nil {
		return err
	}
	if mainHead != l.Head {
		return errors.New("main moved on GitHub meanwhile; update again")
	}
	if head == mainHead || c.isAncestor(ctx, r, mainHead, head) {
		b.Base = mainHead
		return nil // the branch has main in it already
	}
	mc, err := r.commit(ctx, mainHead)
	if err != nil {
		return err
	}
	mainFiles, err := r.files(ctx, mc.Tree.SHA)
	if err != nil {
		return err
	}
	s, owned, err := c.branchSnap(ctx, l, b, mainFiles)
	if err != nil {
		return err
	}
	entries, err := l.entries(ctx, r, s, mainFiles, owned)
	if err != nil {
		return err
	}
	msg := fmt.Sprintf("Merge %s into %s", l.Main, b.Ref)
	commit, err := c.commitOver(ctx, r, l, b.Ref, head, mc.Tree.SHA, msg, author, []string{head, mainHead}, entries)
	if err != nil {
		return err
	}
	b.Base, b.Head, res.Commit, res.Head = mainHead, commit, commit, commit
	return nil
}

// isAncestor: a is head or before it, along first parents and merges (a
// short walk: a branch's history since main is short)
func (c *cloudGit) isAncestor(ctx context.Context, r *ghRepoAt, a, head string) bool {
	seen := map[string]bool{}
	todo := []string{head}
	for n := 0; len(todo) > 0 && n < 200; n++ {
		at := todo[0]
		todo = todo[1:]
		if at == a {
			return true
		}
		if seen[at] {
			continue
		}
		seen[at] = true
		cm, err := r.commit(ctx, at)
		if err != nil {
			return false
		}
		for _, p := range cm.Parents {
			todo = append(todo, p.SHA)
		}
	}
	return false
}

// mergeBranch: the branch into main, through a pull request when the App
// may open one, else by moving main forward to it
func (c *cloudGit) mergeBranch(ctx context.Context, l *topicLink, r *ghRepoAt, b *topicBranch, author string, res *topicSyncResult) error {
	if err := c.updateBranch(ctx, l, r, b, author, res); err != nil {
		return err
	}
	if len(res.Conflicts) > 0 {
		return nil
	}
	title := fmt.Sprintf("%s: %s", nonEmpty(c.roomTitle(ctx, l), "Topic"), b.Name)
	pr, err := c.app.pullRequest(ctx, l, b.Ref, title)
	switch {
	case err == nil:
		b.PR, b.PRURL = pr.Number, pr.URL
		res.PR, res.PRURL = pr.Number, pr.URL
		merged, err := c.app.mergePull(ctx, l, pr.Number, b.Head)
		if err != nil {
			return err
		}
		if !merged {
			// required reviews or checks: it is merged on GitHub, and the
			// push to main finishes it here
			b.State = "review"
			return nil
		}
	case errors.Is(err, errNoPullRights):
		// main to the branch's commit, which has main as a parent
		if err := r.moveBranch(ctx, l.Main, b.Head, true); err != nil {
			return err
		}
	default:
		return err
	}
	return c.finishMerge(ctx, l, r, b, author, res)
}

// finishMerge: main has the branch: main's presentations take its text in,
// the copies go
func (c *cloudGit) finishMerge(ctx context.Context, l *topicLink, r *ghRepoAt, b *topicBranch, author string, res *topicSyncResult) error {
	if err := c.syncMain(ctx, l, r, "", author, res); err != nil {
		return err
	}
	for _, cp := range b.Copies {
		c.removeDeck(ctx, l.uid, cp.Deck)
	}
	// what was new in the branch is main's now (also when GitHub's merge left it out)
	decks, err := c.topicDecks(ctx, l)
	if err == nil {
		for id := range decks.branch[b.Name] {
			c.promote(ctx, l, id)
		}
	}
	c.dropFolder(ctx, l, b.Folder)
	b.Copies, b.Conflicts = map[string]topicCopy{}, map[string]any{}
	b.State = "merged"
	res.Merged = true
	if err := r.call(ctx, http.MethodDelete, "/git/refs/heads/"+b.Ref, nil, nil); err != nil {
		c.logf("git: topic %s: %s not removed on GitHub: %v", l.id, b.Ref, err)
	}
	return nil
}

// dropBranch: the branch given up: its copies go, what was new in it stays
// in the topic, the GitHub branch stays for its history
func (c *cloudGit) dropBranch(ctx context.Context, l *topicLink, b *topicBranch) error {
	for _, cp := range b.Copies {
		c.removeDeck(ctx, l.uid, cp.Deck)
	}
	decks, err := c.topicDecks(ctx, l)
	if err != nil {
		return err
	}
	for id := range decks.branch[b.Name] {
		if err := c.promote(ctx, l, id); err != nil {
			return err
		}
	}
	if err := c.dropFolder(ctx, l, b.Folder); err != nil {
		return err
	}
	b.Copies, b.Conflicts = map[string]topicCopy{}, map[string]any{}
	b.State = "dropped"
	return nil
}

// removeDeck: a branch's copy deleted with its files and its layout facts
func (c *cloudGit) removeDeck(ctx context.Context, uid, id string) {
	if err := c.db.Delete(ctx, "shares", id); err != nil {
		c.logf("git: %s: copy %s not deleted: %v", uid, id, err)
		return
	}
	if rp, ok := c.bucket.(interface {
		RemovePrefix(ctx context.Context, prefix string) error
	}); ok {
		rp.RemovePrefix(ctx, "shares/"+id+"/")
	}
	ls := &layoutStore{db: c.db, now: c.now}
	ls.change(ctx, uid, func(lay *layoutDoc, now int64) error {
		for _, k := range []string{"deck/", "filed/", "story/", "pinned/"} {
			if _, ok := lay.get(k + pageDeck(id)); ok {
				lay.remove(k+pageDeck(id), now)
			}
		}
		return nil
	})
}

func (c *cloudGit) roomTitle(ctx context.Context, l *topicLink) string {
	ld, err := c.db.Get(ctx, layoutCol, l.uid)
	if err != nil {
		return ""
	}
	return viewOf(layoutOf(ld)).title(l.room)
}

// --- conflicts

// topicChunk is one piece of a conflict as the page shows it.
type topicChunk struct {
	Same   string `json:"same,omitempty"`
	Main   string `json:"main,omitempty"`
	Branch string `json:"branch,omitempty"`
	Clash  bool   `json:"clash,omitempty"`
}

// conflictChunks: a branch copy's conflict as pieces: the same text, and
// where both changed it, main's and the branch's
func (c *cloudGit) conflictChunks(ctx context.Context, l *topicLink, r *ghRepoAt, b *topicBranch, deck string) ([]topicChunk, error) {
	cf, ok := b.Conflicts[deck].(map[string]any)
	if !ok {
		return nil, errors.New("no such conflict")
	}
	mine, err := c.db.Get(ctx, "shares", deck)
	if err != nil || mine == nil {
		return nil, errors.New("no such presentation")
	}
	theirs, _ := cf["md"].(string)
	oursMD, _ := mine["md"].(string)
	baseMD := ""
	if base, _ := cf["base"].(string); base != "" {
		bc, err := r.commit(ctx, base)
		if err != nil {
			return nil, err
		}
		files, err := r.files(ctx, bc.Tree.SHA)
		if err != nil {
			return nil, err
		}
		slug, _ := cf["slug"].(string)
		if baseMD, _, err = c.textAt(ctx, r, files, l.prefix()+slug+".md"); err != nil {
			return nil, err
		}
	}
	m := RdText_static_merge3(baseMD, oursMD, theirs)
	var out []topicChunk
	for _, ch := range m.chunks {
		if !ch.conflict {
			s := strings.Join(ch.lines, "\n")
			if n := len(out); n > 0 && !out[n-1].Clash {
				out[n-1].Same += "\n" + s
			} else {
				out = append(out, topicChunk{Same: s})
			}
			continue
		}
		out = append(out, topicChunk{Main: strings.Join(ch.theirs, "\n"), Branch: strings.Join(ch.mine, "\n"), Clash: true})
	}
	return out, nil
}

// resolveBranch: the copy's text as the person chose it; main's text it
// was chosen against is its base from now on
func (c *cloudGit) resolveBranch(ctx context.Context, l *topicLink, b *topicBranch, deck, md, take string) error {
	cf, ok := b.Conflicts[deck].(map[string]any)
	if !ok {
		return errors.New("no such conflict")
	}
	patch := Doc{"updated": c.db.ServerTime()}
	switch take {
	case "main":
		patch["md"], _ = cf["md"].(string)
		patch["css"], _ = cf["css"].(string)
	case "branch":
	case "text":
		if strings.Contains(md, "\n<<<<<<< ") || strings.HasPrefix(md, "<<<<<<< ") {
			return errors.New("the text still has a conflict's marks in it")
		}
		patch["md"] = md
		if v, _ := cf["cssClash"].(bool); v {
			patch["css"], _ = cf["css"].(string)
		}
	default:
		return errors.New("take is main, branch or text")
	}
	if err := c.db.Update(ctx, "shares", deck, patch); err != nil {
		return err
	}
	for main, cp := range b.Copies {
		if cp.Deck == deck {
			if m, _ := cf["main"].(string); m != "" {
				b.Copies[main] = topicCopy{deck, m}
			}
		}
	}
	delete(b.Conflicts, deck)
	return nil
}

// resolveMain: a main presentation's conflict with GitHub: GitHub's text
// ("github") or Sliqtly's ("sliqtly")
func (c *cloudGit) resolveMain(ctx context.Context, l *topicLink, deck, take string) error {
	cf, ok := l.Conflicts[deck].(map[string]any)
	if !ok {
		return errors.New("no such conflict")
	}
	if take == "github" {
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
	delete(l.Conflicts, deck)
	return nil
}

// --- linking

// linkTopic links the account's topic to a repository the App may write,
// at dir; the first sync takes in the Markdown already there
func (c *cloudGit) linkTopic(ctx context.Context, uid, room string, installation, repoID int64, dir string, public bool) (*topicSyncResult, error) {
	gl, _, err := c.link(ctx, uid)
	if err != nil {
		return nil, err
	}
	name, private := "", true
	for _, x := range gl.Choices {
		ch, _ := x.(map[string]any)
		if docInt(ch["installation"]) == installation && docInt(ch["repo"]) == repoID {
			name, _ = ch["name"].(string)
			private, _ = ch["private"].(bool)
		}
	}
	if name != "" && !private && !public {
		return nil, roomErr{fmt.Sprintf("%s is public: every presentation, picture and old version in it would be readable by anyone. Say that it may be, or pick a private one.", name)}
	}
	if name == "" {
		return nil, roomErr{"That repository is not one Sliqtly's GitHub App may write. Add it to the App on GitHub first."}
	}
	d, ok := topicDir(dir)
	if !ok {
		return nil, roomErr{"the folder is a path in the repository, like docs/plans"}
	}
	ld, err := c.db.Get(ctx, layoutCol, uid)
	if err != nil {
		return nil, err
	}
	v := viewOf(layoutOf(ld))
	if _, made := v.rooms[room]; !made {
		return nil, roomErr{"only a topic of your own is linked (not General, Playground or one shared with you)"}
	}
	if v.role(room) != store.Owner {
		return nil, roomErr{"only a topic of your own is linked"}
	}
	old, err := c.topicLink(ctx, uid, room)
	if err != nil {
		return nil, err
	}
	for _, b := range old.Branches {
		if b.open() {
			return nil, roomErr{"merge or drop the topic's branches before it is linked elsewhere"}
		}
	}
	if err := c.db.Set(ctx, topicLinkCol, topicLinkID(uid, room), Doc{"uid": uid, "room": room, "installation": installation,
		"repoId": repoID, "repo": name, "dir": d, "main": "", "head": "", "slugs": Doc{}, "fileKeys": Doc{}, "conflicts": Doc{},
		"branches": Doc{}, "public": public, "linked": c.now()}); err != nil {
		return nil, err
	}
	res := newTopicResult()
	err = c.withTopic(ctx, uid, room, func(l *topicLink, r *ghRepoAt) error {
		return c.syncMain(ctx, l, r, "Sliqtly: topic "+nonEmpty(v.title(room), room), "", res)
	})
	return res, err
}

// topicOfDeck: the linked topic a presentation of the account's is in,
// and its branch ("" main); nil when none
func (c *cloudGit) topicOfDeck(ctx context.Context, uid, deck string) (*topicLink, string, error) {
	d, err := c.db.Get(ctx, "shares", deck)
	if err != nil || d == nil {
		return nil, "", err
	}
	if owner, _ := d["owner"].(string); owner != uid {
		return nil, "", nil
	}
	room, branch := "", ""
	if br, _ := d["branch"].(map[string]any); br != nil {
		room, _ = br["room"].(string)
		branch, _ = br["name"].(string)
	} else {
		ld, err := c.db.Get(ctx, layoutCol, uid)
		if err != nil {
			return nil, "", err
		}
		room = viewOf(layoutOf(ld)).roomOf(pageDeck(deck))
	}
	l, err := c.topicLink(ctx, uid, room)
	if err != nil || !l.linked() {
		return nil, "", err
	}
	return l, branch, nil
}

// syncDeckTopic: the editor saved deck: the topic it is in is synced (its
// branch, or main); nil, nil when it is in no linked topic
func (c *cloudGit) syncDeckTopic(ctx context.Context, uid, deck, message, author string) (*topicSyncResult, error) {
	l, branch, err := c.topicOfDeck(ctx, uid, deck)
	if err != nil || l == nil {
		return nil, err
	}
	res := newTopicResult()
	err = c.withTopic(ctx, uid, l.room, func(l *topicLink, r *ghRepoAt) error {
		if branch != "" {
			b := l.Branches[branch]
			if b == nil || !b.open() {
				return nil
			}
			return c.syncBranch(ctx, l, r, b, message, author, res)
		}
		return c.syncMain(ctx, l, r, message, author, res)
	})
	return res, err
}

// topicsOf: the account's linked topics, for the editor
func (c *cloudGit) topicsOf(ctx context.Context, uid string) ([]map[string]any, error) {
	docs, ids, err := c.db.WhereEq(ctx, topicLinkCol, "uid", uid)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for i, d := range docs {
		room, _ := d["room"].(string)
		l := topicLinkOf(ids[i], uid, room, d)
		if !l.linked() {
			continue
		}
		var open []string
		for name, b := range l.Branches {
			if b.open() {
				open = append(open, name)
			}
		}
		sort.Strings(open)
		out = append(out, map[string]any{"room": room, "repo": l.Repo, "dir": l.Dir, "branches": open})
	}
	return out, nil
}

// topicState: the topic's page
func (c *cloudGit) topicState(ctx context.Context, uid, room string) (map[string]any, error) {
	l, err := c.topicLink(ctx, uid, room)
	if err != nil {
		return nil, err
	}
	gl, _, err := c.link(ctx, uid)
	if err != nil {
		return nil, err
	}
	ld, err := c.db.Get(ctx, layoutCol, uid)
	if err != nil {
		return nil, err
	}
	v := viewOf(layoutOf(ld))
	_, own := v.rooms[room]
	out := map[string]any{"room": room, "title": v.title(room), "own": own && v.role(room) == store.Owner, "linked": l.linked(),
		"install": c.app.installURL(""), "choices": nonNilList(gl.Choices)}
	if gl.Login != "" {
		out["github"] = map[string]any{"login": gl.Login}
	}
	if !l.linked() {
		return out, nil
	}
	url := "https://github.com/" + l.Repo
	out["repo"], out["url"], out["dir"], out["main"], out["head"], out["public"] = l.Repo, url, l.Dir, nonEmpty(l.Main, "main"), l.Head, l.Public
	if !l.Synced.IsZero() {
		out["synced"] = l.Synced.UnixMilli()
	}
	if l.Error != "" {
		out["error"] = l.Error
	}
	decks, err := c.topicDecks(ctx, l)
	if err != nil {
		return nil, err
	}
	name := func(id string) string {
		if s, err := c.db.Get(ctx, "shares", id); err == nil && s != nil {
			n, _ := s["name"].(string)
			return nonEmpty(n, id)
		}
		return id
	}
	var mains []any
	for _, id := range sortedKeys(decks.main) {
		n, _ := decks.main[id]["name"].(string)
		mains = append(mains, map[string]any{"deck": id, "name": nonEmpty(n, id), "slug": l.Slugs[id]})
	}
	out["decks"] = nonNilList(mains)
	var conflicts []any
	for _, id := range sortedKeys(l.Conflicts) {
		cf, _ := l.Conflicts[id].(map[string]any)
		conflicts = append(conflicts, map[string]any{"deck": id, "name": name(id), "sha": cf["sha"], "slug": cf["slug"]})
	}
	out["conflicts"] = nonNilList(conflicts)
	var branches []any
	for _, bn := range sortedKeys(l.Branches) {
		b := l.Branches[bn]
		row := map[string]any{"name": bn, "ref": b.Ref, "state": b.State, "by": b.By, "created": b.Created, "url": url + "/tree/" + b.Ref}
		if b.PRURL != "" {
			row["pr"], row["prUrl"] = b.PR, b.PRURL
		}
		if b.open() {
			copyOf := map[string]string{}
			for main, cp := range b.Copies {
				copyOf[cp.Deck] = main
			}
			var list []any
			for _, id := range sortedKeys(decks.branch[bn]) {
				n, _ := decks.branch[bn][id]["name"].(string)
				item := map[string]any{"deck": id, "name": nonEmpty(n, id)}
				if main, ok := copyOf[id]; ok {
					item["of"] = main
				} else {
					item["new"] = true
				}
				if _, clash := b.Conflicts[id]; clash {
					item["conflict"] = true
				}
				list = append(list, item)
			}
			row["decks"] = nonNilList(list)
			behind := false
			for _, cp := range b.Copies {
				if cp.Base != l.Head {
					behind = true
				}
			}
			row["behind"] = behind || b.Base != l.Head
			row["conflicts"] = len(b.Conflicts)
		}
		branches = append(branches, row)
	}
	out["branches"] = nonNilList(branches)
	return out, nil
}

func nonNilList(l []any) []any {
	if l == nil {
		return []any{}
	}
	return l
}

// --- pull requests

var errNoPullRights = errors.New("the App may not open pull requests on this repository")

type ghPull struct {
	Number int64
	URL    string
}

// prToken: an installation token that may open and merge pull requests
func (a *githubApp) prToken(ctx context.Context, installation, repo int64) (string, error) {
	key := strconv.FormatInt(installation, 10) + "/" + strconv.FormatInt(repo, 10) + "/pr"
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
	in := map[string]any{"repository_ids": []int64{repo}, "permissions": map[string]string{"contents": "write", "metadata": "read", "pull_requests": "write"}}
	if err := a.call(ctx, j, http.MethodPost, fmt.Sprintf("/app/installations/%d/access_tokens", installation), in, &out); err != nil {
		if s := ghStatus(err); s == 422 || s == 403 {
			return "", errNoPullRights
		}
		return "", err
	}
	until := out.ExpiresAt.Add(-5 * time.Minute)
	if out.ExpiresAt.IsZero() {
		until = a.now().Add(50 * time.Minute)
	}
	a.mu.Lock()
	a.tokens[key] = appToken{out.Token, until}
	a.mu.Unlock()
	return out.Token, nil
}

// pullRequest: the branch's open pull request into main, opened when it has none
func (a *githubApp) pullRequest(ctx context.Context, l *topicLink, ref, title string) (ghPull, error) {
	tok, err := a.prToken(ctx, l.Installation, l.RepoID)
	if err != nil {
		return ghPull{}, err
	}
	r := &ghRepoAt{app: a, token: tok, name: l.Repo}
	owner, _, _ := strings.Cut(l.Repo, "/")
	var list []struct {
		Number  int64  `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	q := url.Values{"head": {owner + ":" + ref}, "base": {l.Main}, "state": {"open"}}
	if err := r.call(ctx, http.MethodGet, "/pulls?"+q.Encode(), nil, &list); err != nil {
		if s := ghStatus(err); s == 403 {
			return ghPull{}, errNoPullRights
		}
		return ghPull{}, err
	}
	if len(list) > 0 {
		return ghPull{list[0].Number, list[0].HTMLURL}, nil
	}
	var out struct {
		Number  int64  `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	body := "Merged from Sliqtly: the topic's branch, its presentations as Markdown."
	if err := r.call(ctx, http.MethodPost, "/pulls", map[string]any{"title": title, "head": ref, "base": l.Main, "body": body}, &out); err != nil {
		if s := ghStatus(err); s == 403 {
			return ghPull{}, errNoPullRights
		}
		return ghPull{}, err
	}
	return ghPull{out.Number, out.HTMLURL}, nil
}

// mergePull merges the pull request at the branch's commit sha; false when
// GitHub does not let it be merged yet (reviews or checks required)
func (a *githubApp) mergePull(ctx context.Context, l *topicLink, n int64, sha string) (bool, error) {
	tok, err := a.prToken(ctx, l.Installation, l.RepoID)
	if err != nil {
		return false, err
	}
	r := &ghRepoAt{app: a, token: tok, name: l.Repo}
	var out struct {
		Merged bool `json:"merged"`
	}
	err = r.call(ctx, http.MethodPut, fmt.Sprintf("/pulls/%d/merge", n), map[string]any{"merge_method": "merge", "sha": sha}, &out)
	switch ghStatus(err) {
	case 405, 409:
		return false, nil
	}
	return out.Merged, err
}

// pullMerged: whether GitHub merged the pull request
func (a *githubApp) pullMerged(ctx context.Context, l *topicLink, n int64) (bool, error) {
	tok, err := a.prToken(ctx, l.Installation, l.RepoID)
	if err != nil {
		return false, err
	}
	r := &ghRepoAt{app: a, token: tok, name: l.Repo}
	var out struct {
		Merged bool   `json:"merged"`
		State  string `json:"state"`
	}
	err = r.call(ctx, http.MethodGet, fmt.Sprintf("/pulls/%d", n), nil, &out)
	return out.Merged, err
}

// --- GitHub's push

// topicPushed: GitHub's push webhook for a repository: each topic linked
// to it syncs the branch that moved, and a pull request merged on GitHub
// is finished here
func (c *cloudGit) topicPushed(ctx context.Context, repoID int64, ref, after string) {
	docs, ids, err := c.db.WhereEq(ctx, topicLinkCol, "repoId", repoID)
	if err != nil {
		c.logf("git: topics of repository %d: %v", repoID, err)
		return
	}
	for i, id := range ids {
		uid, _ := docs[i]["uid"].(string)
		room, _ := docs[i]["room"].(string)
		l := topicLinkOf(id, uid, room, docs[i])
		if !l.linked() || strings.Trim(after, "0") == "" {
			continue
		}
		branch, ok := strings.CutPrefix(ref, "refs/heads/")
		if !ok {
			continue
		}
		res := newTopicResult()
		err := c.withTopic(ctx, uid, room, func(l *topicLink, r *ghRepoAt) error {
			if branch == l.Main {
				if after == l.Head {
					return nil
				}
				if err := c.syncMain(ctx, l, r, "", "", res); err != nil {
					return err
				}
				for _, b := range l.Branches {
					if b.State == "review" && b.PR != 0 {
						if merged, err := c.app.pullMerged(ctx, l, b.PR); err == nil && merged {
							if err := c.finishMerge(ctx, l, r, b, "", res); err != nil {
								return err
							}
						}
					}
				}
				return nil
			}
			for _, b := range l.Branches {
				if b.open() && b.Ref == branch && b.Head != after {
					return c.syncBranch(ctx, l, r, b, "", "", res)
				}
			}
			return nil
		})
		if err != nil && !errors.Is(err, errTopicBusy) {
			c.logf("git: topic %s: push from GitHub: %v", id, err)
		}
	}
}
