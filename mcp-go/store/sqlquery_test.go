package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestSQLWhere(t *testing.T) {
	for _, c := range []struct {
		e    Expr
		want string
		args int
	}{
		{Eq("tenant", "t1"), "json_extract(doc, '$.tenant') = ?", 1},
		{Eq("_id", "a"), "id = ?", 1},
		{Eq("n", int64(3)), "", 0}, // numbers are left to Match
		{Eq("a-b", "x"), "", 0},    // not a plain path
		{And{Eq("tenant", "t"), Gt("n", int64(1))}, "(json_extract(doc, '$.tenant') = ?)", 1},
		{Or{Eq("from", "a"), Eq("to", "a")}, "(json_extract(doc, '$.from') = ? OR json_extract(doc, '$.to') = ?)", 2},
		{Or{Eq("from", "a"), Ne("to", "a")}, "", 0}, // one branch cannot be narrowed
		{In("member", "u1", "g:x"), "json_extract(doc, '$.member') IN (?,?)", 2},
		{In("member"), "0", 0},
		{False, "0", 0},
		{Not{X: Eq("kind", "t")}, "", 0},
	} {
		w, a := sqlWhere(c.e)
		if w != c.want || len(a) != c.args {
			t.Errorf("%#v: %q %v, want %q", c.e, w, a, c.want)
		}
	}
}

// the queries rooms and shares make read through their indexes
// once the store has statistics (OpenSQLiteStore runs PRAGMA optimize),
// a query on a field takes that field's index rather than reading the
// whole collection by primary key
func TestSQLIndexesUsed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.db")
	s, err := OpenSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i := 0; i < 2000; i++ {
		doc := fmt.Sprintf(`{"tenant":"t%d","owner":"o%d","room":"r%d","member":"m%d"}`, i%40, i%30, i%20, i%10)
		if _, err := s.db.ExecContext(ctx, `INSERT INTO docs VALUES ('c', ?, 1, ?)`, fmt.Sprint(i), doc); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	if s, err = OpenSQLiteStore(path); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, field := range []string{"tenant", "owner", "room", "member"} {
		w, args := sqlWhere(Eq(field, "x"))
		rows, err := s.db.QueryContext(ctx, "EXPLAIN QUERY PLAN SELECT id, rev, doc FROM docs WHERE col = ? AND "+w, append([]any{"c"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		plan := ""
		for rows.Next() {
			var a, b, c int
			var d string
			rows.Scan(&a, &b, &c, &d)
			plan += d + "\n"
		}
		rows.Close()
		if !strings.Contains(plan, "docs_"+field) {
			t.Errorf("%s: %s", field, plan)
		}
	}
}
