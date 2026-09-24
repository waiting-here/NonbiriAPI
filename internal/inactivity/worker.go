package inactivity

import (
	"context"
	"database/sql"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"math"
	"math/big"
	"time"
)

type accountState struct {
	state                ActivityState
	id                   int64
	admin, level, banned int
	until                *int64
	banKind              string
	revision             []byte
}

func readAccount(ctx context.Context, tx *sql.Tx, userID int64) (accountState, error) {
	var a accountState
	err := tx.QueryRowContext(ctx, `SELECT u.id,u.is_admin,COALESCE(u.level,u.auto_level),u.is_banned,u.banned_until,u.ban_kind,u.revision,s.observation_started_at,s.last_active_at,s.activity_seq,s.activity_epoch,s.schedule_revision,s.next_due_at,s.last_decay_at FROM users u JOIN user_activity_state s ON s.user_id=u.id WHERE u.id=?`, userID).Scan(&a.id, &a.admin, &a.level, &a.banned, &a.until, &a.banKind, &a.revision, &a.state.ObservationStartedAt, &a.state.LastActiveAt, &a.state.ActivitySeq, &a.state.ActivityEpoch, &a.state.ScheduleRevision, &a.state.NextDueAt, &a.state.LastDecayAt)
	return a, err
}
func status(c Configuration, a accountState, at int64) Status {
	out := Status{Configuration: c, State: a.state}
	switch {
	case a.admin != 0:
		out.ExemptReason = "administrator"
	case a.level == 5 || a.level == 6:
		out.ExemptReason = "steward"
	case a.banned != 0 && (a.until == nil || *a.until > at):
		out.ExemptReason = "banned"
	case !c.Enabled:
		out.ExemptReason = "disabled"
	default:
		out.DecayAt, out.ProtectionAt = due(c, a.state)
	}
	return out
}
func writeSchedule(ctx context.Context, tx *sql.Tx, a accountState, c Configuration, at int64) error {
	view := status(c, a, at)
	next := earliest(view.DecayAt, view.ProtectionAt)
	if view.ExemptReason == "banned" && a.until != nil {
		next = a.until
	}
	_, err := tx.ExecContext(ctx, `UPDATE user_activity_state SET schedule_revision=?,next_due_at=?,last_decay_at=? WHERE user_id=?`, c.Revision, next, a.state.LastDecayAt, a.id)
	return err
}

type BatchResult struct {
	Processed int  `json:"processed"`
	Decayed   int  `json:"decayed"`
	Protected int  `json:"protected"`
	More      bool `json:"more"`
}

