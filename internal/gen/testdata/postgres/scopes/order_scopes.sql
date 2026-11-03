-- The scopes of orders, written by hand.

-- PlacedInMonth narrows to the orders placed in the month of at,
-- by the time zone of the database.
-- scope: Order.PlacedInMonth(at time.Time)
date_trunc('month', placed_at) = date_trunc('month', :at)

-- scope: Order.Open()
status <> 'closed' -- a closed order is done with

-- Busy narrows to the orders placed since since with n lines or more.
-- scope: Order.Busy(since time.Time, n int64)
placed_at >= :since
  AND id IN (SELECT order_id FROM order_lines GROUP BY order_id HAVING count(*) >= :n)

-- scope: Order.Tagged(tags ...string)
id IN (SELECT order_id FROM order_tags WHERE tag = ANY(:tags))
