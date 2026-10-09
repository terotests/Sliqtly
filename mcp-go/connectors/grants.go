// SPDX-License-Identifier: AGPL-3.0-or-later

package connectors

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Grant lets one deck (or every deck, "*") use some of a connector's
// operations. Only the admin makes one.
type Grant struct {
	Deck      string   `json:"deck"`
	Connector string   `json:"connector"`
	Ops       []string `json:"ops"`
	By        string   `json:"by"`
	At        int64    `json:"at"`
}

// Request is a call that had no grant: the admin sees it and may approve.
type Request struct {
	Deck      string `json:"deck"`
	Connector string `json:"connector"`
	Op        string `json:"op"`
	Who       string `json:"who"`
	First     int64  `json:"first"`
	Last      int64  `json:"last"`
	Count     int    `json:"count"`
}

// Grants keeps the grants and the open requests in two files.
type Grants struct {
	dir string
	mu  sync.Mutex
	g   []Grant
	r   []Request
	now func() time.Time
}

// OpenGrants reads dir/grants.json and dir/requests.json (missing: none).
func OpenGrants(dir string, now func() time.Time) (*Grants, error) {
	s := &Grants{dir: dir, now: now}
	if err := readJSON(filepath.Join(dir, "grants.json"), &s.g); err != nil {
		return nil, err
	}
	if err := readJSON(filepath.Join(dir, "requests.json"), &s.r); err != nil {
		return nil, err
	}
	return s, nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// writeJSON replaces the file whole (a new file renamed over it), 0600
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Allowed tells whether the deck may call the operation.
func (s *Grants) Allowed(deck, connector, op string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.g {
		if g.Connector != connector || (g.Deck != deck && g.Deck != "*") {
			continue
		}
		for _, o := range g.Ops {
			if o == op || o == "*" {
				return true
			}
		}
	}
	return false
}

// Ask keeps a refused call as a request (once per deck, connector and
// operation; later ones count up).
func (s *Grants) Ask(deck, connector, op, who string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.now().UnixMilli()
	for i := range s.r {
		r := &s.r[i]
		if r.Deck == deck && r.Connector == connector && r.Op == op {
			r.Last, r.Who = t, who
			r.Count++
			return writeJSON(filepath.Join(s.dir, "requests.json"), s.r)
		}
	}
	if len(s.r) >= 500 {
		// the oldest goes; a deck asking again comes back
		sort.Slice(s.r, func(i, j int) bool { return s.r[i].Last > s.r[j].Last })
		s.r = s.r[:499]
	}
	s.r = append(s.r, Request{Deck: deck, Connector: connector, Op: op, Who: who, First: t, Last: t, Count: 1})
	return writeJSON(filepath.Join(s.dir, "requests.json"), s.r)
}

// Approve adds operations to a deck's grant and drops the requests it
// answers.
func (s *Grants) Approve(deck, connector string, ops []string, by string) error {
	if deck == "" || connector == "" || len(ops) == 0 {
		return errors.New("deck, connector and ops are needed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.now().UnixMilli()
	found := false
	for i := range s.g {
		g := &s.g[i]
		if g.Deck == deck && g.Connector == connector {
			g.Ops = union(g.Ops, ops)
			g.By, g.At = by, t
			found = true
		}
	}
	if !found {
		s.g = append(s.g, Grant{Deck: deck, Connector: connector, Ops: union(nil, ops), By: by, At: t})
	}
	if err := writeJSON(filepath.Join(s.dir, "grants.json"), s.g); err != nil {
		return err
	}
	kept := s.r[:0]
	for _, r := range s.r {
		if r.Connector == connector && (r.Deck == deck || deck == "*") && (contains(ops, r.Op) || contains(ops, "*")) {
			continue
		}
		kept = append(kept, r)
	}
	s.r = kept
	return writeJSON(filepath.Join(s.dir, "requests.json"), s.r)
}

// Revoke removes a deck's grant for a connector (all of it when ops is
// empty).
func (s *Grants) Revoke(deck, connector string, ops []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.g[:0]
	for _, g := range s.g {
		if g.Deck == deck && g.Connector == connector {
			if len(ops) == 0 {
				continue
			}
			left := []string{}
			for _, o := range g.Ops {
				if !contains(ops, o) {
					left = append(left, o)
				}
			}
			if len(left) == 0 {
				continue
			}
			g.Ops = left
		}
		kept = append(kept, g)
	}
	s.g = kept
	return writeJSON(filepath.Join(s.dir, "grants.json"), s.g)
}

// Dismiss drops a request without granting it.
func (s *Grants) Dismiss(deck, connector, op string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.r[:0]
	for _, r := range s.r {
		if r.Deck == deck && r.Connector == connector && r.Op == op {
			continue
		}
		kept = append(kept, r)
	}
	s.r = kept
	return writeJSON(filepath.Join(s.dir, "requests.json"), s.r)
}

// List is a copy of the grants and the requests, newest request first.
func (s *Grants) List() ([]Grant, []Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := append([]Grant{}, s.g...)
	r := append([]Request{}, s.r...)
	sort.Slice(r, func(i, j int) bool { return r[i].Last > r[j].Last })
	return g, r
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func union(a, b []string) []string {
	out := append([]string(nil), a...)
	for _, v := range b {
		if !contains(out, v) {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}
