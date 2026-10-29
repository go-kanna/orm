package describe

import (
	"context"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/go-kanna/orm/internal/placeholder"
	"github.com/go-kanna/orm/internal/snapshot"
)

// The tables of the current schema: ordinary and partitioned ones, but not the partitions themselves.
const postgresTablesOfSchema = `c.relnamespace = current_schema()::regnamespace
  AND c.relkind IN ('r', 'p')
  AND NOT c.relispartition`

// The name of the type t, given the joins postgresTypeJoins makes: a domain is named as its base type, and an array
// as its element type followed by [].
const (
	postgresTypeName = `CASE
         WHEN t.typtype = 'd' THEN bt.typname
         WHEN t.typcategory = 'A' THEN et.typname || '[]'
         ELSE t.typname
       END`
	postgresTypeJoins = `LEFT JOIN pg_type bt ON bt.oid = t.typbasetype
LEFT JOIN pg_type et ON et.oid = t.typelem`
)

const postgresColumnsQuery = `
SELECT c.relname,
       a.attname,
       ` + postgresTypeName + `,
       a.attnotnull,
       a.attidentity <> '' OR a.attgenerated <> '' OR coalesce(pg_get_expr(d.adbin, d.adrelid), '') LIKE 'nextval(%',
       a.atthasdef
FROM pg_attribute a
JOIN pg_class c ON c.oid = a.attrelid
JOIN pg_type t ON t.oid = a.atttypid
` + postgresTypeJoins + `
LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
WHERE ` + postgresTablesOfSchema + `
  AND a.attnum > 0
  AND NOT a.attisdropped
ORDER BY c.relname, a.attnum`

// A referenced table in another schema is named with its schema.
const postgresKeysQuery = `
SELECT c.relname,
       k.contype,
       array(SELECT a.attname
             FROM unnest(k.conkey) WITH ORDINALITY AS u(attnum, n)
             JOIN pg_attribute a ON a.attrelid = k.conrelid AND a.attnum = u.attnum
             ORDER BY u.n),
       CASE
         WHEN f.oid IS NULL THEN ''
         WHEN f.relnamespace = c.relnamespace THEN f.relname::text
         ELSE f.relnamespace::regnamespace::text || '.' || f.relname
       END,
       array(SELECT a.attname
             FROM unnest(k.confkey) WITH ORDINALITY AS u(attnum, n)
             JOIN pg_attribute a ON a.attrelid = k.confrelid AND a.attnum = u.attnum
             ORDER BY u.n)
FROM pg_constraint k
JOIN pg_class c ON c.oid = k.conrelid
LEFT JOIN pg_class f ON f.oid = k.confrelid
WHERE ` + postgresTablesOfSchema + `
  AND k.contype IN ('p', 'f')`

// Only a unique index that ON CONFLICT can name is a unique key: one that is checked at once, covers no expression,
// and has no WHERE. Columns an INCLUDE clause adds are not part of the key.
const postgresUniqueKeysQuery = `
SELECT c.relname,
       array(SELECT a.attname
             FROM unnest(i.indkey::int2[]) WITH ORDINALITY AS u(attnum, n)
             JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = u.attnum
             WHERE u.n <= i.indnkeyatts
             ORDER BY u.n)
FROM pg_index i
JOIN pg_class c ON c.oid = i.indrelid
WHERE ` + postgresTablesOfSchema + `
  AND i.indisunique
  AND NOT i.indisprimary
  AND i.indimmediate
  AND i.indpred IS NULL
  AND i.indexprs IS NULL`

