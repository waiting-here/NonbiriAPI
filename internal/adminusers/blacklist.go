package adminusers

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/antiabuse"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"github.com/waiting-here/NonbiriAPI/internal/useractivity"
)

type BlacklistEntry struct {
	DiscordID string  `json:"discord_id"`
	Reason    string  `json:"reason"`
	CreatedAt int64   `json:"created_at"`
	UserID    *string `json:"user_id"`
}

func validDiscordID(value string) bool {
	id, err := strconv.ParseUint(value, 10, 64)
	return err == nil && id > 0 && strconv.FormatUint(id, 10) == value
}

func (s *Service) ListBlacklist(ctx context.Context, adminID int64, q string, page pagination.Request) (Page[BlacklistEntry], error) {
	empty := Page[BlacklistEntry]{Data: []BlacklistEntry{}}
	if !page.Valid() || q != "" && !validDiscordID(q) {
		return empty, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.beginListRead(ctx, adminID, true)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	selection := `SELECT b.discord_id,b.reason,b.created_at,u.id FROM discord_blacklist b LEFT JOIN users u ON u.discord_id=b.discord_id`
	args := []any{}
	if q != "" {
		selection += " WHERE b.discord_id=?"
		args = append(args, q)
	}
	statement, args, meta, err := listPageQuery(ctx, tx, selection, " ORDER BY b.created_at DESC,b.discord_id DESC", args, &page, page.Size)
	if err != nil {
		return empty, err
	}
	rows, err := tx.QueryContext(ctx, statement, args...)
	if err != nil {
		return empty, err
	}
	result := Page[BlacklistEntry]{Data: []BlacklistEntry{}, Pagination: meta}
	for rows.Next() {
		var entry BlacklistEntry
		var id sql.NullInt64
		if err = rows.Scan(&entry.DiscordID, &entry.Reason, &entry.CreatedAt, &id); err != nil {
			rows.Close()
			return empty, err
		}
		if id.Valid {
			value := strconv.FormatInt(id.Int64, 10)
			entry.UserID = &value
		}
		result.Data = append(result.Data, entry)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return empty, err
	}
	if err = rows.Close(); err != nil {
		return empty, err
	}
	if err = tx.Commit(); err != nil {
		return empty, err
	}
	return result, nil
}

// The list update and any existing account revocation share one transaction.
// Removal only permits registration again; it never implicitly unbans an account.
func (s *Service) SetBlacklist(ctx context.Context, adminID int64, control ControlMutation, discordID, reason string, add bool) (MutationResult[struct{}], error) {
	empty := MutationResult[struct{}]{}
	if !validDiscordID(discordID) || add && !validReason(reason) {
		return empty, ErrInvalidRequest
	}
	now := s.now().Unix()
	if !validNow(now) {
		return empty, ErrUnavailable
	}
	tx, err := s.beginAuthorized(ctx, adminID)
	if err != nil {
		return empty, err
	}
	done := false
	defer rollbackUnlessDone(tx, &done)
	decision, err := beginControlMutation(ctx, tx, adminID, roleAdmin, control, now)
	if err != nil {
		return empty, err
	}
	if decision.Kind == idempotency.Replay {
		if err = tx.Commit(); err != nil {
			return empty, err
		}
		done = true
		return MutationResult[struct{}]{Status: decision.HTTPStatus, Body: append([]byte(nil), decision.ResponseBody...), Replayed: true}, nil
	}
	var targetID int64
	if add {
		var admin int
		err = tx.QueryRowContext(ctx, `SELECT id,is_admin FROM users WHERE discord_id=?`, discordID).Scan(&targetID, &admin)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return empty, err
		}
		if admin != 0 {
			return empty, ErrForbidden
		}
		if targetID != 0 {
			row, err := readUserRow(ctx, tx, targetID)
			if err != nil {
				return empty, err
			}
			revision, err := db.DecodeU128(row.revision)
			if err != nil {
				return empty, err
			}
			next, err := incrementU128(revision)
			if err != nil {
				return empty, ErrResourceLimit
			}
			if s.cancelUserDuelsTx != nil {
				finalize, err := s.cancelUserDuelsTx(ctx, tx, targetID, "account_unavailable", now)
				if err != nil {
					return empty, err
				}
				if finalize == nil {
					return empty, ErrInvariant
				}
				defer func() { finalize(done) }()
			}
			result, err := tx.ExecContext(ctx, `UPDATE users SET is_banned=1,banned_reason=?,banned_until=NULL,auto_banned=0,ban_kind='',revision=?,updated_at=? WHERE id=? AND is_admin=0 AND revision=?`, reason, db.EncodeU128(next), now, targetID, row.revision)
			if err != nil {
				return empty, err
			}
			if n, err := result.RowsAffected(); err != nil || n != 1 {
				return empty, ErrConflict
			}
			if err = useractivity.RescheduleTx(ctx, tx, targetID); err != nil {
				return empty, err
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, targetID); err != nil {
				return empty, err
			}
			result, err = tx.ExecContext(ctx, `UPDATE caller_keys SET generation=CASE WHEN key_hash IS NULL THEN generation ELSE generation+1 END,key_hash=NULL,display_head='',display_tail='',key_created_at=NULL,updated_at=? WHERE user_id=? AND (key_hash IS NULL OR generation<?)`, now, targetID, int64(math.MaxInt64))
			if err != nil {
				return empty, err
			}
			if n, err := result.RowsAffected(); err != nil || n != 1 {
				return empty, ErrInvariant
			}
			if err = antiabuse.EndAutomaticTx(ctx, tx, targetID, "ban", adminID, now, true, nil); err != nil {
				return empty, err
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO discord_blacklist(discord_id,reason,created_at) VALUES(?,?,?) ON CONFLICT(discord_id) DO UPDATE SET reason=excluded.reason`, discordID, reason, now)
	} else {
		_, err = tx.ExecContext(ctx, `DELETE FROM discord_blacklist WHERE discord_id=?`, discordID)
	}
	if err != nil {
		return empty, err
	}
	if err = idempotency.Complete(ctx, tx, decision, http.StatusNoContent, []byte{}); err != nil {
		return empty, err
	}
	if err = tx.Commit(); err != nil {
		return empty, err
	}
	done = true
	if targetID != 0 {
		s.invalidator.InvalidateUserAuthority(targetID)
	}
	return MutationResult[struct{}]{Status: http.StatusNoContent, Body: []byte{}}, nil
}
