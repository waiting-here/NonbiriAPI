package db

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"math/big"
)

// ValidateAssetLedger validates monetary facts before committing an upgrade.
// It streams entries and uses independent sums for the two assets.
func ValidateAssetLedger(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `
SELECT e.operation_id,e.asset_type,e.delta_sign,e.delta_mag,a.asset_type
FROM credit_entries e JOIN credit_operations o ON o.id=e.operation_id
LEFT JOIN credit_accounts a ON a.id=e.account_id
ORDER BY o.ledger_seq,e.line_no`)
	if err != nil {
		return err
	}
	operation := ""
	totals := map[string]*big.Int{"general": new(big.Int), "game": new(big.Int)}
	for rows.Next() {
		var id, asset string
		var accountAsset sql.NullString
		var sign int
		var raw []byte
		if err := rows.Scan(&id, &asset, &sign, &raw, &accountAsset); err != nil {
			rows.Close()
			return err
		}
		if id != operation {
			if totals["general"].Sign() != 0 || totals["game"].Sign() != 0 {
				rows.Close()
				return errors.New("ledger asset conservation mismatch")
			}
			totals["general"].SetInt64(0)
			totals["game"].SetInt64(0)
			operation = id
		}
		delta, err := NewSM128(sign, raw)
		total, valid := totals[asset]
		if err != nil || !valid || accountAsset.Valid && asset != accountAsset.String {
			rows.Close()
			return errors.New("invalid ledger entry asset")
		}
		total.Add(total, delta.Big())
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if totals["general"].Sign() != 0 || totals["game"].Sign() != 0 {
		return errors.New("ledger asset conservation mismatch")
	}
	return ValidateAssetBalances(ctx, tx)
}

// ValidateAssetBalances replays each surviving account from its zero opening
// balance, including signed user debt, without collecting the ledger in RAM.
func ValidateAssetBalances(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `
SELECT a.id,a.kind,a.balance_sign,a.balance_mag,
 e.delta_sign,e.delta_mag,e.balance_after_sign,e.balance_after_mag
FROM credit_accounts a
LEFT JOIN credit_entries e ON e.account_id=a.id
LEFT JOIN credit_operations o ON o.id=e.operation_id
ORDER BY a.id,o.ledger_seq,e.line_no`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var previous int64
	running, expected := new(big.Int), new(big.Int)
	for rows.Next() {
		var id int64
		var kind string
		var sign int
		var raw, deltaRaw, afterRaw []byte
		var deltaSign, afterSign sql.NullInt64
		if err := rows.Scan(&id, &kind, &sign, &raw, &deltaSign, &deltaRaw, &afterSign, &afterRaw); err != nil {
			return err
		}
		if id != previous {
			if running.Cmp(expected) != 0 {
				return errors.New("ledger account replay mismatch")
			}
			balance, err := NewSM128(sign, raw)
			if err != nil {
				return err
			}
			expected.Set(balance.Big())
			running.SetInt64(0)
			previous = id
		}
		if !deltaSign.Valid {
			continue
		}
		delta, err := NewSM128(int(deltaSign.Int64), deltaRaw)
		if err != nil {
			return err
		}
		running.Add(running, delta.Big())
		if _, err := SM128FromBig(running); err != nil {
			return err
		}
		if (kind == "platform" || kind == "pool") && running.Sign() < 0 {
			return errors.New("negative reserve during ledger replay")
		}
		if afterSign.Valid {
			after, err := NewSM128(int(afterSign.Int64), afterRaw)
			if err != nil || after.Big().Cmp(running) != 0 {
				return errors.New("ledger balance snapshot mismatch")
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if running.Cmp(expected) != 0 {
		return errors.New("ledger account replay mismatch")
	}
	return nil
}

func validateAssetCapacity(ctx context.Context, tx *sql.Tx) error {
	var last, count, maximum int64
	var reservedRaw []byte
	if err := tx.QueryRowContext(ctx, `SELECT last_ledger_seq,reserved_future_rows FROM credit_capacity WHERE id=1`).Scan(&last, &reservedRaw); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(MAX(ledger_seq),0) FROM credit_operations`).Scan(&count, &maximum); err != nil {
		return err
	}
	reserved, err := DecodeU128(reservedRaw)
	if err != nil || last != count || last != maximum {
		return errors.New("ledger sequence/capacity mismatch")
	}
	total := new(big.Int)
	tables := []string{"logical_requests", "game_fishing_batches", "thursday_periods", "thursday_participants", "game_rps_queue", "game_rps_sessions", "game_onboarding_holds"}
	present, err := DuelStoragePresent(ctx, tx)
	if err != nil {
		return err
	}
	if present {
		tables = append(tables, "game_duel_queue", "game_duel_sessions")
	}
	blackjackPresent, err := BlackjackStoragePresent(ctx, tx)
	if err != nil {
		return err
	}
	if blackjackPresent {
		tables = append(tables, "game_blackjack_payments")
	}
	for _, table := range tables {
		rows, err := tx.QueryContext(ctx, `SELECT ledger_rows_remaining FROM `+quoteSQLiteIdentifier(table))
		if err != nil {
			return err
		}
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return err
			}
			value, err := DecodeU128(raw)
			if err != nil {
				rows.Close()
				return err
			}
			total.Add(total, value.Big())
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	if total.Cmp(reserved.Big()) != 0 || new(big.Int).Add(total, big.NewInt(last)).Cmp(big.NewInt(math.MaxInt64)) > 0 {
		return errors.New("ledger future capacity mismatch")
	}
	return nil
}
