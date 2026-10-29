package describe

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"

	"github.com/go-sql-driver/mysql"

	"github.com/go-kanna/orm/internal/placeholder"
	"github.com/go-kanna/orm/internal/snapshot"
)

const mysqlColumnsQuery = `
SELECT c.TABLE_NAME,
       c.COLUMN_NAME,
       c.COLUMN_TYPE,
       c.IS_NULLABLE = 'NO',
       c.EXTRA LIKE '%auto_increment%' OR c.GENERATION_EXPRESSION <> '',
       c.COLUMN_DEFAULT IS NOT NULL
FROM information_schema.COLUMNS c
JOIN information_schema.TABLES t ON t.TABLE_SCHEMA = c.TABLE_SCHEMA AND t.TABLE_NAME = c.TABLE_NAME
WHERE c.TABLE_SCHEMA = DATABASE()
  AND t.TABLE_TYPE = 'BASE TABLE'
ORDER BY c.TABLE_NAME, c.ORDINAL_POSITION`

// A part without a column is an expression, and one with a SUB_PART covers a prefix of its column.
const mysqlUniqueIndexesQuery = `
SELECT s.TABLE_NAME,
       s.INDEX_NAME,
       s.COLUMN_NAME,
       s.SUB_PART IS NOT NULL
FROM information_schema.STATISTICS s
JOIN information_schema.TABLES t ON t.TABLE_SCHEMA = s.TABLE_SCHEMA AND t.TABLE_NAME = s.TABLE_NAME
WHERE s.TABLE_SCHEMA = DATABASE()
  AND t.TABLE_TYPE = 'BASE TABLE'
  AND s.NON_UNIQUE = 0
ORDER BY s.TABLE_NAME, s.INDEX_NAME, s.SEQ_IN_INDEX`

const mysqlForeignKeysQuery = `
SELECT k.TABLE_NAME,
       k.CONSTRAINT_NAME,
       k.COLUMN_NAME,
       CASE
         WHEN k.REFERENCED_TABLE_SCHEMA = k.TABLE_SCHEMA THEN k.REFERENCED_TABLE_NAME
         ELSE concat(k.REFERENCED_TABLE_SCHEMA, '.', k.REFERENCED_TABLE_NAME)
       END,
       k.REFERENCED_COLUMN_NAME
FROM information_schema.KEY_COLUMN_USAGE k
JOIN information_schema.TABLES t ON t.TABLE_SCHEMA = k.TABLE_SCHEMA AND t.TABLE_NAME = k.TABLE_NAME
WHERE k.TABLE_SCHEMA = DATABASE()
  AND t.TABLE_TYPE = 'BASE TABLE'
  AND k.REFERENCED_TABLE_NAME IS NOT NULL
ORDER BY k.TABLE_NAME, k.CONSTRAINT_NAME, k.ORDINAL_POSITION`

// mysqlTables reads the tables of the database conn uses.
func mysqlTables(ctx context.Context, conn *sql.Conn) (map[string]snapshot.Table, error) {
	tables := map[string]snapshot.Table{}

	var (
		table string
		col   snapshot.Column
		dflt  bool
	)
	err := eachRow(ctx, conn, mysqlColumnsQuery, []any{&table, &col.Name, &col.Type, &col.NotNull, &col.Assigned, &dflt},
		func() {
			col.Default = dflt && !col.Assigned
			t := tables[table]
			t.Columns = append(t.Columns, col)
			tables[table] = t
		})
	if err != nil {
		return nil, fmt.Errorf("read the columns: %w", err)
	}

	type key struct{ table, name string }
	var (
		k       key
		column  sql.NullString
		prefix  bool
		indexes = map[key][]string{}
		partial = map[key]bool{}
	)
	err = eachRow(ctx, conn, mysqlUniqueIndexesQuery, []any{&k.table, &k.name, &column, &prefix}, func() {
		indexes[k] = append(indexes[k], column.String)
		partial[k] = partial[k] || !column.Valid || prefix
	})
	if err != nil {
		return nil, fmt.Errorf("read the unique keys: %w", err)
	}
	for k, columns := range indexes {
		if partial[k] {
			continue
		}
		t := tables[k.table]
		if k.name == "PRIMARY" {
			t.PrimaryKey = columns
		} else {
			t.UniqueKeys = append(t.UniqueKeys, columns)
		}
		tables[k.table] = t
	}

	var (
		references, refColumn string
		fks                   = map[key]snapshot.ForeignKey{}
	)
	err = eachRow(ctx, conn, mysqlForeignKeysQuery, []any{&k.table, &k.name, &column, &references, &refColumn}, func() {
		fk := fks[k]
		fk.Columns = append(fk.Columns, column.String)
		fk.References = references
		fk.ReferencedColumns = append(fk.ReferencedColumns, refColumn)
		fks[k] = fk
	})
	if err != nil {
		return nil, fmt.Errorf("read the foreign keys: %w", err)
	}
	for k, fk := range fks {
		t := tables[k.table]
		t.ForeignKeys = append(t.ForeignKeys, fk)
		tables[k.table] = t
	}

	mergeUniqueKeys(tables)
	return tables, nil
}

