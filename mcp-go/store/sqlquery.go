// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"regexp"
	"strings"
)

// sqlWhere is a condition SQL can test on the docs table that every
// document meeting e also meets: what it lets through is still judged by
// Match, so SQL narrows the rows read and decoded and the three stores
// still answer alike. Only what SQL tests exactly as Match does is handed
// down: a field (or the id) equal to a string, in a list of strings, a list
// holding a string; an Or only when each of its branches is; the rest of
// an And is left to Match. "" when nothing narrows.
func sqlWhere(e Expr) (string, []any) {
	switch x := e.(type) {
	case Const:
		if !bool(x) {
			return "0", nil
		}
	case Cmp:
		return sqlCmp(x)
	case And:
		var parts []string
		var args []any
		for _, y := range x {
			if w, a := sqlWhere(y); w != "" {
				parts = append(parts, w)
				args = append(args, a...)
			}
		}
		if len(parts) > 0 {
			return "(" + strings.Join(parts, " AND ") + ")", args
		}
	case Or:
		var parts []string
		var args []any
		for _, y := range x {
			w, a := sqlWhere(y)
			if w == "" {
				return "", nil // one branch SQL cannot narrow: all of them pass
			}
			parts = append(parts, w)
			args = append(args, a...)
		}
		if len(parts) > 0 {
			return "(" + strings.Join(parts, " OR ") + ")", args
		}
	}
	return "", nil
}

// a dotted field of plain names, as a JSON path
var sqlField = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)

// the expression a field is read with; the indexes (SQLiteSchema) are on
// these same expressions, so they are used
func sqlFieldExpr(field string) string {
	if field == "_id" {
		return "id"
	}
	return "json_extract(doc, '$." + field + "')"
}

func sqlCmp(c Cmp) (string, []any) {
	if c.Field != "_id" && !sqlField.MatchString(c.Field) {
		return "", nil
	}
	switch c.Op {
	case OpEq:
		if s, ok := c.Value.(string); ok {
			return sqlFieldExpr(c.Field) + " = ?", []any{s}
		}
	case OpIn:
		list, _ := c.Value.([]any)
		if len(list) == 0 {
			return "0", nil
		}
		args := make([]any, 0, len(list))
		for _, v := range list {
			s, ok := v.(string)
			if !ok {
				return "", nil
			}
			args = append(args, s)
		}
		return sqlFieldExpr(c.Field) + " IN (" + strings.TrimSuffix(strings.Repeat("?,", len(args)), ",") + ")", args
	case OpHas:
		if s, ok := c.Value.(string); ok && c.Field != "_id" {
			return "EXISTS (SELECT 1 FROM json_each(doc, '$." + c.Field + "') WHERE json_each.value = ? AND json_each.type = 'text')", []any{s}
		}
	}
	return "", nil
}
