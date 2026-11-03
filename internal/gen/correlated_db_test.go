package gen_test

import (
	"testing"

	"github.com/go-kanna/orm/internal/gen/testdata/model"
	mscopes "github.com/go-kanna/orm/internal/gen/testdata/mysql/scopes"
	mstdlib "github.com/go-kanna/orm/internal/gen/testdata/mysql/stdlib"
	prepository "github.com/go-kanna/orm/internal/gen/testdata/postgres/repository"
	pscopes "github.com/go-kanna/orm/internal/gen/testdata/postgres/scopes"
	"github.com/go-kanna/orm/relation"
)

func checkCorrelatedCollections(t *testing.T, order model.Order) {
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
		if len(line.Tags) != 1 || line.Tags[0].Name != "gift" {
			t.Fatalf("line %d tags: %+v", line.LineNo, line.Tags)
		}
	}
}
func TestCorrelatedCollections_Postgres(t *testing.T) {
	t.Parallel()
	onPostgres(t, func(t *testing.T, repo prepository.OrderRepository) {
		order, err := repo.Get(t.Context(), 1, pscopes.Orders().
			PreloadTags(func(_ pscopes.OrderColumns) relation.Many[model.Tag] {
				return pscopes.Tags().NameAsc().Limit(1).Many()
			}).
			PreloadLines(func(_ pscopes.OrderColumns) relation.Many[model.OrderLine] {
				return pscopes.OrderLines().LineNoAsc().
					PreloadNotes(func(parent pscopes.OrderLineColumns) relation.Many[model.LineNote] {
						return pscopes.LineNotes().LineNoEqColumn(parent.LineNo).BodyAsc().Limit(1).Many()
					}).
					PreloadTags(func(_ pscopes.OrderLineColumns) relation.Many[model.Tag] {
						return pscopes.Tags().NameAsc().Limit(1).Many()
					}).Many()
			}))
		if err != nil {
			t.Fatal(err)
		}
		checkCorrelatedCollections(t, order)
	})
}
func TestCorrelatedCollections_MySQL(t *testing.T) {
	t.Parallel()
	repo := mstdlib.NewOrderRepository(mysqlDB(t))
	order, err := repo.Get(t.Context(), 1, mscopes.Orders().
		PreloadTags(func(_ mscopes.OrderColumns) relation.Many[model.Tag] {
			return mscopes.Tags().NameAsc().Limit(1).Many()
		}).
		PreloadLines(func(_ mscopes.OrderColumns) relation.Many[model.OrderLine] {
			return mscopes.OrderLines().LineNoAsc().
				PreloadNotes(func(parent mscopes.OrderLineColumns) relation.Many[model.LineNote] {
					return mscopes.LineNotes().LineNoEqColumn(parent.LineNo).BodyAsc().Limit(1).Many()
				}).
				PreloadTags(func(_ mscopes.OrderLineColumns) relation.Many[model.Tag] {
					return mscopes.Tags().NameAsc().Limit(1).Many()
				}).Many()
		}))
	if err != nil {
		t.Fatal(err)
	}
	checkCorrelatedCollections(t, order)
}
