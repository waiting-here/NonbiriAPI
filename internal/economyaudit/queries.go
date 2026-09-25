package economyaudit

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func sumDimensions(values map[dimension]*measures) (*measures, error) {
	total := &measures{}
	for _, value := range values {
		if err := total.add(value); err != nil {
			return nil, err
		}
	}
	return total, nil
}

func addDimension(out map[dimension]*measures, d dimension, m *measures) error {
	if m == nil {
		return nil
	}
	if out[d] == nil {
		out[d] = &measures{}
	}
	return out[d].add(m)
}

// rangeMeasures uses complete daily aggregates for long ranges, then complete
// hourly aggregates and immutable entries for the partial edge hours.
func rangeMeasures(ctx context.Context, tx *sql.Tx, c checkpoint, f Filter) (map[dimension]*measures, error) {
	out := map[dimension]*measures{}
	if f.From >= f.To {
		return out, nil
	}
	firstDay := bucketStart(f.From, c.offset, 86400)
	if firstDay < f.From {
		firstDay += 86400
	}
	lastDay := bucketStart(f.To, c.offset, 86400)
	if firstDay < lastDay {
		if err := aggregateMeasures(ctx, tx, c, f, "day", firstDay, lastDay, out); err != nil {
			return nil, err
		}
		for _, bounds := range [][2]int64{{f.From, firstDay}, {lastDay, f.To}} {
			part := f
			part.From, part.To = bounds[0], bounds[1]
			values, err := rangeMeasures(ctx, tx, c, part)
			if err != nil {
				return nil, err
			}
			for d, m := range values {
				if err := addDimension(out, d, m); err != nil {
					return nil, err
				}
			}
		}
		return out, nil
	}
	first := bucketStart(f.From, c.offset, 3600)
	if first < f.From {
		first += 3600
	}
	last := bucketStart(f.To, c.offset, 3600)
	if first < last {
		if err := aggregateMeasures(ctx, tx, c, f, "hour", first, last, out); err != nil {
			return nil, err
		}
	}
	if first >= last {
		return out, edgeMeasures(ctx, tx, c, f, f.From, f.To, out)
	}
	if err := edgeMeasures(ctx, tx, c, f, f.From, first, out); err != nil {
		return nil, err
	}
	if err := edgeMeasures(ctx, tx, c, f, last, f.To, out); err != nil {
		return nil, err
	}
	return out, nil
}

func aggregateMeasures(ctx context.Context, tx *sql.Tx, c checkpoint, f Filter, bucket string, from, to int64, out map[dimension]*measures) error {
	query := `SELECT kind,source_type,channel,issued,reclaimed,user_income,user_expense,internal_transfer,operation_count FROM economy_audit_buckets WHERE asset_type=? AND bucket=? AND bucket_start>=? AND bucket_start<? AND offset_minutes=?`
	args := []any{string(f.Asset), bucket, from, to, c.offset}
	if f.Kind != "" {
		query += ` AND kind=?`
		args = append(args, f.Kind)
	}
	if f.Channel != "" {
		query += ` AND channel=?`
		args = append(args, f.Channel)
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var d dimension
		var values [5]string
		var count int64
		if err := rows.Scan(&d.kind, &d.source, &d.channel, &values[0], &values[1], &values[2], &values[3], &values[4], &count); err != nil {
			return err
		}
		m, err := decodeMeasures(values, count)
		if err != nil {
			return err
		}
		if err := addDimension(out, d, m); err != nil {
			return err
		}
	}
	return rows.Err()
}

func edgeMeasures(ctx context.Context, tx *sql.Tx, c checkpoint, f Filter, from, to int64, out map[dimension]*measures) error {
	if from >= to {
		return nil
	}
	afterAt, after := from, int64(0)
	for {
		query := `SELECT id,ledger_seq,kind,source_type,source_id,created_at FROM credit_operations WHERE created_at>=? AND created_at<? AND (created_at,ledger_seq)>(?,?) AND ledger_seq<=?`
		args := []any{from, to, afterAt, after, c.last}
		if f.Kind != "" {
			query += ` AND kind=?`
			args = append(args, f.Kind)
		}
		query += operationChannelCondition(f, "source_id")
		query += ` ORDER BY created_at,ledger_seq LIMIT 100`
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		batch := []operation{}
		for rows.Next() {
			var o operation
			if err := rows.Scan(&o.id, &o.seq, &o.kind, &o.source, &o.sourceID, &o.at); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, o)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, o := range batch {
			amounts, err := operationMeasures(ctx, tx, o.id)
			if err != nil {
				return err
			}
			classification := ledger.ClassifyForAudit(ledger.Kind(o.kind), o.sourceID)
			if err := addDimension(out, dimension{o.kind, o.source, classification.Channel}, amounts[f.Asset]); err != nil {
				return err
			}
			afterAt, after = o.at, o.seq
		}
		if len(batch) < batchSize {
			return nil
		}
	}
}

