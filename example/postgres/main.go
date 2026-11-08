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
	"github.com/go-kanna/orm/hook"
	"github.com/go-kanna/orm/option"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/go-kanna/orm/example/postgres/model"
	"github.com/go-kanna/orm/example/postgres/postgres"
	"github.com/go-kanna/orm/example/postgres/repository"
	"github.com/go-kanna/orm/example/postgres/scopes"
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
	queryLog := option.WithHook(hook.Hook{After: func(_ context.Context, e hook.Execution) {
		fmt.Fprintf(w, "SQL (%s): %s; error=%v\n", e.Duration, e.SQL, e.Err)
	}})
	ex := example{
		db:          pool,
		customers:   postgres.NewCustomer(pool),
		products:    postgres.NewProduct(pool),
		orders:      postgres.NewOrder(pool, queryLog),
		fulfillment: postgres.NewFulfillment(pool),
		w:           w,
	}
	for _, step := range []struct {
		name string
		do   func(context.Context) error
	}{
		{"write the catalog", ex.catalog},
		{"write the customers", ex.signUp},
		{"update selected columns", ex.updateSelectedColumns},
		{"place the orders", ex.placeOrders},
		{"read approvals", ex.readApprovals},
		{"read optional memos", ex.readMemos},
		{"read the open orders", ex.openOrders},
		{"read joined projections", ex.joinedSummaries},
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
	customers   postgres.Customer
	products    postgres.Product
	orders      postgres.Order
	fulfillment postgres.Fulfillment
	w           io.Writer
	ann, bob    model.Customer
}

// catalog writes many rows at once, in one statement: the products, then a price changed.
func (ex *example) catalog(ctx context.Context) error {
	catalog := []model.Product{
		{SKU: "TEA", Name: "Green tea", Price: 800, Currency: "USD"},
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
	if cheap[0].Currency != "JPY" {
		return fmt.Errorf("product currency: got %q, want the database default JPY", cheap[0].Currency)
	}
	fmt.Fprintf(ex.w, "under 1000: %s at %d %s\n", cheap[0].Name, cheap[0].Price, cheap[0].Currency)
	names, err := ex.products.ListNames(ctx, scopes.Products().PriceLt(2000).PriceAsc())
	if err != nil {
		return fmt.Errorf("the affordable product names: %w", err)
	}
	if strings.Join(names, ", ") != "Green tea, Tea cup" {
		return fmt.Errorf("affordable product names: got %v", names)
	}
	fmt.Fprintf(ex.w, "names under 2000: %s\n", strings.Join(names, ", "))
	summaries, err := ex.products.ListSummaries(ctx, scopes.Products().SKUAsc().PriceLt(2000).Limit(2))
	if err != nil {
		return fmt.Errorf("product summaries: %w", err)
	}
	if len(summaries) != 2 || summaries[0].Name != "Tea cup" || summaries[0].Price != 1200 ||
		summaries[1].Name != "Green tea" || summaries[1].Price != 900 {
		return fmt.Errorf("unexpected product summaries: %+v", summaries)
	}
	fmt.Fprintln(ex.w, "read product summaries with name and price, ordered by the omitted SKU")
	return nil
}

// signUp upserts by a natural key, the email address: a customer signing up anew as given, and one signed up
// already with their new name, and their phone when they give one. CreatedAt, written by an insert alone, keeps when
// they joined.
func (ex *example) signUp(ctx context.Context) error {
	joined := time.Date(2026, 1, 15, 9, 0, 0, 0, time.UTC)
	for _, c := range []model.Customer{
		{Email: "ann@example.com", Name: "Ann", Phone: new("03-1234-5678"), CreatedAt: joined},
		{Email: "ann@example.com", Name: "Ann Lee", Phone: new("090-1234-5678"), CreatedAt: time.Now()},
		{Email: "ann@example.com", Name: "Ann Lee", CreatedAt: time.Now()},
		{Email: "bob@example.com", Name: "Bob", CreatedAt: joined},
	} {
		changes := scopes.CustomerChanges().SetName(c.Name)
		if c.Phone != nil {
			changes = changes.SetPhone(c.Phone)
		}
		stored, err := ex.customers.UpsertByEmail(ctx, c, changes)
		if err != nil {
			return fmt.Errorf("%s: %w", c.Email, err)
		}
		if stored.Email == "ann@example.com" {
			ex.ann = stored
		} else {
			ex.bob = stored
		}
	}
	if ex.ann.Name != "Ann Lee" || ex.ann.Phone == nil || *ex.ann.Phone != "090-1234-5678" ||
		!ex.ann.CreatedAt.Equal(joined) {
		return errors.New("customer was not updated with the changes alone")
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
		memo := ""
		if p.shipped {
			memo = "Leave at reception"
		}
		o, err := ex.orders.Insert(ctx, model.Order{
			CustomerID: p.customer, Status: model.OrderOpen, PlacedAt: p.placed, Memo: memo,
		})
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
			if err := ex.orders.Tag(ctx, o.ID, repository.TagInput{Tag: tag}); err != nil {
				return fmt.Errorf("order %d: %w", o.ID, err)
			}
		}
	}
	return nil
}

