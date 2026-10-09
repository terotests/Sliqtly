// SPDX-License-Identifier: AGPL-3.0-or-later

package connectors

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Schema is the part of JSON Schema an operation's arguments are checked
// with: an object of named properties, each a string, a number, an integer,
// a boolean, or an object/array taken as it is (sent as the JSON body).
// Arguments it does not name are refused.
type Schema struct {
	Type       string             `json:"type,omitempty"`
	Required   []string           `json:"required,omitempty"`
	Properties map[string]*Schema `json:"properties,omitempty"`
	Pattern    string             `json:"pattern,omitempty"`
	Enum       []any              `json:"enum,omitempty"`
	MaxLength  int                `json:"maxLength,omitempty"`
	Minimum    *float64           `json:"minimum,omitempty"`
	Maximum    *float64           `json:"maximum,omitempty"`
	// where the argument goes: "path" and "query" are decided by the
	// operation's path and method when left out; "body" for a JSON body
	In          string `json:"in,omitempty"`
	Description string `json:"description,omitempty"`

	re *regexp.Regexp
}

func (s *Schema) check() error {
	if s.Type != "" && s.Type != "object" {
		return errors.New(`type: object`)
	}
	s.Type = "object"
	for name, p := range s.Properties {
		if p == nil {
			return fmt.Errorf("%s is empty", name)
		}
		switch p.Type {
		case "string", "number", "integer", "boolean", "object", "array":
		default:
			return fmt.Errorf("%s: type %q", name, p.Type)
		}
		switch p.In {
		case "", "path", "query", "body":
		default:
			return fmt.Errorf("%s: in %q (path, query or body)", name, p.In)
		}
		if p.Pattern != "" {
			re, err := regexp.Compile(p.Pattern)
			if err != nil {
				return fmt.Errorf("%s: pattern: %v", name, err)
			}
			p.re = re
		}
	}
	for _, r := range s.Required {
		if s.Properties[r] == nil {
			return fmt.Errorf("required %s is not a property", r)
		}
	}
	return nil
}

func (s *Schema) required(name string) bool {
	for _, r := range s.Required {
		if r == name {
			return true
		}
	}
	return false
}

// validate checks the arguments; the error names the first one wrong.
func (s *Schema) validate(args map[string]any) error {
	for name := range args {
		if s.Properties[name] == nil {
			return fmt.Errorf("%s is not an argument of this operation", name)
		}
	}
	for _, r := range s.Required {
		if v, ok := args[r]; !ok || v == nil {
			return fmt.Errorf("%s is required", r)
		}
	}
	for name, v := range args {
		if v == nil {
			continue
		}
		if err := s.Properties[name].value(v); err != nil {
			return fmt.Errorf("%s: %v", name, err)
		}
	}
	return nil
}

func (p *Schema) value(v any) error {
	switch p.Type {
	case "string":
		t, ok := v.(string)
		if !ok {
			return errors.New("a string")
		}
		if p.MaxLength > 0 && len([]rune(t)) > p.MaxLength {
			return fmt.Errorf("at most %d characters", p.MaxLength)
		}
		if p.re != nil && !p.re.MatchString(t) {
			return fmt.Errorf("does not match %s", p.Pattern)
		}
	case "number", "integer":
		n, ok := v.(float64)
		if !ok {
			return errors.New("a number")
		}
		if p.Type == "integer" && n != math.Trunc(n) {
			return errors.New("a whole number")
		}
		if p.Minimum != nil && n < *p.Minimum {
			return fmt.Errorf("at least %v", *p.Minimum)
		}
		if p.Maximum != nil && n > *p.Maximum {
			return fmt.Errorf("at most %v", *p.Maximum)
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return errors.New("true or false")
		}
	case "object":
		if _, ok := v.(map[string]any); !ok {
			return errors.New("an object")
		}
	case "array":
		if _, ok := v.([]any); !ok {
			return errors.New("an array")
		}
	}
	if len(p.Enum) > 0 {
		for _, e := range p.Enum {
			if e == v {
				return nil
			}
		}
		return errors.New("not one of the allowed values")
	}
	return nil
}

