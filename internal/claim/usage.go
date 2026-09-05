package claim

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const usageColumns = `total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests`

// Usage is committed with the terminal request, so retries and recovery never
// count it twice. The site total survives deletion of its contributing account.
func addRequestUsageTx(ctx context.Context, tx *sql.Tx, requestID string, userID *int64, at int64) error {
	var delta [6]int64
	delta[0] = 1
	if err := tx.QueryRowContext(ctx, `SELECT uncached_input_tokens,cache_write_input_tokens,
cache_read_input_tokens,output_tokens,usage_unknown FROM request_logs
WHERE logical_request_id=? AND completed_at IS NOT NULL`, requestID).Scan(
		&delta[1], &delta[2], &delta[3], &delta[4], &delta[5]); err != nil {
		return fmt.Errorf("claim: read terminal usage: %w", err)
	}
	if userID != nil {
		if err := addUsageCountersTx(ctx, tx, "users", *userID, delta, at); err != nil {
			return err
		}
	}
	return addUsageCountersTx(ctx, tx, "site_usage_totals", 1, delta, at)
}

func addUsageCountersTx(ctx context.Context, tx *sql.Tx, table string, id int64, delta [6]int64, at int64) error {
	if table != "users" && table != "site_usage_totals" {
		return ErrInvariant
	}
	var raw [7][]byte
	err := tx.QueryRowContext(ctx, "SELECT "+usageColumns+",revision FROM "+table+" WHERE id=?", id).
		Scan(&raw[0], &raw[1], &raw[2], &raw[3], &raw[4], &raw[5], &raw[6])
	if errors.Is(err, sql.ErrNoRows) && table == "users" {
		return nil
	}
	if err != nil {
		return fmt.Errorf("claim: read cumulative usage: %w", err)
	}
	values := make([]any, 0, 9)
	for index, encoded := range raw {
		current, err := db.DecodeU128(encoded)
		if err != nil {
			return ErrInvariant
		}
		increment := int64(1)
		if index < len(delta) {
			increment = delta[index]
		}
		if increment < 0 {
			return ErrInvariant
		}
		// User revisions protect editable profile/settings. Usage does not
		// invalidate an administrator's pending profile edits.
		if index == len(delta) && table == "users" {
			continue
		}
		next, err := db.U128FromBig(new(big.Int).Add(current.Big(), big.NewInt(increment)))
		if err != nil {
			return fmt.Errorf("claim: cumulative usage overflow: %w", err)
		}
		values = append(values, db.EncodeU128(next))
	}
	query := "UPDATE " + table + ` SET total_requests=?,total_uncached_input_tokens=?,total_cache_write_input_tokens=?,
total_cache_read_input_tokens=?,total_output_tokens=?,total_unknown_usage_requests=?`
	if table == "site_usage_totals" {
		query += ",revision=?,updated_at=MAX(updated_at,?)"
		values = append(values, at)
	}
	query += " WHERE id=?"
	values = append(values, id)
	result, err := tx.ExecContext(ctx, query, values...)
	if err != nil {
		return fmt.Errorf("claim: update cumulative usage: %w", err)
	}
	return requireOneRow(result)
}

// InitializeUsageTotals repairs the uninitialized counters of existing current
// databases from their retained terminal logs. A nonzero site revision is the
// durable completion marker; normal restarts and log retention never rebuild or
// decrease established totals. Call before accepting requests or starting workers.
func (s *Service) InitializeUsageTotals(ctx context.Context) error {
	if s == nil || s.db == nil || ctx == nil {
		return ErrInvalidInput
	}
	at, err := s.nowUnix()
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var revisionBytes []byte
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM site_usage_totals WHERE id=1`).Scan(&revisionBytes); err != nil {
		return err
	}
	revision, err := db.DecodeU128(revisionBytes)
	if err != nil {
		return ErrInvariant
	}
	if revision != (db.U128{}) {
		return nil
	}
	for _, table := range []string{"users", "site_usage_totals"} {
		var nonzero int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+` WHERE
total_requests<>? OR total_uncached_input_tokens<>? OR total_cache_write_input_tokens<>?
OR total_cache_read_input_tokens<>? OR total_output_tokens<>? OR total_unknown_usage_requests<>?`,
			u128Small(0), u128Small(0), u128Small(0), u128Small(0), u128Small(0), u128Small(0)).Scan(&nonzero); err != nil {
			return err
		}
		if nonzero != 0 {
			return fmt.Errorf("claim: inconsistent uninitialized usage totals: %w", ErrInvariant)
		}
	}
	// Establish the marker in the same transaction as every repaired counter.
	if _, err := tx.ExecContext(ctx, `UPDATE site_usage_totals SET revision=?,updated_at=? WHERE id=1`, u128Small(1), at); err != nil {
		return err
	}
	type record struct {
		rowID     int64
		requestID string
		userID    sql.NullInt64
	}
	var cursor int64
	for {
		rows, err := tx.QueryContext(ctx, `SELECT id,logical_request_id,user_id FROM request_logs
WHERE id>? AND completed_at IS NOT NULL ORDER BY id LIMIT 256`, cursor)
		if err != nil {
			return err
		}
		batch := make([]record, 0, 256)
		for rows.Next() {
			var item record
			if err := rows.Scan(&item.rowID, &item.requestID, &item.userID); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		for _, item := range batch {
			var userID *int64
			if item.userID.Valid {
				userID = &item.userID.Int64
			}
			if err := addRequestUsageTx(ctx, tx, item.requestID, userID, at); err != nil {
				return err
			}
			cursor = item.rowID
		}
	}
	return tx.Commit()
}
