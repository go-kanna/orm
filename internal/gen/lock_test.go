package gen_test

import (
	"os"
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
	//kanna:lock key-share
	ListIDsForKeyShare(ctx context.Context, id model.OrderID) ([]model.OrderID, error)
	//kanna:lock update nowait
	GetNumberForUpdate(ctx context.Context, id model.OrderID) (string, error)
	//kanna:lock share
	FindNumberForShare(ctx context.Context, id model.OrderID) (*string, error)
	//kanna:lock no-key-update skip-locked
	FirstStatusForNoKeyUpdate(ctx context.Context, s ...scopes.Order) (model.OrderStatus, error)
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
		`OrderRepository.ListIDsForKeyShare List orders keys=id=id rows pluck=id lock="FOR KEY SHARE"`,
		`OrderRepository.GetNumberForUpdate Get orders keys=id=id row pluck=number lock="FOR UPDATE NOWAIT"`,
		`OrderRepository.FindNumberForShare Find orders keys=id=id row pluck=number lock="FOR SHARE"`,
		`OrderRepository.FirstStatusForNoKeyUpdate First orders scoped row pluck=status ` +
			`lock="FOR NO KEY UPDATE SKIP LOCKED"`,
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
	//kanna:lock share
	ListIDsForShare(ctx context.Context, s ...scopes.Order) ([]model.OrderID, error)
	//kanna:lock update
	GetNumberForUpdate(ctx context.Context, id model.OrderID) (string, error)
`, "", nil)
	if ds != "" {
		t.Fatalf("Repositories:\n%s", ds)
	}
	want := []string{
		`OrderRepository.GetForUpdate Get orders keys=id=id row lock="FOR UPDATE NOWAIT"`,
		`OrderRepository.ListShared List orders scoped rows lock="FOR SHARE"`,
		`OrderRepository.ListIDsForShare List orders scoped rows pluck=id lock="FOR SHARE"`,
		`OrderRepository.GetNumberForUpdate Get orders keys=id=id row pluck=number lock="FOR UPDATE"`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Repositories =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// lockWords are what //kanna:lock takes, as its diagnostics name them.
const lockWords = "update, no-key-update, share, key-share, nowait, or skip-locked"

// lockedVerbs is what is said of //kanna:lock on a method that cannot take one.
const lockedVerbs = "//kanna:lock is for a Get, a Find, a First, a List, or an Update of a change set taking scopes " +
	"and no unique key"

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
			at + "error: Get: //kanna:lock needs " + lockWords + "\n  as in //kanna:lock update skip-locked"},
		{"a word it does not know", snapshot.PostgreSQL, "\t//kanna:lock update nowait now\n" + get,
			at + `error: Get: //kanna:lock takes ` + lockWords + `, not "now"` + "\n" +
				"  as in //kanna:lock update skip-locked"},
		{"a strength misspelled", snapshot.PostgreSQL, "\t//kanna:lock updat\n" + get,
			at + `error: Get: //kanna:lock takes ` + lockWords + `, not "updat"` + "\n" +
				`  did you mean "update"?` + "\n  as in //kanna:lock update skip-locked"},
		{"a wait it does not know", snapshot.PostgreSQL, "\t//kanna:lock update wait=nowait\n" + get,
			at + `error: Get: //kanna:lock takes ` + lockWords + `, not "wait=nowait"` + "\n" +
				"  as in //kanna:lock update skip-locked"},
		{"the strength after the wait", snapshot.PostgreSQL, "\t//kanna:lock nowait update\n" + get,
			at + "error: Get: //kanna:lock takes the strength of the lock, then nowait or skip-locked if any\n" +
				"  as in //kanna:lock update skip-locked"},
		{"two strengths", snapshot.PostgreSQL, "\t//kanna:lock update share\n" + get,
			at + "error: Get: //kanna:lock takes the strength of the lock, then nowait or skip-locked if any\n" +
				"  as in //kanna:lock update skip-locked"},
		{"twice", snapshot.PostgreSQL, "\t//kanna:lock update\n\t//kanna:lock share\n" + get,
			"repository.go:25:2: error: duplicate //kanna:lock directive\n  the first is on line 24"},
		{"a verb that reads no whole rows", snapshot.PostgreSQL,
			"\t//kanna:lock update\n\tCount(ctx context.Context, s ...scopes.Order) (int64, error)\n",
			at + "error: Count: " + lockedVerbs},
		{"a write", snapshot.PostgreSQL,
			"\t//kanna:lock update\n\tDelete(ctx context.Context, id model.OrderID) error\n",
			at + "error: Delete: " + lockedVerbs},
		{"no-key-update on MySQL", snapshot.MySQL, "\t//kanna:lock no-key-update\n" + get,
			at + "error: Get: MySQL has no FOR NO KEY UPDATE\n  lock with update or share"},
		{"key-share on MySQL", snapshot.MySQL, "\t//kanna:lock key-share\n" + get,
			at + "error: Get: MySQL has no FOR KEY SHARE\n  lock with update or share"},
		{"skip-locked on a Get", snapshot.PostgreSQL, "\t//kanna:lock update skip-locked\n" + get,
			at + "error: Get: skip-locked passes a locked row over, which a Get would report as not found\n" +
				"  use nowait, which returns an error for a locked row"},
		{"at the end of the line", snapshot.PostgreSQL,
			strings.TrimSuffix(get, "\n") + " //kanna:lock update\n",
			"repository.go:24:66: warning: //kanna:lock is not attached to a declaration, so nothing reads it\n" +
				"  write it right above what it marks, with no blank line between"},
		{"parted from the method by a blank line", snapshot.PostgreSQL, "\t//kanna:lock update\n\n" + list,
			at + "warning: //kanna:lock is not attached to a declaration, so nothing reads it\n" +
				"  write it right above what it marks, with no blank line between"},
		{"after the last method", snapshot.PostgreSQL, list + "\t//kanna:lock update\n",
			"repository.go:25:2: warning: //kanna:lock is not attached to a declaration, so nothing reads it\n" +
				"  write it right above what it marks, with no blank line between"},
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
		{"a name in which a word follows a lock", snapshot.PostgreSQL,
			"\tGetForUpdateForm(ctx context.Context, id model.OrderID) (model.Order, error)\n", ""},
		{"a name ending in another lock than its directive", snapshot.PostgreSQL,
			"\t//kanna:lock update\n\tListForShare(ctx context.Context, s ...scopes.Order) ([]model.Order, error)\n",
			at + "error: ListForShare: its name ends in a lock of share, but //kanna:lock is of update\n" +
				"  make the two one"},
		{"a column read telling of a lock it has not", snapshot.PostgreSQL,
			"\tListIDsForUpdate(ctx context.Context, s ...scopes.Order) ([]model.OrderID, error)\n",
			at + "warning: ListIDsForUpdate: its name tells of a lock, but it has no //kanna:lock\n" +
				"  declare the lock, or name it otherwise"},
		{"a column read ending in another lock than its directive", snapshot.PostgreSQL,
			"\t//kanna:lock update\n\tListIDsForKeyShare(ctx context.Context, s ...scopes.Order) ([]model.OrderID, error)\n",
			at + "error: ListIDsForKeyShare: its name ends in a lock of key-share, but //kanna:lock is of update\n" +
				"  make the two one"},
		{"a column read of key-share on MySQL", snapshot.MySQL,
			"\t//kanna:lock key-share\n\tListIDsForKeyShare(ctx context.Context, s ...scopes.Order) ([]model.OrderID, error)\n",
			at + "error: ListIDsForKeyShare: MySQL has no FOR KEY SHARE\n  lock with update or share"},
		{"a name ending in the lock of its directive", snapshot.PostgreSQL,
			"\t//kanna:lock key-share\n\tListForKeyShare(ctx context.Context, s ...scopes.Order) ([]model.Order, error)\n",
			""},
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

