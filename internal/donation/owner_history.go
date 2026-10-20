package donation

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

const routeOwnerHistory = "/api/donations/{id}/history"
const routeOwnerHistoryBatch = "/api/donations/history/batch"
const ownerHistoryScope = "owner-donation-history"
const ownerHistoryBatchLimit = 100
const ownerHistoryLifetime = 3600

type OwnerHistoryPreference struct {
	ID            string `json:"donation_id"`
	OwnerHiddenAt *int64 `json:"owner_hidden_at"`
}
type OwnerHistoryBatch struct {
	ChangedCount string  `json:"changed_count"`
	ScannedCount string  `json:"scanned_count"`
	NextCursor   *string `json:"next_cursor"`
}
type OwnerHistorySelection struct{ Action, Status, Query, Cursor string }

func ownerHiddenAtTx(ctx context.Context, tx *sql.Tx, id int64) (*int64, error) {
	var value *int64
	err := tx.QueryRowContext(ctx, `SELECT owner_hidden_at FROM donation_owner_preferences WHERE donation_id=?`, id).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return value, err
}

// Read only the current page's private preferences in one statement.
func ownerHiddenPageTx(ctx context.Context, tx *sql.Tx, userID int64, ids []int64) (map[int64]*int64, error) {
	result := make(map[int64]*int64, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT donation_id,owner_hidden_at FROM donation_owner_preferences WHERE user_id=? AND donation_id IN (SELECT value FROM json_each(?))", userID, string(raw))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var hidden *int64
		if err := rows.Scan(&id, &hidden); err != nil {
			return nil, err
		}
		result[id] = hidden
	}
	return result, rows.Err()
}

