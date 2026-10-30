package orm

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// MayBeNull stands, among the values of a row After and Cursor are given, for that of a column which may be NULL. The
// databases put NULL in different places in an order, so a page cannot continue by such a column.
var MayBeNull any = mayBeNull{}

type mayBeNull struct{}

// seek is the row a page continues from: the values of its columns, and the primary key the order ends in.
type seek struct {
	key      []string
	values   map[string]any
	backward bool
}

// After narrows to the rows that come after a row in the order of q, so that a page continues from the last row of the
// one before. values holds the row's value in each column of its table, and key names the columns of its primary key.
// When the order does not end in the primary key, After adds it, so that rows equal in the order still come in one.
func (q Of[M]) After(key []string, values map[string]any) Of[M] {
	q.seek, q.seeking = seek{key: key, values: values}, true
	return q
}

// Before is After the other way, for the page before the one a row is the first of. The query reads that page in the
// reverse order, and Rendered.Reversed says so, for its rows to be turned around.
func (q Of[M]) Before(key []string, values map[string]any) Of[M] {
	q.seek, q.seeking = seek{key: key, values: values, backward: true}, true
	return q
}

// cursor is what a cursor encodes: the order it was made in, and the row's values in that order.
type cursor struct {
	Order  []string          `json:"order"`
	Values []json.RawMessage `json:"values"`
}

var errNotACursor = errors.New("orm: the cursor is not one kanna-orm made")

// Cursor encodes the position of a row in the order of q, for a client to hand back for the page after it. values and
// key are as After takes them.
func (q Of[M]) Cursor(key []string, values map[string]any) (string, error) {
	orders := q.ordersWith(key)
	c := cursor{Order: describeOrders(orders)}
	for _, o := range orders {
		v, err := valueOf(values, o.column)
		if err != nil {
			return "", err
		}
		data, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("orm: encode %s in a cursor: %w", o.column, err)
		}
		c.Values = append(c.Values, data)
	}
	data, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("orm: encode a cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

// DecodeCursor decodes the values a cursor holds into dest, which has a pointer for each column of the table, so that
// the row can be given to After. A cursor made in another order than that of q is an error.
func (q Of[M]) DecodeCursor(token string, key []string, dest map[string]any) error {
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return errNotACursor
	}
	var c cursor
	if err := json.Unmarshal(data, &c); err != nil {
		return errNotACursor
	}
	orders := q.ordersWith(key)
	if !slices.Equal(c.Order, describeOrders(orders)) {
		return fmt.Errorf("orm: the cursor was made in another order: %v", c.Order)
	}
	if len(c.Values) != len(orders) {
		return errNotACursor
	}
	// Cursor never writes null, as a page cannot continue by a column that may be NULL, and null would leave the
	// value in dest as it was, unchecked by a value type that rejects it.
	if slices.ContainsFunc(c.Values, func(v json.RawMessage) bool { return string(bytes.TrimSpace(v)) == "null" }) {
		return errNotACursor
	}
	for i, o := range orders {
		p, ok := dest[o.column]
		if !ok {
			return fmt.Errorf("orm: the cursor has a value of %s, which the row has nowhere to keep", o.column)
		}
		if err := json.Unmarshal(c.Values[i], p); err != nil {
			return fmt.Errorf("orm: decode %s of the cursor: %w", o.column, err)
		}
	}
	return nil
}

// ordersWith returns the order of q ending in the primary key key, in the direction of the order's last column.
func (q Of[M]) ordersWith(key []string) []order {
	orders := slices.Clone(q.orders)
	desc := len(orders) > 0 && orders[len(orders)-1].desc
	for _, k := range key {
		if !slices.ContainsFunc(orders, func(o order) bool { return o.column == k }) {
			orders = append(orders, order{k, desc})
		}
	}
	return orders
}

// cond is the condition a page continues by. Where every column of the order goes one way, PostgreSQL compares
// rows; otherwise, and on MySQL, the comparison is spelled out a column at a time.
func (s seek) cond(orders []order, d Dialect) (cond, error) {
	values := make([]any, len(orders))
	for i, o := range orders {
		v, err := valueOf(s.values, o.column)
		if err != nil {
			return cond{}, err
		}
		values[i] = v
	}
	op := func(desc bool) string {
		if desc != s.backward {
			return " < "
		}
		return " > "
	}

	c := cond{pieces: []string{""}}
	if d == Postgres && !slices.ContainsFunc(orders, func(o order) bool { return o.desc != orders[0].desc }) {
		columns := make([]string, len(orders))
		for i, o := range orders {
			columns[i] = o.column
		}
		c.text("(" + strings.Join(columns, ", ") + ")" + op(orders[0].desc) + "(")
		for i, v := range values {
			if i > 0 {
				c.text(", ")
			}
			c.arg(v)
		}
		c.text(")")
		return c, nil
	}
	for i, o := range orders {
		if i > 0 {
			c.text(" OR ")
		}
		c.text("(")
		for j := range i {
			c.text(orders[j].column + " = ")
			c.arg(values[j])
			c.text(" AND ")
		}
		c.text(o.column + op(o.desc))
		c.arg(values[i])
		c.text(")")
	}
	return c, nil
}

func valueOf(values map[string]any, column string) (any, error) {
	v, ok := values[column]
	if !ok {
		return nil, fmt.Errorf("orm: a page cannot continue by %s, which the row has no value of", column)
	}
	if _, null := v.(mayBeNull); null {
		return nil, fmt.Errorf("orm: a page cannot continue by %s, which may be NULL", column)
	}
	return v, nil
}

func describeOrders(orders []order) []string {
	ds := make([]string, len(orders))
	for i, o := range orders {
		ds[i] = o.column
		if o.desc {
			ds[i] += " desc"
		}
	}
	return ds
}

func (c *cond) arg(v any) {
	c.args = append(c.args, v)
	c.pieces = append(c.pieces, "")
}
