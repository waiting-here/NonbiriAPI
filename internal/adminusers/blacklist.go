package adminusers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/antiabuse"
	"github.com/waiting-here/NonbiriAPI/internal/blacklist"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"github.com/waiting-here/NonbiriAPI/internal/useractivity"
)

type BlacklistEntry struct {
	DiscordID        string  `json:"discord_id"`
	Reason           string  `json:"reason"`
	CreatedAt        int64   `json:"created_at"`
	UserID           *string `json:"user_id"`
	FirstActorKind   string  `json:"first_actor_kind"`
	FirstActorUserID *string `json:"first_actor_user_id"`
}

func validDiscordID(value string) bool {
	return blacklist.ValidDiscordID(value)
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
	return s.setBlacklist(ctx, adminID, roleAdmin, control, discordID, reason, add)
}

func (s *Service) setBlacklist(ctx context.Context, adminID int64, role managementRole, control ControlMutation, discordID, reason string, add bool) (MutationResult[struct{}], error) {
	empty := MutationResult[struct{}]{}
	if !validDiscordID(discordID) {
		return empty, ErrInvalidRequest
	}
	if role == roleSteward && !add {
		return empty, ErrForbidden
	}
	if add {
		var err error
		reason, err = blacklist.NormalizeNote(reason)
		if err != nil {
			return empty, ErrInvalidRequest
		}
	}
	now := s.now().Unix()
	if !validNow(now) {
		return empty, ErrUnavailable
	}
	if ctx == nil {
		return empty, ErrUnauthorized
	}
	preflight, err := s.beginManagement(ctx, adminID, role, true)
	if err != nil {
		return empty, err
	}
	if add {
		_, err = blacklistTargetTx(ctx, preflight, role, discordID)
	}
	if err != nil {
		preflight.Rollback()
		return empty, err
	}
	if err := preflight.Commit(); err != nil {
		return empty, classifyDatabaseError("commit blacklist authorization", err)
	}
	release, err := s.lockIdentity(ctx, discordID)
	if err != nil {
		return empty, err
	}
	defer release()
	tx, err := s.beginManagement(ctx, adminID, role, false)
	if err != nil {
		return empty, err
	}
	done := false
	defer rollbackUnlessDone(tx, &done)
	decision, err := beginControlMutation(ctx, tx, adminID, role, control, now)
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
		targetID, err = blacklistTargetTx(ctx, tx, role, discordID)
		if err != nil {
			return empty, err
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
			banReason := reason
			if utf8.RuneCountInString(banReason) > 1024 {
				banReason = string([]rune(banReason)[:1024])
			}
			result, err := tx.ExecContext(ctx, `UPDATE users SET is_banned=1,banned_reason=?,banned_until=NULL,auto_banned=0,ban_kind='',revision=?,updated_at=? WHERE id=? AND is_admin=0 AND revision=?`, banReason, db.EncodeU128(next), now, targetID, row.revision)
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
		kind := blacklist.Admin
		if role == roleSteward {
			kind = blacklist.Steward6
		}
		_, err = blacklist.AppendTx(ctx, tx, blacklist.Event{
			DiscordID: discordID, OperationKey: fmt.Sprintf("manual:%s:%d:%s", role.actorKind(), adminID, control.IdempotencyKey),
			ActorKind: kind, ActorUserID: &adminID, Note: reason, At: now,
		})
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

func blacklistTargetTx(ctx context.Context, tx *sql.Tx, role managementRole, discordID string) (int64, error) {
	var targetID int64
	var admin, level int
	err := tx.QueryRowContext(ctx, `SELECT id,is_admin,COALESCE(level,auto_level) FROM users WHERE discord_id=?`, discordID).Scan(&targetID, &admin, &level)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if admin != 0 || role == roleSteward && level >= 6 {
		return 0, ErrForbidden
	}
	return targetID, nil
}
