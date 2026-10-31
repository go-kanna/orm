package gen_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"

	"github.com/go-kanna/orm/internal/snapshot"
)

func TestRepositories_Lock(t *testing.T) {
	t.Parallel()

	got, ds := repositories(t, snapshot.PostgreSQL, `
	//kanna:lock update
	GetForUpdate(ctx context.Context, id model.OrderID) (model.Order, error)
	// GetForNoKeyUpdate fails rather than wait for the row.
	//kanna:lock no-key-update nowait
	GetForNoKeyUpdate(ctx context.Context, id model.OrderID, s ...scopes.Order) (model.Order, error)
	//kanna:lock share skip-locked
	FirstClaimable(ctx context.Context, s ...scopes.Order) (model.Order, error)
	//kanna:lock key-share
	ListHeld(ctx context.Context, s ...scopes.Order) ([]model.Order, error)
	List(ctx context.Context, s ...scopes.Order) ([]model.Order, error)
`, "", nil)
	if ds != "" {
		t.Fatalf("Repositories:\n%s", ds)
	}
	want := []string{
		`OrderRepository.GetForUpdate Get orders keys=id=id row lock="FOR UPDATE"`,
		`OrderRepository.GetForNoKeyUpdate Get orders keys=id=id scoped row lock="FOR NO KEY UPDATE NOWAIT"`,
		`OrderRepository.FirstClaimable First orders scoped row lock="FOR SHARE SKIP LOCKED"`,
		`OrderRepository.ListHeld List orders scoped rows lock="FOR KEY SHARE"`,
		`OrderRepository.List List orders scoped rows`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Repositories =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestRepositories_LockOnMySQL(t *testing.T) {
	t.Parallel()

	got, ds := repositories(t, snapshot.MySQL, `
	//kanna:lock update nowait
	GetForUpdate(ctx context.Context, id model.OrderID) (model.Order, error)
	//kanna:lock share
	ListShared(ctx context.Context, s ...scopes.Order) ([]model.Order, error)
`, "", nil)
	if ds != "" {
		t.Fatalf("Repositories:\n%s", ds)
	}
	want := []string{
		`OrderRepository.GetForUpdate Get orders keys=id=id row lock="FOR UPDATE NOWAIT"`,
		`OrderRepository.ListShared List orders scoped rows lock="FOR SHARE"`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Repositories =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestRepositories_LockMistakes(t *testing.T) {
	t.Parallel()

	const (
		get  = "\tGet(ctx context.Context, id model.OrderID) (model.Order, error)\n"
		list = "\tList(ctx context.Context, s ...scopes.Order) ([]model.Order, error)\n"
		at   = "repository.go:24:2: "
	)
	tests := []struct {
		name    string
		dialect snapshot.Dialect
		methods string
		want    string
	}{
		{"no strength", snapshot.PostgreSQL, "\t//kanna:lock\n" + get,
			at + "error: Get: //kanna:lock takes the strength of the lock, then nowait or skip-locked if any\n" +
				"  as in //kanna:lock update skip-locked"},
		{"too many words", snapshot.PostgreSQL, "\t//kanna:lock update nowait now\n" + get,
			at + "error: Get: //kanna:lock takes the strength of the lock, then nowait or skip-locked if any\n" +
				"  as in //kanna:lock update skip-locked"},
		{"a strength misspelled", snapshot.PostgreSQL, "\t//kanna:lock updat\n" + get,
			at + `error: Get: //kanna:lock takes update, no-key-update, share, or key-share, not "updat"` + "\n" +
				"  did you mean update?"},
		{"a wait it does not know", snapshot.PostgreSQL, "\t//kanna:lock update wait=nowait\n" + get,
			at + `error: Get: //kanna:lock takes nowait or skip-locked after the strength, not "wait=nowait"`},
		{"twice", snapshot.PostgreSQL, "\t//kanna:lock update\n\t//kanna:lock share\n" + get,
			"repository.go:25:2: error: duplicate //kanna:lock directive\n  the first is on line 24"},
		{"a verb that reads no whole rows", snapshot.PostgreSQL,
			"\t//kanna:lock update\n\tCount(ctx context.Context, s ...scopes.Order) (int64, error)\n",
			at + "error: Count: //kanna:lock is for a Get, a First, or a List"},
		{"a write", snapshot.PostgreSQL,
			"\t//kanna:lock update\n\tDelete(ctx context.Context, id model.OrderID) error\n",
			at + "error: Delete: //kanna:lock is for a Get, a First, or a List"},
		{"no-key-update on MySQL", snapshot.MySQL, "\t//kanna:lock no-key-update\n" + get,
			at + "error: Get: MySQL has no FOR NO KEY UPDATE\n  lock with update or share"},
		{"key-share on MySQL", snapshot.MySQL, "\t//kanna:lock key-share\n" + get,
			at + "error: Get: MySQL has no FOR KEY SHARE\n  lock with update or share"},
		{"skip-locked on a Get", snapshot.PostgreSQL, "\t//kanna:lock update skip-locked\n" + get,
			at + "error: Get: skip-locked passes a locked row over, which a Get would report as not found\n" +
				"  use nowait, which returns an error for a locked row"},
		{"at the end of the line", snapshot.PostgreSQL,
			strings.TrimSuffix(get, "\n") + " //kanna:lock update\n",
			"repository.go:24:66: error: //kanna:lock marks no method of a //kanna:repository\n" +
				"  write it right above the method"},
		{"parted from the method by a blank line", snapshot.PostgreSQL, "\t//kanna:lock update\n\n" + list,
			at + "error: //kanna:lock marks no method of a //kanna:repository\n  write it right above the method"},
		{"after the last method", snapshot.PostgreSQL, list + "\t//kanna:lock update\n",
			"repository.go:25:2: error: //kanna:lock marks no method of a //kanna:repository\n" +
				"  write it right above the method"},
		{"a space after the comment marker", snapshot.PostgreSQL, "\t// kanna:lock update\n" + list,
			at + `warning: "// kanna:lock update" is not recognized as a directive; write it as ` +
				`"//kanna:lock update" with no space`},
		{"misspelled at the end of the line", snapshot.PostgreSQL,
			strings.TrimSuffix(list, "\n") + " //kanna:lok update\n",
			`repository.go:24:70: warning: "//kanna:lok update" is not recognized as a directive` + "\n" +
				`  did you mean "//kanna:lock update"?`},
		{"a name telling of a lock it has not", snapshot.PostgreSQL,
			"\tGetForUpdate(ctx context.Context, id model.OrderID) (model.Order, error)\n",
			at + "warning: GetForUpdate: its name tells of a lock, but it has no //kanna:lock\n" +
				"  declare the lock, or name it otherwise"},
		{"a name telling of a lock, a word follows", snapshot.PostgreSQL,
			"\tListForShareByStatus(ctx context.Context, status model.OrderStatus) ([]model.Order, error)\n",
			at + "warning: ListForShareByStatus: its name tells of a lock, but it has no //kanna:lock\n" +
				"  declare the lock, or name it otherwise"},
		{"a name that only starts like one", snapshot.PostgreSQL,
			"\tListForSharedFolders(ctx context.Context, s ...scopes.Order) ([]model.Order, error)\n", ""},
		{"a name telling of a lock on a verb that takes none", snapshot.PostgreSQL,
			"\tCountForUpdate(ctx context.Context, s ...scopes.Order) (int64, error)\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, ds := repositories(t, tt.dialect, tt.methods, "", nil); ds != tt.want {
				t.Errorf("Repositories reports\n%s\nwant\n%s", ds, tt.want)
			}
		})
	}
}

func TestRepositories_LockOnTheInterface(t *testing.T) {
	t.Parallel()

	const want = "repository.go:22:1: error: //kanna:lock marks no method of a //kanna:repository\n" +
		"  write it right above the method"
	_, ds := repositoriesMarked(t, snapshot.PostgreSQL, "//kanna:lock update\n//kanna:repository",
		"\tList(ctx context.Context, s ...scopes.Order) ([]model.Order, error)\n", "", nil)
	if ds != want {
		t.Errorf("Repositories reports\n%s\nwant\n%s", ds, want)
	}
}

func TestRepositories_LockOnAnEmbeddedInterface(t *testing.T) {
	t.Parallel()

	const want = "repository.go:26:2: error: //kanna:lock marks no method of a //kanna:repository\n" +
		"  write it right above the method"
	_, ds := repositoriesMarked(t, snapshot.PostgreSQL, "type Base interface{}\n\n//kanna:repository",
		"\t//kanna:lock update\n\tBase\n", "", nil)
	if ds != want {
		t.Errorf("Repositories reports\n%s\nwant\n%s", ds, want)
	}
}

func TestRepositories_LockOnAQueryWrittenByHand(t *testing.T) {
	t.Parallel()

	const want = "repository.go:24:2: error: MonthCounts: //kanna:lock is for a method kanna-orm writes the SQL of\n" +
		"  lock in -- query: MonthCounts"
	_, ds := repositories(t, snapshot.PostgreSQL,
		"\t//kanna:lock update\n\tMonthCounts(ctx context.Context, since time.Time) ([]MonthCount, error)\n",
		monthCounts, map[string]snapshot.Statement{"MonthCounts": monthCountsDescribed})
	if ds != want {
		t.Errorf("Repositories reports\n%s\nwant\n%s", ds, want)
	}
}

func TestCommand_LockOnAMethodWrittenByHand(t *testing.T) {
	t.Parallel()

	dir := writeModule(t, strings.Replace(repositoryShop, "\tArchive(ctx", "\t//kanna:lock update\n\tArchive(ctx", 1))
	describeRepositoryShop(t, dir, snapshot.PostgreSQL)
	code, _, errOut := run(t, filepath.Join(dir, "repository"), "-destination", "../postgres", "-driver", "database/sql")
	if code == cli.ExitOK {
		t.Fatalf("kanna-orm = %d, want it to stop", code)
	}
	for _, want := range []string{
		"Archive: //kanna:lock is for a method kanna-orm writes the SQL of",
		"lock in the method the destination declares",
	} {
		if !strings.Contains(errOut, want) {
			t.Errorf("kanna-orm reports\n%s\nwant %q", errOut, want)
		}
	}
}
