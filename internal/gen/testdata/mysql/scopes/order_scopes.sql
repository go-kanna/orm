-- scope: Order.PlacedInMonth(at time.Time)
DATE_FORMAT(placed_at, '%Y-%m') = DATE_FORMAT(:at, '%Y-%m')

-- scope: Order.Open()
status <> 'closed';

-- scope: Order.Tagged(tags ...string)
id IN (SELECT order_id FROM order_tags WHERE tag IN (:tags))
