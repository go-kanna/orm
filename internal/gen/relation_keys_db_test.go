package gen_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-kanna/internal/cli"
	"github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/go-kanna/orm/internal/describe"
)

// relationKeysModels are the models of the tests of relations by keys that Go and the database compare otherwise,
// with what each database adds.
const relationKeysModels = `
-- store/store.go --
package store

import "context"

//kanna:scopes model-package=.

type Author struct {
	ID      string
	Name    string
	Books   []Book
	Aliases []string ` + "`orm:\"relation=author_aliases\"`" + `
}

type Book struct {
	ID       int64
	AuthorID string
	Title    string
	Author   *Author
	Tags     []Tag
}

type Tag struct {
	ID string
}

//kanna:repository
type AuthorRepository interface {
	List(ctx context.Context, s ...AuthorScope) ([]Author, error)
}

//kanna:repository
type BookRepository interface {
	List(ctx context.Context, s ...BookScope) ([]Book, error)
}
`

// relationKeysChecks reads the relations of the authors and the books, whose keys differ in case from those they are
// related to, and which the database compares regardless of it.
const relationKeysChecks = `
func TestRelationKeys(t *testing.T) {
	ctx := t.Context()
	db := open(t)
	books, err := NewBookRepository(db).List(ctx, Books().IDAsc().WithAuthor().WithTags(Tags().IDAsc()))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, b := range books {
		author := "nil"
		if b.Author != nil {
			author = b.Author.Name
		}
		var tags []string
		for _, tag := range b.Tags {
			tags = append(tags, tag.ID)
		}
		got = append(got, fmt.Sprintf("%d:%s:%v", b.ID, author, tags))
	}
	if want := []string{"1:Ann:[GO SQL]", "2:Ann:[]", "3:Ann:[]", "4:Bob:[GO]"}; !slices.Equal(got, want) {
		t.Errorf("books = %v, want %v", got, want)
	}

	for _, tt := range []struct {
		name  string
		books BookScope
		want  []string
	}{
		{"every book", Books().IDAsc(), []string{"ANN:[1 2 3]:[annie]", "BOB:[4]:[bobby]"}},
		{"a page of each", Books().IDAsc().Offset(1).Limit(1), []string{"ANN:[2]:[annie]", "BOB:[]:[bobby]"}},
	} {
		authors, err := NewAuthorRepository(db).List(ctx, Authors().IDAsc().WithBooks(tt.books).WithAliases())
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, a := range authors {
			ids := []int64{}
			for _, b := range a.Books {
				ids = append(ids, b.ID)
			}
			got = append(got, fmt.Sprintf("%s:%v:%v", a.ID, ids, a.Aliases))
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("authors with %s = %v, want %v", tt.name, got, tt.want)
		}
	}
	checkMore(t, db)
}
`

// postgresRelationKeys adds to relationKeysModels keys of a float, NaN among them, and of an enum whose name SQL
// quotes.
const postgresRelationKeys = `
type Measure struct {
	ID    float64
	Label string
}

type Reading struct {
	ID        int64
	MeasureID float64
	Measure   *Measure
}

type Kind struct {
	ID    string
	Label string
}

type Item struct {
	ID     int64
	KindID string
	Kind   *Kind
}

//kanna:repository
type ReadingRepository interface {
	List(ctx context.Context, s ...ReadingScope) ([]Reading, error)
}

//kanna:repository
type ItemRepository interface {
	List(ctx context.Context, s ...ItemScope) ([]Item, error)
}
-- store/store_test.go --
package store

import (
	"fmt"
	"slices"
	"testing"
)
` + relationKeysChecks + `
func checkMore(t *testing.T, db DBTX) {
	ctx := t.Context()
	readings, err := NewReadingRepository(db).List(ctx, Readings().IDAsc().WithMeasure())
	if err != nil || len(readings) != 2 || readings[0].Measure == nil || readings[0].Measure.Label != "unknown" ||
		readings[1].Measure == nil || readings[1].Measure.Label != "one and a half" {
		t.Errorf("readings with their measures, one of NaN = %+v, %v", readings, err)
	}
	items, err := NewItemRepository(db).List(ctx, Items().IDAsc().WithKind())
	if err != nil || len(items) != 2 || items[0].Kind == nil || items[0].Kind.Label != "S" ||
		items[1].Kind == nil || items[1].Kind.Label != "L" {
		t.Errorf("items with their kinds = %+v, %v", items, err)
	}
}
`

