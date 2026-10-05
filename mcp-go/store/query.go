// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Query asks for documents as data, not SQL text: the folder and memory
// evaluate it (Run), a SQL backend compiles it, and a SQL front end
// (RangerSQL) parses into it. Field names are dotted paths into the
// document ("meta.kind"); "_id" is the document's id.
//
//	SELECT … FROM rooms WHERE kind = 'ticket' AND owner = ?
//	ORDER BY updated DESC LIMIT 50
//
// is
//
//	Query{From: "rooms", Where: And{Eq("kind", "ticket"), Eq("owner", u)},
//	      OrderBy: []Order{{"updated", true}}, Limit: 50}
type Query struct {
	From    string
	Where   Expr // nil: every document
	OrderBy []Order
	Limit   int // <= 0: no limit
	Offset  int
}

type Order struct {
	Field string
	Desc  bool
}

// Expr is a condition on a document: Cmp, And, Or, Not, True, False.
type Expr interface{ isExpr() }

type Op int

const (
	OpEq Op = iota
	OpNe
	OpLt
	OpLe
	OpGt
	OpGe
	// OpIn: Value is a []any; the field equals one of them
	OpIn
	// OpHas: the field is a list holding Value
	OpHas
)

func (o Op) String() string {
	return [...]string{"=", "!=", "<", "<=", ">", ">=", "IN", "HAS"}[o]
}

// Cmp compares a field with a value. A field the document does not have
// matches nothing, not even !=: as SQL's NULL does.
type Cmp struct {
	Field string
	Op    Op
	Value any
}

type And []Expr
type Or []Expr
type Not struct{ X Expr }

// Const is a condition that is always or never true: what a policy says
// when a principal may read all of a collection, or none of it.
type Const bool

const (
	True  Const = true
	False Const = false
)

func (Cmp) isExpr()   {}
func (And) isExpr()   {}
func (Or) isExpr()    {}
func (Not) isExpr()   {}
func (Const) isExpr() {}

func Eq(field string, v any) Cmp    { return Cmp{field, OpEq, v} }
func Ne(field string, v any) Cmp    { return Cmp{field, OpNe, v} }
func Lt(field string, v any) Cmp    { return Cmp{field, OpLt, v} }
func Le(field string, v any) Cmp    { return Cmp{field, OpLe, v} }
func Gt(field string, v any) Cmp    { return Cmp{field, OpGt, v} }
func Ge(field string, v any) Cmp    { return Cmp{field, OpGe, v} }
func In(field string, v ...any) Cmp { return Cmp{field, OpIn, v} }
func Has(field string, v any) Cmp   { return Cmp{field, OpHas, v} }

// AndOf joins conditions, leaving out the ones that are nil or True.
func AndOf(xs ...Expr) Expr {
	var out And
	for _, x := range xs {
		switch x {
		case nil, True:
			continue
		case False:
			return False
		}
		out = append(out, x)
	}
	switch len(out) {
	case 0:
		return True
	case 1:
		return out[0]
	}
	return out
}

// Field reads a dotted path from a document; "_id" is id.
func Field(id string, d Doc, path string) (any, bool) {
	if path == "_id" {
		return id, true
	}
	var cur any = d
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[part]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// Match tells whether the document id, d meets e.
func Match(e Expr, id string, d Doc) bool {
	switch x := e.(type) {
	case nil:
		return true
	case Const:
		return bool(x)
	case And:
		for _, y := range x {
			if !Match(y, id, d) {
				return false
			}
		}
		return true
	case Or:
		for _, y := range x {
			if Match(y, id, d) {
				return true
			}
		}
		return false
	case Not:
		return !Match(x.X, id, d)
	case Cmp:
		v, ok := Field(id, d, x.Field)
		if !ok || v == nil {
			return false
		}
		switch x.Op {
		case OpIn:
			list, _ := x.Value.([]any)
			for _, w := range list {
				if c, ok := Compare(v, w); ok && c == 0 {
					return true
				}
			}
			return false
		case OpHas:
			list, _ := v.([]any)
			for _, w := range list {
				if c, ok := Compare(w, x.Value); ok && c == 0 {
					return true
				}
			}
			return false
		}
		c, ok := Compare(v, x.Value)
		if !ok {
			return false
		}
		switch x.Op {
		case OpEq:
			return c == 0
		case OpNe:
			return c != 0
		case OpLt:
			return c < 0
		case OpLe:
			return c <= 0
		case OpGt:
			return c > 0
		case OpGe:
			return c >= 0
		}
	}
	panic(fmt.Sprintf("store: unknown condition %T", e))
}

// Compare orders two values of one kind: numbers with numbers (int64 and
// float64 alike), strings, bools, times. ok false: they are not of one
// kind, and no comparison holds between them.
func Compare(a, b any) (c int, ok bool) {
	if x, ok1 := number(a); ok1 {
		y, ok2 := number(b)
		if !ok2 {
			return 0, false
		}
		return cmp3(x < y, x > y), true
	}
	switch x := a.(type) {
	case string:
		y, ok := b.(string)
		return cmp3(ok && x < y, ok && x > y), ok
	case bool:
		y, ok := b.(bool)
		return cmp3(ok && !x && y, ok && x && !y), ok
	case time.Time:
		y, ok := b.(time.Time)
		return cmp3(ok && x.Before(y), ok && x.After(y)), ok
	}
	return 0, false
}

func cmp3(lt, gt bool) int {
	if lt {
		return -1
	}
	if gt {
		return 1
	}
	return 0
}

func number(v any) (float64, bool) {
	switch x := v.(type) {
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}

// Run applies q to items in memory: filter, order (missing fields last,
// then by id, so equal keys come out the same way every time), offset,
// limit.
func Run(q Query, items []Item) []Item {
	out := items[:0:0]
	for _, it := range items {
		if Match(q.Where, it.ID, it.Doc) {
			out = append(out, it)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		for _, o := range q.OrderBy {
			a, okA := Field(out[i].ID, out[i].Doc, o.Field)
			b, okB := Field(out[j].ID, out[j].Doc, o.Field)
			if okA != okB {
				return okA
			}
			if !okA {
				continue
			}
			c, ok := Compare(a, b)
			if !ok || c == 0 {
				continue
			}
			return (c < 0) != o.Desc
		}
		return out[i].ID < out[j].ID
	})
	if q.Offset > 0 {
		if q.Offset >= len(out) {
			return nil
		}
		out = out[q.Offset:]
	}
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out
}
