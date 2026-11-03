package gen_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"

	"github.com/go-kanna/orm/internal/snapshot"
)

func TestCommand_WriteOptionsMistakes(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, before, after, want string
		dialect                   snapshot.Dialect
	}{
		{name: "unknown column", before: "coalesce=owner_id,note", after: "coalesce=missing", want: "has no column missing"},
		{name: "kept column", before: "coalesce=owner_id,note", after: "coalesce=stamp", want: "does not update"},
		{name: "conflict key", before: "coalesce=owner_id,note", after: "coalesce=record_key", want: "does not update"},
		{name: "assigned key", before: "coalesce=owner_id,note", after: "coalesce=id", want: "does not update"},
		{name: "duplicate option", before: "coalesce=owner_id,note", after: "coalesce=owner_id coalesce=note",
			want: "gives coalesce= twice"},
		{name: "duplicate column", before: "coalesce=owner_id,note", after: "coalesce=note,note", want: "each column once"},
		{name: "empty option", before: "coalesce=owner_id,note", after: "coalesce=", want: "takes keep= or coalesce="},
		{name: "no database default", before: `orm:"record_key"`, after: `orm:"record_key,default"`,
			want: "to have a database default"},
		{name: "omitted upsert key", before: "//kanna:conflict record_key coalesce=owner_id,note",
			after: "//kanna:conflict id", want: "an insert never meets a row by it"},
		{name: "MySQL cannot read default key", dialect: snapshot.MySQL,
			before: "Insert(ctx context.Context, record model.Record) error",
			after:  "Insert(ctx context.Context, record model.Record) (model.Record,error)",
			want:   "MySQL cannot read the row back"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dialect := tt.dialect
			if dialect == "" {
				dialect = snapshot.PostgreSQL
			}
			dir := writeModule(t, strings.Replace(writeOptionsShop, tt.before, tt.after, 1))
			writeOptionsSnapshot(t, dir, dialect)
			code, _, errOut := run(t, filepath.Join(dir, "scopes"))
			if code == cli.ExitOK {
				code, _, errOut = run(t, filepath.Join(dir, "repository"), "-destination", "../impl", "-driver", "database/sql")
			}
			if code == cli.ExitOK || !strings.Contains(errOut, tt.want) {
				t.Fatalf("generate = %d, want %q\n%s", code, tt.want, errOut)
			}
		})
	}
}

func TestCommand_WriteOptionsKeepDefault(t *testing.T) {
	t.Parallel()
	for _, dialect := range []snapshot.Dialect{snapshot.PostgreSQL, snapshot.MySQL} {
		t.Run(string(dialect), func(t *testing.T) {
			t.Parallel()
			dir := writeModule(t, strings.Replace(writeOptionsShop, "//kanna:conflict record_key coalesce=owner_id,note\n",
				"//kanna:conflict record_key keep=stamp coalesce=owner_id,note\n", 1))
			writeOptionsSnapshot(t, dir, dialect)
			if code, _, errOut := run(t, filepath.Join(dir, "scopes")); code != cli.ExitOK {
				t.Fatalf("generate scopes = %d\n%s", code, errOut)
			}
			code, _, errOut := run(t, filepath.Join(dir, "repository"), "-destination", "../impl", "-driver", "database/sql")
			want := "warning: Upsert: keep=stamp changes nothing: an upsert keeps a column default leaves out of INSERT"
			if code != cli.ExitOK || !strings.Contains(errOut, want) {
				t.Fatalf("generate = %d, want it to succeed with %q\n%s", code, want, errOut)
			}
		})
	}
}
