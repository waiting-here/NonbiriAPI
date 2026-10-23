package charityrouting

import (
	"context"
	"database/sql"
	"strconv"
)

type BindingMarkerTarget struct {
	CharityModelID string `json:"charity_model_id"`
	FullName       string `json:"full_name"`
	BindingID      string `json:"binding_id"`
}
type BindingMarkers struct {
	CurrentBindingID  *string               `json:"current_binding_id"`
	OtherBindingCount string                `json:"other_binding_count"`
	OtherBindings     []BindingMarkerTarget `json:"other_bindings"`
}

func annotateBindingCandidates(ctx context.Context, tx *sql.Tx, items []AdminBindingCandidate, modelID int64, trainee bool) error {
	for i := range items {
		item := &items[i]
		item.BindingMarkers = BindingMarkers{OtherBindingCount: "0", OtherBindings: []BindingMarkerTarget{}}
		rows, err := tx.QueryContext(ctx, `SELECT DISTINCT b.id,b.charity_model_id,cm.full_name FROM charity_model_bindings b JOIN charity_binding_targets t ON t.binding_id=b.id JOIN charity_models cm ON cm.id=b.charity_model_id WHERE t.donation_key_id=? AND t.upstream_model_id=? AND (?=0 OR cm.is_mainstream=1) ORDER BY cm.full_name,cm.id`, item.DonationKeyID, item.UpstreamModelID, trainee)
		if err != nil {
			return err
		}
		count := 0
		for rows.Next() {
			var id, target int64
			var name string
			if err := rows.Scan(&id, &target, &name); err != nil {
				rows.Close()
				return err
			}
			if target == modelID {
				value := strconv.FormatInt(id, 10)
				item.CurrentBindingID = &value
				continue
			}
			count++
			if len(item.OtherBindings) < 3 {
				item.OtherBindings = append(item.OtherBindings, BindingMarkerTarget{strconv.FormatInt(target, 10), name, strconv.FormatInt(id, 10)})
			}
		}
		err = rows.Err()
		closeErr := rows.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		item.OtherBindingCount = strconv.Itoa(count)
	}
	return nil
}
