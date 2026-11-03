package gen

func (e repoEmitter) queryLogging() {
	ctx, orm := e.f.Import("context", "context"), e.ormPkg()
	db := e.database
	e.printf("type %s struct { db DBTX; options %s.Options }\n\n", db, orm)
	var result string
	if e.drv == driverSQL {
		result = e.f.Import("database/sql", "sql") + ".Result"
	} else {
		result = e.f.Import("github.com/jackc/pgx/v5/pgconn", "pgconn") + ".CommandTag"
	}
	e.printf("func (d %s) %s(ctx %s.Context, query string, args ...any) (%s, error) {\n", db, e.exec, ctx, result)
	e.printf("trace := d.options.StartQuery(ctx, query, args)\nresult,err := d.db.%s(ctx,query,args...)\n", e.exec)
	e.printf("trace.End(err)\nreturn result,err\n}\n\n")
	e.printf("func (d %s) %s(ctx %s.Context, query string, args ...any) (%s.Rows, error) {\n", db, e.query, ctx, orm)
	e.printf("trace := d.options.StartQuery(ctx,query,args)\nrows,err := d.db.%s(ctx,query,args...)\n", e.query)
	if e.drv == driverSQL {
		e.printf("if err != nil { trace.End(err); return nil,err }\nreturn trace.Rows(%s{Rows:rows},nil)\n}\n\n", e.sqlRows)
		e.printf("type %s struct { *%s.Rows }\nfunc (r %s) Close() { _ = r.Rows.Close() }\n\n",
			e.sqlRows, e.f.Import("database/sql", "sql"), e.sqlRows)
	} else {
		e.printf("return trace.Rows(rows,err)\n}\n\n")
	}
	e.printf("func (d %s) %s(ctx %s.Context, query string, args ...any) %s.Row {\n", db, e.queryRow, ctx, orm)
	e.printf("trace := d.options.StartQuery(ctx,query,args)\nreturn trace.Row(d.db.%s(ctx,query,args...))\n}\n\n",
		e.queryRow)
}
