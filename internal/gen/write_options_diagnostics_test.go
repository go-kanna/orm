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
		{name: "unknown column", before: "//kanna:conflict record_key\n", after: "//kanna:conflict missing\n",
			want: "the snapshot has no column records.missing"},
		{name: "conflict option", before: "//kanna:conflict record_key\n", after: "//kanna:conflict record_key keep=stamp\n",
			want: `//kanna:conflict takes <columns>, not "keep=stamp"`},
		{name: "conflict of two words", before: "//kanna:conflict record_key\n",
			after: "//kanna:conflict record_key owner_id\n", want: `names the columns of a key alone, not "owner_id"`},
		{name: "insertonly default", before: `orm:",default"`, after: `orm:",default,insertonly"`,
			want: "so kanna-orm would never write it\n  if a trigger writes it on every write, tag it readonly alone"},
		{name: "change set of many rows", before: "UpsertAll(ctx context.Context, records []model.Record) error",
			after: "UpsertAll(ctx context.Context, records []model.Record, changes scopes.RecordChangeSet) error",
			want:  "an Upsert of many rows takes no change set"},
		{name: "change set and row of an update", before: "Update(ctx context.Context, record model.Record) error",
			after: "Update(ctx context.Context, record model.Record, changes scopes.RecordChangeSet) error",
			want:  "a change set is taken once"},
		{name: "no database default", before: `orm:"record_key"`, after: `orm:"record_key,default"`,
			want: "to have a database default"},
		{name: "omitted upsert key", before: "//kanna:conflict record_key\n", after: "//kanna:conflict id\n",
			want: "an insert never meets a row by it"},
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
