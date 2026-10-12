package gacha

import (
	"context"
	"database/sql"
	"slices"
)

// MomentOptionsTx projects available artwork without entering the activity.
func (s *Service) MomentOptionsTx(ctx context.Context, tx *sql.Tx, user int64) ([]string, error) {
	owned := map[string]bool{}
	rows, err := tx.QueryContext(ctx, "SELECT card_id FROM gacha_owned WHERE user_id=?", user)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		owned[id] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	options := []string{"cover:permanent"}
	at, err := s.now()
	if err != nil {
		return nil, err
	}
	pools, err := readOfferedPools(ctx, tx, at)
	if err != nil {
		return nil, err
	}
	covers := map[string]bool{}
	for _, pool := range pools {
		covers[pool.Cover] = true
	}
	for _, cover := range poolCoverIDs() {
		if cover != "permanent" && covers[cover] {
			options = append(options, "cover:"+cover)
		}
	}
	for _, card := range catalog.Cards {
		if owned[card.ID] {
			options = append(options, "card:"+card.ID)
		}
	}
	for _, theme := range catalog.Themes {
		members := 0
		complete := true
		for _, card := range catalog.Cards {
			if card.Theme != theme.ID {
				continue
			}
			members++
			complete = complete && owned[card.ID]
		}
		if members > 0 && complete {
			options = append(options, "illustration:"+theme.ID)
		}
	}
	// Keep the default first and the rest deterministic across configuration revisions.
	slices.Sort(options[1:])
	return options, nil
}