func setOwnerHiddenTx(ctx context.Context, tx *sql.Tx, userID, id, now int64, hidden bool) (bool, error) {
	previous, err := ownerHiddenAtTx(ctx, tx, id)
	if err != nil {
		return false, err
	}
	if (previous != nil) == hidden {
		return false, nil
	}
	// A fresh row ID records each preference change, including restore. One row
	// per donation bounds storage and prevents old batches absorbing later edits.
	if _, err = tx.ExecContext(ctx, `DELETE FROM donation_owner_preferences WHERE donation_id=? AND user_id=?`, id, userID); err != nil {
		return false, err
	}
	var at any
	if hidden {
		at = now
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO donation_owner_preferences(donation_id,user_id,owner_hidden_at) VALUES(?,?,?)`, id, userID, at)
	return err == nil, err
}
func (s *Service) SetOwnerHistory(ctx context.Context, userID, id int64, hidden bool, mutation resources.ControlMutation) (resources.MutationResult[OwnerHistoryPreference], error) {
	var empty resources.MutationResult[OwnerHistoryPreference]
	if ctx == nil || !validMutation(mutation, http.MethodPut, routeOwnerHistory, id) {
		return empty, ErrInvalidRequest
	}
	tx, err := s.beginOwnerTx(ctx, userID)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	now, err := s.nowUnix()
	if err != nil {
		return empty, err
	}
	decision, err := beginMutation(ctx, tx, "user", userID, idempotency.ScopeDonation, mutation, now)
	if err != nil {
		return empty, err
	}
	if decision.Kind == idempotency.Replay {
		return replay[OwnerHistoryPreference](decision)
	}
	var active bool
	err = tx.QueryRowContext(ctx, `SELECT `+logicallyActiveDonationSQL+` FROM donations d WHERE d.id=? AND d.user_id=? AND (d.status IN ('pending','approved') OR d.terminal_at>?)`, now, id, userID, now-terminalRetention).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, err
	}
	if active {
		return empty, ErrConflict
	}
	if _, err = setOwnerHiddenTx(ctx, tx, userID, id, now, hidden); err != nil {
		return empty, err
	}
	at, err := ownerHiddenAtTx(ctx, tx, id)
	if err != nil {
		return empty, err
	}
	out, err := finishJSON(ctx, tx, decision, http.StatusOK, OwnerHistoryPreference{strconv.FormatInt(id, 10), at})
	if err != nil {
		return empty, err
	}
	return out, tx.Commit()
}

func (s *Service) OwnerHistoryBatch(ctx context.Context, userID int64, input OwnerHistorySelection, mutation resources.ControlMutation) (resources.MutationResult[OwnerHistoryBatch], error) {
	var empty resources.MutationResult[OwnerHistoryBatch]
	if ctx == nil || !validMutation(mutation, http.MethodPost, routeOwnerHistoryBatch) ||
		(input.Action != "hide" && input.Action != "restore") || !validManagementFilter(ManagementFilter{Status: input.Status, Query: input.Query}) ||
		input.Status == "pending" || input.Status == "approved" || len(input.Cursor) > 4096 {
		return empty, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.beginOwnerTx(ctx, userID)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	now, err := s.nowUnix()
	if err != nil {
		return empty, err
	}
	decision, err := beginMutation(ctx, tx, "user", userID, idempotency.ScopeDonation, mutation, now)
	if err != nil {
		return empty, err
	}
	if decision.Kind == idempotency.Replay {
		return replay[OwnerHistoryBatch](decision)
	}
	if nilDependency(s.cursorKeys) {
		return empty, ErrUnavailable
	}
	key, err := s.cursorKeys.DeriveGenerationTwoSubkey([]byte("pagination-cursor/v1"))
	if err != nil || len(key) != 32 {
		clear(key)
		return empty, ErrUnavailable
	}
	defer clear(key)
	owner := fmt.Sprintf("%d:%s:%s:%x", userID, input.Action, input.Status, sha256.Sum256([]byte(input.Query)))
	// Fixed decision time, donation/review/preference high-water marks, then ID.
	bounds := []int64{now, 0, 0, 0, 0}
	if input.Cursor == "" {
		err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT MAX(id) FROM donations WHERE user_id=?),0),COALESCE((SELECT MAX(id) FROM donation_reviews),0),COALESCE((SELECT MAX(id) FROM donation_owner_preferences),0)`, userID).Scan(&bounds[1], &bounds[2], &bounds[3])
		if err != nil {
			return empty, err
		}
	} else {
		cursor, err := db.DecodePaginationCursorWithDerivedKey(key, input.Cursor, ownerHistoryScope, owner, uint64(now))
		if err != nil || len(cursor.Atoms) != 5 {
			return empty, ErrInvalidRequest
		}
		for i, a := range cursor.Atoms {
			if a.Kind != db.CursorUint || a.Uint > math.MaxInt64 {
				return empty, ErrInvalidRequest
			}
			bounds[i] = int64(a.Uint)
		}
		if bounds[0] > now || cursor.Expiry != uint64(bounds[0])+ownerHistoryLifetime || bounds[4] > bounds[1] {
			return empty, ErrInvalidRequest
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM donations WHERE user_id=? AND id>? AND id<=? ORDER BY id LIMIT ?`, userID, bounds[4], bounds[1], ownerHistoryBatchLimit)
	if err != nil {
		return empty, err
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return empty, err
	}
	out := OwnerHistoryBatch{ChangedCount: "0", ScannedCount: strconv.Itoa(len(ids))}
	changed := 0
	for _, id := range ids {
		match, err := ownerHistoryMatches(ctx, tx, userID, id, now, bounds, input)
		if err != nil {
			return empty, err
		}
		if !match {
			continue
		}
		did, err := setOwnerHiddenTx(ctx, tx, userID, id, now, input.Action == "hide")
		if err != nil {
			return empty, err
		}
		if did {
			changed++
		}
	}
	out.ChangedCount = strconv.Itoa(changed)
	if len(ids) > 0 && ids[len(ids)-1] < bounds[1] {
		bounds[4] = ids[len(ids)-1]
		atoms := make([]db.CursorAtom, len(bounds))
		for i, n := range bounds {
			atoms[i] = db.CursorAtom{Kind: db.CursorUint, Uint: uint64(n)}
		}
		cursor, err := db.EncodePaginationCursorWithDerivedKey(key, ownerHistoryScope, owner, uint64(bounds[0])+ownerHistoryLifetime, atoms)
		if err != nil {
			return empty, ErrUnavailable
		}
		out.NextCursor = &cursor
	}
	result, err := finishJSON(ctx, tx, decision, http.StatusOK, out)
	if err != nil {
		return empty, err
	}
	return result, tx.Commit()
}

func ownerHistoryMatches(ctx context.Context, tx *sql.Tx, userID, id, now int64, bounds []int64, input OwnerHistorySelection) (bool, error) {
	// Every non-expiry transition has an append-only review in the same write
	// transaction. A later expiry may only materialize what was already expired
	// at the fixed cutoff; it must not bring a newly expired key into this run.
	query := `SELECT EXISTS(SELECT 1 FROM donations d LEFT JOIN donation_owner_preferences p ON p.donation_id=d.id
 WHERE d.id=? AND d.user_id=? AND (d.status IN ('pending','approved') OR d.terminal_at>?)
 AND (p.id IS NULL OR p.id<=?)
 AND NOT EXISTS(SELECT 1 FROM donation_reviews r WHERE r.donation_id=d.id AND r.id>? AND r.action<>'expire')
 AND (d.status IN ('rejected','deleted') OR NOT EXISTS(
 SELECT 1 FROM donation_keys k WHERE k.donation_id=d.id
 AND (k.ended_at IS NULL OR k.ended_reason='expired')
 AND (k.expires_at IS NULL OR k.expires_at>?)))`
	args := []any{id, userID, now - terminalRetention, bounds[3], bounds[2], bounds[0]}
	if input.Action == "hide" {
		query += ` AND p.owner_hidden_at IS NULL`
	} else {
		query += ` AND p.owner_hidden_at IS NOT NULL`
	}
	query, args = appendManagementFilter(query, args, ManagementFilter{Status: input.Status, Query: input.Query}, bounds[0])
	query += `)`
	var match bool
	err := tx.QueryRowContext(ctx, query, args...).Scan(&match)
	return match, err
}

func validOwnerView(view string) bool {
	return view == "" || view == "all" || view == "current" || view == "history" || view == "hidden"
}
func appendOwnerView(query string, args []any, view string, now int64) (string, []any) {
	switch view {
	case "current":
		query += ` AND ` + logicallyActiveDonationSQL
		args = append(args, now)
	case "history", "hidden":
		query += ` AND NOT ` + logicallyActiveDonationSQL
		args = append(args, now)
		exists := `EXISTS(SELECT 1 FROM donation_owner_preferences p WHERE p.donation_id=d.id AND p.owner_hidden_at IS NOT NULL)`
		if view == "history" {
			query += ` AND NOT ` + exists
		} else {
			query += ` AND ` + exists
		}
	}
	return query, args
}
