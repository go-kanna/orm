package snapshot_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-kanna/orm/internal/snapshot"
)

func shop() snapshot.Snapshot {
	return snapshot.Snapshot{
		Dialect: snapshot.PostgreSQL,
		Tables: map[string]snapshot.Table{
			"orders": {
				Columns: []snapshot.Column{
					{Name: "id", Type: "int8", NotNull: true, Assigned: true},
					{Name: "customer_id", Type: "int8", NotNull: true},
					{Name: "number", Type: "text", NotNull: true},
					{Name: "status", Type: "text", NotNull: true, Default: true},
					{Name: "note", Type: "text"},
				},
				PrimaryKey: []string{"id"},
				UniqueKeys: [][]string{{"number"}, {"customer_id", "number"}},
				ForeignKeys: []snapshot.ForeignKey{
					{Columns: []string{"customer_id"}, References: "customers", ReferencedColumns: []string{"id"}},
				},
			},
			"customers": {
				Columns:    []snapshot.Column{{Name: "id", Type: "int8", NotNull: true, Assigned: true}},
				PrimaryKey: []string{"id"},
			},
		},
		Statements: map[string]snapshot.Statement{
			"example.com/shop/repository.MonthCounts": {
				Digest: "0123456789abcdef",
				Params: []snapshot.Param{{Name: "since", Type: "timestamptz"}},
				Columns: []snapshot.Result{
					{Name: "month", Type: "timestamptz"},
					{Name: "count", Type: "int8", NotNull: true},
					{Name: "note", Type: "text", Nullable: true},
				},
			},
			"example.com/shop/repository.Close": {
				Digest: "fedcba9876543210",
				Params: []snapshot.Param{{Name: "id"}},
			},
		},
	}
}

func TestMarshal(t *testing.T) {
	t.Parallel()

	const want = `{
  "dialect": "postgresql",
  "tables": {
    "customers": {
      "columns": [
        {"name": "id", "type": "int8", "not_null": true, "assigned": true}
      ],
      "primary_key": ["id"],
      "unique_keys": [],
      "foreign_keys": []
    },
    "orders": {
      "columns": [
        {"name": "id", "type": "int8", "not_null": true, "assigned": true},
        {"name": "customer_id", "type": "int8", "not_null": true},
        {"name": "number", "type": "text", "not_null": true},
        {"name": "status", "type": "text", "not_null": true, "default": true},
        {"name": "note", "type": "text"}
      ],
      "primary_key": ["id"],
      "unique_keys": [
        ["customer_id", "number"],
        ["number"]
      ],
      "foreign_keys": [
        {"columns": ["customer_id"], "references": "customers", "referenced_columns": ["id"]}
      ]
    }
  },
  "statements": {
    "example.com/shop/repository.Close": {
      "digest": "fedcba9876543210",
      "params": [
        {"name": "id"}
      ],
      "columns": []
    },
    "example.com/shop/repository.MonthCounts": {
      "digest": "0123456789abcdef",
      "params": [
        {"name": "since", "type": "timestamptz"}
      ],
      "columns": [
        {"name": "month", "type": "timestamptz"},
        {"name": "count", "type": "int8", "not_null": true},
        {"name": "note", "type": "text", "nullable": true}
      ]
    }
  }
}
`
	if got := string(snapshot.Marshal(shop())); got != want {
		t.Errorf("Marshal:\n%s\nwant:\n%s", got, want)
	}
}

func TestMarshal_Empty(t *testing.T) {
	t.Parallel()

	const want = "{\n  \"dialect\": \"mysql\",\n  \"tables\": {},\n  \"statements\": {}\n}\n"
	if got := string(snapshot.Marshal(snapshot.Snapshot{Dialect: snapshot.MySQL})); got != want {
		t.Errorf("Marshal = %q, want %q", got, want)
	}
}

func TestMarshal_LeavesItsArgumentAlone(t *testing.T) {
	t.Parallel()

	s := shop()
	snapshot.Marshal(s)
	want := [][]string{{"number"}, {"customer_id", "number"}}
	if got := s.Tables["orders"].UniqueKeys; !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("unique keys after Marshal = %v, want %v, the order they were given in", got, want)
	}
}

func TestUnmarshal_ReadsWhatMarshalWrites(t *testing.T) {
	t.Parallel()

	data := snapshot.Marshal(shop())
	s, err := snapshot.Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if again := snapshot.Marshal(s); string(again) != string(data) {
		t.Errorf("Marshal after Unmarshal:\n%s\nwant:\n%s", again, data)
	}
}

func TestUnmarshal_Rejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, data, want string
	}{
		{"not JSON", `{"dialect": `, "parse the snapshot"},
		{"unknown field", `{"dialect": "mysql", "tables": {}, "views": {}}`, `unknown field "views"`},
		{"unknown column field", `{"dialect": "mysql", "tables": {"t": {"columns": [{"name": "a", "collation": "x"}]}}}`,
			`unknown field "collation"`},
		{"unknown dialect", `{"dialect": "sqlite", "tables": {}}`, `"sqlite", which is neither postgresql nor mysql`},
		{"no dialect", `{"tables": {}}`, `"", which is neither`},
		{"trailing data", `{"dialect": "mysql", "tables": {}} {}`, "something follows it"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := snapshot.Unmarshal([]byte(tt.data))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Unmarshal error = %v, want one containing %q", err, tt.want)
			}
		})
	}
}

func TestDigest(t *testing.T) {
	t.Parallel()

	a, b := snapshot.Digest("SELECT 1"), snapshot.Digest("SELECT 2")
	if len(a) != 16 || a == b || a != snapshot.Digest("SELECT 1") {
		t.Errorf("Digest gives %q and %q, want 16 hexadecimal digits that differ by the text alone", a, b)
	}
}