// postgresTables reads the tables of the schema conn has as its current one.
func postgresTables(ctx context.Context, conn *pgx.Conn) (map[string]snapshot.Table, error) {
	tables := map[string]snapshot.Table{}

	var (
		table string
		col   snapshot.Column
		dflt  bool
	)
	rows, _ := conn.Query(ctx, postgresColumnsQuery)
	_, err := pgx.ForEachRow(rows, []any{&table, &col.Name, &col.Type, &col.NotNull, &col.Assigned, &dflt}, func() error {
		col.Default = dflt && !col.Assigned
		t := tables[table]
		t.Columns = append(t.Columns, col)
		tables[table] = t
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read the columns: %w", err)
	}

	var (
		kind             byte
		columns, refCols []string
		references       string
	)
	rows, _ = conn.Query(ctx, postgresKeysQuery)
	_, err = pgx.ForEachRow(rows, []any{&table, &kind, &columns, &references, &refCols}, func() error {
		t := tables[table]
		if kind == 'p' {
			t.PrimaryKey = columns
		} else {
			t.ForeignKeys = append(t.ForeignKeys, snapshot.ForeignKey{
				Columns: columns, References: references, ReferencedColumns: refCols,
			})
		}
		tables[table] = t
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read the primary and foreign keys: %w", err)
	}

	rows, _ = conn.Query(ctx, postgresUniqueKeysQuery)
	_, err = pgx.ForEachRow(rows, []any{&table, &columns}, func() error {
		t := tables[table]
		t.UniqueKeys = append(t.UniqueKeys, columns)
		tables[table] = t
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read the unique keys: %w", err)
	}

	mergeUniqueKeys(tables)
	return tables, nil
}

const postgresTypesQuery = `
SELECT t.oid, ` + postgresTypeName + `
FROM pg_type t
` + postgresTypeJoins + `
WHERE t.oid = ANY($1)`

const postgresNotNullQuery = `
SELECT u.rel, u.num, a.attnotnull
FROM unnest($1::oid[], $2::int2[]) AS u(rel, num)
JOIN pg_attribute a ON a.attrelid = u.rel AND a.attnum = u.num`

// postgresStatement describes the statement sql, written with :name parameters. PostgreSQL tells whether a result
// column may be NULL only for one read straight from a table, and then as the table declares it.
func postgresStatement(ctx context.Context, conn *pgx.Conn, sql string) (snapshot.Statement, error) {
	text, names, err := placeholder.Rewrite(sql, snapshot.PostgreSQL)
	if err != nil {
		return snapshot.Statement{}, fmt.Errorf("%w", err)
	}
	desc, err := conn.Prepare(ctx, "", text)
	if err != nil {
		return snapshot.Statement{}, fmt.Errorf("prepare: %w", err)
	}

	oids := slices.Clone(desc.ParamOIDs)
	type attribute struct {
		rel uint32
		num uint16
	}
	var (
		rels []uint32
		nums []uint16
	)
	for _, f := range desc.Fields {
		oids = append(oids, f.DataTypeOID)
		if f.TableOID != 0 {
			rels = append(rels, f.TableOID)
			nums = append(nums, f.TableAttributeNumber)
		}
	}

	types := map[uint32]string{}
	var (
		oid  uint32
		name string
	)
	rows, _ := conn.Query(ctx, postgresTypesQuery, oids)
	if _, err := pgx.ForEachRow(rows, []any{&oid, &name}, func() error {
		types[oid] = name
		return nil
	}); err != nil {
		return snapshot.Statement{}, fmt.Errorf("read the types: %w", err)
	}

	notNull := map[attribute]bool{}
	var (
		attr attribute
		nn   bool
	)
	rows, _ = conn.Query(ctx, postgresNotNullQuery, rels, nums)
	if _, err := pgx.ForEachRow(rows, []any{&attr.rel, &attr.num, &nn}, func() error {
		notNull[attr] = nn
		return nil
	}); err != nil {
		return snapshot.Statement{}, fmt.Errorf("read which columns may be NULL: %w", err)
	}

	var st snapshot.Statement
	for i, oid := range desc.ParamOIDs {
		st.Params = append(st.Params, snapshot.Param{Name: names[i], Type: types[oid]})
	}
	for _, f := range desc.Fields {
		c := snapshot.Result{Name: f.Name, Type: types[f.DataTypeOID]}
		if nn, ok := notNull[attribute{f.TableOID, f.TableAttributeNumber}]; ok {
			c.NotNull, c.Nullable = nn, !nn
		}
		st.Columns = append(st.Columns, c)
	}
	return st, distinctNames(st.Columns)
}
