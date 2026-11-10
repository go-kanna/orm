package gen

// hooks writes kDatabase, which the repositories run their statements on: DBTX between the hooks of their options,
// with the time of their clock.
func (e repoEmitter) hooks() {
	ctx := e.f.Import("context", "context")
	db := e.database
	e.printf("// %s runs statements on dbtx between the hooks of options, as the methods of the repositories, and those\n"+
		"// written by hand, run them.\ntype %s struct { dbtx DBTX; options %s }\n\n", db, db, e.ref("Options"))
	e.printf("// Now is the time of the clock of options, which the repositories write the creation and update times of\n"+
		"// rows by, and those written by hand can.\n"+
		"func (d %s) Now(ctx %s.Context) %s.Time { return d.options.Now(ctx) }\n\n", db, ctx, e.f.Import("time", "time"))
	var result string
	if e.drv == driverSQL {
		result = e.f.Import("database/sql", "sql") + ".Result"
	} else {
		result = e.f.Import("github.com/jackc/pgx/v5/pgconn", "pgconn") + ".CommandTag"
	}
	e.printf("func (d %s) %s(ctx %s.Context, query string, args ...any) (%s, error) {\n", db, e.exec, ctx, result)
	e.printf("ctx, trace := d.options.StartQuery(ctx, query, args)\nresult,err := d.dbtx.%s(ctx,query,args...)\n", e.exec)
	e.printf("trace.End(err)\nreturn result,err\n}\n\n")
	if e.drv == driverSQL {
		e.sqlQueries(ctx)
		return
	}
	pgx := e.f.Import("github.com/jackc/pgx/v5", "pgx")
	e.printf("func (d %s) %s(ctx %s.Context, query string, args ...any) (%s.Rows, error) {\n", db, e.query, ctx, pgx)
	e.printf("ctx, trace := d.options.StartQuery(ctx,query,args)\nrows,err := d.dbtx.%s(ctx,query,args...)\n", e.query)
	e.printf("traced,err := trace.Rows(rows,err)\nif err != nil { return nil,err }\nif trace == nil { return rows,nil }\n"+
		"return %s{Rows:rows, traced:traced},nil\n}\n\n", e.rows)
	e.printf("// %[1]s is pgx.Rows read through traced, for the hooks to see the rows read to the end.\n"+
		"type %[1]s struct { %[2]s.Rows; traced %[3]s }\n"+
		"func (r %[1]s) Next() bool { return r.traced.Next() }\n"+
		"func (r %[1]s) Scan(dest ...any) error { return r.traced.Scan(dest...) }\n"+
		"func (r %[1]s) Close() { r.traced.Close() }\n\n", e.rows, pgx, e.ref("Rows"))
	e.printf("func (d %s) %s(ctx %s.Context, query string, args ...any) %s.Row {\n", db, e.queryRow, ctx, pgx)
	e.printf("ctx, trace := d.options.StartQuery(ctx,query,args)\nreturn trace.Row(d.dbtx.%s(ctx,query,args...))\n}\n\n",
		e.queryRow)
	if e.copies {
		e.printf("// CopyFrom copies the rows of rowSrc, which the hooks see as the statement it runs, with no arguments.\n")
		e.printf("func (d %[1]s) CopyFrom(ctx %[2]s.Context, tableName %[3]s.Identifier, columnNames []string, "+
			"rowSrc %[3]s.CopyFromSource) (int64, error) {\n", db, ctx, pgx)
		e.printf("ctx, trace := d.options.StartQuery(ctx, %s(tableName, columnNames), nil)\n"+
			"n, err := d.dbtx.CopyFrom(ctx, tableName, columnNames, rowSrc)\ntrace.End(err)\nreturn n, err\n}\n\n",
			e.ref("CopyFromSQL"))
	}
}

// sqlQueries writes the queries of kDatabase on database/sql: QueryContext and QueryRowContext, for methods written
// by hand, return *sql.Rows and *sql.Row as they are, which nothing can read through, and query and queryRow, for the
// repositories, read the rows through the hooks.
func (e repoEmitter) sqlQueries(ctx string) {
	db, sql := e.database, e.f.Import("database/sql", "sql")
	e.printf("// QueryContext and QueryRowContext return the rows of database/sql as they are, so the hooks see the " +
		"statement done\n// as these return, before its rows are read.\n")
	e.printf("func (d %[1]s) QueryContext(ctx %[2]s.Context, query string, args ...any) (*%[3]s.Rows, error) {\n"+
		"ctx, trace := d.options.StartQuery(ctx, query, args)\nrows, err := d.dbtx.QueryContext(ctx, query, args...)\n"+
		"trace.End(err)\nreturn rows, err\n}\n\n", db, ctx, sql)
	e.printf("func (d %[1]s) QueryRowContext(ctx %[2]s.Context, query string, args ...any) *%[3]s.Row {\n"+
		"ctx, trace := d.options.StartQuery(ctx, query, args)\nrow := d.dbtx.QueryRowContext(ctx, query, args...)\n"+
		"trace.End(row.Err())\nreturn row\n}\n\n", db, ctx, sql)
	e.printf("// %[1]s and %[2]s are QueryContext and QueryRowContext that read the rows through the hooks, for them to "+
		"see the\n// statement done once its rows are read.\n", e.query, e.queryRow)
	e.printf("func (d %s) %s(ctx %s.Context, query string, args ...any) (%s, error) {\n", db, e.query, ctx, e.ref("Rows"))
	e.printf("ctx, trace := d.options.StartQuery(ctx,query,args)\nrows,err := d.dbtx.QueryContext(ctx,query,args...)\n")
	e.printf("if err != nil { trace.End(err); return nil,err }\nreturn trace.Rows(%s{Rows:rows},nil)\n}\n\n", e.rows)
	e.printf("type %s struct { *%s.Rows }\nfunc (r %s) Close() { _ = r.Rows.Close() }\n\n", e.rows, sql, e.rows)
	e.printf("func (d %s) %s(ctx %s.Context, query string, args ...any) %s {\n", db, e.queryRow, ctx, e.ref("Row"))
	if e.row == "" {
		e.printf("ctx, trace := d.options.StartQuery(ctx,query,args)\n" +
			"return trace.Row(d.dbtx.QueryRowContext(ctx,query,args...))\n}\n\n")
		return
	}
	e.printf("ctx, trace := d.options.StartQuery(ctx,query,args)\n"+
		"return trace.Row(%s{Row: d.dbtx.QueryRowContext(ctx,query,args...)})\n}\n\n", e.row)
	scanError := e.ref("MySQLScanError")
	e.printf("// %[1]s and %[2]s tell of a time read without parseTime=true, which the DSN of go-sql-driver/mysql "+
		"needs.\n"+
		"func (r %[1]s) Scan(dest ...any) error { return %[3]s(r.Rows.Scan(dest...)) }\n\n"+
		"type %[2]s struct { *%[4]s.Row }\n"+
		"func (r %[2]s) Scan(dest ...any) error { return %[3]s(r.Row.Scan(dest...)) }\n\n",
		e.rows, e.row, scanError, sql)
}
