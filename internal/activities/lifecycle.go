package activities

import (
	"context"
	"database/sql"
	"sort"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

const maxActivityExportRows = 10_000

// ExportUser returns only the account's own claim/contribution/result facts.
// It excludes shared pool history, operation IDs, checkpoints and participant
// references.
func (r *Repository) ExportUser(ctx context.Context, userID int64) (UserExport, error) {
	if r == nil || r.db == nil || ctx == nil || userID <= 0 {
		return UserExport{}, ErrInvalidRequest
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return UserExport{}, classifyDatabaseError("begin activity export", err)
	}
	defer tx.Rollback()
	export, err := r.ExportUserTx(ctx, tx, userID, maxActivityExportRows)
	if err != nil {
		return UserExport{}, err
	}
	if err := tx.Commit(); err != nil {
		return UserExport{}, classifyDatabaseError("commit activity export", err)
	}
	return export, nil
}

// ExportUserTx returns the account's activity facts from the caller-owned
// transaction. The caller owns authorization, snapshot selection, and commit.
// Each exported collection fails closed when it contains more than limit rows.
func (r *Repository) ExportUserTx(ctx context.Context, tx *sql.Tx, userID int64, limit int) (UserExport, error) {
	if r == nil || ctx == nil || tx == nil || userID <= 0 || limit < 1 || limit > maxActivityExportRows {
		return UserExport{}, ErrInvalidRequest
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=?)`, userID).Scan(&exists); err != nil {
		return UserExport{}, classifyDatabaseError("read activity export owner", err)
	}
	if exists != 1 {
		return UserExport{}, ErrNotFound
	}
	export := UserExport{WelfareClaims: []WelfareClaimExport{}, Thursday: []ThursdayParticipantExport{}}
	claimRows, err := tx.QueryContext(ctx, `
SELECT site_day,threshold_milli,cap_milli,award_milli,created_at
FROM welfare_claims WHERE user_id=? ORDER BY site_day,created_at LIMIT ?`, userID, limit+1)
	if err != nil {
		return UserExport{}, classifyDatabaseError("read welfare export", err)
	}
	for claimRows.Next() {
		claim, err := welfareClaimForExport(claimRows)
		if err != nil {
			claimRows.Close()
			return UserExport{}, classifyDatabaseError("scan welfare export", err)
		}
		export.WelfareClaims = append(export.WelfareClaims, claim)
	}
	if err := claimRows.Err(); err != nil {
		claimRows.Close()
		return UserExport{}, classifyDatabaseError("iterate welfare export", err)
	}
	if err := claimRows.Close(); err != nil {
		return UserExport{}, classifyDatabaseError("close welfare export", err)
	}
	if len(export.WelfareClaims) > limit {
		return UserExport{}, ErrResourceLimit
	}
	participantRows, err := tx.QueryContext(ctx, `
SELECT p.period_id,t.period_key,p.contribution_count,p.contributed_mag,p.eligible_at_freeze,
 p.settled,p.payout_mag,p.unpaid_reason,p.created_at,p.updated_at
FROM thursday_participants p
JOIN thursday_periods t ON t.id=p.period_id
WHERE p.user_id=? ORDER BY t.opens_at,p.period_id LIMIT ?`, userID, limit+1)
	if err != nil {
		return UserExport{}, classifyDatabaseError("read Thursday export", err)
	}
	for participantRows.Next() {
		var item ThursdayParticipantExport
		var countRaw, contributedRaw, payoutRaw []byte
		var eligible, settled int
		var unpaid sql.NullString
		if err := participantRows.Scan(&item.PeriodID, &item.PeriodKey, &countRaw, &contributedRaw,
			&eligible, &settled, &payoutRaw, &unpaid, &item.CreatedAt, &item.UpdatedAt); err != nil {
			participantRows.Close()
			return UserExport{}, classifyDatabaseError("scan Thursday export", err)
		}
		count, err := decodeU128(countRaw)
		if err != nil {
			participantRows.Close()
			return UserExport{}, err
		}
		contributed, err := decodeU128(contributedRaw)
		if err != nil {
			participantRows.Close()
			return UserExport{}, err
		}
		payout, err := decodeU128(payoutRaw)
		if err != nil || eligible != 0 && eligible != 1 || settled != 0 && settled != 1 {
			participantRows.Close()
			return UserExport{}, ErrInvariant
		}
		item.Count = count.Decimal()
		item.Contributed = formatMilliPoints(contributed.Big())
		item.Payout = formatMilliPoints(payout.Big())
		item.Eligible, item.Settled = eligible == 1, settled == 1
		if unpaid.Valid {
			value := unpaid.String
			item.UnpaidReason = &value
		}
		export.Thursday = append(export.Thursday, item)
	}
	if err := participantRows.Err(); err != nil {
		participantRows.Close()
		return UserExport{}, classifyDatabaseError("iterate Thursday export", err)
	}
	if err := participantRows.Close(); err != nil {
		return UserExport{}, classifyDatabaseError("close Thursday export", err)
	}
	if len(export.Thursday) > limit {
		return UserExport{}, ErrResourceLimit
	}
	return export, nil
}

// PrepareUserDeletion is called inside the lifecycle owner's deletion
// transaction before the users row is removed. It releases every unsettled
// payout reservation and de-identifies all period facts without changing C,
// another participant's payout, or any completed pool transfer.
func (r *Repository) PrepareUserDeletion(ctx context.Context, tx *sql.Tx, userID, now int64) error {
	if r == nil || ctx == nil || tx == nil || userID <= 0 || now < 0 || now > maxUnixSecond {
		return ErrInvalidRequest
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+participantColumns()+` FROM thursday_participants WHERE user_id=? ORDER BY period_id,participant_ref`, userID)
	if err != nil {
		return classifyDatabaseError("read Thursday deletion facts", err)
	}
	participants := make([]participantRecord, 0)
	for rows.Next() {
		participant, err := scanParticipantRecord(rows)
		if err != nil {
			rows.Close()
			return classifyDatabaseError("scan Thursday deletion facts", err)
		}
		participants = append(participants, participant)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return classifyDatabaseError("iterate Thursday deletion facts", err)
	}
	if err := rows.Close(); err != nil {
		return classifyDatabaseError("close Thursday deletion facts", err)
	}
	affectedPeriods := make(map[string]struct{})
	zero := db.EncodeU128(db.U128{})
	for _, participant := range participants {
		affectedPeriods[participant.periodID] = struct{}{}
		if participant.settled {
			result, err := tx.ExecContext(ctx, `
UPDATE thursday_participants
SET user_id=NULL,updated_at=?
WHERE period_id=? AND participant_ref=? AND user_id=? AND settled=1`,
				now, participant.periodID, participant.participantRef, userID)
			if err != nil {
				return classifyDatabaseError("deidentify settled Thursday participant", err)
			}
			if err := mustRowsAffected(result, 1, false); err != nil {
				return err
			}
			continue
		}
		ref, err := ledger.ThursdayParticipantReservation(participant.periodID, participant.participantRef)
		if err != nil {
			return classifyLedgerError("build deleted Thursday reservation", err)
		}
		err = ledger.ReleaseReserved(ctx, tx, ref, oneU128(), func(callbackCtx context.Context, callbackTx *sql.Tx) error {
			result, err := callbackTx.ExecContext(callbackCtx, `
UPDATE thursday_participants
SET user_id=NULL,settled=1,payout_mag=?,unpaid_reason='account_deleted',
 ledger_rows_remaining=?,updated_at=?
WHERE period_id=? AND participant_ref=? AND user_id=? AND settled=0 AND ledger_rows_remaining=?`,
				zero, zero, now, participant.periodID, participant.participantRef, userID, db.EncodeU128(oneU128()))
			if err != nil {
				return classifyDatabaseError("deidentify unsettled Thursday participant", err)
			}
			return mustRowsAffected(result, 1, false)
		})
		if err != nil {
			return classifyLedgerError("release deleted Thursday payout", err)
		}
	}
	periodIDs := make([]string, 0, len(affectedPeriods))
	for periodID := range affectedPeriods {
		periodIDs = append(periodIDs, periodID)
	}
	sort.Strings(periodIDs)
	for _, periodID := range periodIDs {
		result, err := tx.ExecContext(ctx, `UPDATE thursday_periods SET revision=revision+1 WHERE id=? AND revision<9223372036854775807`, periodID)
		if err != nil {
			return classifyDatabaseError("advance deidentified Thursday period", err)
		}
		if err := mustRowsAffected(result, 1, false); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM welfare_claims WHERE user_id=?`, userID); err != nil {
		return classifyDatabaseError("delete welfare claim facts", err)
	}
	return nil
}