// lockedUpdateShop declares jobs, a table of a primary key, and logs, one of no key, with a repository of the methods
// a test puts in place of METHODS.
const lockedUpdateShop = `-- model/model.go --
package model
type Job struct {
	ID    int64
	State string
}
type Log struct{ Message string }
-- scopes/scopes.go --
package scopes
import (
	"example.com/shop/model"
	"github.com/go-kanna/orm/scope"
)
//kanna:scope table=jobs
type Job struct{ q scope.Of[model.Job] }
//kanna:scope table=logs
type Log struct{ q scope.Of[model.Log] }
-- repository/repository.go --
package repository
import (
	"context"
	"example.com/shop/model"
	"example.com/shop/scopes"
)
var (
	_ context.Context
	_ model.Job
	_ scopes.Job
)
//kanna:repository
type Jobs interface {
METHODS}
`

// generateLockedUpdate generates the scopes and the repository of lockedUpdateShop with methods, on dialect, and
// returns what kanna-orm returns and reports of the repository, and the code it generates for it if it does.
func generateLockedUpdate(t *testing.T, dialect snapshot.Dialect, methods string) (int, string, string) {
	t.Helper()

	dir := writeModule(t, strings.Replace(lockedUpdateShop, "METHODS", methods, 1))
	s := snapshot.Snapshot{Dialect: dialect, Tables: map[string]snapshot.Table{
		"jobs": {Columns: []snapshot.Column{
			{Name: "id", Type: "bigint", NotNull: true}, {Name: "state", Type: "text", NotNull: true},
		}, PrimaryKey: []string{"id"}},
		"logs": {Columns: []snapshot.Column{{Name: "message", Type: "text", NotNull: true}}},
	}}
	if err := os.WriteFile(filepath.Join(dir, snapshot.Name), snapshot.Marshal(s), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := run(t, filepath.Join(dir, "scopes")); code != cli.ExitOK {
		t.Fatalf("generate scopes = %d\n%s", code, errOut)
	}
	code, _, errOut := run(t, filepath.Join(dir, "repository"), "-destination", "../impl", "-driver", "database/sql")
	generated, _ := os.ReadFile(filepath.Join(dir, "impl", "repository_gen.go")) //nolint:gosec // the test's own file
	return code, errOut, string(generated)
}

func TestCommand_LockedUpdate(t *testing.T) {
	t.Parallel()

	code, errOut, generated := generateLockedUpdate(t, snapshot.PostgreSQL, `
	//kanna:lock update skip-locked
	UpdateClaimed(ctx context.Context, c scopes.JobChangeSet, s ...scopes.Job) (int64, error)
	//kanna:lock no-key-update nowait
	UpdateHeld(ctx context.Context, state string, c scopes.JobChangeSet, s ...scopes.Job) (int64, error)
`)
	if code != cli.ExitOK {
		t.Fatalf("kanna-orm = %d\n%s", code, errOut)
	}
	for _, want := range []string{
		`[]string{"id"}, ` + "` FOR UPDATE SKIP LOCKED`, q)",
		`[]string{"id"}, ` + "` FOR NO KEY UPDATE NOWAIT`, q)",
	} {
		if !strings.Contains(generated, want) {
			t.Errorf("repository_gen.go has no %s\n%s", want, generated)
		}
	}
	if strings.Contains(generated, "InTransaction") {
		t.Errorf("a locked Update asks for a transaction, though its one statement holds the lock\n%s", generated)
	}
}

func TestCommand_LockedUpdateMistakes(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name    string
		dialect snapshot.Dialect
		methods string
		want    string
	}{
		{"on MySQL", snapshot.MySQL, "\t//kanna:lock update skip-locked\n" +
			"\tUpdateClaimed(ctx context.Context, c scopes.JobChangeSet, s ...scopes.Job) (int64, error)\n",
			"error: UpdateClaimed: MySQL may still wait for the rows a locked subquery passes over, as its UPDATE can " +
				"read more of the table than the rows it writes\n" +
				"  lock the rows with a List in a transaction, then update them by their keys\n"},
		{"of a table of no key", snapshot.PostgreSQL, "\t//kanna:lock update\n" +
			"\tUpdateLogs(ctx context.Context, c scopes.LogChangeSet, s ...scopes.Log) (int64, error)\n",
			"error: UpdateLogs: logs has no primary key or non-null unique key to select the rows to lock by\n"},
		{"an Update by a unique key", snapshot.PostgreSQL, "\t//kanna:lock update nowait\n" +
			"\tUpdateJob(ctx context.Context, id int64, c scopes.JobChangeSet, s ...scopes.Job) (int64, error)\n",
			"error: UpdateJob: " + lockedVerbs + "\n"},
		{"an Update of a condition taking no scopes", snapshot.PostgreSQL, "\t//kanna:lock update nowait\n" +
			"\tUpdateByState(ctx context.Context, state string, c scopes.JobChangeSet) (int64, error)\n",
			"error: UpdateByState: " + lockedVerbs + "\n"},
		{"an Update taking no scopes", snapshot.PostgreSQL, "\t//kanna:lock update nowait\n" +
			"\tUpdateState(ctx context.Context, id int64, c scopes.JobChangeSet) error\n",
			"error: UpdateState: " + lockedVerbs + "\n"},
		{"an Update of a whole row", snapshot.PostgreSQL, "\t//kanna:lock update nowait\n" +
			"\tUpdate(ctx context.Context, j model.Job, s ...scopes.Job) error\n",
			"error: Update: " + lockedVerbs + "\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			code, errOut, _ := generateLockedUpdate(t, tt.dialect, tt.methods)
			if code == cli.ExitOK || !strings.HasSuffix(errOut, tt.want) {
				t.Errorf("kanna-orm = %d, reports\n%s\nwant it to end in\n%s", code, errOut, tt.want)
			}
		})
	}
}
