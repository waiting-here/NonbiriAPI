package adminusers

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/waiting-here/NonbiriAPI/internal/antiabuse"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/useractivity"
	"math"
	"strconv"
)

func (service *Service) applyProfileTx(ctx context.Context, tx *sql.Tx, row userRow, userID int64, input ProfileMutation, now int64) (AdminUser, error) {
	if !equalU128Bytes(row.revision, input.ExpectedRevision) {
		return AdminUser{}, ErrConflict
	}
	next, err := incrementU128(input.ExpectedRevision)
	if err != nil {
		return AdminUser{}, ErrResourceLimit
	}
	result, err := tx.ExecContext(ctx, `
UPDATE users SET
 endpoint_limit=CASE WHEN ? THEN ? ELSE endpoint_limit END,
 rpm_limit=CASE WHEN ? THEN ? ELSE rpm_limit END,
 concurrency_limit=CASE WHEN ? THEN ? ELSE concurrency_limit END,
 light_rpm_limit=CASE WHEN ? THEN ? ELSE light_rpm_limit END,
 light_concurrency_limit=CASE WHEN ? THEN ? ELSE light_concurrency_limit END,
 global_rpm_limit=CASE WHEN ? THEN ? ELSE global_rpm_limit END,
 lang=CASE WHEN ? THEN ? ELSE lang END,
 level=CASE WHEN ? THEN ? ELSE level END,
 discord_gate_policy=CASE WHEN ? THEN ? ELSE discord_gate_policy END,
 revision=?,updated_at=?
WHERE id=? AND is_admin=0 AND revision=?`,
		input.EndpointLimitSet, nullableIntValue(input.EndpointLimit),
		input.RPMLimitSet, nullableIntValue(input.RPMLimit),
		input.ConcurrencySet, nullableIntValue(input.Concurrency),
		input.LightRPMLimitSet, nullableIntValue(input.LightRPMLimit),
		input.LightConcurrencySet, nullableIntValue(input.LightConcurrency),
		input.GlobalRPMLimitSet, nullableIntValue(input.GlobalRPMLimit),
		input.LangSet, input.Lang, input.LevelSet, nullableIntValueFromInt(input.Level),
		input.DiscordGatePolicySet, input.DiscordGatePolicy,
		db.EncodeU128(next), now, userID, row.revision)
	if err != nil {
		return AdminUser{}, classifyDatabaseError("update user profile", err)
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return AdminUser{}, ErrConflict
	}
	if input.LevelSet {
		if err := useractivity.RescheduleTx(ctx, tx, userID); err != nil {
			return AdminUser{}, err
		}
	}
	row, err = readUserRow(ctx, tx, userID)
	if err != nil {
		return AdminUser{}, err
	}
	config, err := readProjectionConfig(ctx, tx)
	if err != nil {
		return AdminUser{}, err
	}
	user, err := projectUser(ctx, tx, row, config, now)
	if err != nil {
		return AdminUser{}, err
	}
	return user, nil
}

func (service *Service) applyEconomyTx(ctx context.Context, tx *sql.Tx, row userRow, actorID, userID int64, input EconomyMutation, now int64) (AdminUser, bool, error) {
	if !equalU128Bytes(row.revision, input.ExpectedRevision) {
		return AdminUser{}, false, ErrConflict
	}
	asset := ledger.General
	if input.Target == "game_balance" {
		asset = ledger.Game
	}
	wallet, err := ledger.UserAssetAccount(ctx, tx, userID, asset)
	if err != nil {
		return AdminUser{}, false, classifyLedgerError("read adjustment wallet", err)
	}
	external, err := ledger.CodedAssetAccount(ctx, tx, "external", asset)
	if err != nil {
		return AdminUser{}, false, classifyLedgerError("read external account", err)
	}
	delta := input.AmountMilli
	if input.Direction == "decrease" {
		delta = -delta
	}
	creditDelta, donationDelta := ledger.AmountFromMilli(0), ledger.AmountFromMilli(0)
	donationUserID := int64(0)
	if input.Target == "balance" || input.Target == "game_balance" {
		creditDelta = ledger.AmountFromMilli(delta)
	} else {
		donationDelta = ledger.AmountFromMilli(delta)
		donationUserID = userID
	}
	operationID, err := service.newID("op_")
	if err != nil || !db.ValidateOpaqueID(operationID, "op_") {
		return AdminUser{}, false, ErrUnavailable
	}
	meta := ledger.Meta{OperationID: operationID, ActorUserID: actorID, CreatedAt: now}
	var plan ledger.Plan
	if asset == ledger.Game {
		plan, err = ledger.NewAdminGameAdjustment(meta, wallet.ID, external.ID, creditDelta, input.Reason)
	} else {
		plan, err = ledger.NewAdminUserAdjustment(meta, wallet.ID, external.ID, creditDelta, donationUserID, donationDelta, input.Reason)
	}
	if err != nil {
		return AdminUser{}, false, classifyLedgerError("build user adjustment", err)
	}
	if _, err := ledger.Apply(ctx, tx, plan); err != nil {
		return AdminUser{}, false, classifyLedgerError("apply user adjustment", err)
	}
	next, err := incrementU128(input.ExpectedRevision)
	if err != nil {
		return AdminUser{}, false, ErrResourceLimit
	}
	newAuto := row.autoLevel
	if input.Target == "donation_credit" {
		var donationRaw []byte
		if err := tx.QueryRowContext(ctx, `SELECT donation_credit_mag FROM users WHERE id=?`, userID).Scan(&donationRaw); err != nil {
			return AdminUser{}, false, classifyDatabaseError("read adjusted donation credit", err)
		}
		donation, err := db.DecodeU128(donationRaw)
		if err != nil {
			return AdminUser{}, false, fmt.Errorf("%w: decode adjusted donation credit", ErrInvariant)
		}
		config, err := readProjectionConfig(ctx, tx)
		if err != nil {
			return AdminUser{}, false, err
		}
		newAuto = authz.AutomaticLevel(newAuto, donation, config.thresholds)
	}
	authorityChanged := newAuto != row.autoLevel
	var update sql.Result
	if input.Target == "donation_credit" {
		// The closed ledger primitive advances the user revision together with
		// donation_credit_mag. Reuse that CAS winner instead of advancing the
		// same mutation a second time.
		update, err = tx.ExecContext(ctx, `UPDATE users SET auto_level=?,updated_at=? WHERE id=? AND is_admin=0 AND revision=?`, newAuto, now, userID, db.EncodeU128(next))
	} else {
		update, err = tx.ExecContext(ctx, `UPDATE users SET auto_level=?,revision=?,updated_at=? WHERE id=? AND is_admin=0 AND revision=?`, newAuto, db.EncodeU128(next), now, userID, row.revision)
	}
	if err != nil {
		return AdminUser{}, false, classifyDatabaseError("update adjusted user revision", err)
	}
	updated, err := update.RowsAffected()
	if err != nil || updated != 1 {
		return AdminUser{}, false, ErrConflict
	}
	if authorityChanged {
		if err := useractivity.RescheduleTx(ctx, tx, userID); err != nil {
			return AdminUser{}, false, err
		}
	}
	row, err = readUserRow(ctx, tx, userID)
	if err != nil {
		return AdminUser{}, false, err
	}
	config, err := readProjectionConfig(ctx, tx)
	if err != nil {
		return AdminUser{}, false, err
	}
	user, err := projectUser(ctx, tx, row, config, now)
	if err != nil {
		return AdminUser{}, false, err
	}
	return user, authorityChanged, nil
}

