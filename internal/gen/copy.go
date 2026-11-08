package gen

import (
	"github.com/go-kanna/internal/diag"

	"github.com/go-kanna/orm/internal/snapshot"
)

// copyDriver reports why m, a Copy, which writes its rows by CopyFrom of pgx, cannot be written for the database and
// the driver of the run, if it cannot.
func (r repoReader) copyDriver(m repoMethod) []diag.Diag {
	insert := "write the rows by an Insert of many rows: Insert(ctx context.Context, rows []model.Order) error"
	switch {
	case r.s.Dialect == snapshot.MySQL:
		return []diag.Diag{diag.Errorf(m.pos, "%s: MySQL has no COPY, which a Copy writes its rows by", m.name).
			WithHints(insert)}
	case r.driver != driverPgx:
		return []diag.Diag{diag.Errorf(m.pos, "%s: %s has no CopyFrom of pgx, which a Copy writes its rows by", m.name,
			r.driver).WithHints("generate the repositories on pgx: -driver pgx", "or "+insert)}
	}
	return nil
}

// copyBinds reports each Bind of repos that gives the implementation what cannot copy rows, when kanna-orm writes a
// method copying them for one of repos, as the DBTX of their destination then copies them.
func (r repoReader) copyBinds(repos []repository) []diag.Diag {
	copier := ""
	for _, repo := range repos {
		for _, m := range repo.methods {
			if copier == "" && m.verb == verbCopy {
				copier = repo.name + "." + m.fn.Name()
			}
		}
	}
	if copier == "" || r.driver != driverPgx {
		return nil
	}
	var ds []diag.Diag
	for _, repo := range repos {
		for _, m := range repo.methods {
			if m.bind == nil || m.bind.localDBTX || acceptsDatabase(m.bind.db, r.imported, r.driver, true) {
				continue
			}
			what, fix := "its argument", "take"
			if m.bind.accessor {
				what, fix = "what DBTX() of its argument returns", "have DBTX() return"
			}
			ds = append(ds, diag.Errorf(m.pos, "Bind: %s has no CopyFrom, which the DBTX kanna-orm writes has, as %s "+
				"copies rows by it", what, copier).WithHints(fix+" CopyDBTX of github.com/go-kanna/orm/pgx, which "+
				"*pgxpool.Pool, *pgx.Conn, and pgx.Tx implement"))
		}
	}
	return ds
}
