package placeholder_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/go-kanna/orm/internal/placeholder"
	"github.com/go-kanna/orm/internal/snapshot"
)

func TestRewrite(t *testing.T) {
	t.Parallel()

	const (
		pg = snapshot.PostgreSQL
		my = snapshot.MySQL
	)
	tests := []struct {
		name      string
		dialect   snapshot.Dialect
		sql, want string
		names     []string
	}{
		{"one number for each name", pg,
			`WHERE a = :a AND (b > :b OR c = :a)`,
			`WHERE a = $1 AND (b > $2 OR c = $1)`, []string{"a", "b"}},
		{"a name in camel case", pg, `WHERE customer_id = :customerID`, `WHERE customer_id = $1`, []string{"customerID"}},
		{"casts", pg, `SELECT :at::date, x::text`, `SELECT $1::date, x::text`, []string{"at"}},
		{"slices", pg, `SELECT arr[1:n], arr[i:n], arr[i]:n`, `SELECT arr[1:n], arr[i:n], arr[i]:n`, nil},
		{"strings", pg,
			`SELECT ':a', 'it''s :a', E'\':a', e'\\', '\', :b`,
			`SELECT ':a', 'it''s :a', E'\':a', e'\\', '\', $1`, []string{"b"}},
		{"a name ending in e before a string", pg, `SELECT name'\', :a`, `SELECT name'\', $1`, []string{"a"}},
		{"quoted names", pg, `SELECT "col:a", "x""y:a" WHERE z = :z`, `SELECT "col:a", "x""y:a" WHERE z = $1`, []string{"z"}},
		{"comments", pg,
			"-- :a\n/* :b /* :c */ :d */ :e",
			"-- :a\n/* :b /* :c */ :d */ $1", []string{"e"}},
		{"dollar quotes", pg,
			`SELECT $$ :a $$, $fn$ :b $$ :c $fn$, :d`,
			`SELECT $$ :a $$, $fn$ :b $$ :c $fn$, $1`, []string{"d"}},
		{"a dollar in a name", pg, `SELECT a$b WHERE x = :x`, `SELECT a$b WHERE x = $1`, []string{"x"}},
		{"a name beyond ASCII", pg, `WHERE size = :größe`, `WHERE size = $1`, []string{"größe"}},

		{"a ? for each use", my,
			`WHERE a = :a AND (b > :b OR c = :a)`,
			`WHERE a = ? AND (b > ? OR c = ?)`, []string{"a", "b", "a"}},
		{"an assignment", my, `SET @x := :a`, `SET @x := ?`, []string{"a"}},
		{"strings with backslashes", my,
			`SELECT '\':a', "\":a", 'it''s :a', :b`,
			`SELECT '\':a', "\":a", 'it''s :a', ?`, []string{"b"}},
		{"quoted names", my, "SELECT `col:a`, `x``y:a`, :b", "SELECT `col:a`, `x``y:a`, ?", []string{"b"}},
		{"comments, which do not nest", my,
			"-- :a\n# :b\n/* /* */ :c",
			"-- :a\n# :b\n/* /* */ ?", []string{"c"}},
		{"-- with no space after it", my, `SELECT 1--:a`, `SELECT 1--?`, []string{"a"}},
		{"$ as any other character", my, `SELECT '$$', :a`, `SELECT '$$', ?`, []string{"a"}},

		{"the ; that ends it", pg, "SELECT :a;\n", "SELECT $1", []string{"a"}},
		{"the ; that ends it, before comments", my, "SELECT :a ; -- done\n# for now\n", "SELECT ?", []string{"a"}},
		{"a ; between statements", pg, "SELECT 1; SELECT 2;", "SELECT 1; SELECT 2", nil},
		{"a ; in a string", pg, "SELECT ';'", "SELECT ';'", nil},
		{"a comment at the end", pg, "SELECT 1 -- see;\n", "SELECT 1", nil},
		{"a comment at the end of a line before more", my, "SELECT 1 -- one\n, 2 # two", "SELECT 1 -- one\n, 2", nil},
		{"the space at the end", my, "SELECT 1\n\n", "SELECT 1", nil},
	}
	for _, tt := range tests {
		t.Run(string(tt.dialect)+"/"+tt.name, func(t *testing.T) {
			t.Parallel()

			got, names, err := placeholder.Rewrite(tt.sql, tt.dialect)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("Rewrite = %s, want %s", got, tt.want)
			}
			if !slices.Equal(names, tt.names) {
				t.Errorf("names = %q, want %q", names, tt.names)
			}
		})
	}
}