const postgresRelationKeysSchema = `
CREATE COLLATION ci (provider = icu, locale = 'und-u-ks-level2', deterministic = false);
CREATE TYPE "Kind" AS ENUM ('small', 'large');
CREATE TABLE authors (id text COLLATE ci PRIMARY KEY, name text NOT NULL);
CREATE TABLE books (id bigint PRIMARY KEY, author_id text COLLATE ci NOT NULL REFERENCES authors, title text NOT NULL);
CREATE TABLE author_aliases (author_id text COLLATE ci NOT NULL REFERENCES authors, alias text NOT NULL);
CREATE TABLE tags (id text COLLATE ci PRIMARY KEY);
CREATE TABLE book_tags (book_id bigint NOT NULL REFERENCES books, tag_id text COLLATE ci NOT NULL REFERENCES tags);
CREATE TABLE measures (id float8 PRIMARY KEY, label text NOT NULL);
CREATE TABLE readings (id bigint PRIMARY KEY, measure_id float8 NOT NULL REFERENCES measures);
CREATE TABLE kinds (id "Kind" PRIMARY KEY, label text NOT NULL);
CREATE TABLE items (id bigint PRIMARY KEY, kind_id "Kind" NOT NULL REFERENCES kinds);
INSERT INTO authors VALUES ('ANN', 'Ann'), ('BOB', 'Bob');
INSERT INTO books VALUES (1, 'ann', 'A1'), (2, 'Ann', 'A2'), (3, 'ANN', 'A3'), (4, 'bob', 'B1');
INSERT INTO author_aliases VALUES ('ann', 'annie'), ('Bob', 'bobby');
INSERT INTO tags VALUES ('GO'), ('SQL');
INSERT INTO book_tags VALUES (1, 'go'), (1, 'Sql'), (4, 'GO');
INSERT INTO measures VALUES ('NaN', 'unknown'), (1.5, 'one and a half');
INSERT INTO readings VALUES (1, 'NaN'), (2, 1.5);
INSERT INTO kinds VALUES ('small', 'S'), ('large', 'L');
INSERT INTO items VALUES (1, 'small'), (2, 'large');
`

// TestCommand_RelationKeysPostgres loads relations whose keys PostgreSQL compares otherwise than Go: text of a
// nondeterministic collation, which ignores case, NaN, which equals itself, and an enum whose name is quoted.
func TestCommand_RelationKeysPostgres(t *testing.T) {
	t.Parallel()
	url := os.Getenv("KANNA_ORM_TEST_POSTGRES")
	if url == "" {
		t.Skip("integration database is not configured")
	}
	schema := fmt.Sprintf("kanna_keys_%d", time.Now().UnixNano())
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.WithoutCancel(t.Context()), "DROP SCHEMA "+schema+" CASCADE")
		_ = conn.Close()
	})
	if _, err := conn.ExecContext(ctx, "CREATE SCHEMA "+schema+"; SET search_path TO "+schema+";"+
		postgresRelationKeysSchema); err != nil {
		t.Fatal(err)
	}

	for _, driver := range []string{"pgx", "database/sql"} {
		t.Run(driver, func(t *testing.T) {
			t.Parallel()
			open := `
-- store/open_test.go --
package store

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func open(t *testing.T) DBTX {
	db, err := sql.Open("pgx", os.Getenv("KANNA_ORM_TEST_POSTGRES"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := conn.ExecContext(t.Context(), "SET search_path TO ` + schema + `"); err != nil {
		t.Fatal(err)
	}
	return conn
}
`
			if driver == "pgx" {
				open = `
-- store/open_test.go --
package store

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

func open(t *testing.T) DBTX {
	conn, err := pgx.Connect(t.Context(), os.Getenv("KANNA_ORM_TEST_POSTGRES"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	if _, err := conn.Exec(t.Context(), "SET search_path TO ` + schema + `"); err != nil {
		t.Fatal(err)
	}
	return conn
}
`
			}
			dir := writeModule(t, relationKeysModels+postgresRelationKeys+open)
			describeRelationKeys(t, dir, schemaURL(url, schema))
			code, _, errOut := run(t, filepath.Join(dir, "store"), "-destination", ".", "-driver", driver)
			if code != cli.ExitOK {
				t.Fatalf("kanna-orm = %d\n%s", code, errOut)
			}
			moduleCommand(t, dir, "test", "-mod=mod", "./...")
		})
	}
}

// mysqlRelationKeys adds to relationKeysModels keys of a column that tells case apart, which a connection of a
// collation that does not reads.
const mysqlRelationKeys = `
type Code struct {
	ID    string
	Label string
}

type Use struct {
	ID     int64
	CodeID string
	Code   *Code
}

//kanna:repository
type UseRepository interface {
	List(ctx context.Context, s ...UseScope) ([]Use, error)
}
-- store/store_test.go --
package store

import (
	"database/sql"
	"fmt"
	"os"
	"slices"
	"testing"

	_ "github.com/go-sql-driver/mysql"
)

func open(t *testing.T) DBTX {
	db, err := sql.Open("mysql", os.Getenv("KANNA_RELATION_KEYS_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
` + relationKeysChecks + `
func checkMore(t *testing.T, db DBTX) {
	uses, err := NewUseRepository(db).List(t.Context(), Uses().IDAsc().WithCode())
	if err != nil || len(uses) != 2 || uses[0].Code == nil || uses[0].Code.Label != "lower" ||
		uses[1].Code == nil || uses[1].Code.Label != "upper" {
		t.Errorf("uses with their codes, told apart by case = %+v, %v", uses, err)
	}
}
`

