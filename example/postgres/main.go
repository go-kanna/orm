// Command postgres runs the example on the PostgreSQL database its argument names, whose tables migrate has created:
// it writes a catalog, customers, and their orders through the repositories kanna-orm implements in store, and reads
// them back through the scopes it generates in scopes.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/go-kanna/orm"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/go-kanna/orm/example/postgres/model"
	"github.com/go-kanna/orm/example/postgres/scopes"
	"github.com/go-kanna/orm/example/postgres/store"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: postgres <database URL>")
		os.Exit(2)
	}
	if err := run(context.Background(), os.Args[1], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "postgres:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, url string, w io.Writer) error {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer pool.Close()
	ex := example{
		db:          pool,
		customers:   store.NewCustomerRepository(pool),
		products:    store.NewProductRepository(pool),
		orders:      store.NewOrderRepository(pool),
		fulfillment: store.NewFulfillmentRepository(pool),
		w:           w,
	}
	for _, step := range []struct {
		name string
		do   func(context.Context) error
	}{
		{"write the catalog", ex.catalog},
		{"write the customers", ex.signUp},
		{"place the orders", ex.placeOrders},
		{"read the open orders", ex.openOrders},
		{"page through the orders", ex.page},
		{"report", ex.report},
		{"cancel an order", ex.cancel},
		{"delete the stale orders", ex.deleteStale},
		{"ship the next order", ex.shipNext},
	} {
		if err := step.do(ctx); err != nil {
			return fmt.Errorf("%s: %w", step.name, err)
		}
	}
	return nil
}

// example is what each step of the example runs on, and the customers it has written.
type example struct {
	db          *pgxpool.Pool
	customers   store.CustomerRepository
	products    store.ProductRepository
	orders      store.OrderRepository
	fulfillment store.FulfillmentRepository
	w           io.Writer
	ann, bob    model.Customer
}

// catalog writes many rows at once, in one statement: the products, then a price changed.
func (ex *example) catalog(ctx context.Context) error {
	catalog := []model.Product{
		{SKU: "TEA", Name: "Green tea", Price: 800},
		{SKU: "CUP", Name: "Tea cup", Price: 1200},
		{SKU: "POT", Name: "Teapot", Price: 4500},
	}
	if err := ex.products.UpsertAll(ctx, catalog); err != nil {
		return fmt.Errorf("the products: %w", err)
	}
	catalog[0].Price = 900
	if err := ex.products.UpsertAll(ctx, catalog[:1]); err != nil {
		return fmt.Errorf("a price: %w", err)
	}
	if err := ex.products.InsertTags(ctx, []model.Tag{{Name: "gift"}, {Name: "rush"}}); err != nil {
		return fmt.Errorf("the tags: %w", err)
	}
	cheap, err := ex.products.List(ctx, scopes.Products().PriceLt(1000))
	if err != nil {
		return fmt.Errorf("the cheap products: %w", err)
	}
	fmt.Fprintf(ex.w, "under 1000: %s at %d\n", cheap[0].Name, cheap[0].Price)
	return nil
}

// signUp upserts by a natural key, the email address, keeping when the customer joined.
func (ex *example) signUp(ctx context.Context) error {
	joined := time.Date(2026, 1, 15, 9, 0, 0, 0, time.UTC)
	for _, c := range []model.Customer{
		{Email: "ann@example.com", Name: "Ann", CreatedAt: joined},
		{Email: "ann@example.com", Name: "Ann Lee", CreatedAt: time.Now()},
		{Email: "bob@example.com", Name: "Bob", CreatedAt: joined},
	} {
		stored, err := ex.customers.UpsertByEmail(ctx, c)
		if err != nil {
			return fmt.Errorf("%s: %w", c.Email, err)
		}
		if stored.Email == "ann@example.com" {
			ex.ann = stored
		} else {
			ex.bob = stored
		}
	}
	fmt.Fprintf(ex.w, "customer %d: %s, joined %s\n", ex.ann.ID, ex.ann.Name,
		ex.ann.CreatedAt.UTC().Format(time.DateOnly))
	return nil
}

