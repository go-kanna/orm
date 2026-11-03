package gen_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"

	"github.com/go-kanna/orm/internal/snapshot"
)

func TestRepositories_StrayDirectives(t *testing.T) {
	t.Parallel()

	const (
		list  = "\tList(ctx context.Context, s ...scopes.Order) ([]model.Order, error)\n"
		at    = "repository.go:24:2: "
		stray = " marks no method of a //kanna:repository\n  write it right above the method"
	)
	for _, tt := range []struct{ name, methods, want string }{
		{"a conflict parted from the method by a blank line", "\t//kanna:conflict id\n\n" + list,
			at + "error: //kanna:conflict" + stray},
		{"a pluck at the end of the line", strings.TrimSuffix(list, "\n") + " //kanna:pluck id\n",
			"repository.go:24:70: error: //kanna:pluck" + stray},
		{"an insert after the last method", list + "\t//kanna:insert\n",
			"repository.go:25:2: error: //kanna:insert" + stray},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, ds := repositories(t, snapshot.PostgreSQL, tt.methods, "", nil); ds != tt.want {
				t.Errorf("Repositories reports\n%s\nwant\n%s", ds, tt.want)
			}
		})
	}
}

func TestRepositories_ConflictOnAQueryWrittenByHand(t *testing.T) {
	t.Parallel()

	const want = "repository.go:24:2: error: MonthCounts: //kanna:conflict is for a method kanna-orm writes the SQL of\n" +
		"  handle the conflict in -- query: MonthCounts"
	_, ds := repositories(t, snapshot.PostgreSQL,
		"\t//kanna:conflict id\n\tMonthCounts(ctx context.Context, since time.Time) ([]MonthCount, error)\n",
		monthCounts, map[string]snapshot.Statement{"MonthCounts": monthCountsDescribed})
	if ds != want {
		t.Errorf("Repositories reports\n%s\nwant\n%s", ds, want)
	}
}

func TestCommand_DirectivesOnAMethodWrittenByHand(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct{ directive, instead string }{
		{"//kanna:conflict id", "handle the conflict"},
		{"//kanna:pluck id", "read the column"},
		{"//kanna:insert", "write the row"},
	} {
		t.Run(tt.directive, func(t *testing.T) {
			t.Parallel()

			dir := writeModule(t, strings.Replace(repositoryShop, "\tArchive(ctx", "\t"+tt.directive+"\n\tArchive(ctx", 1))
			describeRepositoryShop(t, dir, snapshot.PostgreSQL)
			code, _, errOut := run(t, filepath.Join(dir, "repository"), "-destination", "../postgres", "-driver",
				"database/sql")
			if code == cli.ExitOK {
				t.Fatalf("kanna-orm = %d, want it to stop", code)
			}
			key, _, _ := strings.Cut(strings.TrimPrefix(tt.directive, "//kanna:"), " ")
			for _, want := range []string{
				"Archive: //kanna:" + key + " is for a method kanna-orm writes the SQL of",
				tt.instead + " in the method the destination declares",
			} {
				if !strings.Contains(errOut, want) {
					t.Errorf("kanna-orm reports\n%s\nwant %q", errOut, want)
				}
			}
		})
	}
}
