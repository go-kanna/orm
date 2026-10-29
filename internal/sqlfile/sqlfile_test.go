package sqlfile_test

import (
	"go/token"
	"reflect"
	"strings"
	"testing"

	"github.com/go-kanna/internal/diag"

	"github.com/go-kanna/orm/internal/sqlfile"
)

const path = "order_repository.sql"

func at(line int) token.Position {
	return token.Position{Filename: path, Line: line, Column: 1}
}

func TestParse(t *testing.T) {
	t.Parallel()

	const data = `-- The queries of OrderRepository.

-- query: MonthCounts
SELECT date_trunc('month', placed_at) AS month, count(*) AS count
FROM orders
WHERE placed_at >= :since -- the start of the first month
GROUP BY 1;

-- TODO: count by week too.

-- Close closes the order of id,
--
--   whatever its status.
-- query: Close

UPDATE orders SET status = 'closed' WHERE id = :id
-- PlacedInMonth narrows to the orders placed in the month of at.
-- scope: Order.PlacedInMonth(at time.Time)
-- by the month of the database's time zone
date_trunc('month', placed_at) = date_trunc('month', :at)
-- the end
`
	stmts, ds := sqlfile.Parse(path, []byte(data))
	if len(ds) > 0 {
		t.Fatalf("Parse: %v", ds)
	}
	want := []sqlfile.Statement{
		{
			Kind: sqlfile.Query, Name: "MonthCounts",
			Text: "SELECT date_trunc('month', placed_at) AS month, count(*) AS count\nFROM orders\n" +
				"WHERE placed_at >= :since -- the start of the first month\nGROUP BY 1;",
			Header: at(3), Line: 4,
		},
		{
			Kind: sqlfile.Query, Name: "Close",
			Doc:    "Close closes the order of id,\n\n  whatever its status.",
			Text:   "UPDATE orders SET status = 'closed' WHERE id = :id",
			Header: at(14), Line: 16,
		},
		{
			Kind: sqlfile.Scope, Type: "Order", Name: "PlacedInMonth", Params: "at time.Time",
			Doc: "PlacedInMonth narrows to the orders placed in the month of at.",
			Text: "-- by the month of the database's time zone\n" +
				"date_trunc('month', placed_at) = date_trunc('month', :at)",
			Header: at(18), Line: 19,
		},
	}
	if !reflect.DeepEqual(stmts, want) {
		t.Errorf("Parse =\n%+v\nwant\n%+v", stmts, want)
	}
}

func TestParse_NoHeaderIsNoMistake(t *testing.T) {
	t.Parallel()

	stmts, ds := sqlfile.Parse(path, []byte("CREATE TABLE orders (id bigint);\n"))
	if stmts != nil || ds != nil {
		t.Errorf("Parse = %v, %v; want nothing of kanna-orm's", stmts, ds)
	}
}

func TestParse_LinesEndingInCRLF(t *testing.T) {
	t.Parallel()

	stmts, ds := sqlfile.Parse(path, []byte("-- query: Count\r\nSELECT count(*) FROM orders\r\n"))
	if len(ds) > 0 || len(stmts) != 1 || stmts[0].Name != "Count" || stmts[0].Text != "SELECT count(*) FROM orders" {
		t.Errorf("Parse = %+v, %v", stmts, ds)
	}
}

func TestParse_Mistakes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, data string
		want       []string
	}{
		{"a statement before any header", "-- notes\nSELECT 1;\n-- query: A\nSELECT 2",
			[]string{"order_repository.sql:2:1: error: a statement comes before any -- query: or -- scope: line"}},
		{"a header with no statement", "-- query: A\n\n-- query: B\nSELECT 1",
			[]string{"order_repository.sql:1:1: error: -- query: A holds no statement"}},
		{"a header with comments alone", "-- query: A\n-- SELECT 1\n\n-- query: B\nSELECT 1",
			[]string{"order_repository.sql:1:1: error: -- query: A holds no statement"}},
		{"a name written twice", "-- query: A\nSELECT 1\n-- query: A\nSELECT 2",
			[]string{"order_repository.sql:3:1: error: -- query: A is written a second time"}},
		{"a query without a name", "-- query: month counts\nSELECT 1", []string{
			"order_repository.sql:1:1: error: -- query: wants the name of its method\n  as in -- query: MonthCounts",
		}},
		{"a scope without its parameters", "-- scope: Order.PlacedInMonth\nx = 1", []string{
			"order_repository.sql:1:1: error: -- scope: wants the method it is, with its parameters\n" +
				"  as in -- scope: Order.PlacedInMonth(at time.Time)",
		}},
		{"a misspelled header", "-- query: A\nSELECT 1\n-- qurey: B\nSELECT 2", []string{
			"order_repository.sql:3:1: warning: -- qurey: is not recognized, so the line is a comment\n" +
				"  did you mean -- query:?",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, ds := sqlfile.Parse(path, []byte(tt.data))
			got := make([]string, 0, len(ds))
			for _, d := range ds {
				got = append(got, d.String())
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parse reports\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
		})
	}
}

func TestParse_KeepsAMisspelledHeaderInTheStatementBefore(t *testing.T) {
	t.Parallel()

	stmts, ds := sqlfile.Parse(path, []byte("-- query: A\nSELECT 1\n-- qurey: B\nSELECT 2"))
	if diag.HasErrors(ds) || len(stmts) != 1 || stmts[0].Text != "SELECT 1\n-- qurey: B\nSELECT 2" {
		t.Errorf("Parse = %+v, %v", stmts, ds)
	}
}
