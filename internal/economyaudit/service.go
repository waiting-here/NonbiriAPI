package economyaudit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func New(config Config) (*Service, error) {
	if config.Database == nil || config.FinalAuth == nil || config.CursorKeys == nil {
		return nil, ErrInvalid
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Service{config.Database, config.FinalAuth, config.CursorKeys, config.Now}, nil
}

func (s *Service) Advance(ctx context.Context) (bool, error) {
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	ready, err := AdvanceTx(ctx, tx, s.now().Unix())
	if err != nil {
		return false, err
	}
	return ready, tx.Commit()
}

func (s *Service) Run(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		batch, cancel := context.WithTimeout(ctx, 2*time.Second)
		ready, err := s.Advance(batch)
		cancel()
		delay := time.Second
		if err != nil {
			delay = 30 * time.Second
		} else if !ready {
			delay = 25 * time.Millisecond
		}
		timer.Reset(delay)
	}
}

func validWord(s string, max int) bool {
	if len(s) == 0 || len(s) > max {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}

func validateFilter(f Filter) error {
	if !validAsset(f.Asset) || f.From < 0 || f.To <= f.From || f.To > maxUnix || f.Bucket != "hour" && f.Bucket != "day" || f.Kind != "" && !validWord(f.Kind, 64) || len(f.Cursor) > 2048 {
		return ErrInvalid
	}
	if f.Channel != "" && (f.Kind == "" || !validWord(f.Channel, 32)) {
		return ErrInvalid
	}
	return nil
}

func (s *Service) read(ctx context.Context, admin int64, f Filter, fn func(context.Context, *sql.Tx, checkpoint, int64) error) error {
	if s == nil || admin <= 0 || validateFilter(f) != nil {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.auth.AuthorizeAdmin(ctx, tx, admin); err != nil {
		return err
	}
	now := s.now().Unix()
	if _, err := AdvanceTx(ctx, tx, now); err != nil {
		return err
	}
	c, err := readCheckpoint(ctx, tx)
	if err != nil {
		return err
	}
	if err := fn(ctx, tx, c, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) Summary(ctx context.Context, admin int64, f Filter) (Summary, error) {
	out := Summary{Flows: zeroMetrics(), Reconciliation: Reconciliation{Status: "catching_up", Scope: "retained_ledger"}}
	if f.Cursor != "" {
		return out, ErrInvalid
	}
	err := s.read(ctx, admin, f, func(ctx context.Context, tx *sql.Tx, c checkpoint, now int64) error {
		out.Metadata = metadata(c, f, now)
		if c.last != c.head {
			return nil
		}
		values, err := rangeMeasures(ctx, tx, c, f)
		if err != nil {
			return err
		}
		m, err := sumDimensions(values)
		if err != nil {
			return err
		}
		out.Flows = m.wire()
		stock, err := inventoryTx(ctx, tx, f.Asset)
		if err != nil {
			return err
		}
		out.Inventory = &stock
		all, err := allTimeMeasures(ctx, tx, f.Asset, c.offset)
		if err != nil {
			return err
		}
		ledgerNet := new(big.Int).Sub(&all.issued, &all.reclaimed).String()
		out.Reconciliation.InventoryNet = &stock.Net
		out.Reconciliation.LedgerNet = &ledgerNet
		if !c.opening {
			out.Reconciliation.Status = "opening_unknown"
			return nil
		}
		out.Reconciliation.Status = "matched"
		if stock.Net != ledgerNet {
			out.Reconciliation.Status = "mismatch"
		} else if c.unknown > 0 {
			out.Reconciliation.Status = "unclassified"
		}
		// Interval stock is a net quantity for the whole selected asset, not a
		// channel-filtered balance. Financial categories never rewrite stocks.
		before := f
		before.From = 0
		before.To = f.From
		before.Kind = ""
		before.Channel = ""
		opening := &measures{}
		if before.To > 0 {
			values, err := rangeMeasures(ctx, tx, c, before)
			if err != nil {
				return err
			}
			opening, err = sumDimensions(values)
			if err != nil {
				return err
			}
		}
		change := m
		if f.Kind != "" {
			interval := f
			interval.Kind = ""
			interval.Channel = ""
			values, err = rangeMeasures(ctx, tx, c, interval)
			if err != nil {
				return err
			}
			change, err = sumDimensions(values)
			if err != nil {
				return err
			}
		}
		a := new(big.Int).Sub(&opening.issued, &opening.reclaimed)
		delta := new(big.Int).Sub(&change.issued, &change.reclaimed)
		b := new(big.Int).Add(a, delta)
		as, bs, ds := a.String(), b.String(), delta.String()
		out.Reconciliation.IntervalOpeningNet = &as
		out.Reconciliation.IntervalClosingNet = &bs
		out.Reconciliation.IntervalNetChange = &ds
		return nil
	})
	return out, err
}

func (s *Service) Series(ctx context.Context, admin int64, f Filter) (Series, error) {
	out := Series{Bucket: f.Bucket, Data: []Point{}}
	if f.Cursor != "" {
		return out, ErrInvalid
	}
	err := s.read(ctx, admin, f, func(ctx context.Context, tx *sql.Tx, c checkpoint, now int64) error {
		out.Metadata = metadata(c, f, now)
		step, limit := int64(3600), int64(744)
		if f.Bucket == "day" {
			step, limit = 86400, 366
		}
		start := bucketStart(f.From, c.offset, step)
		last := bucketStart(f.To-1, c.offset, step)
		if (last-start)/step+1 > limit {
			return ErrInvalid
		}
		if c.last != c.head {
			return nil
		}
		for at := start; at < f.To; at += step {
			part := f
			part.From = max(f.From, at)
			part.To = min(f.To, at+step)
			values, err := rangeMeasures(ctx, tx, c, part)
			if err != nil {
				return err
			}
			m, err := sumDimensions(values)
			if err != nil {
				return err
			}
			out.Data = append(out.Data, Point{part.From, part.To, m.wire()})
		}
		return nil
	})
	return out, err
}

func (s *Service) Channels(ctx context.Context, admin int64, f Filter) (Channels, error) {
	out := Channels{Data: []Channel{}}
	if f.Cursor != "" {
		return out, ErrInvalid
	}
	err := s.read(ctx, admin, f, func(ctx context.Context, tx *sql.Tx, c checkpoint, now int64) error {
		out.Metadata = metadata(c, f, now)
		if c.last != c.head {
			return nil
		}
		values, err := rangeMeasures(ctx, tx, c, f)
		if err != nil {
			return err
		}
		out.Data = channelsWire(values)
		return nil
	})
	return out, err
}

func (s *Service) Operations(ctx context.Context, admin int64, f Filter) (Operations, error) {
	out := Operations{Data: []Operation{}}
	err := s.read(ctx, admin, f, func(ctx context.Context, tx *sql.Tx, c checkpoint, now int64) error {
		out.Metadata = metadata(c, f, now)
		head, before := c.head, int64(0)
		if f.Cursor != "" {
			var err error
			head, before, err = s.decodeCursor(f.Cursor, admin, f, now)
			if err != nil {
				return err
			}
			if head > c.head {
				return ErrInvalid
			}
		}
		out.AnchorSeq = strconv.FormatInt(head, 10)
		query := `SELECT o.id,o.ledger_seq,o.kind,o.source_type,o.source_id,o.created_at FROM credit_operations o WHERE o.ledger_seq<=? AND o.created_at>=? AND o.created_at<? AND EXISTS(SELECT 1 FROM credit_entries e WHERE e.operation_id=o.id AND e.asset_type=?)`
		args := []any{head, f.From, f.To, string(f.Asset)}
		if before > 0 {
			query += ` AND o.ledger_seq<?`
			args = append(args, before)
		}
		if f.Kind != "" {
			query += ` AND o.kind=?`
			args = append(args, f.Kind)
		}
		query += operationChannelCondition(f, "o.source_id")
		query += ` ORDER BY o.ledger_seq DESC LIMIT 101`
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
		more := len(batch) > 100
		if more {
			batch = batch[:100]
		}
		for _, o := range batch {
			entries, err := readOperationEntries(ctx, tx, o.id)
			if err != nil {
				return err
			}
			out.Data = append(out.Data, Operation{o.id, strconv.FormatInt(o.seq, 10), o.kind, o.source, o.sourceID, o.at, ledger.ClassifyForAudit(ledger.Kind(o.kind), o.sourceID), entries})
		}
		if more {
			token, err := s.encodeCursor(admin, f, now, head, batch[len(batch)-1].seq)
			if err != nil {
				return err
			}
			out.NextCursor = &token
		}
		return nil
	})
	return out, err
}

func cursorOwner(admin int64, f Filter) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d/%s/%d/%d/%s/%s/%s", admin, f.Asset, f.From, f.To, f.Bucket, f.Kind, f.Channel)))
	return hex.EncodeToString(sum[:])
}

func (s *Service) cursorKey() ([]byte, error) {
	key, err := s.keys.DeriveGenerationTwoSubkey([]byte("pagination-cursor/v1"))
	if err != nil || len(key) != 32 {
		clear(key)
		return nil, ErrUnavailable
	}
	return key, nil
}

func (s *Service) encodeCursor(admin int64, f Filter, now, head, before int64) (string, error) {
	if now < 0 || now > maxUnix-900 {
		return "", ErrUnavailable
	}
	key, err := s.cursorKey()
	if err != nil {
		return "", err
	}
	defer clear(key)
	return db.EncodePaginationCursorWithDerivedKey(key, "economy-audit/operations", cursorOwner(admin, f), uint64(now+900), []db.CursorAtom{{Kind: db.CursorUint, Uint: uint64(head)}, {Kind: db.CursorUint, Uint: uint64(before)}})
}

func (s *Service) decodeCursor(token string, admin int64, f Filter, now int64) (int64, int64, error) {
	key, err := s.cursorKey()
	if err != nil {
		return 0, 0, err
	}
	defer clear(key)
	value, err := db.DecodePaginationCursorWithDerivedKey(key, token, "economy-audit/operations", cursorOwner(admin, f), uint64(now))
	if err != nil || len(value.Atoms) != 2 || value.Atoms[0].Kind != db.CursorUint || value.Atoms[1].Kind != db.CursorUint || value.Atoms[0].Uint > math.MaxInt64 || value.Atoms[1].Uint == 0 || value.Atoms[1].Uint > value.Atoms[0].Uint {
		return 0, 0, ErrInvalid
	}
	return int64(value.Atoms[0].Uint), int64(value.Atoms[1].Uint), nil
}
