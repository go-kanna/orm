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
	if len(r.Writes) != 1 || !strings.HasSuffix(r.Writes[0], filepath.Join("scopes", "orm_gen.go")) {
		t.Errorf("Writes = %q", r.Writes)
	}
	want := []deps.Ref{{Package: "example.com/shop/model", Via: "the scope type Order"}}
	if !slices.Equal(r.Refs, want) {
		t.Errorf("Refs = %+v, want %+v", r.Refs, want)
	}
	for _, name := range []string{"example.com/shop/scopes.Orders", "example.com/shop/scopes.Order.StatusNe"} {
		if !slices.Contains(r.Declares, name) {
			t.Errorf("Declares = %q, want %s among them", r.Declares, name)
		}
	}
}

// A run needs the snapshot, whose absence is for the kanna command to report
// before anything runs.
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
	if len(r.Writes) != 0 {
		t.Errorf("Writes = %q, want none", r.Writes)
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

	if len(r.Writes) != 1 || !strings.HasSuffix(r.Writes[0], filepath.Join("postgres", "orm_gen.go")) {
		t.Errorf("Writes = %q", r.Writes)
	}
	if !slices.Contains(r.Declares, "example.com/shop/postgres.DBTX") {
		t.Errorf("Declares = %q", r.Declares)
	}
}
