package orm_test

import (
	"encoding/base64"
	"slices"
	"strings"
	"testing"

	"github.com/go-kanna/orm"
)

var (
	key = []string{"id"}
	row = map[string]any{"id": 9, "placed_at": 5, "status": "open", "note": orm.MayBeNull}
)

func TestAfter(t *testing.T) {
	t.Parallel()

	var q orm.Of[order]
	tests := []struct {
		name    string
		q       orm.Of[order]
		dialect orm.Dialect
		sql     string
		args    []any
	}{
		{"rows compared", q.OrderBy("placed_at", false).After(key, row).Limit(20), orm.Postgres,
			"SELECT id FROM orders WHERE ((placed_at, id) > ($1, $2)) ORDER BY placed_at, id LIMIT 20", []any{5, 9}},
		{"rows compared, the latest first", q.OrderBy("placed_at", true).After(key, row), orm.Postgres,
			"SELECT id FROM orders WHERE ((placed_at, id) < ($1, $2)) ORDER BY placed_at DESC, id DESC", []any{5, 9}},
		{"spelled out on MySQL", q.OrderBy("placed_at", true).After(key, row), orm.MySQL,
			"SELECT id FROM orders WHERE ((placed_at < ?) OR (placed_at = ? AND id < ?)) ORDER BY placed_at DESC, id DESC",
			[]any{5, 5, 9}},
		{"spelled out for an order going both ways", q.OrderBy("status", false).OrderBy("placed_at", true).After(key, row),
			orm.Postgres, "SELECT id FROM orders WHERE ((status > $1) OR (status = $2 AND placed_at < $3) OR " +
				"(status = $4 AND placed_at = $5 AND id < $6)) ORDER BY status, placed_at DESC, id DESC",
			[]any{"open", "open", 5, "open", 5, 9}},
		{"an order holding the key already", q.OrderBy("id", true).OrderBy("placed_at", false).After(key, row), orm.Postgres,
			"SELECT id FROM orders WHERE ((id < $1) OR (id = $2 AND placed_at > $3)) ORDER BY id DESC, placed_at",
			[]any{9, 9, 5}},
		{"no order but the key", q.After(key, row), orm.Postgres,
			"SELECT id FROM orders WHERE ((id) > ($1)) ORDER BY id", []any{9}},
		{"after conditions of its own", q.Where("status = ", "open").OrderBy("placed_at", false).After(key, row),
			orm.Postgres, "SELECT id FROM orders WHERE (status = $1) AND ((placed_at, id) > ($2, $3)) ORDER BY placed_at, id",
			[]any{"open", 5, 9}},
		{"before, read the other way", q.OrderBy("placed_at", false).Before(key, row).Limit(20), orm.Postgres,
			"SELECT id FROM orders WHERE ((placed_at, id) < ($1, $2)) ORDER BY placed_at DESC, id DESC LIMIT 20",
			[]any{5, 9}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sql, args := query(t, tt.q, tt.dialect, 1)
			if sql != tt.sql || !slices.Equal(args, tt.args) {
				t.Errorf("Render =\n%s %v\nwant\n%s %v", sql, args, tt.sql, tt.args)
			}
		})
	}
}

func TestBefore_Reversed(t *testing.T) {
	t.Parallel()

	var q orm.Of[order]
	for _, tt := range []struct {
		q    orm.Of[order]
		want bool
	}{
		{q.OrderBy("placed_at", false), false},
		{q.OrderBy("placed_at", false).After(key, row), false},
		{q.OrderBy("placed_at", false).Before(key, row), true},
	} {
		r, err := tt.q.Render(orm.Postgres, 1)
		if err != nil {
			t.Fatal(err)
		}
		if r.Reversed() != tt.want {
			t.Errorf("Reversed = %v, want %v", r.Reversed(), tt.want)
		}
	}
}

func TestAfter_Errors(t *testing.T) {
	t.Parallel()

	var q orm.Of[order]
	tests := []struct {
		name string
		q    orm.Of[order]
		want string
	}{
		{"a column that may be NULL", q.OrderBy("note", false).After(key, row),
			"orm: a page cannot continue by note, which may be NULL"},
		{"a column the row has no value of", q.OrderBy("total", false).After(key, row),
			"orm: a page cannot continue by total, which the row has no value of"},
		{"a page continued in or", q.Or(q.After(key, row)),
			"orm: Or takes conditions alone, but its scope keeps only some of the rows"},
		{"a page continued among the related rows", orm.Preload(q, "Lines", orm.Of[line]{}.After(key, row)),
			"orm: Lines keeps only some of the rows it loads, which would count those of every row together"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := tt.q.Render(orm.Postgres, 1); err == nil || err.Error() != tt.want {
				t.Errorf("Render error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestCursor(t *testing.T) {
	t.Parallel()

	var q orm.Of[order]
	s := q.OrderBy("placed_at", true)
	token, err := s.Cursor(key, row)
	if err != nil {
		t.Fatal(err)
	}

	var id, placedAt int
	if err := s.DecodeCursor(token, key, map[string]any{"id": &id, "placed_at": &placedAt}); err != nil {
		t.Fatal(err)
	}
	if id != 9 || placedAt != 5 {
		t.Errorf("DecodeCursor gives id %d and placed_at %d, want 9 and 5", id, placedAt)
	}

	tests := []struct {
		name, token string
		q           orm.Of[order]
		want        string
	}{
		{"another order", token, q.OrderBy("placed_at", false),
			"orm: the cursor was made in another order: [placed_at desc id desc]"},
		{"not base64", "!", s, "orm: the cursor is not one kanna-orm made"},
		{"not JSON", "bm90IEpTT04", s, "orm: the cursor is not one kanna-orm made"},
		{"a null value", forged(`{"order":["placed_at desc","id desc"],"values":[5,null]}`), s,
			"orm: the cursor is not one kanna-orm made"},
		{"null values only", forged(`{"order":["placed_at desc","id desc"],"values":[null, null]}`), s,
			"orm: the cursor is not one kanna-orm made"},
		{"a value missing", forged(`{"order":["placed_at desc","id desc"],"values":[5]}`), s,
			"orm: the cursor is not one kanna-orm made"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var id, placedAt int
			err := tt.q.DecodeCursor(tt.token, key, map[string]any{"id": &id, "placed_at": &placedAt})
			if err == nil || err.Error() != tt.want {
				t.Errorf("DecodeCursor error = %v, want %q", err, tt.want)
			}
		})
	}
}

// forged encodes data as a cursor, as a client could send one kanna-orm did not make.
func forged(data string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(data))
}

func TestCursor_Errors(t *testing.T) {
	t.Parallel()

	var q orm.Of[order]
	if _, err := q.OrderBy("note", false).Cursor(key, row); err == nil ||
		err.Error() != "orm: a page cannot continue by note, which may be NULL" {
		t.Errorf("Cursor error = %v", err)
	}

	token, err := q.OrderBy("status", false).Cursor(key, row)
	if err != nil {
		t.Fatal(err)
	}
	var id, status int
	err = q.OrderBy("status", false).DecodeCursor(token, key, map[string]any{"id": &id, "status": &status})
	if err == nil || !strings.HasPrefix(err.Error(), "orm: decode status of the cursor: ") {
		t.Errorf("DecodeCursor error = %v, want one about decoding status", err)
	}
}
