package gen_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"
	"github.com/go-kanna/internal/packages"

	"github.com/go-kanna/orm/internal/gen"
	"github.com/go-kanna/orm/internal/snapshot"
)

// run runs kanna-orm in dir with args, and returns its exit code and what it wrote to stdout and stderr.
func run(t *testing.T, dir string, args ...string) (code int, stdout, stderr string) {
	t.Helper()

	var out, errOut bytes.Buffer
	code = gen.Command().Exec(cli.Env{Out: &out, Err: &errOut, Dir: dir}, args)
	return code, out.String(), errOut.String()
}

// shopModule declares orders, a hand-written scope in Go that calls a method kanna-orm generates, and one in a .sql
// file.
const shopModule = `
-- model/model.go --
package model

type Order struct {
	ID     int64
	Status string
}
-- scopes/scopes.go --
package scopes

import (
	"example.com/shop/model"
	"github.com/go-kanna/orm"
)

//kanna:scope table=orders
type Order struct{ q orm.Of[model.Order] }

// Open narrows to the orders that are not closed.
func (s Order) Open() Order {
	return s.StatusNe("closed")
}

func recent() Order {
	return Orders().IDDesc().Limit(10)
}
-- scopes/order_scopes.sql --
-- scope: Order.Paid()
status = 'paid'
`