// column is an internal SQL identifier, never a caller-supplied field name.
func operationChannelCondition(f Filter, column string) string {
	if f.Channel == "" {
		return ""
	}
	switch ledger.Kind(f.Kind) {
	case ledger.KindDuelQueueReserve, ledger.KindDuelQueueRelease, ledger.KindDuelSessionStart, ledger.KindDuelTerminal:
		bidding := fmt.Sprintf("(substr(%s,1,4)='bid_' OR substr(%s,1,5)='bidq_')", column, column)
		likes := fmt.Sprintf("(substr(%s,1,4)='lik_' OR substr(%s,1,5)='likq_')", column, column)
		switch f.Channel {
		case "bidding":
			return " AND " + bidding
		case "likes":
			return " AND " + likes
		case "unclassified":
			return " AND NOT (" + bidding + " OR " + likes + ")"
		default:
			return " AND 0"
		}
	default:
		if ledger.ClassifyForAudit(ledger.Kind(f.Kind), "").Channel != f.Channel {
			return " AND 0"
		}
		return ""
	}
}

func allTimeMeasures(ctx context.Context, tx *sql.Tx, asset ledger.Asset, offset int) (*measures, error) {
	rows, err := tx.QueryContext(ctx, `SELECT issued,reclaimed,user_income,user_expense,internal_transfer,operation_count FROM economy_audit_buckets WHERE asset_type=? AND bucket='day' AND offset_minutes=?`, string(asset), offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	total := &measures{}
	for rows.Next() {
		var values [5]string
		var count int64
		if err := rows.Scan(&values[0], &values[1], &values[2], &values[3], &values[4], &count); err != nil {
			return nil, err
		}
		m, err := decodeMeasures(values, count)
		if err != nil {
			return nil, err
		}
		if err := total.add(m); err != nil {
			return nil, err
		}
	}
	return total, rows.Err()
}

func inventoryTx(ctx context.Context, tx *sql.Tx, asset ledger.Asset) (Inventory, error) {
	rows, err := tx.QueryContext(ctx, `SELECT kind,COALESCE(code,''),balance_sign,balance_mag FROM credit_accounts WHERE asset_type=?`, string(asset))
	if err != nil {
		return Inventory{}, err
	}
	defer rows.Close()
	var positive, negative [4]big.Int
	for rows.Next() {
		var kind, code string
		var sign int
		var raw []byte
		if err := rows.Scan(&kind, &code, &sign, &raw); err != nil {
			return Inventory{}, err
		}
		amount, err := db.NewSM128(sign, raw)
		if err != nil {
			return Inventory{}, ErrInvariant
		}
		if asset.IsActivity() && new(big.Int).Mod(amount.Big(), big.NewInt(1000)).Sign() != 0 {
			return Inventory{}, ErrInvariant
		}
		if kind == "external" {
			continue
		}
		var group int
		switch kind {
		case "user":
			group = 0
		case "pool":
			group = 2
		case "platform":
			group = 3
			if frozenAccount(code) {
				group = 1
			}
		default:
			return Inventory{}, ErrInvariant
		}
		if sign < 0 {
			negative[group].Sub(&negative[group], amount.Big())
		} else {
			positive[group].Add(&positive[group], amount.Big())
		}
	}
	if err := rows.Err(); err != nil {
		return Inventory{}, err
	}
	net := new(big.Int)
	for i := range positive {
		net.Add(net, &positive[i])
		net.Sub(net, &negative[i])
	}
	return Inventory{positive[0].String(), positive[1].String(), positive[2].String(), positive[3].String(), negative[0].String(), negative[1].String(), negative[2].String(), negative[3].String(), net.String()}, nil
}

func channelsWire(values map[dimension]*measures) []Channel {
	out := make([]Channel, 0, len(values))
	for d, m := range values {
		out = append(out, Channel{d.kind, d.source, d.channel, d.channel != "unclassified", m.wire()})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Channel != b.Channel {
			return a.Channel < b.Channel
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.SourceType < b.SourceType
	})
	return out
}

// readOperationEntriesBatch keeps the immutable entry lookup bounded while
// avoiding one query per operation in the administrator ledger view.
func readOperationEntriesBatch(ctx context.Context, tx *sql.Tx, ids []string) (map[string][]OperationEntry, error) {
	out := make(map[string][]OperationEntry, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	query := `SELECT e.operation_id,e.asset_type,e.account_kind_snapshot,a.user_id,e.delta_sign,e.delta_mag
FROM credit_entries e LEFT JOIN credit_accounts a ON a.id=e.account_id
WHERE e.operation_id IN (` + strings.Join(placeholders, ",") + `) ORDER BY e.operation_id,e.line_no`
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var e OperationEntry
		var user sql.NullInt64
		var sign int
		var raw []byte
		if err := rows.Scan(&id, &e.Asset, &e.AccountKind, &user, &sign, &raw); err != nil {
			return nil, err
		}
		amount, err := db.NewSM128(sign, raw)
		if err != nil || !validAsset(e.Asset) {
			return nil, ErrInvariant
		}
		if e.Asset.IsActivity() && new(big.Int).Mod(amount.Big(), big.NewInt(1000)).Sign() != 0 {
			return nil, ErrInvariant
		}
		e.Delta = amount.Big().String()
		if user.Valid {
			value := strconv.FormatInt(user.Int64, 10)
			e.UserID = &value
		}
		entries := out[id]
		if len(entries) >= 256 {
			return nil, ErrInvariant
		}
		out[id] = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, rows.Close()
}
