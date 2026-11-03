package stdlib

import "context"

// OrderIDsByHand reads the ids of the orders and counts them, by statements written by hand on the rows of
// database/sql.
func (r OrderRepository) OrderIDsByHand(ctx context.Context) ([]int64, int64, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT id FROM orders ORDER BY id")
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var n int64
	if err := r.db.QueryRowContext(ctx, "SELECT count(*) FROM orders").Scan(&n); err != nil {
		return nil, 0, err
	}
	return ids, n, nil
}
