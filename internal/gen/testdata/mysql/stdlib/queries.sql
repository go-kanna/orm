-- query: MonthCounts
SELECT CAST(DATE_FORMAT(placed_at, '%Y-%m-01') AS DATE) AS month, count(*) AS count
FROM orders
WHERE placed_at >= :since
GROUP BY 1
ORDER BY 1;

-- query: TotalOf
SELECT c.name AS customer, CAST(sum(o.total) AS SIGNED) AS total
FROM orders o
JOIN customers c ON c.id = o.customer_id
WHERE c.id = :customerID AND o.customer_id = :customerID
GROUP BY c.name;

-- query: CloseOld
UPDATE orders SET status = 'closed' WHERE placed_at < :before AND status <> 'closed';

-- query: Reopen
UPDATE orders SET status = 'open' WHERE id = :id;

-- query: CustomersOf
SELECT customer_id FROM orders WHERE id IN (:ids) ORDER BY id;

-- query: CustomersNotOf
SELECT customer_id FROM orders WHERE id NOT IN (:ids) ORDER BY id;

-- query: ListWithCustomers
SELECT o.id, o.customer_id, o.status, o.placed_at, o.note, o.total, o.paid, o.contact, o.receipt, o.`order`, c.name AS customer_name
FROM orders o
JOIN customers c ON c.id = o.customer_id;

-- query: FirstPlacedSince
SELECT o.id, o.customer_id, o.status, o.placed_at, o.note, o.total, o.paid, o.contact, o.receipt, o.`order`
FROM orders o
WHERE o.placed_at >= :since;

-- query: ListRepeated
SELECT o.id, o.customer_id, o.status, o.placed_at, o.note, o.total, o.paid, o.contact, o.receipt, o.`order`
FROM orders o
CROSS JOIN (WITH RECURSIVE n (i) AS (SELECT 0 UNION ALL SELECT i + 1 FROM n WHERE i < 10) SELECT a.i FROM n a, n b, n c) AS r;

-- query: ListOthers
SELECT o.id, o.customer_id, o.status, o.placed_at, o.note, o.total, o.paid, o.contact, o.receipt, o.`order`
FROM orders o
WHERE o.id NOT IN (:ids);