// describeShop writes the snapshot of the tables shopModule reads into the module at dir.
func describeShop(t *testing.T, dir string) {
	t.Helper()

	s := snapshot.Snapshot{
		Dialect: snapshot.PostgreSQL,
		Tables: map[string]snapshot.Table{
			"orders": {
				Columns: []snapshot.Column{
					{Name: "id", Type: "int8", NotNull: true, Assigned: true},
					{Name: "status", Type: "text", NotNull: true},
				},
				PrimaryKey: []string{"id"},
			},
		},
		Statements: map[string]snapshot.Statement{
			"example.com/shop/scopes.Order.Paid": {Digest: snapshot.Digest("status = 'paid'")},
		},
	}
	if err := os.WriteFile(filepath.Join(dir, snapshot.Name), snapshot.Marshal(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCommand(t *testing.T) {
	t.Parallel()

	dir := writeModule(t, shopModule)
	describeShop(t, dir)
	scopes := filepath.Join(dir, "scopes")

	if code, out, errOut := run(t, scopes, "-v"); code != cli.ExitOK || out != "wrote ./orm_gen.go\n" || errOut != "" {
		t.Fatalf("kanna-orm -v = %d\n%s%s", code, out, errOut)
	}
	set, err := packages.Load([]string{"./..."}, packages.Config{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Diags) > 0 {
		t.Fatalf("the module does not compile once generated: %v", set.Diags)
	}

	if code, _, errOut := run(t, scopes, "-check"); code != cli.ExitOK {
		t.Errorf("kanna-orm -check right after generating = %d\n%s", code, errOut)
	}
	path := filepath.Join(scopes, "orm_gen.go")
	src, err := os.ReadFile(path) //nolint:gosec // the file written above
	if err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // the file written above
	if err := os.WriteFile(path, bytes.Replace(src, []byte("status"), []byte("state"), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := run(t, scopes, "-check"); code != cli.ExitFailure || !strings.Contains(errOut, "orm_gen.go") {
		t.Errorf("kanna-orm -check of a file changed since = %d\n%s", code, errOut)
	}
}

func TestCommand_RemovesWhatNoDeclarationNeeds(t *testing.T) {
	t.Parallel()

	dir := writeModule(t, shopModule)
	describeShop(t, dir)
	scopes := filepath.Join(dir, "scopes")
	if code, _, errOut := run(t, scopes); code != cli.ExitOK {
		t.Fatalf("kanna-orm = %d\n%s", code, errOut)
	}

	const plain = "package scopes\n\ntype Order struct{}\n"
	if err := os.WriteFile(filepath.Join(scopes, "scopes.go"), []byte(plain), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := run(t, scopes, "-v")
	if code != cli.ExitOK || out != "removed ./orm_gen.go\n" ||
		errOut != "warning: no type is marked //kanna:scope or //kanna:repository in example.com/shop/scopes\n" {
		t.Errorf("kanna-orm -v = %d\n%s%s", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(scopes, "orm_gen.go")); !os.IsNotExist(err) {
		t.Errorf("orm_gen.go is still there: %v", err)
	}
}

func TestCommand_Mistakes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// describe writes the snapshot, and extra adds to the module.
		describe bool
		extra    string
		want     string
	}{
		{"no snapshot", false, "",
			"../kanna-orm.snapshot.json: error: not described yet\n  run go tool kanna-orm describe to write it\n"},
		{"an error not about what is generated", true, "-- scopes/broken.go --\npackage scopes\n\nvar _ int = \"one\"\n",
			"./broken.go:3:13: error: cannot use \"one\" (untyped string constant) as int value in variable declaration\n"},
		{"a mistake behind a call to what is generated", true,
			"-- scopes/broken.go --\npackage scopes\n\nvar _ = Orders().Closed()\n",
			"./broken.go:3:18: error: Orders().Closed undefined (type Order has no field or method Closed)\n"},
		{"a mistake in a declaration", true, "-- scopes/more.go --\npackage scopes\n\nimport (\n" +
			"\t\"example.com/shop/model\"\n\t\"github.com/go-kanna/orm\"\n)\n\n" +
			"//kanna:scope table=order\ntype Archived struct{ q orm.Of[model.Order] }\n",
			"./more.go:8:1: error: Archived: the snapshot has no table order\n  did you mean orders?\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := writeModule(t, shopModule+tt.extra)
			if tt.describe {
				describeShop(t, dir)
			}
			code, _, errOut := run(t, filepath.Join(dir, "scopes"))
			if code != cli.ExitFailure || errOut != tt.want {
				t.Errorf("kanna-orm = %d\n%s\nwant %d\n%s", code, errOut, cli.ExitFailure, tt.want)
			}
		})
	}
}

func TestCommand_NothingDeclared(t *testing.T) {
	t.Parallel()

	dir := writeModule(t, "-- plain/plain.go --\npackage plain\n")
	code, out, errOut := run(t, filepath.Join(dir, "plain"))
	if code != cli.ExitOK || out != "" ||
		errOut != "warning: no type is marked //kanna:scope or //kanna:repository in example.com/shop/plain\n" {
		t.Errorf("kanna-orm = %d\n%s%s", code, out, errOut)
	}
}

func TestCommand_KeepsWhatCodeStillCalls(t *testing.T) {
	t.Parallel()

	dir := writeModule(t, shopModule)
	describeShop(t, dir)
	scopes := filepath.Join(dir, "scopes")
	if code, _, errOut := run(t, scopes); code != cli.ExitOK {
		t.Fatalf("kanna-orm = %d\n%s", code, errOut)
	}

	// The directive goes, but Open still calls what kanna-orm wrote for it.
	path := filepath.Join(scopes, "scopes.go")
	src, err := os.ReadFile(path) //nolint:gosec // the file written above
	if err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // the file written above
	if err := os.WriteFile(path, bytes.Replace(src, []byte("//kanna:scope table=orders\n"), nil, 1), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := run(t, scopes)
	if want := "./scopes.go:12:11: error: s.StatusNe undefined (type Order has no field or method StatusNe)\n" +
		"./scopes.go:16:9: error: undefined: Orders\n"; code != cli.ExitFailure || errOut != want {
		t.Errorf("kanna-orm = %d\n%s\nwant %d\n%s", code, errOut, cli.ExitFailure, want)
	}
	if _, err := os.Stat(filepath.Join(scopes, "orm_gen.go")); err != nil {
		t.Errorf("orm_gen.go is gone: %v", err)
	}
}

func TestCommand_ACallChangedAlongWithWhatItCalls(t *testing.T) {
	t.Parallel()

	dir := writeModule(t, shopModule)
	describeShop(t, dir)
	scopes := filepath.Join(dir, "scopes")
	if code, _, errOut := run(t, scopes); code != cli.ExitOK {
		t.Fatalf("kanna-orm = %d\n%s", code, errOut)
	}

	// Status takes a type of its own, and Open passes it, which StatusNe as kanna-orm wrote it does not take yet.
	model := filepath.Join(dir, "model", "model.go")
	const typed = "package model\n\ntype Status string\n\nconst Closed Status = \"closed\"\n\n" +
		"type Order struct {\n\tID     int64\n\tStatus Status\n}\n"
	if err := os.WriteFile(model, []byte(typed), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(scopes, "scopes.go")
	src, err := os.ReadFile(path) //nolint:gosec // the file written above
	if err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // the file written above
	if err := os.WriteFile(path, bytes.Replace(src, []byte(`s.StatusNe("closed")`), []byte(`s.StatusNe(model.Closed)`),
		1), 0o600); err != nil {
		t.Fatal(err)
	}

	if code, _, errOut := run(t, scopes); code != cli.ExitOK {
		t.Fatalf("kanna-orm after the change = %d\n%s", code, errOut)
	}
	set, err := packages.Load([]string{"./..."}, packages.Config{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Diags) > 0 {
		t.Errorf("the module does not compile once generated again: %v", set.Diags)
	}
}

func TestCommand_ChecksEveryPackageItLoads(t *testing.T) {
	t.Parallel()

	const caller = `
-- orders/orders.go --
package orders

import "example.com/shop/scopes"

var Open = scopes.Orders().Open()
`
	tests := []struct {
		name, extra, file, edit, want string
	}{
		// Paid goes from scopes, which itself does not call it.
		{"a package calling what the run removes", caller + "\nvar Paid = scopes.Orders().Paid()\n",
			"order_scopes.sql", "-- scope: Order.Paid()\nstatus = 'paid'\n",
			"orders/orders.go:7:28: error: scopes.Orders().Paid undefined (type scopes.Order has no field or method Paid)\n"},
		{"a package with a mistake of its own", caller + "\nvar _ int = \"one\"\n", "", "",
			"orders/orders.go:7:13: error: cannot use \"one\" (untyped string constant) as int value in variable " +
				"declaration\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := writeModule(t, shopModule+tt.extra)
			describeShop(t, dir)
			if tt.edit != "" {
				if code, _, errOut := run(t, dir, "./..."); code != cli.ExitOK {
					t.Fatalf("kanna-orm ./... = %d\n%s", code, errOut)
				}
				path := filepath.Join(dir, "scopes", tt.file)
				src, err := os.ReadFile(path) //nolint:gosec // the file written above
				if err != nil {
					t.Fatal(err)
				}
				//nolint:gosec // the file written above
				if err := os.WriteFile(path, bytes.Replace(src, []byte(tt.edit), nil, 1), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if code, _, errOut := run(t, dir, "./..."); code != cli.ExitFailure || errOut != tt.want {
				t.Errorf("kanna-orm ./... = %d\n%s\nwant %d\n%s", code, errOut, cli.ExitFailure, tt.want)
			}
		})
	}
}

func TestCommand_StopsSayingWhatTheGeneratedNamesDoNotExplain(t *testing.T) {
	t.Parallel()

	// other.Order is no scope type: its StatusEq is a mistake, though scopes.Order is about to have one.
	dir := writeModule(t, shopModule+`
-- other/other.go --
package other

type Order struct{}

var _ = Order{}.StatusEq("open")
-- scopes/more.go --
package scopes

import (
	"example.com/shop/model"
	"github.com/go-kanna/orm"
)

//kanna:scope table=order
type Archived struct{ q orm.Of[model.Order] }
`)
	describeShop(t, dir)
	code, _, errOut := run(t, dir, "./...")
	want := "other/other.go:5:17: error: Order{}.StatusEq undefined (type Order has no field or method StatusEq)\n" +
		"scopes/more.go:8:1: error: Archived: the snapshot has no table order\n  did you mean orders?\n"
	if code != cli.ExitFailure || errOut != want {
		t.Errorf("kanna-orm ./... = %d\n%s\nwant %d\n%s", code, errOut, cli.ExitFailure, want)
	}
}
