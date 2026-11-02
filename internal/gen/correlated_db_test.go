package gen_test

import (
	"testing"

	"github.com/go-kanna/orm"
	"github.com/go-kanna/orm/internal/gen/testdata/model"
	"github.com/go-kanna/orm/internal/gen/testdata/mysql"
	"github.com/go-kanna/orm/internal/gen/testdata/mysqlrepo"
	"github.com/go-kanna/orm/internal/gen/testdata/postgres"
	"github.com/go-kanna/orm/internal/gen/testdata/repository"
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
	onPostgres(t, func(t *testing.T, repo repository.OrderRepository) {
		order, err := repo.Get(t.Context(), 1, postgres.Orders().
			PreloadTags(func(_ postgres.OrderColumns) orm.Many[model.Tag] { return postgres.Tags().NameAsc().Limit(1).Many() }).
			PreloadLines(func(_ postgres.OrderColumns) orm.Many[model.OrderLine] {
				return postgres.OrderLines().LineNoAsc().
					PreloadNotes(func(parent postgres.OrderLineColumns) orm.Many[model.LineNote] {
						return postgres.LineNotes().LineNoEqColumn(parent.LineNo).BodyAsc().Limit(1).Many()
					}).
					PreloadTags(func(_ postgres.OrderLineColumns) orm.Many[model.Tag] {
						return postgres.Tags().NameAsc().Limit(1).Many()
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
	repo := mysqlrepo.NewOrderRepository(mysqlDB(t))
	order, err := repo.Get(t.Context(), 1, mysql.Orders().
		PreloadTags(func(_ mysql.OrderColumns) orm.Many[model.Tag] { return mysql.Tags().NameAsc().Limit(1).Many() }).
		PreloadLines(func(_ mysql.OrderColumns) orm.Many[model.OrderLine] {
			return mysql.OrderLines().LineNoAsc().
				PreloadNotes(func(parent mysql.OrderLineColumns) orm.Many[model.LineNote] {
					return mysql.LineNotes().LineNoEqColumn(parent.LineNo).BodyAsc().Limit(1).Many()
				}).
				PreloadTags(func(_ mysql.OrderLineColumns) orm.Many[model.Tag] {
					return mysql.Tags().NameAsc().Limit(1).Many()
				}).Many()
		}))
	if err != nil {
		t.Fatal(err)
	}
	checkCorrelatedCollections(t, order)
}
