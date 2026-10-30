-- PlacedInMonth narrows to the orders placed in the month of at.
-- scope: Order.PlacedInMonth(at time.Time)
DATE_FORMAT(placed_at, '%Y-%m') = DATE_FORMAT(:at, '%Y-%m')
