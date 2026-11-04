package gen_test

import (
	"testing"

	"github.com/go-kanna/orm/internal/gen/testdata/model"
	mscopes "github.com/go-kanna/orm/internal/gen/testdata/mysql/scopes"
	mstdlib "github.com/go-kanna/orm/internal/gen/testdata/mysql/stdlib"
	prepository "github.com/go-kanna/orm/internal/gen/testdata/postgres/repository"
	pscopes "github.com/go-kanna/orm/internal/gen/testdata/postgres/scopes"
)

// checkPagesOfEach checks the first related row of each kept by a limit: the tags of the order, through a table
// between, and the notes and the tags of each of its lines, by a foreign key of two columns.
func checkPagesOfEach(t *testing.T, order model.Order) {
	t.Helper()
	if len(order.Tags) != 1 || order.Tags[0].Name != "gift" {
		t.Fatalf("order tags: %+v", order.Tags)
	}
	if len(order.Lines) != 2 {
		t.Fatalf("lines: %+v", order.Lines)
	}
	for i, line := range order.Lines {
		want := []string{"fragile", "spare"}[i]
		if len(line.Notes) != 1 || line.Notes[0].Body != want {
			t.Fatalf("line %d notes: %+v", line.LineNo, line.Notes)
		}
		if len(line.Tags) != 1 || line.Tags[0].TagID != "gift" {
			t.Fatalf("line %d tags: %+v", line.LineNo, line.Tags)
		}
	}
}

func TestWithPagesEach_Postgres(t *testing.T) {
	t.Parallel()
	onPostgres(t, func(t *testing.T, repo prepository.OrderRepository) {
		order, err := repo.Get(t.Context(), 1, pscopes.Orders().
			WithTags(pscopes.Tags().NameAsc().Limit(1)).
			WithLines(pscopes.OrderLines().LineNoAsc().
				WithNotes(pscopes.LineNotes().BodyAsc().Limit(1)).
				WithTags(pscopes.LineTags().TagIDAsc().Limit(1))))
		if err != nil {
			t.Fatal(err)
		}
		checkPagesOfEach(t, order)
	})
}

func TestWithPagesEach_MySQL(t *testing.T) {
	t.Parallel()
	repo := mstdlib.NewOrderRepository(mysqlDB(t))
	order, err := repo.Get(t.Context(), 1, mscopes.Orders().
		WithTags(mscopes.Tags().NameAsc().Limit(1)).
		WithLines(mscopes.OrderLines().LineNoAsc().
			WithNotes(mscopes.LineNotes().BodyAsc().Limit(1)).
			WithTags(mscopes.LineTags().TagIDAsc().Limit(1))))
	if err != nil {
		t.Fatal(err)
	}
	checkPagesOfEach(t, order)
}
