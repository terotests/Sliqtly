// SPDX-License-Identifier: AGPL-3.0-or-later

package rgit

// Pushing a repository's branch to a remote over smart HTTP
// (git-receive-pack): the refs the remote advertises, the pack of what it
// has not got (GitWalk), the request and its report (GitPush), all from the
// Ranger client; this side does the HTTPS and reads the objects.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ErrNotFastForward: the remote's branch has commits this repository has
// not got, or is not behind the commit being pushed.
var ErrNotFastForward = errors.New("rgit: the remote branch has moved on")

// Remote is a repository at an https URL ("https://github.com/o/r.git").
type Remote struct {
	URL string
	// sets Authorization (a token); nil for none
	Auth   func(*http.Request)
	Client *http.Client
	// the agent= capability (e.g. "sliqtly/0.1")
	Agent string
}

// PushResult is what a push did.
type PushResult struct {
	// the remote's commit before; "" when it had no such branch
	Old string
	New string
	// how many objects were sent; 0 when the remote was up to date
	Objects int
	// the remote's progress messages
	Progress string
}

const maxAnswer = 16 << 20

func (rm Remote) client() *http.Client {
	if rm.Client != nil {
		return rm.Client
	}
	return http.DefaultClient
}

func (rm Remote) do(ctx context.Context, method, path, ctype string, body []byte) ([]byte, error) {
	u := strings.TrimRight(rm.URL, "/") + path
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
		req.Header.Set("Accept", "application/x-git-receive-pack-result")
	}
	if rm.Auth != nil {
		rm.Auth(req)
	}
	res, err := rm.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, maxAnswer))
	if err != nil {
		return nil, err
	}
	if res.StatusCode != 200 {
		msg := strings.TrimSpace(string(b))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return nil, fmt.Errorf("rgit: %s %s: %s %s", method, redact(u), res.Status, msg)
	}
	return b, nil
}

// the URL without any user:password in it, for messages
func redact(u string) string {
	if at := strings.Index(u, "@"); at > 0 {
		if s := strings.Index(u, "://"); s > 0 && s < at {
			return u[:s+3] + u[at+1:]
		}
	}
	return u
}

// Refs are the remote's branches and tags (name → commit) as its
// receive-pack advertises them.
func (rm Remote) Refs(ctx context.Context) (map[string]string, error) {
	adv, err := rm.advertise(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, r := range adv.refs {
		if strings.HasPrefix(r.name, "refs/") {
			out[r.name] = r.sha
		}
	}
	return out, nil
}

func (rm Remote) advertise(ctx context.Context) (*GitAdvertisement, error) {
	b, err := rm.do(ctx, http.MethodGet, "/info/refs?service=git-receive-pack", "", nil)
	if err != nil {
		return nil, err
	}
	adv := GitPktIO_static_parseAdvertisement(b)
	if !adv.ok {
		return nil, fmt.Errorf("rgit: refs of %s: %s", redact(rm.URL), adv.err)
	}
	if adv.service != "" && adv.service != "git-receive-pack" {
		return nil, fmt.Errorf("rgit: %s answers as %s", redact(rm.URL), adv.service)
	}
	return adv, nil
}

// Push moves the remote's branch to commit sha of r, sending the objects
// the remote has not got. The remote's branch must be absent or behind sha
// (an ancestor this repository has); otherwise nothing is sent and the
// error is ErrNotFastForward.
func (r *Repo) Push(ctx context.Context, rm Remote, branch, sha string) (PushResult, error) {
	ref := "refs/heads/" + branch
	res := PushResult{New: sha}
	if err := checkRefName(ref); err != nil {
		return res, err
	}
	if !validID(sha) {
		return res, fmt.Errorf("rgit: bad commit %q", sha)
	}
	adv, err := rm.advertise(ctx)
	if err != nil {
		return res, err
	}
	var remote []string
	for _, a := range adv.refs {
		if a.name == ref {
			res.Old = a.sha
		}
		if strings.HasPrefix(a.name, "refs/") && validID(a.sha) && r.Has(a.sha) {
			remote = append(remote, a.sha)
		}
	}
	if res.Old == GitPush_static_zeros() {
		res.Old = ""
	}
	if res.Old == sha {
		return res, nil
	}
	if res.Old != "" {
		if !r.Has(res.Old) {
			return res, fmt.Errorf("%w: %s is at %s, which this server has not got", ErrNotFastForward, ref, short(res.Old))
		}
		ok, err := r.IsAncestor(res.Old, sha)
		if err != nil {
			return res, err
		}
		if !ok {
			return res, fmt.Errorf("%w: %s is at %s, not behind %s", ErrNotFastForward, ref, short(res.Old), short(sha))
		}
	}
	walk := CreateNew_GitWalk([]string{sha}, remote)
	for {
		need := walk.need()
		if need == "" {
			break
		}
		kind, payload, err := r.Get(need)
		if errors.Is(err, ErrNotFound) {
			walk.missing(need)
			continue
		}
		if err != nil {
			return res, err
		}
		walk.give(need, kind, payload)
	}
	if !walk.ok {
		return res, errors.New("rgit: " + walk.err)
	}
	w := walk.pack()
	res.Objects = int(w.count())
	u := CreateNew_GitRefUpdate()
	u.name, u.oldSha, u.newSha = ref, res.Old, sha
	body := GitPush_static_request([]*GitRefUpdate{u}, GitPush_static_caps(adv, rm.Agent), w.finish())
	answer, err := rm.do(ctx, http.MethodPost, "/git-receive-pack", "application/x-git-receive-pack-request", body)
	if err != nil {
		return res, err
	}
	rep := GitPush_static_parseReport(answer)
	res.Progress = rep.progress
	if !rep.ok {
		return res, fmt.Errorf("rgit: push refused: %s", rep.err)
	}
	if rep.unpack != "ok" {
		return res, fmt.Errorf("rgit: the remote did not take the pack: %s", rep.unpack)
	}
	for _, s := range rep.refs {
		if s.name == ref && !s.ok {
			if strings.Contains(s.reason, "fast-forward") || strings.Contains(s.reason, "fetch first") || strings.Contains(s.reason, "stale") {
				return res, fmt.Errorf("%w: %s", ErrNotFastForward, s.reason)
			}
			return res, fmt.Errorf("rgit: %s refused: %s", ref, s.reason)
		}
	}
	if !rep.refOk(ref) {
		return res, fmt.Errorf("rgit: the remote did not report %s", ref)
	}
	return res, nil
}

// IsAncestor tells whether commit a is b or reachable from b by parents.
func (r *Repo) IsAncestor(a, b string) (bool, error) {
	seen := map[string]bool{}
	queue := []string{b}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == a {
			return true, nil
		}
		if seen[c] {
			continue
		}
		seen[c] = true
		cm, err := r.ReadCommit(c)
		if err != nil {
			return false, err
		}
		queue = append(queue, cm.Parents...)
	}
	return false, nil
}

// Ahead counts the commits from tip back to (not including) base along
// first parents; base "" counts them all.
func (r *Repo) Ahead(base, tip string) (int, error) {
	n := 0
	for tip != "" && tip != base {
		c, err := r.ReadCommit(tip)
		if err != nil {
			return n, err
		}
		n++
		tip = ""
		if len(c.Parents) > 0 {
			tip = c.Parents[0]
		}
	}
	return n, nil
}
