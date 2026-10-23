package donation

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
)

// Pending and approved are independent pointers: one use can count in both.
type UseSummary struct {
	Total    string `json:"total"`
	Pending  string `json:"pending"`
	Approved string `json:"approved"`
}

func readUseSummariesTx(ctx context.Context, tx *sql.Tx, ids []int64) (map[int64]UseSummary, error) {
	out := make(map[int64]UseSummary, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	if len(ids) > 100 {
		return nil, ErrInvalidRequest
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
		out[id] = UseSummary{"0", "0", "0"}
	}
	rows, err := tx.QueryContext(ctx, `SELECT dk.donation_id,COUNT(*),COUNT(u.current_pending_revision_id),COUNT(u.current_approved_revision_id) FROM donation_keys dk JOIN donation_uses u ON u.donation_key_id=dk.id WHERE dk.donation_id IN (`+strings.TrimRight(strings.Repeat("?,", len(ids)), ",")+`) GROUP BY dk.donation_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, total, pending, approved int64
		if err := rows.Scan(&id, &total, &pending, &approved); err != nil {
			return nil, err
		}
		out[id] = UseSummary{strconv.FormatInt(total, 10), strconv.FormatInt(pending, 10), strconv.FormatInt(approved, 10)}
	}
	return out, rows.Err()
}
