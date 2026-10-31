package gen_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"testing"

	gomysql "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/go-kanna/orm"
	"github.com/go-kanna/orm/internal/gen/testdata/model"
	"github.com/go-kanna/orm/internal/gen/testdata/mysql"
	"github.com/go-kanna/orm/internal/gen/testdata/mysqlrepo"
	"github.com/go-kanna/orm/internal/gen/testdata/mysqlrepository"
	"github.com/go-kanna/orm/internal/gen/testdata/pgxrepo"
	"github.com/go-kanna/orm/internal/gen/testdata/postgres"
	"github.com/go-kanna/orm/internal/gen/testdata/repository"
)

// The codes the databases refuse a lock with: lock_not_available on PostgreSQL, for NOWAIT and for lock_timeout, and
// ER_LOCK_NOWAIT on MySQL.
const (
	pgLockNotAvailable = "55P03"
	mysqlLockNoWait    = 3572
)

func lockNotAvailable(err error) bool {
	if e, ok := errors.AsType[*pgconn.PgError](err); ok {
		return e.Code == pgLockNotAvailable
	}
	e, ok := errors.AsType[*gomysql.MySQLError](err)
	return ok && e.Number == mysqlLockNoWait
}

// pgxTx begins a transaction through pgx on a connection of its own to the database at url.
func pgxTx(t *testing.T, url string) pgx.Tx {
	t.Helper()

	tx, err := pgxConn(t, url).Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return tx
}

// pgLocked reports whether another transaction holds a lock that keeps conn from clause on the rows of from.
func pgLocked(t *testing.T, conn *pgx.Conn, from, clause string) bool {
	t.Helper()

	_, err := conn.Exec(t.Context(), "SELECT 1 FROM "+from+" "+clause+" NOWAIT")
	if err != nil && !lockNotAvailable(err) {
		t.Fatal(err)
	}
	return err != nil
}

func TestGeneratedRepository_PostgresLockStrengths(t *testing.T) {
	t.Parallel()

	const (
		keyShare = "FOR KEY SHARE"
		share    = "FOR SHARE"
		noKey    = "FOR NO KEY UPDATE"
		update   = "FOR UPDATE"
	)
	all := []string{keyShare, share, noKey, update}
	for _, tt := range []struct {
		name   string
		get    func(repository.OrderRepository, context.Context, model.OrderID) (model.Order, error)
		blocks []string
	}{
		{"key-share", repository.OrderRepository.GetForKeyShare, []string{update}},
		{"share", repository.OrderRepository.GetForShare, []string{noKey, update}},
		{"no-key-update", repository.OrderRepository.GetForNoKeyUpdate, []string{share, noKey, update}},
		{"update", repository.OrderRepository.GetForUpdate, all},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			url := postgresSchema(t)
			other := pgxConn(t, url)
			if o, err := tt.get(pgxrepo.NewOrderRepository(pgxTx(t, url)), t.Context(), 1); err != nil || o.ID != 1 {
				t.Fatalf("the row = %v, %v", o.ID, err)
			}
			for _, clause := range all {
				if got, want := pgLocked(t, other, "orders WHERE id = 1", clause), slices.Contains(tt.blocks, clause); got != want {
					t.Errorf("%s blocked = %v, want %v", clause, got, want)
				}
			}
		})
	}
}

func TestGeneratedRepository_PostgresLockEndsWithTheTransaction(t *testing.T) {
	t.Parallel()

	for name, end := range map[string]func(pgx.Tx, context.Context) error{
		"commit": pgx.Tx.Commit, "rollback": pgx.Tx.Rollback,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			url := postgresSchema(t)
			other, tx := pgxConn(t, url), pgxTx(t, url)
			if _, err := pgxrepo.NewOrderRepository(tx).GetForUpdate(t.Context(), 1); err != nil {
				t.Fatal(err)
			}
			if !pgLocked(t, other, "orders WHERE id = 1", "FOR UPDATE") {
				t.Fatal("the row is not locked")
			}
			if err := end(tx, t.Context()); err != nil {
				t.Fatal(err)
			}
			if pgLocked(t, other, "orders WHERE id = 1", "FOR UPDATE") {
				t.Errorf("the row is still locked after %s", name)
			}
		})
	}
}

