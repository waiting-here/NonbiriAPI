package rewards

import (
	"context"
	"database/sql"
	"github.com/waiting-here/NonbiriAPI/internal/rewardasset"
)

// ValidateItems applies the same asset and quantity rules as reward campaigns.
func ValidateItems(items []rewardasset.Item) error { return validateRewards(items) }

// GrantTx delivers a bundle in the caller's authorized transaction. The caller
// owns uniqueness and must roll back the transaction if any item fails.
func GrantTx(ctx context.Context, tx *sql.Tx, user, actor, at int64, title string, items []rewardasset.Item) ([]rewardasset.Result, error) {
	if err := validateRewards(items); err != nil {
		return nil, err
	}
	return grant(ctx, tx, user, actor, at, title, items)
}
