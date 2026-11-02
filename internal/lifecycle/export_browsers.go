package lifecycle

import (
	"context"
	"database/sql"
	"fmt"
)

type BrowserAssociationExport struct {
	FirstSeen int64 `json:"first_seen"`
	LastSeen  int64 `json:"last_seen"`
	ExpiresAt int64 `json:"expires_at"`
}

// Personal exports deliberately omit grouping material and other accounts.
func exportBrowserAssociations(ctx context.Context, tx *sql.Tx, userID, now int64) ([]BrowserAssociationExport, error) {
	items := []BrowserAssociationExport{}
	rows, err := tx.QueryContext(ctx, `SELECT first_seen,last_seen,expires_at FROM browser_associations WHERE user_id=? AND expires_at>? ORDER BY last_seen DESC,browser_digest LIMIT 33`, userID, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item BrowserAssociationExport
		if err = rows.Scan(&item.FirstSeen, &item.LastSeen, &item.ExpiresAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if len(items) > 32 {
		return nil, fmt.Errorf("browser export exceeds collection limit")
	}
	return items, rows.Err()
}
