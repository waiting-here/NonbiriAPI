package economyaudit

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type checkpoint struct {
	last, head, unknown int64
	first, started      sql.NullInt64
	offset              int
	opening             bool
}

type operation struct {
	id, kind, source, sourceID string
	seq, at                    int64
}
type dimension struct{ kind, source, channel string }

func readCheckpoint(ctx context.Context, tx *sql.Tx) (checkpoint, error) {
	var c checkpoint
	var offset sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT last_ledger_seq,first_ledger_seq,first_occurred_at,offset_minutes,unclassified_operations,opening_known FROM economy_audit_checkpoint WHERE id=1`).Scan(&c.last, &c.first, &c.started, &offset, &c.unknown, &c.opening)
	if err != nil {
		return c, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT last_ledger_seq FROM credit_capacity WHERE id=1`).Scan(&c.head); err != nil {
		return c, err
	}
	resolved, err := db.ResolveSiteTimezoneTx(ctx, tx)
	if err != nil {
		return c, err
	}
	if offset.Valid && int(offset.Int64) != resolved {
		return c, ErrInvariant
	}
	c.offset = resolved
	if c.last < 0 || c.last > c.head || c.unknown < 0 || c.unknown > c.last {
		return c, ErrInvariant
	}
	return c, nil
}

func operationsAfter(ctx context.Context, tx *sql.Tx, after, head int64) ([]operation, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,ledger_seq,kind,source_type,source_id,created_at FROM credit_operations WHERE ledger_seq>? AND ledger_seq<=? ORDER BY ledger_seq LIMIT ?`, after, head, batchSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []operation{}
	for rows.Next() {
		var o operation
		if err := rows.Scan(&o.id, &o.seq, &o.kind, &o.source, &o.sourceID, &o.at); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func operationMeasures(ctx context.Context, tx *sql.Tx, id string) (map[ledger.Asset]*measures, error) {
	rows, err := tx.QueryContext(ctx, `SELECT asset_type,account_kind_snapshot,delta_sign,delta_mag FROM credit_entries WHERE operation_id=? ORDER BY line_no`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []posting{}
	for rows.Next() {
		var e posting
		var sign int
		var raw []byte
		if err := rows.Scan(&e.asset, &e.kind, &sign, &raw); err != nil {
			return nil, err
		}
		amount, err := db.NewSM128(sign, raw)
		if err != nil {
			return nil, ErrInvariant
		}
		e.delta = amount.Big()
		entries = append(entries, e)
		if len(entries) > 256 {
			return nil, ErrInvariant
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return classifyPostings(entries)
}

// AdvanceTx projects at most one bounded batch within the caller's transaction.
// Inventory must only be read with ready=true in this same transaction.
func AdvanceTx(ctx context.Context, tx *sql.Tx, now int64) (bool, error) {
	if ctx == nil || tx == nil || now < 0 || now > maxUnix {
		return false, ErrInvalid
	}
	c, err := readCheckpoint(ctx, tx)
	if err != nil {
		return false, err
	}
	if c.last == c.head {
		return true, nil
	}
	operations, err := operationsAfter(ctx, tx, c.last, c.head)
	if err != nil {
		return false, err
	}
	if len(operations) == 0 {
		return false, ErrInvariant
	}
	if c.last == 0 {
		if err := db.FreezeSiteTimezoneTx(ctx, tx, now); err != nil {
			return false, err
		}
	}
	previous := c.last
	for _, o := range operations {
		if o.seq != c.last+1 || o.at < 0 || o.at > maxUnix {
			return false, ErrInvariant
		}
		amounts, err := operationMeasures(ctx, tx, o.id)
		if err != nil {
			return false, err
		}
		classification := ledger.ClassifyForAudit(ledger.Kind(o.kind), o.sourceID)
		if !classification.Known {
			c.unknown++
		}
		for asset, m := range amounts {
			for _, bucket := range []struct {
				name    string
				seconds int64
			}{{"hour", 3600}, {"day", 86400}} {
				if err := addBucket(ctx, tx, asset, dimension{o.kind, o.source, classification.Channel}, bucket.name, bucketStart(o.at, c.offset, bucket.seconds), c.offset, o.seq, m); err != nil {
					return false, err
				}
			}
		}
		if !c.first.Valid {
			c.first = sql.NullInt64{Int64: o.seq, Valid: true}
		}
		if !c.started.Valid || o.at < c.started.Int64 {
			c.started = sql.NullInt64{Int64: o.at, Valid: true}
		}
		c.last = o.seq
	}
	result, err := tx.ExecContext(ctx, `UPDATE economy_audit_checkpoint SET last_ledger_seq=?,first_ledger_seq=?,first_occurred_at=?,offset_minutes=?,unclassified_operations=?,updated_at=? WHERE id=1 AND last_ledger_seq=?`, c.last, c.first.Int64, c.started.Int64, c.offset, c.unknown, now, previous)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return false, ErrInvariant
	}
	return c.last == c.head, nil
}

func addBucket(ctx context.Context, tx *sql.Tx, asset ledger.Asset, d dimension, bucket string, start int64, offset int, seq int64, delta *measures) error {
	key := []any{string(asset), d.kind, d.source, d.channel, bucket, start, offset}
	var values [5]string
	var count, first, last int64
	err := tx.QueryRowContext(ctx, `SELECT issued,reclaimed,user_income,user_expense,internal_transfer,operation_count,first_ledger_seq,last_ledger_seq FROM economy_audit_buckets WHERE asset_type=? AND kind=? AND source_type=? AND channel=? AND bucket=? AND bucket_start=? AND offset_minutes=?`, key...).Scan(&values[0], &values[1], &values[2], &values[3], &values[4], &count, &first, &last)
	current := &measures{}
	if errors.Is(err, sql.ErrNoRows) {
		first = seq
	} else if err != nil {
		return err
	} else {
		if last >= seq {
			return ErrInvariant
		}
		current, err = decodeMeasures(values, count)
		if err != nil {
			return err
		}
	}
	if err := current.add(delta); err != nil {
		return err
	}
	args := append(key, current.issued.String(), current.reclaimed.String(), current.income.String(), current.expense.String(), current.transfer.String(), current.count, first, seq)
	_, err = tx.ExecContext(ctx, `INSERT INTO economy_audit_buckets(asset_type,kind,source_type,channel,bucket,bucket_start,offset_minutes,issued,reclaimed,user_income,user_expense,internal_transfer,operation_count,first_ledger_seq,last_ledger_seq) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(asset_type,kind,source_type,channel,bucket,bucket_start,offset_minutes) DO UPDATE SET issued=excluded.issued,reclaimed=excluded.reclaimed,user_income=excluded.user_income,user_expense=excluded.user_expense,internal_transfer=excluded.internal_transfer,operation_count=excluded.operation_count,last_ledger_seq=excluded.last_ledger_seq`, args...)
	return err
}

func metadata(c checkpoint, f Filter, now int64) Metadata {
	coverage := Coverage{Status: "complete", OpeningKnown: c.opening, UnclassifiedOperations: strconv.FormatInt(c.unknown, 10)}
	if c.first.Valid {
		value := strconv.FormatInt(c.first.Int64, 10)
		coverage.FirstLedgerSeq = &value
	}
	if c.started.Valid {
		value := c.started.Int64
		coverage.FirstOccurredAt = &value
	}
	if c.last != c.head {
		coverage.Status = "catching_up"
	} else if c.unknown > 0 {
		coverage.Status = "unclassified"
	} else if !c.opening {
		coverage.Status = "opening_unknown"
	}
	return Metadata{Asset: f.Asset, From: f.From, To: f.To, Unit: "milliunits", Scale: "1000", OffsetMinutes: c.offset, LedgerSeq: strconv.FormatInt(c.head, 10), ProjectedSeq: strconv.FormatInt(c.last, 10), SnapshotAt: now, Coverage: coverage}
}