// placeOrders writes orders, each with its lines at once, and some with a shipment and tags.
func (ex *example) placeOrders(ctx context.Context) error {
	for _, p := range []struct {
		customer model.CustomerID
		placed   time.Time
		lines    map[model.SKU]int
		shipped  bool
		tags     []string
	}{
		{ex.ann.ID, time.Date(2026, 2, 3, 10, 0, 0, 0, time.UTC), map[model.SKU]int{"TEA": 2, "CUP": 1}, true, nil},
		{ex.bob.ID, time.Date(2026, 2, 14, 12, 0, 0, 0, time.UTC), map[model.SKU]int{"POT": 1}, false,
			[]string{"rush", "gift"}},
		{ex.ann.ID, time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC), map[model.SKU]int{"TEA": 5}, false, []string{"rush"}},
		{ex.bob.ID, time.Date(2026, 3, 9, 18, 0, 0, 0, time.UTC), map[model.SKU]int{"CUP": 4}, false, nil},
	} {
		o, err := ex.orders.Insert(ctx, model.Order{CustomerID: p.customer, Status: model.OrderOpen, PlacedAt: p.placed})
		if err != nil {
			return fmt.Errorf("an order: %w", err)
		}
		lines := make([]model.OrderLine, 0, len(p.lines))
		for _, sku := range []model.SKU{"TEA", "CUP", "POT"} {
			if quantity, ok := p.lines[sku]; ok {
				lines = append(lines, model.OrderLine{OrderID: o.ID, LineNo: len(lines) + 1, SKU: sku, Quantity: quantity})
			}
		}
		if err := ex.orders.InsertLines(ctx, lines); err != nil {
			return fmt.Errorf("the lines of order %d: %w", o.ID, err)
		}
		if p.shipped {
			if err := ex.ship(ctx, ex.fulfillment, o); err != nil {
				return fmt.Errorf("order %d: %w", o.ID, err)
			}
		}
		for _, tag := range p.tags {
			if err := ex.orders.Tag(ctx, o.ID, tag); err != nil {
				return fmt.Errorf("order %d: %w", o.ID, err)
			}
		}
	}
	return nil
}

// ship writes the shipment of o, and o as shipped.
func (ex *example) ship(ctx context.Context, fulfillment store.FulfillmentRepository, o model.Order) error {
	s := model.Shipment{OrderID: o.ID, Carrier: "Kanna Post", Tracking: fmt.Sprintf("KP%06d", o.ID)}
	if err := fulfillment.InsertShipment(ctx, s); err != nil {
		return fmt.Errorf("the shipment: %w", err)
	}
	o.Status = model.OrderShipped
	if err := fulfillment.Update(ctx, o); err != nil {
		return fmt.Errorf("the status: %w", err)
	}
	return nil
}

// openOrders reads the relations a scope asks for, each by one query, or two through the table between.
func (ex *example) openOrders(ctx context.Context) error {
	open, err := ex.orders.List(ctx, scopes.Orders().Open().Latest().
		WithCustomer().
		WithLines(scopes.OrderLines().LineNoAsc().WithProduct()).
		WithShipment().
		WithTags(scopes.Tags().NameAsc()))
	if err != nil {
		return fmt.Errorf("the list: %w", err)
	}
	for _, o := range open {
		var lines strings.Builder
		for _, l := range o.Lines {
			fmt.Fprintf(&lines, " %d x %s", l.Quantity, l.Product.Name)
		}
		tags := make([]string, 0, len(o.Tags))
		for _, t := range o.Tags {
			tags = append(tags, t.Name)
		}
		fmt.Fprintf(ex.w, "open order %d of %s:%s, tagged %v\n", o.ID, o.Customer.Name, lines.String(), tags)
	}
	shipped, err := ex.orders.Get(ctx, 1, scopes.Orders().WithShipment())
	if err != nil {
		return fmt.Errorf("order 1: %w", err)
	}
	fmt.Fprintf(ex.w, "order 1 is %s by %s\n", shipped.Status, shipped.Shipment.Carrier)
	if _, err := ex.orders.Get(ctx, 99); !errors.Is(err, orm.ErrNotFound) {
		return fmt.Errorf("order 99: %w, not orm.ErrNotFound", err)
	}
	return nil
}

