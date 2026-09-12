package donation

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const failureSelectionScope = "donation-failure-reset-selection"
const failureSelectionLifetime = 3600

type failureSelection struct {
	View       string
	DonationID int64
	SourceKey  string
	Management ManagementFilter
	Source     SourceFilter
}
type FailureResetSelection struct {
	Items      []FailureResetRef `json:"items"`
	NextCursor *string           `json:"next_cursor"`
}

func (s *Service) selectFailureReset(ctx context.Context, role reviewerRole, userID int64, selection failureSelection, token string) (FailureResetSelection, error) {
	var empty FailureResetSelection
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, actorID, err := s.beginBrowseActorTx(ctx, role, userID)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	now, err := s.nowUnix()
	if err != nil {
		return empty, err
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
	canonical, err := json.Marshal(selection)
	if err != nil {
		return empty, err
	}
	owner := fmt.Sprintf("%s:%d:%x", role, actorID, sha256.Sum256(canonical))
	started, maximum, after := now, int64(0), int64(0)
	if token == "" {
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM donation_keys`).Scan(&maximum)
		if err != nil {
			return empty, err
		}
	} else {
		cursor, err := db.DecodePaginationCursorWithDerivedKey(key, token, failureSelectionScope, owner, uint64(now))
		if err != nil || len(cursor.Atoms) != 3 {
			return empty, ErrInvalidRequest
		}
		for _, atom := range cursor.Atoms {
			if atom.Kind != db.CursorUint || atom.Uint > math.MaxInt64 {
				return empty, ErrInvalidRequest
			}
		}
		started, maximum, after = int64(cursor.Atoms[0].Uint), int64(cursor.Atoms[1].Uint), int64(cursor.Atoms[2].Uint)
		if started > now || cursor.Expiry != uint64(started)+failureSelectionLifetime || after > maximum {
			return empty, ErrInvalidRequest
		}
	}
	if selection.View == "donation_keys" {
		visible, err := donationOrdinarilyVisibleTx(ctx, tx, selection.DonationID, now)
		if err != nil {
			return empty, err
		}
		if !visible {
			visible, err = s.managementHeldRead(ctx, tx, role, actorID, selection.DonationID, now)
			if err != nil {
				return empty, err
			}
		}
		if !visible {
			return empty, ErrNotFound
		}
	}
	// Apply the row budget before applying mutable filters. Even an empty match
	// advances by at most 100 candidate IDs, without OFFSET or a long snapshot.
	rows, err := tx.QueryContext(ctx, `SELECT id FROM donation_keys WHERE id>? AND id<=? ORDER BY id LIMIT ?`, after, maximum, maxFailureResetItems)
	if err != nil {
		return empty, err
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return empty, err
	}
	out := FailureResetSelection{Items: []FailureResetRef{}}
	if len(ids) == 0 {
		return out, nil
	}
	query, args := failureSelectionSQL(selection, now, ids)
	rows, err = tx.QueryContext(ctx, query, args...)
	if err != nil {
		return empty, err
	}
	for rows.Next() {
		var donationID, keyID, revision int64
		if err := rows.Scan(&donationID, &keyID, &revision); err != nil {
			rows.Close()
			return empty, err
		}
		out.Items = append(out.Items, failureResetRef(donationID, keyID, revision))
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return empty, err
	}
	after = ids[len(ids)-1]
	if after < maximum {
		cursor, err := db.EncodePaginationCursorWithDerivedKey(key, failureSelectionScope, owner, uint64(started)+failureSelectionLifetime,
			[]db.CursorAtom{{Kind: db.CursorUint, Uint: uint64(started)}, {Kind: db.CursorUint, Uint: uint64(maximum)}, {Kind: db.CursorUint, Uint: uint64(after)}})
		if err != nil {
			return empty, ErrUnavailable
		}
		out.NextCursor = &cursor
	}
	return out, nil
}

func failureSelectionSQL(selection failureSelection, now int64, ids []int64) (string, []any) {
	if selection.View == "sources" || selection.View == "source_keys" {
		query, args := sourceSelectionSQL(selection.Source, nil, now, ids...)
		query = `SELECT donation_id,id,revision FROM (` + query + `)`
		if selection.View == "source_keys" {
			query += ` WHERE nbi_donation_source(NULLIF(source_channel,''),source_connector,source_url)=?`
			args = append(args, selection.SourceKey)
		}
		return query + ` ORDER BY id`, args
	}
	query := `SELECT d.id,dk.id,d.revision FROM donation_keys dk CROSS JOIN donations d ON d.id=dk.donation_id
 JOIN donation_handling h ON h.donation_id=d.id WHERE dk.id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + `)`
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	if selection.View == "donation_keys" {
		query += ` AND d.id=?`
		args = append(args, selection.DonationID)
	} else {
		query += ` AND (d.status IN ('pending','approved') OR d.terminal_at>?)`
		args = append(args, now-terminalRetention)
		query, args = appendManagementFilter(query, args, selection.Management, now)
	}
	return query + ` ORDER BY dk.id`, args
}
