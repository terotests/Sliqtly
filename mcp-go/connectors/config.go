// SPDX-License-Identifier: AGPL-3.0-or-later

// Package connectors lets scripts and workflows inside a deck use services
// outside it, through the server and never directly
// (/mnt/project-files/workflow/CONNECTORS.md):
//
//	<data>/connectors/<id>.json   a connector the server's admin wrote:
//	                              its address, the hosts it may reach, how
//	                              it signs in, and the operations it allows
//	<data>/connectors/grants.json which deck may use which operations
//	                              (the admin approves; a refused call is
//	                              kept as a request for the admin to see)
//	<data>/connectors/tokens/     each person's own sign-in to a service
//	                              (OAuth), encrypted with connectors/key
//	<data>/connectors/audit.log   one line per call
//
// A caller names a connector, an operation and its arguments; the gateway
// checks the grant, the arguments against the operation's schema and the
// limits, builds the request itself (only to the connector's hosts), adds
// the secret or the person's token, and hands back only the fields the
// operation picks. Secrets and tokens never leave the server.
package connectors

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Connector is one service as the admin configured it.
type Connector struct {
	ID      string   `json:"id"`
	Title   string   `json:"title,omitempty"`
	Type    string   `json:"type"`
	BaseURL string   `json:"baseUrl"`
	Egress  []string `json:"egress,omitempty"`
	// "user": calls are made with the caller's own sign-in (OAuth);
	// "service": with the connector's secret (Auth)
	Identity   string            `json:"identity,omitempty"`
	Auth       *Auth             `json:"auth,omitempty"`
	OAuth      *OAuth            `json:"oauth,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Operations map[string]*Op    `json:"operations"`
	Limits     Limits            `json:"limits,omitempty"`
}

// Auth is how a service connector signs its calls.
type Auth struct {
	// "bearer" (Authorization: Bearer <secret>) or "header" (Header: <secret>)
	Kind   string `json:"kind"`
	Header string `json:"header,omitempty"`
	Secret Secret `json:"secret"`
}

// Secret names where a secret is: an environment variable. The value is
// never written in a connector file.
type Secret struct {
	Env string `json:"env"`
}

// Value is the secret, or "" when the variable is not set.
func (s Secret) Value() string {
	if s.Env == "" {
		return ""
	}
	return os.Getenv(s.Env)
}

// OAuth is a person's own sign-in to the service.
type OAuth struct {
	// "github", or "" with AuthorizeURL and TokenURL given
	Provider     string   `json:"provider,omitempty"`
	ClientID     string   `json:"clientId"`
	ClientSecret Secret   `json:"clientSecret"`
	Scopes       []string `json:"scopes,omitempty"`
	AuthorizeURL string   `json:"authorizeUrl,omitempty"`
	TokenURL     string   `json:"tokenUrl,omitempty"`
	// where the signed-in account's name is read after sign-in (optional)
	UserURL   string `json:"userUrl,omitempty"`
	UserField string `json:"userField,omitempty"`
}

// Limits keep one connector from being called without end.
type Limits struct {
	PerMinute        int   `json:"perMinute,omitempty"`
	PerDay           int   `json:"perDay,omitempty"`
	MaxResponseBytes int64 `json:"maxResponseBytes,omitempty"`
	TimeoutMs        int   `json:"timeoutMs,omitempty"`
}

// Op is one operation a connector allows.
type Op struct {
	Title  string `json:"title,omitempty"`
	Method string `json:"method"`
	// path below baseUrl; {name} is filled from the arguments, escaped
	Path string  `json:"path"`
	In   *Schema `json:"in,omitempty"`
	// the fields handed back, e.g. "workflow_runs[].name"; empty: all
	Pick []string `json:"pick,omitempty"`
	// "read", "write" (changes the service) or "external-write"
	// (changes something people outside see, e.g. posts a comment)
	Effect string `json:"effect,omitempty"`
}

var (
	idRule    = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
	opRule    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.]{0,63}$`)
	paramRule = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)
	envRule   = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,99}$`)
)

// what a provider name stands for
var providers = map[string]OAuth{
	"github": {
		AuthorizeURL: "https://github.com/login/oauth/authorize",
		TokenURL:     "https://github.com/login/oauth/access_token",
		UserURL:      "https://api.github.com/user",
		UserField:    "login",
	},
}

// check fills in the defaults and tells what is wrong, if anything.
func (c *Connector) check() error {
	if !idRule.MatchString(c.ID) {
		return fmt.Errorf("id %q: lower-case letters, digits and -, starting with a letter", c.ID)
	}
	if c.ID == "sliqtly" {
		return errors.New(`id "sliqtly" is the server's own`)
	}
	if c.Type == "" {
		c.Type = "http"
	}
	if c.Type != "http" {
		return fmt.Errorf("type %q: only http for now", c.Type)
	}
	base, err := url.Parse(c.BaseURL)
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return fmt.Errorf("baseUrl %q: an https:// address without a user, query or fragment", c.BaseURL)
	}
	c.BaseURL = strings.TrimRight(c.BaseURL, "/")
	if len(c.Egress) == 0 {
		c.Egress = []string{base.Hostname()}
	}
	for i, h := range c.Egress {
		c.Egress[i] = strings.ToLower(h)
	}
	if !c.mayReach(base.Hostname()) {
		return fmt.Errorf("baseUrl's host %s is not in egress", base.Hostname())
	}
	if c.Identity == "" {
		if c.OAuth != nil {
			c.Identity = "user"
		} else {
			c.Identity = "service"
		}
	}
	switch c.Identity {
	case "user":
		if c.OAuth == nil {
			return errors.New(`identity "user" needs oauth`)
		}
	case "service":
	default:
		return fmt.Errorf("identity %q: user or service", c.Identity)
	}
	if c.Auth != nil {
		if c.Auth.Kind == "" {
			c.Auth.Kind = "bearer"
		}
		if c.Auth.Kind != "bearer" && !(c.Auth.Kind == "header" && c.Auth.Header != "") {
			return errors.New(`auth.kind: bearer, or header with auth.header`)
		}
		if !envRule.MatchString(c.Auth.Secret.Env) {
			return errors.New(`auth.secret: {"env": "NAME"}, an environment variable in capitals`)
		}
	}
	if o := c.OAuth; o != nil {
		if p, ok := providers[o.Provider]; ok {
			if o.AuthorizeURL == "" {
				o.AuthorizeURL = p.AuthorizeURL
			}
			if o.TokenURL == "" {
				o.TokenURL = p.TokenURL
			}
			if o.UserURL == "" {
				o.UserURL, o.UserField = p.UserURL, p.UserField
			}
		} else if o.Provider != "" {
			return fmt.Errorf("oauth.provider %q: github, or leave it out and give authorizeUrl and tokenUrl", o.Provider)
		}
		for _, u := range []string{o.AuthorizeURL, o.TokenURL} {
			if x, err := url.Parse(u); err != nil || x.Scheme != "https" || x.Host == "" {
				return fmt.Errorf("oauth: %q is not an https:// address", u)
			}
		}
		if o.ClientID == "" {
			return errors.New("oauth.clientId is missing")
		}
		if !envRule.MatchString(o.ClientSecret.Env) {
			return errors.New(`oauth.clientSecret: {"env": "NAME"}, an environment variable in capitals`)
		}
		if o.UserURL != "" {
			if x, err := url.Parse(o.UserURL); err != nil || x.Scheme != "https" || !c.mayReach(x.Hostname()) {
				return fmt.Errorf("oauth.userUrl %q: an https:// address on an egress host", o.UserURL)
			}
		}
	}
	for k := range c.Headers {
		if strings.EqualFold(k, "Authorization") || strings.EqualFold(k, "Cookie") || strings.EqualFold(k, "Host") {
			return fmt.Errorf("headers: %s is set by the server", k)
		}
	}
	if len(c.Operations) == 0 {
		return errors.New("no operations")
	}
	for name, op := range c.Operations {
		if op == nil {
			return fmt.Errorf("operation %s is empty", name)
		}
		if err := op.check(); err != nil {
			return fmt.Errorf("operation %s: %v", name, err)
		}
		if !opRule.MatchString(name) {
			return fmt.Errorf("operation %q: letters, digits, _ and .", name)
		}
	}
	if c.Limits.PerMinute <= 0 {
		c.Limits.PerMinute = 30
	}
	if c.Limits.PerDay <= 0 {
		c.Limits.PerDay = 2000
	}
	if c.Limits.MaxResponseBytes <= 0 {
		c.Limits.MaxResponseBytes = 256 << 10
	}
	if c.Limits.MaxResponseBytes > 4<<20 {
		c.Limits.MaxResponseBytes = 4 << 20
	}
	if c.Limits.TimeoutMs <= 0 {
		c.Limits.TimeoutMs = 8000
	}
	if c.Limits.TimeoutMs > 30000 {
		c.Limits.TimeoutMs = 30000
	}
	return nil
}

