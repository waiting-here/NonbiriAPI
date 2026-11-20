package rewards

import (
	"context"
	"database/sql"
	"github.com/waiting-here/NonbiriAPI/internal/rewardasset"
)

// ValidateItems applies the same asset and quantity rules as reward campaigns.
func ValidateItems(items []rewardasset.Item) error { return validateRewards(items) }

// GrantAttendanceTierTx delivers a sign-in tier in the caller's authorized transaction. The caller
// owns uniqueness and must roll back the transaction if any item fails.
func GrantAttendanceTierTx(ctx context.Context, tx *sql.Tx, user, actor, at int64, title string, items []rewardasset.Item, tierID string) ([]rewardasset.Result, error) {
	if err := validateRewards(items); err != nil {
		return nil, err
	}
	return grant(ctx, tx, user, actor, at, title, items, "attendance_tier", tierID)
}
