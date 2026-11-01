package gen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"

	"github.com/go-kanna/orm/internal/snapshot"
)

func TestCommand_ScalarRelations(t *testing.T) {
	t.Parallel()
	for _, driver := range []string{"pgx", "database/sql"} {
		t.Run(driver, func(t *testing.T) {
			t.Parallel()
			archive := strings.Replace(typedQueryShop, "type Record struct {",
				"type Record struct {\nTopics []State `orm:\"relation=record_states,column=state\"`", 1)
			if driver == "database/sql" {
				runtime := strings.Replace(typedQueryRuntime, "lastArgs = args\n\tif strings.HasPrefix", `lastArgs = args
    if strings.Contains(query, "FROM record_states") {
        if relationValue == "" { return &fakeRows{[]string{"record_id", "state"}, nil}, nil }
        return &fakeRows{[]string{"record_id", "state"}, [][]driver.Value{{int64(7), relationValue}}}, nil
    }
    if strings.HasPrefix`, 1)
				runtime = strings.Replace(runtime, "invalidState = true", `
    loaded, err := records.List(ctx, scopes.Records().WithTopics())
    if err != nil || len(loaded) != 1 || len(loaded[0].Topics) != 1 || loaded[0].Topics[0] != "active" {
        t.Fatalf("WithTopics = %+v, %v", loaded, err)
    }
    plain, err := records.List(ctx)
    if err != nil || plain[0].Topics != nil { t.Fatalf("unrequested relation = %+v, %v", plain, err) }
    relationValue = ""
    empty, err := records.List(ctx, scopes.Records().WithTopics())
    if err != nil || empty[0].Topics == nil || len(empty[0].Topics) != 0 {
        t.Fatalf("empty relation = %+v, %v", empty, err)
    }
    relationValue = "invalid"
    if _, err := records.List(ctx, scopes.Records().WithTopics()); err == nil {
        t.Fatal("invalid related enum was accepted")
    }
    invalidState = true`, 1)
				runtime = strings.Replace(runtime, "var invalidState bool",
					"var invalidState bool\nvar relationValue = \"active\"", 1)
				runtime = strings.Replace(runtime, `"example.com/shop/model"`,
					"\"example.com/shop/model\"\n\"example.com/shop/scopes\"", 1)
				archive += "\n-- impl/runtime_test.go --\n" + runtime
			}
			dir := writeModule(t, archive)
			writeTypedQuerySnapshot(t, dir)
			path := filepath.Join(dir, snapshot.Name)
			data, err := os.ReadFile(path) //nolint:gosec // snapshot written in this test's module
			if err != nil {
				t.Fatal(err)
			}
			s, err := snapshot.Unmarshal(data)
			if err != nil {
				t.Fatal(err)
			}
			s.Tables["record_states"] = snapshot.Table{Columns: []snapshot.Column{
				{Name: "record_id", Type: "int8", NotNull: true}, {Name: "state", Type: "text", NotNull: true},
			}, ForeignKeys: []snapshot.ForeignKey{{Columns: []string{"record_id"}, References: "records",
				ReferencedColumns: []string{"id"}}}}
			if err := os.WriteFile(path, snapshot.Marshal(s), 0o600); err != nil { //nolint:gosec // test module snapshot
				t.Fatal(err)
			}
			for _, step := range []struct {
				pkg  string
				args []string
			}{
				{"scopes", nil}, {"repository", []string{"-destination", "../impl", "-driver", driver}},
			} {
				if code, _, errOut := run(t, filepath.Join(dir, step.pkg), step.args...); code != cli.ExitOK {
					t.Fatalf("generate %s = %d\n%s", step.pkg, code, errOut)
				}
			}
			moduleCommand(t, dir, "test", "-mod=mod", "./...")
		})
	}
}
