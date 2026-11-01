package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-kanna/orm/example/mysql/scopes"
)

func (ex *example) readMemos(ctx context.Context) error {
	withoutMemo, err := ex.orders.List(ctx, scopes.Orders().MemoIsNull())
	if err != nil {
		return fmt.Errorf("orders without a memo: %w", err)
	}
	if len(withoutMemo) != 3 {
		return fmt.Errorf("orders with a NULL memo: got %d, want 3", len(withoutMemo))
	}
	for _, order := range withoutMemo {
		if order.Memo != "" {
			return fmt.Errorf("order %d: NULL memo read as %q", order.ID, order.Memo)
		}
	}
	withMemo, err := ex.orders.List(ctx, scopes.Orders().MemoEq("Leave at reception"))
	if err != nil {
		return fmt.Errorf("orders with a memo: %w", err)
	}
	if len(withMemo) != 1 || withMemo[0].Memo != "Leave at reception" {
		return errors.New("the non-empty memo was not preserved")
	}
	fmt.Fprintf(ex.w, "%d orders without a memo; order %d: %s\n", len(withoutMemo), withMemo[0].ID, withMemo[0].Memo)
	return nil
}