func scalar(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// request is the method, the full address and the body of a call: the
// path filled in (each value escaped, so it cannot leave its segment),
// the rest of the arguments as the query of a GET or DELETE and as a JSON
// body otherwise.
func (op *Op) request(base string, args map[string]any) (method, addr string, body []byte, err error) {
	if err := op.In.validate(args); err != nil {
		return "", "", nil, err
	}
	used := map[string]bool{}
	path := paramRule.ReplaceAllStringFunc(op.Path, func(m string) string {
		name := m[1 : len(m)-1]
		used[name] = true
		return url.PathEscape(scalar(args[name]))
	})
	q := url.Values{}
	payload := map[string]any{}
	names := make([]string, 0, len(args))
	for n := range args {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		v := args[n]
		if used[n] || v == nil {
			continue
		}
		where := op.In.Properties[n].In
		if where == "" || where == "path" {
			if op.Method == "GET" || op.Method == "DELETE" {
				where = "query"
			} else {
				where = "body"
			}
		}
		if where == "query" {
			if a, ok := v.([]any); ok {
				for _, x := range a {
					q.Add(n, scalar(x))
				}
				continue
			}
			q.Set(n, scalar(v))
		} else {
			payload[n] = v
		}
	}
	addr = base + path
	if len(q) > 0 {
		addr += "?" + q.Encode()
	}
	if len(payload) > 0 {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(payload); err != nil {
			return "", "", nil, err
		}
		body = bytes.TrimRight(buf.Bytes(), "\n")
	}
	return op.Method, addr, body, nil
}

// a pick is dot-separated field names, a name followed by [] stepping into
// each element of an array: "workflow_runs[].head_commit.message"
var pickPart = regexp.MustCompile(`^[A-Za-z0-9_@$-]+(\[\])?$`)

func checkPick(p string) error {
	if p == "" {
		return errors.New("pick: an empty field")
	}
	for _, part := range strings.Split(p, ".") {
		if !pickPart.MatchString(part) {
			return fmt.Errorf("pick %q: field names separated by ., [] after an array", p)
		}
	}
	return nil
}

// Pick keeps only the named fields of v, in the same shape; a field that
// is not there is left out.
func Pick(v any, picks []string) any {
	if len(picks) == 0 {
		return v
	}
	var out any
	for _, p := range picks {
		out = merge(out, pickPath(v, strings.Split(p, ".")))
	}
	if out == nil {
		return map[string]any{}
	}
	return out
}

// pickPath is v cut down to one path (nil when the path is not there)
func pickPath(v any, parts []string) any {
	if len(parts) == 0 {
		return v
	}
	part := parts[0]
	each := strings.HasSuffix(part, "[]")
	name := strings.TrimSuffix(part, "[]")
	var inner any
	switch t := v.(type) {
	case map[string]any:
		x, ok := t[name]
		if !ok {
			return nil
		}
		inner = x
	case []any:
		// a field of an array's elements without [] (the top level is an array)
		out := make([]any, 0, len(t))
		for _, e := range t {
			out = append(out, pickPath(e, parts))
		}
		return out
	default:
		return nil
	}
	if each {
		a, ok := inner.([]any)
		if !ok {
			return nil
		}
		out := make([]any, 0, len(a))
		for _, e := range a {
			out = append(out, pickPath(e, parts[1:]))
		}
		return map[string]any{name: out}
	}
	x := pickPath(inner, parts[1:])
	if x == nil && len(parts) > 1 {
		return nil
	}
	return map[string]any{name: x}
}

// merge puts two cut-down copies of the same value together
func merge(a, b any) any {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok {
			return a
		}
		for k, v := range y {
			x[k] = merge(x[k], v)
		}
		return x
	case []any:
		y, ok := b.([]any)
		if !ok || len(y) != len(x) {
			return a
		}
		for i := range x {
			x[i] = merge(x[i], y[i])
		}
		return x
	}
	return a
}
