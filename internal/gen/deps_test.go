package gen_test

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-kanna/internal/deps"

	"github.com/go-kanna/orm/internal/snapshot"
)

// Code calling what the run writes does not compile before it, which is when
// the kanna command asks.
func TestDeps(t *testing.T) {
	t.Parallel()

	dir := writeModule(t, shopModule)
	describeShop(t, dir)

	code, stdout, stderr := run(t, filepath.Join(dir, "scopes"), "-deps")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var r deps.Report
	if err := json.Unmarshal([]byte(stdout), &r); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(r.Packages, []string{"example.com/shop/scopes"}) {
		t.Errorf("Packages = %q", r.Packages)
	}
	if len(r.Inputs) != 1 || filepath.Base(r.Inputs[0]) != snapshot.Name {
		t.Errorf("Inputs = %q", r.Inputs)
	}
	if len(r.Writes) != 1 || !strings.HasSuffix(r.Writes[0], filepath.Join("scopes", "scopes_gen.go")) {
		t.Errorf("Writes = %q", r.Writes)
	}
	want := []deps.Ref{{Package: "example.com/shop/model", Via: "the scope type Order"}}
	if !slices.Equal(r.Refs, want) {
		t.Errorf("Refs = %+v, want %+v", r.Refs, want)
	}
	for _, name := range []string{"example.com/shop/scopes.OrderChanges", "example.com/shop/scopes.Order.StatusNe"} {
		if !slices.Contains(r.Declares, name) {
			t.Errorf("Declares = %q, want %s among them", r.Declares, name)
		}
	}
}

// A run needs the snapshot, whose absence is for the kanna command to report
// before anything runs. Where the run writes is told all the same.
func TestDeps_NoSnapshot(t *testing.T) {
	t.Parallel()

	dir := writeModule(t, shopModule)

	code, stdout, stderr := run(t, filepath.Join(dir, "scopes"), "-deps")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var r deps.Report
	if err := json.Unmarshal([]byte(stdout), &r); err != nil {
		t.Fatal(err)
	}

	if len(r.Inputs) != 1 || filepath.Base(r.Inputs[0]) != snapshot.Name {
		t.Errorf("Inputs = %q", r.Inputs)
	}
	if len(r.Writes) != 1 || !strings.HasSuffix(r.Writes[0], filepath.Join("scopes", "scopes_gen.go")) {
		t.Errorf("Writes = %q", r.Writes)
	}
	if len(r.Declares) != 0 {
		t.Errorf("Declares = %q, want none", r.Declares)
	}
}

func TestDeps_Repositories(t *testing.T) {
	t.Parallel()

	dir := writeModule(t, repositoryShop)
	describeRepositoryShop(t, dir, snapshot.PostgreSQL)

	code, stdout, stderr := run(t, filepath.Join(dir, "repository"), "-deps", "-destination", "../postgres")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var r deps.Report
	if err := json.Unmarshal([]byte(stdout), &r); err != nil {
		t.Fatal(err)
	}

	if len(r.Writes) != 1 || !strings.HasSuffix(r.Writes[0], filepath.Join("postgres", "repository_gen.go")) {
		t.Errorf("Writes = %q", r.Writes)
	}
	if !slices.Contains(r.Declares, "example.com/shop/postgres.DBTX") {
		t.Errorf("Declares = %q", r.Declares)
	}
}

// Before the first run of kanna-value, a model whose column types it gives
// methods does not resolve. Where the implementations go, and what they
// declare, follows from -destination and the interfaces all the same, for
// what selects their constructors to run after.
func TestDeps_RepositoriesUnresolved(t *testing.T) {
	t.Parallel()

	dir := writeModule(t, strings.Replace(repositoryShop, "\tStatus string\n", "\tStatus Status\n", 1))
	describeRepositoryShop(t, dir, snapshot.PostgreSQL)

	code, stdout, stderr := run(t, filepath.Join(dir, "repository"), "-deps", "-destination", "../postgres")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var r deps.Report
	if err := json.Unmarshal([]byte(stdout), &r); err != nil {
		t.Fatal(err)
	}

	if len(r.Writes) != 1 || !strings.HasSuffix(r.Writes[0], filepath.Join("postgres", "repository_gen.go")) {
		t.Errorf("Writes = %q", r.Writes)
	}
	if !slices.Contains(r.Declares, "example.com/shop/postgres.NewOrderRepository") {
		t.Errorf("Declares = %q", r.Declares)
	}
}
