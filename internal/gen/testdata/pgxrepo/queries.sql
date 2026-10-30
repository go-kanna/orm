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