// ship writes the shipment of o, and o as shipped.
func (ex *example) ship(ctx context.Context, fulfillment postgres.Fulfillment, o model.Order) error {
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
	found, err := ex.orders.Find(ctx, 1, scopes.Orders().WithCustomer().WithLines())
	if err != nil {
		return fmt.Errorf("find order 1: %w", err)
	}
	if found == nil || found.Customer == nil || len(found.Lines) == 0 {
		return errors.New("order 1 or its relations were not loaded")
	}
	missing, err := ex.orders.Find(ctx, 99)
	if err != nil {
		return fmt.Errorf("find order 99: %w", err)
	}
	if missing != nil {
		return errors.New("order 99 unexpectedly exists")
	}
	shipped, err := ex.orders.Get(ctx, 1, scopes.Orders().WithShipment())
	if err != nil {
		return fmt.Errorf("order 1: %w", err)
	}
	fmt.Fprintf(ex.w, "order 1 is %s by %s\n", shipped.Status, shipped.Shipment.Carrier)
	if _, err := ex.orders.Get(ctx, 99); !errors.Is(err, model.ErrOrderNotFound) {
		return fmt.Errorf("order 99: %w, not model.ErrOrderNotFound", err)
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
	fulfillment := postgres.NewFulfillment(tx)
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
	fulfillment := postgres.NewFulfillment(first)
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
	if _, err := postgres.NewFulfillment(second).FirstToShip(ctx, next); !errors.Is(err, orm.ErrNotFound) {
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
	deleted, err := ex.orders.DeleteSummaries(ctx, scopes.Orders().Open().
		PlacedAtLt(before).PlacedAtAsc().IDAsc().Limit(1))
	if err != nil {
		return fmt.Errorf("the open orders before March 5: %w", err)
	}
	fmt.Fprintf(ex.w, "deleted the open orders before March 5: %d (%v)\n", len(deleted), deleted)
	return nil
}

func (ex *example) readApprovals(ctx context.Context) error {
	orders, err := ex.orders.List(ctx, scopes.Orders().IDAsc().Limit(2))
	if err != nil {
		return fmt.Errorf("read approvals: %w", err)
	}
	if len(orders) != 2 {
		return errors.New("approval example needs two orders")
	}
	at := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	var approvals []model.Approval
	for _, order := range orders {
		for revision := int32(1); revision <= 5; revision++ {
			approvals = append(approvals, model.Approval{
				OrderID:    order.ID,
				Revision:   revision,
				Approver:   fmt.Sprintf("reviewer-%d", revision),
				ApprovedAt: at.Add(time.Duration(revision) * time.Hour),
			})
		}
	}
	copied, err := ex.orders.CopyApprovals(ctx, approvals)
	if err != nil {
		return fmt.Errorf("read approvals: %w", err)
	}
	if copied != int64(len(approvals)) {
		return fmt.Errorf("copied approvals: got %d, want %d", copied, len(approvals))
	}
	loaded, err := ex.orders.List(ctx, scopes.Orders().IDAsc().Limit(2).WithRecentApprovals())
	if err != nil {
		return fmt.Errorf("read approvals: %w", err)
	}
	for _, order := range loaded {
		if len(order.Approvals) != 3 || order.Approvals[0].Revision != 5 || order.Approvals[2].Revision != 3 {
			return fmt.Errorf("recent approvals: %+v", order)
		}
	}
	latest, err := ex.orders.Get(ctx, orders[0].ID, scopes.Orders().WithLatestApproval())
	if err != nil {
		return fmt.Errorf("read approvals: %w", err)
	}
	if len(latest.Approvals) != 1 || latest.Approvals[0].Approver != "reviewer-5" {
		return fmt.Errorf("latest approval: %+v", latest)
	}
	page := scopes.Orders().WithApprovals(scopes.Approvals().ApprovedAtDesc().IDDesc().Offset(1).Limit(2))
	offset, err := ex.orders.Get(ctx, orders[0].ID, page)
	if err != nil {
		return fmt.Errorf("read approvals: %w", err)
	}
	if len(offset.Approvals) != 2 || offset.Approvals[0].Revision != 4 || offset.Approvals[1].Revision != 3 {
		return fmt.Errorf("approval offset: %+v", offset)
	}
	ids := make([]model.OrderID, len(loaded))
	for i, order := range loaded {
		ids[i] = order.ID
	}
	approvers, err := ex.orders.ListApprovers(ctx, ids)
	if err != nil {
		return fmt.Errorf("read approvals: %w", err)
	}
	approverOf := make(map[model.OrderID]*string, len(approvers))
	for _, a := range approvers {
		approverOf[a.OrderID] = a.Approver
	}
	for _, order := range loaded {
		if current := approverOf[order.ID]; current == nil || *current != "reviewer-1" {
			return fmt.Errorf("current approver of order %d: %v", order.ID, current)
		}
	}
	fmt.Fprintln(ex.w, "current approver: reviewer-1; latest: reviewer-5; three approvals per order")
	return nil
}

func (ex *example) updateSelectedColumns(ctx context.Context) error {
	changes := scopes.CustomerChanges().SetPhone(new("050-1234-5678"))
	updated, err := ex.customers.UpdatePhone(ctx, ex.ann.ID, changes, scopes.Customers().PhoneIsNotNull())
	if err != nil {
		return fmt.Errorf("update customer phone: %w", err)
	}
	if updated.Phone == nil || *updated.Phone != "050-1234-5678" || updated.Name != ex.ann.Name ||
		updated.Email != ex.ann.Email || !updated.CreatedAt.Equal(ex.ann.CreatedAt) {
		return fmt.Errorf("partial update changed unexpected columns: %+v", updated)
	}
	cleared, err := ex.customers.UpdatePhone(ctx, ex.ann.ID, scopes.CustomerChanges().SetPhone(nil))
	if err != nil {
		return fmt.Errorf("clear customer phone: %w", err)
	}
	_, err = ex.customers.UpdatePhone(ctx, ex.ann.ID, changes, scopes.Customers().PhoneIsNotNull())
	if cleared.Phone != nil || !errors.Is(err, orm.ErrNotFound) {
		return fmt.Errorf("conditional update after NULL: %+v, %w", cleared, err)
	}
	fmt.Fprintln(ex.w, "updated only phone, cleared it with nil, and rejected a failed condition")
	return nil
}

func (ex *example) joinedSummaries(ctx context.Context) error {
	rows, err := ex.orders.ListSummaries(ctx, scopes.Orders().IDAsc().Limit(2))
	if err != nil {
		return fmt.Errorf("read order summaries: %w", err)
	}
	for _, row := range rows {
		fmt.Fprintf(ex.w, "order %d: customer %s, status %s\n", row.ID, row.Customer.Name, row.Status)
	}
	return nil
}