func (op *Op) check() error {
	op.Method = strings.ToUpper(op.Method)
	switch op.Method {
	case "":
		op.Method = "GET"
	case "GET", "POST", "PUT", "PATCH", "DELETE":
	default:
		return fmt.Errorf("method %q", op.Method)
	}
	if !strings.HasPrefix(op.Path, "/") || strings.Contains(op.Path, "..") || strings.ContainsAny(op.Path, "?#") {
		return fmt.Errorf("path %q: starts with /, no .., ? or #", op.Path)
	}
	switch op.Effect {
	case "":
		if op.Method == "GET" {
			op.Effect = "read"
		} else {
			op.Effect = "write"
		}
	case "read", "write", "external-write":
	default:
		return fmt.Errorf("effect %q: read, write or external-write", op.Effect)
	}
	if op.In == nil {
		op.In = &Schema{Type: "object"}
	}
	if err := op.In.check(); err != nil {
		return fmt.Errorf("in: %v", err)
	}
	for _, m := range paramRule.FindAllStringSubmatch(op.Path, -1) {
		p, ok := op.In.Properties[m[1]]
		if !ok || !op.In.required(m[1]) {
			return fmt.Errorf("{%s} in the path must be a required property of in", m[1])
		}
		if p.Type == "object" || p.Type == "array" {
			return fmt.Errorf("{%s} in the path must be a string or a number", m[1])
		}
	}
	for _, p := range op.Pick {
		if err := checkPick(p); err != nil {
			return err
		}
	}
	return nil
}