func TestGeneratedRepository_PostgresLockWaits(t *testing.T) {
	t.Parallel()

	url := postgresSchema(t)
	ctx := t.Context()
	if _, err := pgxrepo.NewOrderRepository(pgxTx(t, url)).GetForUpdate(ctx, 1); err != nil {
		t.Fatal(err)
	}

	waiting := pgxTx(t, url)
	if _, err := waiting.Exec(ctx, "SET LOCAL lock_timeout = '100ms'"); err != nil {
		t.Fatal(err)
	}
	_, err := pgxrepo.NewOrderRepository(waiting).GetForUpdate(ctx, 1)
	if !lockNotAvailable(err) || errors.Is(err, orm.ErrNotFound) {
		t.Errorf("GetForUpdate of a row locked past the timeout error = %v, want %s", err, pgLockNotAvailable)
	}
}

func TestGeneratedRepository_PostgresLockNoRow(t *testing.T) {
	t.Parallel()

	_, err := pgxrepo.NewOrderRepository(pgxTx(t, postgresSchema(t))).GetForUpdate(t.Context(), 9)
	if !errors.Is(err, orm.ErrNotFound) {
		t.Errorf("GetForUpdate(9) error = %v, want orm.ErrNotFound", err)
	}
}

func TestGeneratedRepository_PostgresNoWait(t *testing.T) {
	t.Parallel()

	url := postgresSchema(t)
	ctx := t.Context()
	if _, err := pgxrepo.NewOrderRepository(pgxTx(t, url)).GetForUpdate(ctx, 1); err != nil {
		t.Fatal(err)
	}

	_, err := pgxrepo.NewOrderRepository(pgxTx(t, url)).GetNoWait(ctx, 1)
	if !lockNotAvailable(err) || errors.Is(err, orm.ErrNotFound) {
		t.Errorf("GetNoWait of a locked row error = %v, want %s", err, pgLockNotAvailable)
	}
	if o, err := pgxrepo.NewOrderRepository(pgxTx(t, url)).GetNoWait(ctx, 2); err != nil || o.ID != 2 {
		t.Errorf("GetNoWait of a free row = %v, %v", o.ID, err)
	}
}

