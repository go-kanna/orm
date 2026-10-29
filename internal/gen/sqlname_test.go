package gen_test

import (
	"database/sql"
	"os"
	"reflect"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"

	"github.com/go-kanna/orm/internal/gen"
	"github.com/go-kanna/orm/internal/snapshot"
)

func TestSQLName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, postgres, mysql string
	}{
		{"placed_at", "placed_at", "placed_at"},
		{"x509", "x509", "x509"},
		{"order", `"order"`, "`order`"},
		{"user", `"user"`, "user"},
		{"key", "key", "`key`"},
		{"left", `"left"`, "`left`"},
		{"Note", `"Note"`, "`Note`"},
		{"2fa", `"2fa"`, "`2fa`"},
		{"a b", `"a b"`, "`a b`"},
		{`a"b`, `"a""b"`, "`a\"b`"},
		{"a`b", "\"a`b\"", "`a``b`"},
	}
	for _, tt := range tests {
		if got := gen.SQLName(snapshot.PostgreSQL, tt.name); got != tt.postgres {
			t.Errorf("sqlName(PostgreSQL, %q) = %s, want %s", tt.name, got, tt.postgres)
		}
		if got := gen.SQLName(snapshot.MySQL, tt.name); got != tt.mysql {
			t.Errorf("sqlName(MySQL, %q) = %s, want %s", tt.name, got, tt.mysql)
		}
	}
}

func TestReserved_Postgres(t *testing.T) {
	t.Parallel()

	url := os.Getenv("KANNA_ORM_TEST_POSTGRES")
	if url == "" {
		t.Skip("KANNA_ORM_TEST_POSTGRES is not set; make db-up test-db sets it")
	}
	conn, err := pgx.Connect(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(t.Context()) //nolint:errcheck // nothing was written
	rows, err := conn.Query(t.Context(),
		"SELECT word FROM pg_get_keywords() WHERE catcode IN ('R', 'T') ORDER BY 1")
	if err != nil {
		t.Fatal(err)
	}
	words, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if got := gen.Reserved(snapshot.PostgreSQL); !reflect.DeepEqual(got, words) {
		t.Errorf("the reserved words of PostgreSQL are\n%q\nbut the database has\n%q", got, words)
	}
}

func TestReserved_MySQL(t *testing.T) {
	t.Parallel()

	dsn := os.Getenv("KANNA_ORM_TEST_MYSQL")
	if dsn == "" {
		t.Skip("KANNA_ORM_TEST_MYSQL is not set; make db-up test-db sets it")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck // nothing was written
	rows, err := db.QueryContext(t.Context(),
		"SELECT lower(word) FROM information_schema.keywords WHERE reserved = 1 ORDER BY 1")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close() //nolint:errcheck // rows.Err tells
	var words []string
	for rows.Next() {
		var w string
		if err := rows.Scan(&w); err != nil {
			t.Fatal(err)
		}
		words = append(words, w)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if got := gen.Reserved(snapshot.MySQL); !reflect.DeepEqual(got, words) {
		t.Errorf("the reserved words of MySQL are\n%q\nbut the database has\n%q", got, words)
	}
}
