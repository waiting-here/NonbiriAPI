package lakenotes

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"

	"github.com/waiting-here/NonbiriAPI/internal/lakenotes/rules"
	"github.com/waiting-here/NonbiriAPI/internal/rewardasset"
)

// SetManagedResourceTx preserves live cast snapshots by refusing adjustment
// until the account has ended its current cast, including a paused cast.
func SetManagedResourceTx(ctx context.Context, tx *sql.Tx, user, at int64, asset, quantity string) ([]rewardasset.Change, error) {
	var active bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM lake_notes_casts WHERE user_id=? AND phase IN ('waiting','playing'))", user).Scan(&active); err != nil {
		return nil, err
	}
	if active {
		return nil, ErrConflict
	}
	row, err := profileTx(ctx, tx, user, true, at)
	if err != nil {
		return nil, err
	}
	next, err := rules.SetManagedResource(row.profile, asset, quantity)
	if err != nil {
		return nil, ErrInvalid
	}
	changes := []rewardasset.Change{}
	for _, change := range []struct {
		field         string
		before, after any
	}{
		{"lake.skills", []string{row.profile.First, row.profile.Second, row.profile.Third, row.profile.Fourth}, []string{next.First, next.Second, next.Third, next.Fourth}},
		{"lake.equipped", row.profile.Equipped, next.Equipped},
		{"lake.saved_loadouts", row.profile.SavedLoadouts, next.SavedLoadouts},
		{"lake.selected_bait", row.profile.SelectedBait, next.SelectedBait},
	} {
		if !reflect.DeepEqual(change.before, change.after) {
			before, _ := json.Marshal(change.before)
			after, _ := json.Marshal(change.after)
			changes = append(changes, rewardasset.Change{Field: change.field, Before: string(before), After: string(after)})
		}
	}
	row.profile = next
	return changes, saveProfileTx(ctx, tx, user, &row, at)
}
