package describe_test

import (
	"context"
	"crypto/rand"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/go-kanna/orm/internal/describe"
	"github.com/go-kanna/orm/internal/snapshot"
)

// postgres connects to the database KANNA_ORM_TEST_POSTGRES names, with a schema of the test's own as its current
// one, dropped when the test ends. Without the variable, the test is skipped.
func postgres(t *testing.T) (*pgx.Conn, string) {
	t.Helper()

	url := os.Getenv("KANNA_ORM_TEST_POSTGRES")
	if url == "" {
		t.Skip("KANNA_ORM_TEST_POSTGRES is not set; make db-up test-db sets it")
	}
	ctx := t.Context()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	schema := "test_" + strings.ToLower(rand.Text())
	t.Cleanup(func() {
		ctx := context.WithoutCancel(ctx)
		if _, err := conn.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
		if err := conn.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	postgresExec(t, conn, "CREATE SCHEMA "+schema+"; SET search_path TO "+schema)
	return conn, schema
}

func postgresExec(t *testing.T, conn *pgx.Conn, sql string) {
	t.Helper()

	if _, err := conn.Exec(t.Context(), sql); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresTables(t *testing.T) {
	t.Parallel()

	conn, _ := postgres(t)
	postgresExec(t, conn, `
CREATE TYPE mood AS ENUM ('ok', 'meh');
CREATE DOMAIN email AS text;

CREATE TABLE customers (
	id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	tenant_id int NOT NULL,
	email email NOT NULL,
	UNIQUE (tenant_id, email)
);
CREATE UNIQUE INDEX customers_lower_email ON customers (lower(email));

CREATE TABLE orders (
	id bigserial PRIMARY KEY,
	customer_id bigint NOT NULL REFERENCES customers (id),
	number text NOT NULL,
	status text NOT NULL DEFAULT 'open',
	mood mood,
	tags text[] NOT NULL DEFAULT '{}',
	total numeric(10, 2) NOT NULL,
	total_with_tax numeric GENERATED ALWAYS AS (total * 1.1) STORED,
	legacy text,
	note text
);
ALTER TABLE orders DROP COLUMN legacy;
CREATE UNIQUE INDEX orders_number ON orders (number) INCLUDE (status);
CREATE UNIQUE INDEX orders_number_again ON orders (number);
CREATE UNIQUE INDEX orders_one_open ON orders (customer_id) WHERE status = 'open';
ALTER TABLE orders ADD CONSTRAINT orders_customer_number UNIQUE (customer_id, number) DEFERRABLE;

CREATE TABLE order_lines (
	order_id bigint REFERENCES orders (id),
	sku text,
	quantity int NOT NULL,
	PRIMARY KEY (order_id, sku)
);

CREATE TABLE events (
	id bigint NOT NULL,
	at date NOT NULL,
	PRIMARY KEY (id, at)
) PARTITION BY RANGE (at);
CREATE TABLE events_2026 PARTITION OF events FOR VALUES FROM ('2026-01-01') TO ('2027-01-01');

CREATE VIEW open_orders AS SELECT id, number FROM orders WHERE status = 'open';
`)

	tables, err := describe.PostgresTables(t.Context(), conn)
	if err != nil {
		t.Fatal(err)
	}

	// The identity, serial, and generated columns are assigned rather than defaulted; the domain is its base type; the
	// unique keys leave out the expression, partial, and deferrable ones, the INCLUDE column, and the duplicate; and
	// neither the partition nor the view is a table.
	const want = `{
  "dialect": "postgresql",
  "tables": {
    "customers": {
      "columns": [
        {"name": "id", "type": "int8", "not_null": true, "assigned": true},
        {"name": "tenant_id", "type": "int4", "not_null": true},
        {"name": "email", "type": "text", "not_null": true}
      ],
      "primary_key": ["id"],
      "unique_keys": [
        ["tenant_id", "email"]
      ],
      "foreign_keys": []
    },
    "events": {
      "columns": [
        {"name": "id", "type": "int8", "not_null": true},
        {"name": "at", "type": "date", "not_null": true}
      ],
      "primary_key": ["id", "at"],
      "unique_keys": [],
      "foreign_keys": []
    },
    "order_lines": {
      "columns": [
        {"name": "order_id", "type": "int8", "not_null": true},
        {"name": "sku", "type": "text", "not_null": true},
        {"name": "quantity", "type": "int4", "not_null": true}
      ],
      "primary_key": ["order_id", "sku"],
      "unique_keys": [],
      "foreign_keys": [
        {"columns": ["order_id"], "references": "orders", "referenced_columns": ["id"]}
      ]
    },
    "orders": {
      "columns": [
        {"name": "id", "type": "int8", "not_null": true, "assigned": true},
        {"name": "customer_id", "type": "int8", "not_null": true},
        {"name": "number", "type": "text", "not_null": true},
        {"name": "status", "type": "text", "not_null": true, "default": true},
        {"name": "mood", "type": "mood"},
        {"name": "tags", "type": "text[]", "not_null": true, "default": true},
        {"name": "total", "type": "numeric", "not_null": true},
        {"name": "total_with_tax", "type": "numeric", "assigned": true},
        {"name": "note", "type": "text"}
      ],
      "primary_key": ["id"],
      "unique_keys": [
        ["number"]
      ],
      "foreign_keys": [
        {"columns": ["customer_id"], "references": "customers", "referenced_columns": ["id"]}
      ]
    }
  },
  "statements": {}
}
`
	got := snapshot.Marshal(snapshot.Snapshot{Dialect: snapshot.PostgreSQL, Tables: tables})
	if string(got) != want {
		t.Errorf("tables:\n%s\nwant:\n%s", got, want)
	}
}

func TestPostgresTables_NamesATableOfAnotherSchemaWithItsSchema(t *testing.T) {
	t.Parallel()

	conn, schema := postgres(t)
	other := schema + "_other"
	postgresExec(t, conn, `
CREATE SCHEMA `+other+`;
CREATE TABLE `+other+`.countries (code text PRIMARY KEY);
CREATE TABLE addresses (country text NOT NULL REFERENCES `+other+`.countries (code));
`)
	t.Cleanup(func() {
		if _, err := conn.Exec(context.WithoutCancel(t.Context()), "DROP SCHEMA "+other+" CASCADE"); err != nil {
			t.Error(err)
		}
	})

	tables, err := describe.PostgresTables(t.Context(), conn)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := tables["countries"]; ok {
		t.Error("countries is read, though it is not in the current schema")
	}
	fks := tables["addresses"].ForeignKeys
	if len(fks) != 1 || fks[0].References != other+".countries" {
		t.Errorf("foreign keys of addresses = %+v, want one to %s.countries", fks, other)
	}
}

const postgresShop = `
CREATE TYPE mood AS ENUM ('ok', 'meh');
CREATE DOMAIN email AS text;
CREATE TABLE customers (id bigint PRIMARY KEY, email email NOT NULL);
CREATE TABLE orders (
	id bigint PRIMARY KEY,
	customer_id bigint NOT NULL REFERENCES customers (id),
	status text NOT NULL,
	mood mood,
	tags text[] NOT NULL,
	total bigint NOT NULL,
	placed_at timestamptz NOT NULL,
	note text
);
`

func TestPostgresStatement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, sql string
		want      snapshot.Statement
	}{
		{"columns read straight from a table",
			`SELECT id, note FROM orders WHERE placed_at >= :since AND status = :status;`,
			snapshot.Statement{
				Params: []snapshot.Param{{Name: "since", Type: "timestamptz"}, {Name: "status", Type: "text"}},
				Columns: []snapshot.Result{
					{Name: "id", Type: "int8", NotNull: true},
					{Name: "note", Type: "text", Nullable: true},
				},
			}},
		{"expressions, which may or may not be NULL",
			`SELECT date_trunc('month', placed_at) AS month, count(*) AS count, sum(total) AS total FROM orders GROUP BY 1`,
			snapshot.Statement{Columns: []snapshot.Result{
				{Name: "month", Type: "timestamptz"},
				{Name: "count", Type: "int8"},
				{Name: "total", Type: "numeric"},
			}}},
		{"a parameter used twice",
			`SELECT id FROM orders WHERE customer_id = :c OR id = :c`,
			snapshot.Statement{
				Params:  []snapshot.Param{{Name: "c", Type: "int8"}},
				Columns: []snapshot.Result{{Name: "id", Type: "int8", NotNull: true}},
			}},
		{"a domain, an enum, and an array",
			`SELECT c.email, o.mood, o.tags FROM orders o JOIN customers c ON c.id = o.customer_id
			 WHERE o.mood = :mood AND c.email = :email AND o.tags @> :tags`,
			snapshot.Statement{
				Params: []snapshot.Param{
					{Name: "mood", Type: "mood"}, {Name: "email", Type: "text"}, {Name: "tags", Type: "text[]"},
				},
				Columns: []snapshot.Result{
					{Name: "email", Type: "text", NotNull: true},
					{Name: "mood", Type: "mood", Nullable: true},
					{Name: "tags", Type: "text[]", NotNull: true},
				},
			}},
		{"an outer join, read as the table declares the column",
			`SELECT o.id, c.email FROM orders o LEFT JOIN customers c ON c.id = o.customer_id`,
			snapshot.Statement{Columns: []snapshot.Result{
				{Name: "id", Type: "int8", NotNull: true},
				{Name: "email", Type: "text", NotNull: true},
			}}},
		{"no rows", `UPDATE orders SET status = :status WHERE id = :id`,
			snapshot.Statement{Params: []snapshot.Param{{Name: "status", Type: "text"}, {Name: "id", Type: "int8"}}}},
		{"rows returned", `UPDATE orders SET status = :status WHERE id = :id RETURNING id, status`,
			snapshot.Statement{
				Params: []snapshot.Param{{Name: "status", Type: "text"}, {Name: "id", Type: "int8"}},
				Columns: []snapshot.Result{
					{Name: "id", Type: "int8", NotNull: true},
					{Name: "status", Type: "text", NotNull: true},
				},
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conn, _ := postgres(t)
			postgresExec(t, conn, postgresShop)
			got, err := describe.PostgresStatement(t.Context(), conn, tt.sql)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("PostgresStatement =\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}

func TestPostgresStatement_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, sql, want string
	}{
		{"a column that is not there", `SELECT totl FROM orders`, `column "totl" does not exist`},
		{"two columns of one name", `SELECT o.id, c.id FROM orders o JOIN customers c ON c.id = o.customer_id`,
			"two columns are named id: give them names of their own with AS"},
		{"two statements", `SELECT 1; SELECT 2`, "cannot insert multiple commands into a prepared statement"},
		{"a placeholder of PostgreSQL's own", `SELECT id FROM orders WHERE id = $1`,
			"$1 is a placeholder of PostgreSQL's own"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conn, _ := postgres(t)
			postgresExec(t, conn, postgresShop)
			_, err := describe.PostgresStatement(t.Context(), conn, tt.sql)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("PostgresStatement error = %v, want one containing %q", err, tt.want)
			}
		})
	}
}