func TestRewrite_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		dialect      snapshot.Dialect
		sql          string
		line, column int
		message      string
	}{
		{"a string", snapshot.PostgreSQL, "SELECT 'a\nFROM t", 1, 8, "a string that starts here does not end"},
		{"a quoted name", snapshot.PostgreSQL, `SELECT "a`, 1, 8, "a quoted name that starts here does not end"},
		{"a comment", snapshot.PostgreSQL, "SELECT 1\n/* /* */", 2, 1, "a comment that starts here does not end"},
		{"a dollar-quoted body", snapshot.PostgreSQL, "SELECT $q$ x $$", 1, 8,
			"a dollar-quoted body that starts here does not end"},
		{"a placeholder of PostgreSQL", snapshot.PostgreSQL, "WHERE a = :a\n  AND b = $12", 2, 11,
			"$12 is a placeholder of PostgreSQL's own: write the parameter as :name"},
		{"a placeholder of MySQL", snapshot.MySQL, "WHERE a = ?", 1, 11,
			"? is a placeholder of MySQL's own: write the parameter as :name"},
		{"a quoted name in MySQL", snapshot.MySQL, "SELECT `a", 1, 8, "a quoted name that starts here does not end"},
		{"a column counted in characters", snapshot.MySQL, "SELECT 'é', 'x", 1, 13,
			"a string that starts here does not end"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := placeholder.Rewrite(tt.sql, tt.dialect)
			got, ok := errors.AsType[*placeholder.Error](err)
			if !ok {
				t.Fatalf("Rewrite error = %v, want a *placeholder.Error", err)
			}
			want := placeholder.Error{Line: tt.line, Column: tt.column, Message: tt.message}
			if *got != want {
				t.Errorf("Rewrite error = %+v, want %+v", *got, want)
			}
		})
	}
}

func TestSplit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dialect snapshot.Dialect
		sql     string
		pieces  []string
		names   []string
	}{
		{"no parameter", snapshot.PostgreSQL, `status <> 'closed'`, []string{`status <> 'closed'`}, nil},
		{"a name used twice", snapshot.PostgreSQL,
			`date_trunc('month', placed_at) = date_trunc('month', :at) OR :at IS NULL`,
			[]string{`date_trunc('month', placed_at) = date_trunc('month', `, `) OR `, ` IS NULL`}, []string{"at", "at"}},
		{"what holds no parameter", snapshot.PostgreSQL,
			"data ? ':a' /* :b */ AND x::text = :c -- :d\n",
			[]string{"data ? ':a' /* :b */ AND x::text = ", ""}, []string{"c"}},
		{"a parameter at either end", snapshot.MySQL, `:a = :b`, []string{"", " = ", ""}, []string{"a", "b"}},
		{"the ; at the end", snapshot.MySQL, "`order` = :n;\n-- done\n", []string{"`order` = ", ""}, []string{"n"}},
	}
	for _, tt := range tests {
		pieces, names, err := placeholder.Split(tt.sql, tt.dialect)
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if !slices.Equal(pieces, tt.pieces) || !slices.Equal(names, tt.names) {
			t.Errorf("%s: Split = %q, %q; want %q, %q", tt.name, pieces, names, tt.pieces, tt.names)
		}
	}

	if _, _, err := placeholder.Split(`a = $1`, snapshot.PostgreSQL); err == nil {
		t.Error("Split takes a placeholder of PostgreSQL's own")
	}
}
