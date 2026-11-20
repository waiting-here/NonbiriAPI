package whalehaven

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/whalehaven/assets"
)

var ErrExportLimit = errors.New("whale haven export exceeds collection limit")

type movementExport struct {
	Time        int64  `json:"at_ms"`
	Reason      string `json:"reason"`
	Operation   string `json:"operation_id,omitempty"`
	Pearls      int64  `json:"whale_coins_delta"`
	Shards      int64  `json:"conch_shards_delta"`
	PearlsAfter int64  `json:"whale_coins_after"`
	ShardsAfter int64  `json:"conch_shards_after"`
}

func Export(ctx context.Context, tx *sql.Tx, user, now int64, limit int) (json.RawMessage, error) {
	// A site grant can exist before adoption or even before a site timezone is configured.
	var adopted bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM whale_haven_profiles WHERE user_id=?)", user).Scan(&adopted); err != nil {
		return nil, err
	}
	var house any
	var balance assets.Balance
	if !adopted {
		var err error
		balance, err = assets.Read(ctx, tx, user)
		if err != nil {
			return nil, err
		}
	} else {
		w, err := openWorld(ctx, tx, now*1000)
		if err != nil {
			return nil, err
		}
		r, err := w.load(user)
		if err != nil {
			return nil, err
		}
		if err = w.settle(r); err != nil {
			return nil, err
		}
		if err = w.setDay(r, w.day(w.now)); err != nil {
			return nil, err
		}
		wage, err := w.wageBalance(r.id)
		if err != nil {
			return nil, err
		}
		balance = r.original
		// A personal download only contains the owner's home and currency movements.
		whale := r.state.Whale
		whale.Location.NeighborID = ""
		house = map[string]any{"pending_social_whale_coins": r.state.Wallet.Pearls - balance.Pearls, "pending_conch_shards": r.state.Wallet.ConchShards - balance.Shards, "wage_progress": wage / 1e6, "door": r.door, "open": r.open, "revision": r.revision, "whale": whale, "home": r.state.Home, "inventory": r.state.Inventory, "progress_at_ms": r.state.LastTickAt, "site_day": r.state.Wallet.SiteDayKey,
			"daily_social_whale_coins": r.state.Wallet.DailySocialPearlsGained, "daily_social_conch_shards": r.state.Wallet.DailyConchShardsGained, "daily_exchanged_whale_coins": r.state.Wallet.DailyPearlExchanged}
	}
	rows, err := tx.QueryContext(ctx, "SELECT created_at,reason,operation_id,pearls_delta,shards_delta,pearls_after,shards_after FROM whale_haven_movements WHERE user_id=? ORDER BY id LIMIT ?", user, limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	moves := []movementExport{}
	for rows.Next() {
		if len(moves) == limit {
			return nil, ErrExportLimit
		}
		var m movementExport
		if err = rows.Scan(&m.Time, &m.Reason, &m.Operation, &m.Pearls, &m.Shards, &m.PearlsAfter, &m.ShardsAfter); err != nil {
			return nil, err
		}
		moves = append(moves, m)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"profile": house, "whale_coins": balance.Pearls, "conch_shards": balance.Shards, "movements": moves})
}

// CloseAccountTx settles both homes before cascading relationships and grants from the closing account.
func CloseAccountTx(ctx context.Context, tx *sql.Tx, user, now int64) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM whale_haven_profiles WHERE user_id=?)", user).Scan(&exists); err != nil || !exists {
		return err
	}
	w, err := openWorld(ctx, tx, now*1000)
	if err != nil {
		return err
	}
	r, err := w.load(user)
	if err != nil {
		return err
	}
	if err = w.settle(r); err != nil {
		return err
	}
	if r.open {
		open := false
		if err = w.socialAction(r, actionRequest{Type: "set_open", Open: &open}); err != nil {
			return err
		}
	}
	op, err := db.GenerateOpaqueID("op_")
	if err != nil {
		return err
	}
	return w.save("account_closure", op)
}
