-- name: GetOrder :one
SELECT id, customer_id, status, placed_at, note FROM orders WHERE id = $1;

-- name: ListOrders :many
SELECT id, customer_id, status, placed_at, note FROM orders
WHERE status = $1 AND placed_at >= $2 ORDER BY placed_at DESC, id DESC LIMIT 20;

-- name: ListOrderLines :many
SELECT id, order_id, sku, quantity FROM order_lines WHERE order_id = ANY(@order_ids::bigint[]) ORDER BY id;

-- name: PageOrders :many
SELECT id, customer_id, status, placed_at, note FROM orders
WHERE status = @status AND (placed_at, id) < (@placed_at::timestamptz, @id::bigint)
ORDER BY placed_at DESC, id DESC LIMIT 20;

-- name: InsertCustomer :one
INSERT INTO customers (email, name, created_at) VALUES ($1, $2, $3) RETURNING id;

-- name: InsertOrderLines :many
INSERT INTO order_lines (order_id, sku, quantity)
SELECT unnest(@order_ids::bigint[]), unnest(@skus::text[]), unnest(@quantities::integer[])
RETURNING id;

-- name: UpsertCustomer :one
INSERT INTO customers (email, name, created_at) VALUES ($1, $2, $3)
ON CONFLICT (email) DO UPDATE SET name = EXCLUDED.name RETURNING id;