const mysqlRelationKeysSchema = `
CREATE TABLE authors (id varchar(10) PRIMARY KEY, name varchar(20) NOT NULL) COLLATE utf8mb4_0900_ai_ci;
CREATE TABLE books (id bigint PRIMARY KEY, author_id varchar(10) NOT NULL, title varchar(20) NOT NULL,
	FOREIGN KEY (author_id) REFERENCES authors (id)) COLLATE utf8mb4_0900_ai_ci;
CREATE TABLE author_aliases (author_id varchar(10) NOT NULL, alias varchar(20) NOT NULL,
	FOREIGN KEY (author_id) REFERENCES authors (id)) COLLATE utf8mb4_0900_ai_ci;
CREATE TABLE tags (id varchar(10) PRIMARY KEY) COLLATE utf8mb4_0900_ai_ci;
CREATE TABLE book_tags (book_id bigint NOT NULL, tag_id varchar(10) NOT NULL,
	FOREIGN KEY (book_id) REFERENCES books (id), FOREIGN KEY (tag_id) REFERENCES tags (id)) COLLATE utf8mb4_0900_ai_ci;
CREATE TABLE codes (id varchar(10) PRIMARY KEY, label varchar(20) NOT NULL) COLLATE utf8mb4_bin;
CREATE TABLE uses (id bigint PRIMARY KEY, code_id varchar(10) NOT NULL,
	FOREIGN KEY (code_id) REFERENCES codes (id)) COLLATE utf8mb4_bin;
INSERT INTO authors VALUES ('ANN', 'Ann'), ('BOB', 'Bob');
INSERT INTO books VALUES (1, 'ann', 'A1'), (2, 'Ann', 'A2'), (3, 'ANN', 'A3'), (4, 'bob', 'B1');
INSERT INTO author_aliases VALUES ('ann', 'annie'), ('Bob', 'bobby');
INSERT INTO tags VALUES ('GO'), ('SQL');
INSERT INTO book_tags VALUES (1, 'go'), (1, 'Sql'), (4, 'GO');
INSERT INTO codes VALUES ('abc', 'lower'), ('ABC', 'upper');
INSERT INTO uses VALUES (1, 'abc'), (2, 'ABC');
`

// TestCommand_RelationKeysMySQL loads relations whose keys MySQL compares otherwise than Go: text of the default
// collation, which ignores case, and of one that does not, read on a connection of a collation that ignores it.
func TestCommand_RelationKeysMySQL(t *testing.T) {
	t.Parallel()
	dsn := os.Getenv("KANNA_ORM_TEST_MYSQL")
	if dsn == "" {
		t.Skip("integration database is not configured")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	database := fmt.Sprintf("kanna_keys_%d", time.Now().UnixNano())
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.WithoutCancel(t.Context()), "DROP DATABASE "+database)
		_ = db.Close()
	})
	if _, err := db.ExecContext(t.Context(), "CREATE DATABASE "+database+"; USE "+database+";"+
		mysqlRelationKeysSchema); err != nil {
		t.Fatal(err)
	}

	cfg.DBName, cfg.Collation, cfg.MultiStatements = database, "utf8mb4_general_ci", false
	dir := writeModule(t, relationKeysModels+mysqlRelationKeys)
	describeRelationKeys(t, dir, fmt.Sprintf("mysql://%s:%s@%s/%s", cfg.User, cfg.Passwd, cfg.Addr, database))
	if code, _, errOut := run(t, filepath.Join(dir, "store"), "-destination", "."); code != cli.ExitOK {
		t.Fatalf("kanna-orm = %d\n%s", code, errOut)
	}
	moduleCommandEnv(t, dir, []string{"KANNA_RELATION_KEYS_DSN=" + cfg.FormatDSN()}, "test", "-mod=mod", "./...")
}

// describeRelationKeys writes the snapshot of the database at url into the module at dir.
func describeRelationKeys(t *testing.T, dir, url string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := describe.Exec(cli.Env{Out: &out, Err: &errOut, Dir: dir}, []string{"-database-url", url, "./..."})
	if code != cli.ExitOK {
		t.Fatalf("kanna-orm describe = %d\n%s%s", code, out.String(), errOut.String())
	}
}

// schemaURL is url, of PostgreSQL, with its tables those of schema.
func schemaURL(url, schema string) string {
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	return url + sep + "search_path=" + schema
}

// moduleCommandEnv runs go with args in the module at dir, as moduleCommand does, with env added to the environment.
func moduleCommandEnv(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "go", args...) //nolint:gosec // the test supplies Go tool arguments
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), "GOWORK=off"), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
