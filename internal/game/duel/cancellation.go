package duel

import (
	"context"
	"database/sql"
	"sync"

	"github.com/waiting-here/NonbiriAPI/internal/game/host"
)

// Cancellation binds exactly the two new game lifecycles at composition time.
// The callback borrows the ban transaction and never commits or publishes early.
func Cancellation(bidding, likes *Service) (func(context.Context, *sql.Tx, int64, string, int64) (func(bool), error), error) {
	if bidding == nil || likes == nil || bidding.rules.ID() != "bidding" || likes.rules.ID() != "likes" {
		return nil, ErrInvariant
	}
	return func(ctx context.Context, tx *sql.Tx, user int64, reason string, now int64) (func(bool), error) {
		if reason != "account_unavailable" {
			return nil, ErrInvalidRequest
		}
		ends := []host.Finalizer{}
		for _, s := range []*Service{bidding, likes} {
			end, err := s.CancelUserTx(ctx, tx, user, now)
			if err != nil {
				for _, prior := range ends {
					prior.Abort()
				}
				return nil, err
			}
			ends = append(ends, end)
		}
		var once sync.Once
		return func(committed bool) {
			once.Do(func() {
				for _, end := range ends {
					if committed {
						end.Commit()
					} else {
						end.Abort()
					}
				}
			})
		}, nil
	}, nil
}
