-- query: Tag
INSERT IGNORE INTO order_tags (order_id, tag_id) VALUES (:orderID, :tag);

-- MySQL says a function of a column may be NULL, and a sum too, so COALESCE tells it they are not.
-- query: MonthlySales
SELECT COALESCE(DATE_FORMAT(o.placed_at, '%Y-%m'), '') AS month,
       count(DISTINCT o.id) AS orders,
       CAST(COALESCE(sum(l.quantity * p.price), 0) AS SIGNED) AS revenue
FROM orders o
JOIN order_lines l ON l.order_id = o.id
JOIN products p ON p.sku = l.sku
WHERE o.placed_at >= :since AND o.status <> 'canceled'
GROUP BY 1
ORDER BY 1;

-- query: ListApprovers
SELECT
    o.id AS order_id,
    a.approver
FROM orders AS o
LEFT JOIN approvals AS a ON a.order_id = o.id AND a.revision = o.revision AND a.revoked_at IS NULL
WHERE o.id IN (:ids)
ORDER BY o.id;
