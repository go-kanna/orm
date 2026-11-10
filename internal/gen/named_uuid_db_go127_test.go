//go:build go1.27

package gen_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-kanna/internal/cli"
)

// namedUUIDs keys orders by a type declared of uuid.UUID, which the drivers read and write as they do a uuid.UUID,
// and checks that a cursor holds one as the text of a uuid.
const namedUUIDs = `
-- store/store.go --
package store

import (
	"context"
	"uuid"
)

//kanna:scopes model-package=.

type OrderID uuid.UUID

type Order struct {
	ID    OrderID
	Lines []OrderLine
}

type OrderLine struct {
	OrderID OrderID
	SKU     string
}

//kanna:repository
type OrderRepository interface {
	Get(ctx context.Context, id OrderID, s ...OrderScope) (Order, error)
	List(ctx context.Context, s ...OrderScope) ([]Order, error)
	Insert(ctx context.Context, o Order) error
	InsertLines(ctx context.Context, lines []OrderLine) error
	Delete(ctx context.Context, id OrderID) error
}
-- store/store_test.go --
package store

import (
	"encoding/base64"
	"strings"
	"testing"
	"uuid"
)

func TestOrders(t *testing.T) {
	ctx := t.Context()
	repo := NewOrderRepository(open(t))
	first, second := OrderID(uuid.MustParse("00000000-0000-4000-8000-000000000001")),
		OrderID(uuid.MustParse("00000000-0000-4000-8000-000000000002"))
	for _, id := range []OrderID{second, first} {
		if err := repo.Insert(ctx, Order{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.InsertLines(ctx, []OrderLine{{OrderID: first, SKU: "a"}, {OrderID: second, SKU: "b"}}); err != nil {
		t.Fatal(err)
	}

	got, err := repo.Get(ctx, second, Orders().WithLines())
	if err != nil || got.ID != second || len(got.Lines) != 1 || got.Lines[0].OrderID != second {
		t.Errorf("Get = %+v, %v", got, err)
	}
	page, err := repo.List(ctx, Orders().IDIn(first, second).IDAsc().Limit(1))
	if err != nil || len(page) != 1 || page[0].ID != first {
		t.Fatalf("the first page = %+v, %v", page, err)
	}
	cursor, err := Orders().IDAsc().Cursor(page[0])
	if err != nil {
		t.Fatal(err)
	}
	if data, err := base64.RawURLEncoding.DecodeString(cursor); err != nil ||
		!strings.Contains(string(data), uuid.UUID(first).String()) {
		t.Errorf("the cursor holds %s, %v, not the text of %s", data, err, uuid.UUID(first))
	}
	after, err := Orders().IDAsc().AfterCursor(cursor)
	if err != nil {
		t.Fatal(err)
	}
	page, err = repo.List(ctx, after)
	if err != nil || len(page) != 1 || page[0].ID != second {
		t.Errorf("the page after the cursor = %+v, %v", page, err)
	}

	if err := repo.Delete(ctx, first); err != nil {
		t.Fatal(err)
	}
	if rest, err := repo.List(ctx, Orders()); err != nil || len(rest) != 1 || rest[0].ID != second {
		t.Errorf("the orders after Delete = %+v, %v", rest, err)
	}
}
`

const namedUUIDsSchema = `
CREATE TABLE orders (id uuid PRIMARY KEY);
CREATE TABLE order_lines (order_id uuid NOT NULL REFERENCES orders (id) ON DELETE CASCADE, sku text NOT NULL,
	PRIMARY KEY (order_id, sku));
`

// TestCommand_NamedUUIDsPostgres reads and writes orders keyed by a type declared of uuid.UUID through pgx and
// database/sql, and pages through them by a cursor.
func TestCommand_NamedUUIDsPostgres(t *testing.T) {
	t.Parallel()
	url := os.Getenv("KANNA_ORM_TEST_POSTGRES")
	if url == "" {
		t.Skip("integration database is not configured")
	}

	for _, driver := range []string{"pgx", "database/sql"} {
		t.Run(driver, func(t *testing.T) {
			t.Parallel()
			schema := fmt.Sprintf("kanna_uuids_%d", time.Now().UnixNano())
			db, err := sql.Open("pgx", url)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_, _ = db.ExecContext(context.WithoutCancel(t.Context()), "DROP SCHEMA "+schema+" CASCADE")
				_ = db.Close()
			})
			if _, err := db.ExecContext(t.Context(), "CREATE SCHEMA "+schema+"; SET search_path TO "+schema+";"+
				namedUUIDsSchema); err != nil {
				t.Fatal(err)
			}

			url := schemaURL(url, schema)
			open := `
-- store/open_test.go --
package store

import (
	"database/sql"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func open(t *testing.T) DBTX {
	db, err := sql.Open("pgx", "` + url + `")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
`
			if driver == "pgx" {
				open = `
-- store/open_test.go --
package store

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

func open(t *testing.T) DBTX {
	conn, err := pgx.Connect(t.Context(), "` + url + `")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}
`
			}
			dir := writeModuleGo(t, namedUUIDs+open, "1.27.0")
			describeRelationKeys(t, dir, url)
			code, _, errOut := run(t, filepath.Join(dir, "store"), "-destination", ".", "-driver", driver)
			if code != cli.ExitOK {
				t.Fatalf("kanna-orm = %d\n%s", code, errOut)
			}
			moduleCommand(t, dir, "test", "-mod=mod", "./...")
		})
	}
}
