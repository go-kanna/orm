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
		loose = " is not attached to a declaration, so nothing reads it\n" +
			"  write it right above what it marks, with no blank line between"
	)
	for _, tt := range []struct{ name, methods, want string }{
		{"a conflict parted from the method by a blank line", "\t//kanna:conflict id\n\n" + list,
			at + "warning: //kanna:conflict" + loose},
		{"a conflict at the end of the line", strings.TrimSuffix(list, "\n") + " //kanna:conflict id\n",
			"repository.go:24:70: warning: //kanna:conflict" + loose},
		{"a lock after the last method", list + "\t//kanna:lock update\n",
			"repository.go:25:2: warning: //kanna:lock" + loose},
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
		{"//kanna:lock update", "lock"},
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
