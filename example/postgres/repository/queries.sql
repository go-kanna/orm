-- query: Tag
INSERT INTO order_tags (order_id, tag) VALUES (:orderID, :tag)
ON CONFLICT DO NOTHING;

-- query: MonthlySales
SELECT date_trunc('month', o.placed_at) AS month,
       count(DISTINCT o.id) AS orders,
       sum(l.quantity * p.price)::bigint AS revenue
FROM orders o
JOIN order_lines l ON l.order_id = o.id
JOIN products p ON p.sku = l.sku
WHERE o.placed_at >= :since AND o.status <> 'canceled'
GROUP BY 1
ORDER BY 1;
