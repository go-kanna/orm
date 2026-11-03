// Package scopes holds the scope types the repositories read through, one for each table, which kanna-orm declares
// of the models.
package scopes

import "github.com/go-kanna/orm/example/postgres/model"

//go:generate go tool kanna-orm

//kanna:scopes ../model

// Open narrows to the orders neither shipped nor canceled.
func (s Order) Open() Order {
	return s.StatusEq(model.OrderOpen)
}

// Latest orders the orders latest first.
func (s Order) Latest() Order {
	return s.PlacedAtDesc().IDDesc()
}

// WithRecentApprovals loads the three latest approvals of each order.
func (s Order) WithRecentApprovals() Order {
	return s.WithApprovals(Approvals().ApprovedAtDesc().IDDesc().Limit(3))
}

// WithLatestApproval loads the latest approval of each order that is not revoked.
func (s Order) WithLatestApproval() Order {
	return s.WithApprovals(Approvals().RevokedAtIsNull().ApprovedAtDesc().IDDesc().Limit(1))
}
