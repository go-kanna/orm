-- PlacedInMonth narrows to the orders placed in the month of at.
-- scope: Order.PlacedInMonth(at time.Time)
date_trunc('month', placed_at) = date_trunc('month', :at::timestamptz)
