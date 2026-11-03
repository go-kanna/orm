-- scope: Order.PlacedInMonth(at time.Time)
DATE_FORMAT(placed_at, '%Y-%m') = DATE_FORMAT(:at, '%Y-%m')

-- scope: Order.Open()
status <> 'closed';