// page goes through the orders a page at a time, by their keys, with a cursor a client would hand back.
func (ex *example) page(ctx context.Context) error {
	page := scopes.Orders().Latest().Limit(3)
	cursor := ""
	for n := 1; ; n++ {
		after, err := page.AfterCursor(cursor)
		if err != nil {
			return fmt.Errorf("the cursor: %w", err)
		}
		rows, err := ex.orders.List(ctx, after)
		if err != nil {
			return fmt.Errorf("page %d: %w", n, err)
		}
		if len(rows) == 0 {
			return nil
		}
		ids := make([]string, 0, len(rows))
		for _, o := range rows {
			ids = append(ids, fmt.Sprint(o.ID))
		}
		fmt.Fprintf(ex.w, "page %d: %s\n", n, strings.Join(ids, ", "))
		if cursor, err = page.Cursor(rows[len(rows)-1]); err != nil {
			return fmt.Errorf("the cursor: %w", err)
		}
	}
}

// report counts by a scope written in SQL, and sums by a query written by hand.
func (ex *example) report(ctx context.Context) error {
	february, err := ex.orders.Count(ctx, scopes.Orders().PlacedInMonth(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)))
	if err != nil {
		return fmt.Errorf("the orders of February: %w", err)
	}
	fmt.Fprintf(ex.w, "placed in February: %d\n", february)
	sales, err := ex.orders.MonthlySales(ctx, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		return fmt.Errorf("the sales: %w", err)
	}
	for _, s := range sales {
		fmt.Fprintf(ex.w, "%s: %d orders, %d\n", s.Month.UTC().Format("2006-01"), s.Orders, s.Revenue)
	}
	return nil
}

// cancel cancels an order that is still open. The lock keeps other transactions off it between the check and the write.
func (ex *example) cancel(ctx context.Context) error {
	tx, err := ex.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	fulfillment := store.NewFulfillmentRepository(tx)
	o, err := fulfillment.GetForUpdate(ctx, 3)
	if err != nil {
		return fmt.Errorf("order 3: %w", err)
	}
	if o.Status != model.OrderOpen {
		return fmt.Errorf("order 3 is %s", o.Status)
	}
	o.Status = model.OrderCanceled
	if err := fulfillment.Update(ctx, o); err != nil {
		return fmt.Errorf("order 3: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	fmt.Fprintf(ex.w, "order %d is %s\n", o.ID, o.Status)
	return nil
}

// shipNext has two workers each take the oldest open order, in a transaction of its own. The first holds it until it
// commits, so the second passes it over, and finds none left.
func (ex *example) shipNext(ctx context.Context) error {
	next := scopes.Orders().Open().PlacedAtAsc()

	first, err := ex.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = first.Rollback(ctx) }()
	fulfillment := store.NewFulfillmentRepository(first)
	o, err := fulfillment.FirstToShip(ctx, next)
	if err != nil {
		return fmt.Errorf("worker 1: %w", err)
	}
	fmt.Fprintf(ex.w, "worker 1 takes order %d\n", o.ID)

	second, err := ex.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = second.Rollback(ctx) }()
	if _, err := store.NewFulfillmentRepository(second).FirstToShip(ctx, next); !errors.Is(err, orm.ErrNotFound) {
		return fmt.Errorf("worker 2: %w, not orm.ErrNotFound", err)
	}
	fmt.Fprintln(ex.w, "worker 2 finds no order to take")

	if err := ex.ship(ctx, fulfillment, o); err != nil {
		return fmt.Errorf("order %d: %w", o.ID, err)
	}
	if err := first.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// deleteStale deletes the rows a scope matches.
func (ex *example) deleteStale(ctx context.Context) error {
	before := time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC)
	deleted, err := ex.orders.DeleteAll(ctx, scopes.Orders().Open().PlacedAtLt(before))
	if err != nil {
		return fmt.Errorf("the open orders before March 5: %w", err)
	}
	fmt.Fprintf(ex.w, "deleted the open orders before March 5: %d\n", deleted)
	return nil
}