func TestGeneratedRepository_PostgresSkipLocked(t *testing.T) {
	t.Parallel()

	url := postgresSchema(t)
	ctx := t.Context()
	holder := pgxrepo.NewOrderRepository(pgxTx(t, url))
	if _, err := holder.GetForUpdate(ctx, 1); err != nil {
		t.Fatal(err)
	}

	tx := pgxTx(t, url)
	repo := pgxrepo.NewOrderRepository(tx)
	orders := postgres.Orders().IDAsc()
	for _, tt := range []struct {
		name string
		s    postgres.Order
		want []model.OrderID
	}{
		{"the free rows", orders, []model.OrderID{2, 3}},
		{"some of them", orders.Limit(1), []model.OrderID{2}},
		{"those of a condition", orders.CustomerIDEq(10), []model.OrderID{2}},
		{"a page read backward", orders.Before(model.Order{ID: 3}), []model.OrderID{2}},
	} {
		if got, err := repo.ListUnlocked(ctx, tt.s); err != nil || !slices.Equal(ids(got), tt.want) {
			t.Errorf("ListUnlocked %s = %v, %v; want %v", tt.name, ids(got), err, tt.want)
		}
	}
	if o, err := repo.FirstUnlocked(ctx, orders); err != nil || o.ID != 2 {
		t.Errorf("FirstUnlocked = %v, %v; want 2", o.ID, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	// A row every transaction holds looks like one that is not there.
	for _, id := range []model.OrderID{2, 3} {
		if _, err := holder.GetForUpdate(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	repo = pgxrepo.NewOrderRepository(pgxTx(t, url))
	if _, err := repo.FirstUnlocked(ctx, orders); !errors.Is(err, orm.ErrNotFound) {
		t.Errorf("FirstUnlocked with every row locked error = %v, want orm.ErrNotFound", err)
	}
	if got, err := repo.ListUnlocked(ctx); err != nil || got == nil || len(got) != 0 {
		t.Errorf("ListUnlocked with every row locked = %v, %v; want none", got, err)
	}
	if n, err := repo.Count(ctx); err != nil || n != 3 {
		t.Errorf("Count = %d, %v; want 3", n, err)
	}
}

func TestGeneratedRepository_PostgresLockLeavesRelations(t *testing.T) {
	t.Parallel()

	url := postgresSchema(t)
	other := pgxConn(t, url)
	repo := pgxrepo.NewOrderRepository(pgxTx(t, url))
	o, err := repo.GetNoWait(t.Context(), 1, postgres.Orders().WithLines().WithTags())
	if err != nil || len(o.Lines) != 2 || len(o.Tags) != 2 {
		t.Fatalf("GetNoWait with relations = %+v, %v", o, err)
	}
	if !pgLocked(t, other, "orders WHERE id = 1", "FOR UPDATE") {
		t.Error("the row is not locked")
	}
	for _, from := range []string{"order_lines WHERE order_id = 1", "order_tags WHERE order_id = 1", "tags"} {
		if pgLocked(t, other, from, "FOR UPDATE") {
			t.Errorf("%s is locked", from)
		}
	}
}

func TestGeneratedRepository_PostgresLockOutsideATransaction(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	url := postgresSchema(t)
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	const want = "orm: ListUnlocked needs a transaction to hold its lock: give the repository a pgx.Tx"
	for name, db := range map[string]pgxrepo.DBTX{"a connection": pgxConn(t, url), "a pool": pool} {
		repo := pgxrepo.NewOrderRepository(db)
		if _, err := repo.ListUnlocked(ctx); err == nil || err.Error() != want {
			t.Errorf("ListUnlocked on %s error = %v, want %q", name, err, want)
		}
		if _, err := repo.GetForUpdate(ctx, 1); err == nil {
			t.Errorf("GetForUpdate on %s: no error", name)
		}
		if _, err := repo.Get(ctx, 1); err != nil {
			t.Errorf("Get on %s: %v", name, err)
		}
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := pgxrepo.NewOrderRepository(tx).GetForUpdate(ctx, 1); err != nil {
		t.Errorf("GetForUpdate in a transaction of the pool: %v", err)
	}
}

// mysqlTx begins a transaction on db, which ends with the test if it has not by then.
func mysqlTx(t *testing.T, db *sql.DB) *sql.Tx {
	t.Helper()

	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

// mysqlLocked reports whether another transaction holds a lock that keeps db from clause on the rows of from.
func mysqlLocked(t *testing.T, db *sql.DB, from, clause string) bool {
	t.Helper()

	_, err := db.ExecContext(t.Context(), "SELECT 1 FROM "+from+" "+clause+" NOWAIT") //nolint:gosec // the test's own SQL
	if err != nil && !lockNotAvailable(err) {
		t.Fatal(err)
	}
	return err != nil
}

func TestGeneratedRepository_MySQLLockStrengths(t *testing.T) {
	t.Parallel()

	const (
		share  = "FOR SHARE"
		update = "FOR UPDATE"
	)
	for _, tt := range []struct {
		name   string
		get    func(mysqlrepository.OrderRepository, context.Context, model.OrderID) (model.Order, error)
		blocks []string
	}{
		{"share", mysqlrepository.OrderRepository.GetForShare, []string{update}},
		{"update", mysqlrepository.OrderRepository.GetForUpdate, []string{share, update}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := mysqlDB(t)
			if o, err := tt.get(mysqlrepo.NewOrderRepository(mysqlTx(t, db)), t.Context(), 1); err != nil || o.ID != 1 {
				t.Fatalf("the row = %v, %v", o.ID, err)
			}
			for _, clause := range []string{share, update} {
				if got, want := mysqlLocked(t, db, "orders WHERE id = 1", clause), slices.Contains(tt.blocks, clause); got != want {
					t.Errorf("%s blocked = %v, want %v", clause, got, want)
				}
			}
		})
	}
}

func TestGeneratedRepository_MySQLLockEndsWithTheTransaction(t *testing.T) {
	t.Parallel()

	for name, end := range map[string]func(*sql.Tx) error{"commit": (*sql.Tx).Commit, "rollback": (*sql.Tx).Rollback} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			db := mysqlDB(t)
			tx := mysqlTx(t, db)
			if _, err := mysqlrepo.NewOrderRepository(tx).GetForUpdate(t.Context(), 1); err != nil {
				t.Fatal(err)
			}
			if !mysqlLocked(t, db, "orders WHERE id = 1", "FOR UPDATE") {
				t.Fatal("the row is not locked")
			}
			if err := end(tx); err != nil {
				t.Fatal(err)
			}
			if mysqlLocked(t, db, "orders WHERE id = 1", "FOR UPDATE") {
				t.Errorf("the row is still locked after %s", name)
			}
		})
	}
}

func TestGeneratedRepository_MySQLNoWait(t *testing.T) {
	t.Parallel()

	db := mysqlDB(t)
	ctx := t.Context()
	if _, err := mysqlrepo.NewOrderRepository(mysqlTx(t, db)).GetForUpdate(ctx, 1); err != nil {
		t.Fatal(err)
	}

	repo := mysqlrepo.NewOrderRepository(mysqlTx(t, db))
	if _, err := repo.GetNoWait(ctx, 1); !lockNotAvailable(err) || errors.Is(err, orm.ErrNotFound) {
		t.Errorf("GetNoWait of a locked row error = %v, want %d", err, mysqlLockNoWait)
	}
	if o, err := repo.GetNoWait(ctx, 2); err != nil || o.ID != 2 {
		t.Errorf("GetNoWait of a free row = %v, %v", o.ID, err)
	}
}

func TestGeneratedRepository_MySQLSkipLocked(t *testing.T) {
	t.Parallel()

	db := mysqlDB(t)
	ctx := t.Context()
	holder := mysqlrepo.NewOrderRepository(mysqlTx(t, db))
	if _, err := holder.GetForUpdate(ctx, 1); err != nil {
		t.Fatal(err)
	}

	tx := mysqlTx(t, db)
	repo := mysqlrepo.NewOrderRepository(tx)
	orders := mysql.Orders().IDAsc()
	if got, err := repo.ListUnlocked(ctx, orders); err != nil || !slices.Equal(ids(got), []model.OrderID{2, 3}) {
		t.Errorf("ListUnlocked = %v, %v; want the free rows", ids(got), err)
	}
	if got, err := repo.ListUnlocked(ctx, orders.Limit(1)); err != nil || !slices.Equal(ids(got), []model.OrderID{2}) {
		t.Errorf("ListUnlocked of one = %v, %v; want 2", ids(got), err)
	}
	if o, err := repo.FirstUnlocked(ctx, orders); err != nil || o.ID != 2 {
		t.Errorf("FirstUnlocked = %v, %v; want 2", o.ID, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	// A row every transaction holds looks like one that is not there.
	for _, id := range []model.OrderID{2, 3} {
		if _, err := holder.GetForUpdate(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	repo = mysqlrepo.NewOrderRepository(mysqlTx(t, db))
	if _, err := repo.FirstUnlocked(ctx, orders); !errors.Is(err, orm.ErrNotFound) {
		t.Errorf("FirstUnlocked with every row locked error = %v, want orm.ErrNotFound", err)
	}
	if n, err := repo.Count(ctx); err != nil || n != 3 {
		t.Errorf("Count = %d, %v; want 3", n, err)
	}
}

func TestGeneratedRepository_MySQLLockLeavesRelations(t *testing.T) {
	t.Parallel()

	db := mysqlDB(t)
	repo := mysqlrepo.NewOrderRepository(mysqlTx(t, db))
	o, err := repo.GetNoWait(t.Context(), 1, mysql.Orders().WithLines().WithTags())
	if err != nil || len(o.Lines) != 2 || len(o.Tags) != 2 {
		t.Fatalf("GetNoWait with relations = %+v, %v", o, err)
	}
	if !mysqlLocked(t, db, "orders WHERE id = 1", "FOR UPDATE") {
		t.Error("the row is not locked")
	}
	for _, from := range []string{"order_lines WHERE order_id = 1", "order_tags WHERE order_id = 1", "tags"} {
		if mysqlLocked(t, db, from, "FOR UPDATE") {
			t.Errorf("%s is locked", from)
		}
	}
}

func TestGeneratedRepository_MySQLLockOutsideATransaction(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := mysqlDB(t)
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	const want = "orm: ListUnlocked needs a transaction to hold its lock: give the repository a *sql.Tx"
	for name, on := range map[string]mysqlrepo.DBTX{"a pool": db, "a connection": conn} {
		repo := mysqlrepo.NewOrderRepository(on)
		if _, err := repo.ListUnlocked(ctx); err == nil || err.Error() != want {
			t.Errorf("ListUnlocked on %s error = %v, want %q", name, err, want)
		}
		if _, err := repo.GetForUpdate(ctx, 1); err == nil {
			t.Errorf("GetForUpdate on %s: no error", name)
		}
		if _, err := repo.Get(ctx, 1); err != nil {
			t.Errorf("Get on %s: %v", name, err)
		}
	}
}