// mayReach tells whether a host is one the connector may call.
func (c *Connector) mayReach(host string) bool {
	host = strings.ToLower(host)
	for _, h := range c.Egress {
		if h == host {
			return true
		}
	}
	return false
}

// Registry is the connectors in a folder.
type Registry struct {
	byID map[string]*Connector
	// files that could not be read, with why (shown to the admin)
	Problems map[string]string
}

// Load reads every <id>.json in dir (grants.json aside). A missing folder
// is an empty registry; a file that does not hold is left out and listed
// in Problems.
func Load(dir string) (*Registry, error) {
	r := &Registry{byID: map[string]*Connector{}, Problems: map[string]string{}}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || name == "grants.json" || name == "requests.json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			r.Problems[name] = err.Error()
			continue
		}
		c, err := Parse(b)
		if err == nil && c.ID+".json" != name {
			err = fmt.Errorf("id %q does not match the file name", c.ID)
		}
		if err == nil && r.byID[c.ID] != nil {
			err = fmt.Errorf("id %q is used twice", c.ID)
		}
		if err != nil {
			r.Problems[name] = err.Error()
			continue
		}
		r.byID[c.ID] = c
	}
	return r, nil
}

// Parse reads and checks one connector.
func Parse(b []byte) (*Connector, error) {
	var c Connector
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return nil, err
	}
	if err := c.check(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Get is the connector of that id, or nil.
func (r *Registry) Get(id string) *Connector { return r.byID[id] }

// IDs are the connectors' ids in order.
func (r *Registry) IDs() []string {
	out := make([]string, 0, len(r.byID))
	for id := range r.byID {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Public is what anyone who can use the server may see of a connector:
// no secrets, no secret names.
func (c *Connector) Public() map[string]any {
	ops := []map[string]any{}
	names := make([]string, 0, len(c.Operations))
	for n := range c.Operations {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		op := c.Operations[n]
		ops = append(ops, map[string]any{"name": n, "title": op.Title, "effect": op.Effect, "in": op.In})
	}
	title := c.Title
	if title == "" {
		title = c.ID
	}
	return map[string]any{"id": c.ID, "title": title, "type": c.Type, "identity": c.Identity, "oauth": c.OAuth != nil, "operations": ops}
}
