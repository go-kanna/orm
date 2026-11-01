package gen_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"
	"github.com/go-kanna/internal/deps"

	"github.com/go-kanna/orm/internal/snapshot"
)

const wiringShop = `
-- db/db.go --
package db

import %q

type Reader struct { *%s }
type Tx struct { Connection %s }
func (t Tx) DBTX() %s { return t.Connection }
type NotADatabase struct{}
-- repository/repository.go --
package repository

import (
	"context"
	"example.com/shop/model"
	"example.com/shop/db"
)

//kanna:repository
type Orders interface {
	Insert(ctx context.Context, row model.Order) error
	Bind(tx db.Tx) Orders
}

-- repository/wiring_test.go --
package repository

import (
	"testing"
	"example.com/shop/db"
)

func TestWiring(t *testing.T) {
	reader := &db.Reader{}
	first := NewOrders(reader)
	if first.(*orders).db != reader { t.Fatal("constructor did not use reader") }
	tx := db.Tx{}
	bound := first.Bind(%s)
	if bound == first { t.Fatal("Bind reused the original repository") }
	if bound.(*orders).db != tx.DBTX() { t.Fatal("Bind did not use the transaction") }
	if first.(*orders).db != reader { t.Fatal("Bind changed the original repository") }
}
`

func wiringModule(t *testing.T, driver string, accessor bool) string {
	t.Helper()

	path, reader, transaction := "github.com/jackc/pgx/v5", "pgx.Conn", "pgx.Tx"
	if driver == "database/sql" {
		path, reader, transaction = "database/sql", "sql.DB", "*sql.Tx"
	}
	argument := "tx"
	if !accessor {
		argument = "tx.DBTX()"
	}
	archive := fmt.Sprintf(wiringShop, path, reader, transaction, transaction, argument)
	if !accessor {
		archive = strings.ReplaceAll(archive, "Bind(tx db.Tx)", "Bind(tx DBTX)")
		archive = strings.Replace(archive, "\t\"example.com/shop/db\"\n", "", 1)
	}
	models, _, _ := strings.Cut(repositoryShop, "-- repository/repository.go --")
	dir := writeModule(t, models+archive)
	describeRepositoryShop(t, dir, snapshot.PostgreSQL)
	moduleCommand(t, dir, "mod", "tidy")
	return dir
}

func moduleCommand(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "go", args...) //nolint:gosec // the test supplies Go tool arguments
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func TestCommand_RepositoryWiring(t *testing.T) {
	t.Parallel()

	for _, driver := range []string{"pgx", "database/sql"} {
		for _, accessor := range []bool{false, true} {
			for _, custom := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/accessor=%v/custom=%v", driver, accessor, custom), func(t *testing.T) {
					t.Parallel()

					dir := wiringModule(t, driver, accessor)
					args := []string{"-destination", ".", "-driver", driver}
					if custom {
						args = append(args, "-constructor-db", "*example.com/shop/db.Reader")
					}
					code, stdout, stderr := run(t, filepath.Join(dir, "repository"),
						append([]string{"-deps"}, args...)...)
					if code != cli.ExitOK {
						t.Fatalf("kanna-orm -deps = %d\n%s", code, stderr)
					}
					var report deps.Report
					if err := json.Unmarshal([]byte(stdout), &report); err != nil {
						t.Fatal(err)
					}
					if !slices.Contains(report.Declares, "example.com/shop/repository.NewOrders") {
						t.Fatalf("generated constructor missing from dependency report: %v", report.Declares)
					}
					for range 2 {
						if code, _, errOut := run(t, filepath.Join(dir, "repository"), args...); code != cli.ExitOK {
							t.Fatalf("kanna-orm = %d\n%s", code, errOut)
						}
					}
					if code, _, errOut := run(t, filepath.Join(dir, "scopes")); code != cli.ExitOK {
						t.Fatalf("kanna-orm in scopes = %d\n%s", code, errOut)
					}
					moduleCommand(t, dir, "test", "-mod=mod", "./...")
					if code, _, errOut := run(t, filepath.Join(dir, "repository"),
						append([]string{"-check"}, args...)...); code != cli.ExitOK {
						t.Fatalf("kanna-orm -check = %d\n%s", code, errOut)
					}
				})
			}
		}
	}
}

func TestCommand_RepositoryWiringMistakes(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, input, bind, want string
	}{
		{"unqualified type", "Reader", "", "-constructor-db wants a qualified type"},
		{"missing type", "*example.com/shop/db.Missing", "", "db.Missing is not a type"},
		{"not a database", "*example.com/shop/db.NotADatabase", "", "does not implement DBTX"},
		{"wrong argument", "", "Bind(tx int) Orders", "its argument must implement DBTX"},
		{"wrong arity", "", "Bind() Orders", "declare it as Bind(db DBTX) Orders"},
		{"wrong result", "", "Bind(tx db.Tx) error", "declare it as Bind(db DBTX) Orders"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := wiringModule(t, "database/sql", true)
			if tt.bind != "" {
				path := filepath.Join(dir, "repository", "repository.go")
				data, err := os.ReadFile(path) //nolint:gosec // the fixture written above
				if err != nil {
					t.Fatal(err)
				}
				text := strings.Replace(string(data), "Bind(tx db.Tx) Orders", tt.bind, 1)
				if !strings.Contains(tt.bind, "db.Tx") {
					text = strings.Replace(text, "\t\"example.com/shop/db\"\n", "", 1)
				}
				//nolint:gosec // the fixture written above
				if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{"-destination", ".", "-driver", "database/sql"}
			if tt.input != "" {
				args = append(args, "-constructor-db", tt.input)
			}
			code, _, errOut := run(t, filepath.Join(dir, "repository"), args...)
			if code != cli.ExitFailure || !strings.Contains(errOut, tt.want) {
				t.Fatalf("kanna-orm = %d\n%s\nwant %s", code, errOut, tt.want)
			}
		})
	}
}