// eachRow scans each row query returns into dest and calls f.
func eachRow(ctx context.Context, conn *sql.Conn, query string, dest []any, f func()) (err error) {
	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		f()
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("query: %w", err)
	}
	return nil
}

// mysqlStatement describes the statement sql, written with :name parameters. MySQL tells the types of the columns a
// statement returns only when it runs, so the statement runs wrapped in a query that asks for no rows; one that cannot
// be wrapped so is one that returns none, such as an UPDATE.
func mysqlStatement(ctx context.Context, conn *sql.Conn, sql string) (snapshot.Statement, error) {
	text, names, err := placeholder.Rewrite(sql, snapshot.MySQL)
	if err != nil {
		return snapshot.Statement{}, fmt.Errorf("%w", err)
	}
	var inputs int
	err = conn.Raw(func(dc any) error {
		p, ok := dc.(driver.ConnPrepareContext)
		if !ok {
			return errors.New("the driver cannot prepare a statement")
		}
		stmt, err := p.PrepareContext(ctx, text)
		if err != nil {
			return err //nolint:wrapcheck // wrapped below, as every error of Raw is
		}
		inputs = stmt.NumInput()
		return stmt.Close()
	})
	if err != nil {
		return snapshot.Statement{}, fmt.Errorf("prepare: %w", err)
	}
	if inputs != len(names) {
		return snapshot.Statement{}, fmt.Errorf("the statement has %d placeholders for its %d parameters", inputs, len(names))
	}

	var st snapshot.Statement
	for _, name := range names {
		st.Params = append(st.Params, snapshot.Param{Name: name})
	}
	st.Columns, err = mysqlResult(ctx, conn, text, inputs)
	if err != nil {
		return snapshot.Statement{}, err
	}
	return st, distinctNames(st.Columns)
}

func mysqlResult(ctx context.Context, conn *sql.Conn, text string, inputs int) (_ []snapshot.Result, err error) {
	wrapped := "SELECT t.* FROM (\n" + text + "\n) AS t LIMIT 0" //nolint:gosec,unqueryvet // the statement described
	rows, err := conn.QueryContext(ctx, wrapped, make([]any, inputs)...)

	if e, ok := errors.AsType[*mysql.MySQLError](err); ok {
		switch e.Number {
		case 1060: // ER_DUP_FIELDNAME
			name := strings.TrimSuffix(strings.TrimPrefix(e.Message, "Duplicate column name '"), "'")
			return nil, fmt.Errorf("two columns are named %s: give them names of their own with AS", name)
		case 1064: // ER_PARSE_ERROR
			return nil, nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("read the result: %w", err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()

	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, fmt.Errorf("read the result: %w", err)
	}
	columns := make([]snapshot.Result, 0, len(types))
	for _, t := range types {
		c := snapshot.Result{Name: t.Name(), Type: mysqlTypeName(t.DatabaseTypeName())}
		if nullable, ok := t.Nullable(); ok {
			c.NotNull, c.Nullable = !nullable, nullable
		}
		columns = append(columns, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the result: %w", err)
	}
	return columns, nil
}

// mysqlTypeName spells a type of a result as information_schema spells that of a column, without its width.
func mysqlTypeName(name string) string {
	name = strings.ToLower(name)
	if base, ok := strings.CutPrefix(name, "unsigned "); ok {
		return base + " unsigned"
	}
	return name
}
