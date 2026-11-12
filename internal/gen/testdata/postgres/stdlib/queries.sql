-- query: MonthCounts
SELECT date_trunc('month', placed_at) AS month, count(*) AS count
FROM orders
WHERE placed_at >= :since
GROUP BY 1
ORDER BY 1;

-- query: TotalOf
SELECT c.name AS customer, sum(o.total)::bigint AS total
FROM orders o
JOIN customers c ON c.id = o.customer_id
WHERE c.id = :customerID
GROUP BY c.name;

-- query: CloseOld
UPDATE orders SET status = 'closed' WHERE placed_at < :before AND status <> 'closed';

-- query: Reopen
UPDATE orders SET status = 'open' WHERE id = :id;

-- query: ListWithCustomers
SELECT o.id, o.customer_id, o.status, o.placed_at, o.note, o.total, o.paid, o.contact, o.receipt, o."order", c.name AS customer_name
FROM orders o
JOIN customers c ON c.id = o.customer_id;

-- query: FirstPlacedSince
SELECT o.id, o.customer_id, o.status, o.placed_at, o.note, o.total, o.paid, o.contact, o.receipt, o."order"
FROM orders o
WHERE o.placed_at >= :since;

-- query: ListRepeated
SELECT o.id, o.customer_id, o.status, o.placed_at, o.note, o.total, o.paid, o.contact, o.receipt, o."order"
FROM orders o
CROSS JOIN (WITH RECURSIVE n (i) AS (SELECT 0 UNION ALL SELECT i + 1 FROM n WHERE i < 10) SELECT a.i FROM n a, n b, n c) AS r;

-- query: CustomersOf
SELECT customer_id FROM orders WHERE id = ANY(:ids) ORDER BY id;

-- query: CustomersNotOf
SELECT customer_id FROM orders WHERE id <> ALL(:ids) ORDER BY id;

-- query: ListOthers
SELECT o.id, o.customer_id, o.status, o.placed_at, o.note, o.total, o.paid, o.contact, o.receipt, o."order"
FROM orders o
WHERE o.id <> ALL(:ids);
