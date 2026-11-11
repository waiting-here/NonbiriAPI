package charityrouting

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
)

// Read only IDs already selected by the authorized page. Keep the caller's
// order, including when a future page orders by a field other than the ID.
func getAdminModelsTx(ctx context.Context, tx *sql.Tx, ids []int64, at int64) ([]AdminCharityModel, error) {
	result := make([]AdminCharityModel, 0, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	query := strings.TrimSuffix(modelProjectionSQL, ` WHERE cm.id=?`) + ` WHERE cm.id IN (SELECT value FROM json_each(?))`
	rows, err := tx.QueryContext(ctx, query, string(encoded))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := make(map[string]AdminCharityModel, len(ids))
	for rows.Next() {
		item, err := scanAdminModel(rows, at)
		if err != nil {
			return nil, err
		}
		byID[item.ID] = item
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		item, ok := byID[strconv.FormatInt(id, 10)]
		if !ok {
			return nil, ErrNotFound
		}
		result = append(result, item)
	}
	return result, nil
}