// Process has no goroutine or timer of its own. Root lifecycle schedules one
// bounded pass per minute. Each account commits its receipt and money together.
func (s *Service) Process(ctx context.Context, at int64, limit int, deadline time.Time) (BatchResult, error) {
	var result BatchResult
	if !validTime(at) || limit < 1 || limit > 100 || deadline.IsZero() {
		return result, ErrInvalid
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	var revision int64
	if err := s.db.QueryRowContext(ctx, `SELECT revision FROM inactivity_policy WHERE id=1`).Scan(&revision); err != nil {
		return result, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT user_id FROM (SELECT user_id FROM (SELECT user_id FROM user_activity_state WHERE schedule_revision<? ORDER BY schedule_revision,user_id LIMIT ?) UNION SELECT user_id FROM (SELECT user_id FROM user_activity_state WHERE next_due_at<=? ORDER BY next_due_at,user_id LIMIT ?)) ORDER BY user_id LIMIT ?`, revision, limit+1, at, limit+1, limit+1)
	if err != nil {
		return result, err
	}
	ids := make([]int64, 0, limit+1)
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return result, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return result, err
	}
	if err = rows.Close(); err != nil {
		return result, err
	}
	result.More = len(ids) > limit
	if len(ids) > limit {
		ids = ids[:limit]
	}
	for _, id := range ids {
		action, err := s.processOne(ctx, id, at)
		if err != nil {
			result.More = true
			return result, err
		}
		result.Processed++
		if action == "decay" {
			result.Decayed++
		}
		if action == "protection" {
			result.Protected++
		}
	}
	return result, nil
}
func (s *Service) processOne(ctx context.Context, userID, at int64) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE inactivity_policy SET revision=revision WHERE id=1`); err != nil {
		return "", err
	}
	c, err := readPolicy(ctx, tx)
	if err != nil {
		return "", err
	}
	a, err := readAccount(ctx, tx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	view := status(c, a, at)
	if view.ProtectionAt != nil && *view.ProtectionAt <= at {
		finalize, err := s.cancelUser(ctx, tx, userID, "account_unavailable", at)
		if err != nil {
			return "", err
		}
		if finalize == nil {
			return "", ErrInvalid
		}
		committed := false
		defer func() { finalize(committed) }()
		if err = protect(ctx, tx, a, at); err != nil {
			return "", err
		}
		id, err := db.GenerateOpaqueID("op_")
		if err != nil {
			return "", err
		}
		if err = recordRun(ctx, tx, id, a, c, *view.ProtectionAt, "protection", ledger.Payment{}, nil, at); err != nil {
			return "", err
		}
		a.banned = 1
		a.until = nil
		a.banKind = "protective_inactivity"
		if err = writeSchedule(ctx, tx, a, c, at); err != nil {
			return "", err
		}
		if err = tx.Commit(); err != nil {
			return "", err
		}
		committed = true
		s.invalidator.InvalidateUserAuthority(userID)
		return "protection", nil
	}
	if view.DecayAt != nil && *view.DecayAt <= at {
		wallets, amount, err := decayAmount(ctx, tx, userID, c.Decay.Assets)
		if err != nil {
			return "", err
		}
		id, err := db.GenerateOpaqueID("op_")
		if err != nil {
			return "", err
		}
		var operation *string
		if !amount.General.IsZero() || !amount.Game.IsZero() {
			operationID, err := db.GenerateOpaqueID("op_")
			if err != nil {
				return "", err
			}
			general, err := ledger.CodedAssetAccount(ctx, tx, "external", ledger.General)
			if err != nil {
				return "", err
			}
			game, err := ledger.CodedAssetAccount(ctx, tx, "external", ledger.Game)
			if err != nil {
				return "", err
			}
			plan, err := ledger.NewInactivityDecay(ledger.Meta{OperationID: operationID, CreatedAt: at}, wallets, ledger.AccountPair{General: general.ID, Game: game.ID}, amount)
			if err != nil {
				return "", err
			}
			posted, err := ledger.Apply(ctx, tx, plan)
			if err != nil {
				return "", err
			}
			operation = &posted.OperationID
		}
		if err = recordRun(ctx, tx, id, a, c, *view.DecayAt, "decay", amount, operation, at); err != nil {
			return "", err
		}
		a.state.LastDecayAt = &at
		if err = writeSchedule(ctx, tx, a, c, at); err != nil {
			return "", err
		}
		return "decay", tx.Commit()
	}
	if err = writeSchedule(ctx, tx, a, c, at); err != nil {
		return "", err
	}
	return "", tx.Commit()
}
func decayAmount(ctx context.Context, tx *sql.Tx, userID int64, rules Assets) (ledger.AccountPair, ledger.Payment, error) {
	general, err := ledger.UserAssetAccount(ctx, tx, userID, ledger.General)
	if err != nil {
		return ledger.AccountPair{}, ledger.Payment{}, err
	}
	game, err := ledger.UserAssetAccount(ctx, tx, userID, ledger.Game)
	if err != nil {
		return ledger.AccountPair{}, ledger.Payment{}, err
	}
	g, err := charge(general.Balance, rules.General)
	if err != nil {
		return ledger.AccountPair{}, ledger.Payment{}, err
	}
	m, err := charge(game.Balance, rules.Game)
	return ledger.AccountPair{General: general.ID, Game: game.ID}, ledger.Payment{General: g, Game: m}, err
}
func protect(ctx context.Context, tx *sql.Tx, a accountState, at int64) error {
	old, err := db.DecodeU128(a.revision)
	if err != nil {
		return err
	}
	next, err := db.U128FromBig(new(big.Int).Add(old.Big(), big.NewInt(1)))
	if err != nil {
		return err
	}
	changed, err := tx.ExecContext(ctx, `UPDATE users SET is_banned=1,ban_kind='protective_inactivity',banned_reason='Account protected due to inactivity',banned_until=NULL,auto_banned=0,revision=?,updated_at=? WHERE id=? AND is_admin=0 AND revision=?`, db.EncodeU128(next), at, a.id, a.revision)
	if err != nil {
		return err
	}
	n, err := changed.RowsAffected()
	if err != nil || n != 1 {
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, a.id); err != nil {
		return err
	}
	changed, err = tx.ExecContext(ctx, `UPDATE caller_keys SET generation=CASE WHEN key_hash IS NULL THEN generation ELSE generation+1 END,key_hash=NULL,display_head='',display_tail='',key_created_at=NULL,updated_at=? WHERE user_id=? AND (key_hash IS NULL OR generation<?)`, at, a.id, int64(math.MaxInt64))
	if err != nil {
		return err
	}
	n, err = changed.RowsAffected()
	if err != nil || n != 1 {
		return ErrConflict
	}
	return nil
}
func recordRun(ctx context.Context, tx *sql.Tx, id string, a accountState, c Configuration, slot int64, action string, amount ledger.Payment, operation *string, at int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO inactivity_runs(id,user_id,policy_revision,activity_epoch,due_slot,action,general_milli,game_milli,ledger_operation_id,created_at,deidentify_at,retain_until) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, id, a.id, c.Revision, a.state.ActivityEpoch, slot, action, amount.General.Decimal(), amount.Game.Decimal(), operation, at, at+identityLife, at+auditLife)
	return err
}
