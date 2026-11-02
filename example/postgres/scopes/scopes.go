// Package scopes holds the scope types the repositories read through, one for each table, which kanna-orm declares
// of the models.
package scopes

import (
	"github.com/go-kanna/orm"

	"github.com/go-kanna/orm/example/postgres/model"
)

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

func (s Order) WithCurrentApprover() Order {
	return s.SelectApprover(func(parent OrderColumns) orm.Single[model.Approval] {
		return Approvals().RevisionEqColumn(parent.Revision).RevokedAtIsNull().One()
	})
}

func (s Order) WithLatestApprover() Order {
	return s.SelectApprover(func(_ OrderColumns) orm.Single[model.Approval] {
		return Approvals().RevokedAtIsNull().ApprovedAtDesc().IDDesc().First()
	})
}

func (s Order) WithRecentApprovals() Order {
	return s.PreloadApprovals(func(_ OrderColumns) orm.Many[model.Approval] {
		return Approvals().ApprovedAtDesc().IDDesc().Limit(3).Many()
	})
}