func (service *Service) applyBanTx(ctx context.Context, tx *sql.Tx, row userRow, adminID, userID int64, expected db.U128, banned bool, reason string, duration *int64, now int64, finalizers *[]func(bool)) error {
	if !equalU128Bytes(row.revision, expected) {
		return ErrConflict
	}
	if !banned || duration != nil {
		var blocked bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM discord_blacklist WHERE discord_id=?)`, row.discordID.String).Scan(&blocked); err != nil {
			return err
		}
		if blocked {
			return ErrBlacklisted
		}
	}
	next, err := incrementU128(expected)
	if err != nil {
		return ErrResourceLimit
	}
	var result sql.Result
	if banned {
		var until any
		if duration != nil {
			if *duration <= 0 || *duration > db.MaxBanDurationSeconds || now > maxUnixSecond-*duration {
				return ErrInvalidRequest
			}
			until = now + *duration
		}
		if service.cancelUserDuelsTx != nil {
			finalize, err := service.cancelUserDuelsTx(ctx, tx, userID, "account_unavailable", now)
			if err != nil {
				return classifyDatabaseError("cancel user games", err)
			}
			if finalize == nil {
				return ErrInvariant
			}
			*finalizers = append(*finalizers, finalize)
		}
		result, err = tx.ExecContext(ctx, `
UPDATE users SET is_banned=1,banned_reason=?,banned_until=?,auto_banned=0,ban_kind='',revision=?,updated_at=?
WHERE id=? AND is_admin=0 AND revision=?`, reason, until, db.EncodeU128(next), now, userID, row.revision)
	} else {
		if err := useractivity.ResetObservationTx(ctx, tx, userID, now); err != nil {
			return err
		}
		result, err = tx.ExecContext(ctx, `
UPDATE users SET is_banned=0,banned_reason='',banned_until=NULL,auto_banned=0,ban_kind='',revision=?,updated_at=?
WHERE id=? AND is_admin=0 AND revision=?`, db.EncodeU128(next), now, userID, row.revision)
	}
	if err != nil {
		return classifyDatabaseError("update ban state", err)
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM automatic_reason_metadata WHERE owner_kind='user_ban' AND owner_id=?`, strconv.FormatInt(userID, 10)); err != nil {
		return err
	}
	if err := useractivity.RescheduleTx(ctx, tx, userID); err != nil {
		return err
	}
	if banned {
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, userID); err != nil {
			return classifyDatabaseError("revoke user sessions", err)
		}
		keyUpdate, err := tx.ExecContext(ctx, `
UPDATE caller_keys SET generation=CASE WHEN key_hash IS NULL THEN generation ELSE generation+1 END,key_hash=NULL,display_head='',display_tail='',key_created_at=NULL,updated_at=?
WHERE user_id=? AND (key_hash IS NULL OR generation<?)`, now, userID, int64(math.MaxInt64))
		if err != nil {
			return classifyDatabaseError("revoke caller key", err)
		}
		keys, err := keyUpdate.RowsAffected()
		if err != nil || keys != 1 {
			return fmt.Errorf("%w: caller key generation", ErrInvariant)
		}
	}
	var replacementEnd *int64
	if banned && duration != nil {
		replacementEnd = new(now + *duration)
	}
	if err := antiabuse.EndAutomaticTx(ctx, tx, userID, "ban", adminID, now, banned, replacementEnd); err != nil {
		return classifyDatabaseError("record automatic ban disposition", err)
	}
	return nil
}
