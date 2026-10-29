package describe_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"os"
	"reflect"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"

	"github.com/go-kanna/orm/internal/describe"
	"github.com/go-kanna/orm/internal/snapshot"
)

// mysql connects to the server KANNA_ORM_TEST_MYSQL names, using a database of the test's own, dropped when the test
// ends. Without the variable, the test is skipped.
func mysql(t *testing.T) (*sql.Conn, string) {
	t.Helper()

	dsn := os.Getenv("KANNA_ORM_TEST_MYSQL")
	if dsn == "" {
		t.Skip("KANNA_ORM_TEST_MYSQL is not set; make db-up test-db sets it")
	}
	ctx := t.Context()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	name := "test_" + strings.ToLower(rand.Text())
	t.Cleanup(func() {
		if _, err := conn.ExecContext(context.WithoutCancel(ctx), "DROP DATABASE IF EXISTS "+name); err != nil {
			t.Error(err)
		}
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	mysqlExec(t, conn, "CREATE DATABASE "+name+"; USE "+name)
	return conn, name
}

func mysqlExec(t *testing.T, conn *sql.Conn, sql string) {
	t.Helper()

	if _, err := conn.ExecContext(t.Context(), sql); err != nil {
		t.Fatal(err)
	}
}

func TestMySQLTables(t *testing.T) {
	t.Parallel()

	conn, _ := mysql(t)
	mysqlExec(t, conn, `
CREATE TABLE customers (
	id BIGINT AUTO_INCREMENT PRIMARY KEY,
	tenant_id INT NOT NULL,
	email VARCHAR(255) NOT NULL,
	UNIQUE KEY (tenant_id, email)
);
CREATE UNIQUE INDEX customers_lower_email ON customers ((lower(email)));
CREATE UNIQUE INDEX customers_email_prefix ON customers (email(10));

CREATE TABLE orders (
	id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
	customer_id BIGINT NOT NULL,
	number VARCHAR(32) NOT NULL,
	status ENUM('open', 'closed') NOT NULL DEFAULT 'open',
	paid TINYINT(1) NOT NULL DEFAULT 0,
	total DECIMAL(10, 2) NOT NULL,
	total_with_tax DECIMAL(12, 2) AS (total * 1.1) STORED,
	placed_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
	note TEXT,
	UNIQUE KEY orders_number (number),
	UNIQUE KEY orders_number_again (number),
	FOREIGN KEY (customer_id) REFERENCES customers (id)
);

CREATE TABLE order_lines (
	order_id BIGINT UNSIGNED,
	sku VARCHAR(32),
	quantity INT NOT NULL,
	PRIMARY KEY (order_id, sku),
	FOREIGN KEY (order_id) REFERENCES orders (id)
);

CREATE VIEW open_orders AS SELECT id, number FROM orders WHERE status = 'open';
`)

	tables, err := describe.MySQLTables(t.Context(), conn)
	if err != nil {
		t.Fatal(err)
	}

	// The AUTO_INCREMENT and generated columns are assigned rather than defaulted; the unique keys leave out the
	// functional and prefix ones and the duplicate; and the view is no table.
	const want = `{
  "dialect": "mysql",
  "tables": {
    "customers": {
      "columns": [
        {"name": "id", "type": "bigint", "not_null": true, "assigned": true},
        {"name": "tenant_id", "type": "int", "not_null": true},
        {"name": "email", "type": "varchar(255)", "not_null": true}
      ],
      "primary_key": ["id"],
      "unique_keys": [
        ["tenant_id", "email"]
      ],
      "foreign_keys": []
    },
    "order_lines": {
      "columns": [
        {"name": "order_id", "type": "bigint unsigned", "not_null": true},
        {"name": "sku", "type": "varchar(32)", "not_null": true},
        {"name": "quantity", "type": "int", "not_null": true}
      ],
      "primary_key": ["order_id", "sku"],
      "unique_keys": [],
      "foreign_keys": [
        {"columns": ["order_id"], "references": "orders", "referenced_columns": ["id"]}
      ]
    },
    "orders": {
      "columns": [
        {"name": "id", "type": "bigint unsigned", "not_null": true, "assigned": true},
        {"name": "customer_id", "type": "bigint", "not_null": true},
        {"name": "number", "type": "varchar(32)", "not_null": true},
        {"name": "status", "type": "enum('open','closed')", "not_null": true, "default": true},
        {"name": "paid", "type": "tinyint(1)", "not_null": true, "default": true},
        {"name": "total", "type": "decimal(10,2)", "not_null": true},
        {"name": "total_with_tax", "type": "decimal(12,2)", "assigned": true},
        {"name": "placed_at", "type": "datetime(6)", "not_null": true, "default": true},
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
	got := snapshot.Marshal(snapshot.Snapshot{Dialect: snapshot.MySQL, Tables: tables})
	if string(got) != want {
		t.Errorf("tables:\n%s\nwant:\n%s", got, want)
	}
}

func TestMySQLTables_NamesATableOfAnotherDatabaseWithItsDatabase(t *testing.T) {
	t.Parallel()

	conn, name := mysql(t)
	other := name + "_other"
	t.Cleanup(func() {
		const drop = "DROP TABLE IF EXISTS addresses; DROP DATABASE IF EXISTS "
		if _, err := conn.ExecContext(context.WithoutCancel(t.Context()), drop+other); err != nil {
			t.Error(err)
		}
	})
	mysqlExec(t, conn, `
CREATE DATABASE `+other+`;
CREATE TABLE `+other+`.countries (code VARCHAR(2) PRIMARY KEY);
CREATE TABLE addresses (country VARCHAR(2) NOT NULL, FOREIGN KEY (country) REFERENCES `+other+`.countries (code));
`)

	tables, err := describe.MySQLTables(t.Context(), conn)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := tables["countries"]; ok {
		t.Error("countries is read, though it is not in the database in use")
	}
	fks := tables["addresses"].ForeignKeys
	if len(fks) != 1 || fks[0].References != other+".countries" {
		t.Errorf("foreign keys of addresses = %+v, want one to %s.countries", fks, other)
	}
}

const mysqlShop = `
CREATE TABLE customers (id BIGINT PRIMARY KEY, email VARCHAR(255) NOT NULL);
CREATE TABLE orders (
	id BIGINT UNSIGNED PRIMARY KEY,
	customer_id BIGINT NOT NULL,
	status VARCHAR(20) NOT NULL,
	total BIGINT NOT NULL,
	placed_at DATETIME(6) NOT NULL,
	note TEXT,
	FOREIGN KEY (customer_id) REFERENCES customers (id)
);
`

func TestMySQLStatement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, sql string
		want      snapshot.Statement
	}{
		{"columns read straight from a table",
			`SELECT id, customer_id, note FROM orders WHERE placed_at >= :since AND status = :status;`,
			snapshot.Statement{
				Params: []snapshot.Param{{Name: "since"}, {Name: "status"}},
				Columns: []snapshot.Result{
					{Name: "id", Type: "bigint unsigned", NotNull: true},
					{Name: "customer_id", Type: "bigint", NotNull: true},
					{Name: "note", Type: "text", Nullable: true},
				},
			}},
		{"expressions",
			`SELECT date_format(placed_at, '%Y-%m') AS month, count(*) AS count, sum(total) AS total FROM orders GROUP BY 1`,
			snapshot.Statement{Columns: []snapshot.Result{
				{Name: "month", Type: "varchar", Nullable: true},
				{Name: "count", Type: "bigint", NotNull: true},
				{Name: "total", Type: "decimal", Nullable: true},
			}}},
		{"a parameter used twice",
			`SELECT id FROM orders WHERE customer_id = :c OR id = :c`,
			snapshot.Statement{
				Params:  []snapshot.Param{{Name: "c"}, {Name: "c"}},
				Columns: []snapshot.Result{{Name: "id", Type: "bigint unsigned", NotNull: true}},
			}},
		{"an outer join",
			`SELECT o.id, c.email FROM orders o LEFT JOIN customers c ON c.id = o.customer_id`,
			snapshot.Statement{Columns: []snapshot.Result{
				{Name: "id", Type: "bigint unsigned", NotNull: true},
				{Name: "email", Type: "varchar", Nullable: true},
			}}},
		{"no rows", `UPDATE orders SET status = :status WHERE id = :id`,
			snapshot.Statement{Params: []snapshot.Param{{Name: "status"}, {Name: "id"}}}},
		{"a comment after the end", "SELECT id FROM orders; -- done",
			snapshot.Statement{Columns: []snapshot.Result{{Name: "id", Type: "bigint unsigned", NotNull: true}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conn, _ := mysql(t)
			mysqlExec(t, conn, mysqlShop)
			got, err := describe.MySQLStatement(t.Context(), conn, tt.sql)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("MySQLStatement =\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}

func TestMySQLStatement_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, sql, want string
	}{
		{"a column that is not there", `SELECT totl FROM orders`, "Unknown column 'totl'"},
		{"two columns of one name", `SELECT o.id, c.id FROM orders o JOIN customers c ON c.id = o.customer_id`,
			"two columns are named id: give them names of their own with AS"},
		{"a placeholder of MySQL's own", `SELECT id FROM orders WHERE id = ?`, "? is a placeholder of MySQL's own"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conn, _ := mysql(t)
			mysqlExec(t, conn, mysqlShop)
			_, err := describe.MySQLStatement(t.Context(), conn, tt.sql)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("MySQLStatement error = %v, want one containing %q", err, tt.want)
			}
		})
	}
}
