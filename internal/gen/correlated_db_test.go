package gen_test

import (
	"testing"

	"github.com/go-kanna/orm/internal/gen/testdata/model"
	mysqlscopes "github.com/go-kanna/orm/internal/gen/testdata/mysql/scopes"
	mysqlstore "github.com/go-kanna/orm/internal/gen/testdata/mysql/sqlstore"
	pgrepository "github.com/go-kanna/orm/internal/gen/testdata/postgres/repository"
	pgscopes "github.com/go-kanna/orm/internal/gen/testdata/postgres/scopes"
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
	onPostgres(t, func(t *testing.T, repo pgrepository.OrderRepository) {
		order, err := repo.Get(t.Context(), 1, pgscopes.Orders().
			PreloadTags(func(_ pgscopes.OrderColumns) relation.Many[model.Tag] {
				return pgscopes.Tags().NameAsc().Limit(1).Many()
			}).
			PreloadLines(func(_ pgscopes.OrderColumns) relation.Many[model.OrderLine] {
				return pgscopes.OrderLines().LineNoAsc().
					PreloadNotes(func(parent pgscopes.OrderLineColumns) relation.Many[model.LineNote] {
						return pgscopes.LineNotes().LineNoEqColumn(parent.LineNo).BodyAsc().Limit(1).Many()
					}).
					PreloadTags(func(_ pgscopes.OrderLineColumns) relation.Many[model.Tag] {
						return pgscopes.Tags().NameAsc().Limit(1).Many()
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
	repo := mysqlstore.NewOrderRepository(mysqlDB(t))
	order, err := repo.Get(t.Context(), 1, mysqlscopes.Orders().
		PreloadTags(func(_ mysqlscopes.OrderColumns) relation.Many[model.Tag] {
			return mysqlscopes.Tags().NameAsc().Limit(1).Many()
		}).
		PreloadLines(func(_ mysqlscopes.OrderColumns) relation.Many[model.OrderLine] {
			return mysqlscopes.OrderLines().LineNoAsc().
				PreloadNotes(func(parent mysqlscopes.OrderLineColumns) relation.Many[model.LineNote] {
					return mysqlscopes.LineNotes().LineNoEqColumn(parent.LineNo).BodyAsc().Limit(1).Many()
				}).
				PreloadTags(func(_ mysqlscopes.OrderLineColumns) relation.Many[model.Tag] {
					return mysqlscopes.Tags().NameAsc().Limit(1).Many()
				}).Many()
		}))
	if err != nil {
		t.Fatal(err)
	}
	checkCorrelatedCollections(t, order)
}
