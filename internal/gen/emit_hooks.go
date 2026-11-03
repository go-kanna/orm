package gen

// hooks writes ormDatabase, which the repositories run their statements on: DBTX between the hooks of their options.
func (e repoEmitter) hooks() {
	ctx := e.f.Import("context", "context")
	db := e.database
	e.printf("// %s runs statements on dbtx between the hooks of options, as the methods of the repositories, and those\n"+
		"// written by hand, run them.\ntype %s struct { dbtx DBTX; options %s }\n\n", db, db, e.ref("Options"))
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
		e.printf("func (d %s) %s(ctx %s.Context, query string, args ...any) (%s, error) {\n", db, e.query, ctx,
			e.ref("Rows"))
		e.printf("ctx, trace := d.options.StartQuery(ctx,query,args)\nrows,err := d.dbtx.%s(ctx,query,args...)\n", e.query)
		e.printf("if err != nil { trace.End(err); return nil,err }\nreturn trace.Rows(%s{Rows:rows},nil)\n}\n\n", e.rows)
		e.printf("type %s struct { *%s.Rows }\nfunc (r %s) Close() { _ = r.Rows.Close() }\n\n",
			e.rows, e.f.Import("database/sql", "sql"), e.rows)
		e.printf("func (d %s) %s(ctx %s.Context, query string, args ...any) %s {\n", db, e.queryRow, ctx, e.ref("Row"))
	} else {
		pgx := e.f.Import("github.com/jackc/pgx/v5", "pgx")
		e.printf("func (d %s) %s(ctx %s.Context, query string, args ...any) (%s.Rows, error) {\n", db, e.query, ctx, pgx)
		e.printf("ctx, trace := d.options.StartQuery(ctx,query,args)\nrows,err := d.dbtx.%s(ctx,query,args...)\n", e.query)
		e.printf("traced,err := trace.Rows(rows,err)\nif err != nil { return nil,err }\n"+
			"return %s{Rows:rows, traced:traced},nil\n}\n\n", e.rows)
		e.printf("// %[1]s is pgx.Rows read through traced, for the hooks to see the rows read to the end.\n"+
			"type %[1]s struct { %[2]s.Rows; traced %[3]s }\n"+
			"func (r %[1]s) Next() bool { return r.traced.Next() }\n"+
			"func (r %[1]s) Scan(dest ...any) error { return r.traced.Scan(dest...) }\n"+
			"func (r %[1]s) Close() { r.traced.Close() }\n\n", e.rows, pgx, e.ref("Rows"))
		e.printf("func (d %s) %s(ctx %s.Context, query string, args ...any) %s.Row {\n", db, e.queryRow, ctx, pgx)
	}
	e.printf("ctx, trace := d.options.StartQuery(ctx,query,args)\nreturn trace.Row(d.dbtx.%s(ctx,query,args...))\n}\n\n",
		e.queryRow)
}
