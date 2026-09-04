package activities

import (
	"context"
	"database/sql"
	"math/big"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

// ClaimWelfare evaluates eligibility and, when the computed award is
// positive, transfers the award and consumes the user's one successful claim
// for the frozen site day. A zero award is a successful no-op and does not
// create a welfare_claims row.
func (r *Repository) ClaimWelfare(ctx context.Context, userID int64, mutation ControlMutation) (MutationResult[WelfareClaimResult], PublishFacts, error) {
	if r == nil || ctx == nil || userID <= 0 {
		return MutationResult[WelfareClaimResult]{}, PublishFacts{}, ErrInvalidRequest
	}
	tx, err := r.beginUserMutation(ctx, userID)
	if err != nil {
		return MutationResult[WelfareClaimResult]{}, PublishFacts{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	now, err := r.decisionNow()
	if err != nil {
		return MutationResult[WelfareClaimResult]{}, PublishFacts{}, err
	}
	decision, err := beginControlMutation(ctx, tx, "user", userID, mutation, now)
	if err != nil {
		return MutationResult[WelfareClaimResult]{}, PublishFacts{}, err
	}
	if decision.Kind == idempotency.Replay {
		replayed, replayErr := replayMutation[WelfareClaimResult](decision)
		if replayErr != nil {
			return MutationResult[WelfareClaimResult]{}, PublishFacts{}, replayErr
		}
		if err := commitTx(tx, &committed); err != nil {
			return MutationResult[WelfareClaimResult]{}, PublishFacts{}, err
		}
		return replayed, PublishFacts{}, nil
	}

	config, err := readActivityConfigTx(ctx, tx)
	if err != nil {
		return MutationResult[WelfareClaimResult]{}, PublishFacts{}, err
	}
	if !config.masterEnabled || !config.welfareEnabled {
		return MutationResult[WelfareClaimResult]{}, PublishFacts{}, ErrFeatureDisabled
	}
	if !config.timezoneSet {
		return MutationResult[WelfareClaimResult]{}, PublishFacts{}, ErrUnavailable
	}
	day, err := siteDay(now, config.timezoneMinutes)
	if err != nil {
		return MutationResult[WelfareClaimResult]{}, PublishFacts{}, err
	}
	var claimed int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM welfare_claims WHERE user_id=? AND site_day=?)`, userID, day).Scan(&claimed); err != nil {
		return MutationResult[WelfareClaimResult]{}, PublishFacts{}, classifyDatabaseError("read welfare claim slot", err)
	}
	if claimed != 0 {
		return MutationResult[WelfareClaimResult]{}, PublishFacts{}, ErrConflict
	}

	assets, wallet, err := welfareAssetsTx(ctx, tx, userID)
	if err != nil {
		return MutationResult[WelfareClaimResult]{}, PublishFacts{}, err
	}
	if assets.Cmp(big.NewInt(config.welfareThreshold)) >= 0 {
		return MutationResult[WelfareClaimResult]{}, PublishFacts{}, ErrConflict
	}
	destination, err := r.WelfareDestination(ctx, tx)
	if err != nil {
		return MutationResult[WelfareClaimResult]{}, PublishFacts{}, err
	}
	poolAccount, err := ledger.ReadAccount(ctx, tx, destination.AccountID)
	if err != nil {
		return MutationResult[WelfareClaimResult]{}, PublishFacts{}, classifyLedgerError("read welfare pool", err)
	}
	if poolAccount.Kind != ledger.AccountPool || poolAccount.Code != "pool:"+destination.PoolID || poolAccount.Balance.Sign() < 0 {
		return MutationResult[WelfareClaimResult]{}, PublishFacts{}, ErrInvariant
	}
	awardBig := new(big.Int).Quo(poolAccount.Balance.Big(), big.NewInt(10))
	if awardBig.Cmp(big.NewInt(config.welfareCap)) > 0 {
		awardBig.SetInt64(config.welfareCap)
	}
	if awardBig.Sign() == 0 {
		value := WelfareClaimResult{
			Awarded: "0", Balance: formatMilliPoints(wallet.Balance.Big()),
			PoolBalance: formatMilliPoints(poolAccount.Balance.Big()), SiteDay: day,
		}
		response, err := finishJSONMutation(ctx, tx, decision, http.StatusOK, value)
		if err != nil {
			return MutationResult[WelfareClaimResult]{}, PublishFacts{}, err
		}
		if err := commitTx(tx, &committed); err != nil {
			return MutationResult[WelfareClaimResult]{}, PublishFacts{}, err
		}
		return response, PublishFacts{}, nil
	}

	award, err := ledger.AmountFromBig(awardBig)
	if err != nil {
		return MutationResult[WelfareClaimResult]{}, PublishFacts{}, ErrResourceLimit
	}
	operationID, err := generateCanonical(r.operationID, "op_")
	if err != nil {
		return MutationResult[WelfareClaimResult]{}, PublishFacts{}, err
	}
	plan, err := ledger.NewWelfareClaim(ledger.Meta{OperationID: operationID, ActorUserID: userID, CreatedAt: now}, destination.AccountID, wallet.ID, award)
	if err != nil {
		return MutationResult[WelfareClaimResult]{}, PublishFacts{}, classifyLedgerError("build welfare claim", err)
	}
	if _, err := ledger.Apply(ctx, tx, plan); err != nil {
		return MutationResult[WelfareClaimResult]{}, PublishFacts{}, classifyLedgerError("apply welfare claim", err)
	}
	poolBefore := poolAccount.Balance.Big()
	if poolBefore.Cmp(dbMaxMoneyBig()) > 0 {
		// The frozen claim fact stores an int64 primitive while pool accounts are
		// wide. Preserve the exact economics in the ledger and store the largest
		// representable proof bound; award and cap are both bounded by M.
		poolBefore = dbMaxMoneyBig()
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO welfare_claims(user_id,site_day,operation_id,threshold_milli,cap_milli,pool_before_milli,award_milli,created_at)
VALUES(?,?,?,?,?,?,?,?)`, userID, day, operationID, config.welfareThreshold, config.welfareCap,
		poolBefore.Int64(), awardBig.Int64(), now); err != nil {
		return MutationResult[WelfareClaimResult]{}, PublishFacts{}, classifyDatabaseError("record welfare claim", err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE shared_pools SET revision=revision+1 WHERE id=? AND account_id=? AND state='open' AND revision<9223372036854775807`, destination.PoolID, destination.AccountID)
	if err != nil {
		return MutationResult[WelfareClaimResult]{}, PublishFacts{}, classifyDatabaseError("advance welfare pool revision", err)
	}
	if err := mustRowsAffected(result, 1, false); err != nil {
		return MutationResult[WelfareClaimResult]{}, PublishFacts{}, err
	}
	if err := freezeSiteTimezoneTx(ctx, tx, now); err != nil {
		return MutationResult[WelfareClaimResult]{}, PublishFacts{}, err
	}
	value := WelfareClaimResult{
		Awarded:     formatMilliPoints(award.Big()),
		Balance:     formatMilliPoints(new(big.Int).Add(wallet.Balance.Big(), awardBig)),
		PoolBalance: formatMilliPoints(new(big.Int).Sub(poolAccount.Balance.Big(), awardBig)), SiteDay: day,
	}
	response, err := finishJSONMutation(ctx, tx, decision, http.StatusOK, value)
	if err != nil {
		return MutationResult[WelfareClaimResult]{}, PublishFacts{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return MutationResult[WelfareClaimResult]{}, PublishFacts{}, err
	}
	return response, PublishFacts{Global: true, AccountIDs: []int64{userID}}, nil
}

func welfareAssetsTx(ctx context.Context, tx *sql.Tx, userID int64) (*big.Int, ledger.Account, error) {
	wallet, err := ledger.UserAccount(ctx, tx, userID)
	if err != nil {
		return nil, ledger.Account{}, classifyLedgerError("read welfare wallet", err)
	}
	total := wallet.Balance.Big()
	addIntRows := func(query string) error {
		rows, err := tx.QueryContext(ctx, query, userID)
		if err != nil {
			return classifyDatabaseError("read welfare refundable assets", err)
		}
		defer rows.Close()
		for rows.Next() {
			var value int64
			if err := rows.Scan(&value); err != nil {
				return classifyDatabaseError("scan welfare refundable assets", err)
			}
			if err := addWelfareNonnegativeInt(total, value); err != nil {
				return err
			}
		}
		if err := rows.Err(); err != nil {
			return classifyDatabaseError("iterate welfare refundable assets", err)
		}
		return nil
	}
	if err := addIntRows(`SELECT account_reserved_milli FROM logical_requests WHERE user_id=? AND state IN ('accepted','running') AND accounting_state='reserved'`); err != nil {
		return nil, ledger.Account{}, err
	}
	if err := addIntRows(`SELECT entry_total_milli FROM game_fishing_batches WHERE user_id=? AND state='reserved'`); err != nil {
		return nil, ledger.Account{}, err
	}
	addU128Rows := func(query string) error {
		rows, err := tx.QueryContext(ctx, query, userID)
		if err != nil {
			return classifyDatabaseError("read welfare wide refundable assets", err)
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				return classifyDatabaseError("scan welfare wide refundable assets", err)
			}
			if err := addWelfareU128(total, raw); err != nil {
				return err
			}
		}
		if err := rows.Err(); err != nil {
			return classifyDatabaseError("iterate welfare wide refundable assets", err)
		}
		return nil
	}
	if err := addU128Rows(`SELECT reserved FROM game_rps_queue WHERE user_id=?`); err != nil {
		return nil, ledger.Account{}, err
	}
	if err := addU128Rows(`
SELECT seat.current_balance
FROM game_rps_seats seat
JOIN game_rps_sessions session ON session.id=seat.session_id
WHERE seat.user_id=? AND session.state IN ('started','terminal_processing')`); err != nil {
		return nil, ledger.Account{}, err
	}
	return total, wallet, nil
}

func addWelfareNonnegativeInt(total *big.Int, value int64) error {
	if value < 0 {
		return ErrInvariant
	}
	total.Add(total, big.NewInt(value))
	return nil
}

func addWelfareU128(total *big.Int, raw []byte) error {
	value, err := decodeU128(raw)
	if err != nil {
		return err
	}
	total.Add(total, value.Big())
	return nil
}

func readWelfareClaimTx(ctx context.Context, tx *sql.Tx, userID int64, day string) (bool, error) {
	var exists int
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM welfare_claims WHERE user_id=? AND site_day=?)`, userID, day).Scan(&exists)
	if err != nil {
		return false, classifyDatabaseError("read welfare claim", err)
	}
	return exists != 0, nil
}

func welfareClaimForExport(rows *sql.Rows) (WelfareClaimExport, error) {
	var claim WelfareClaimExport
	var threshold, cap, award int64
	if err := rows.Scan(&claim.SiteDay, &threshold, &cap, &award, &claim.CreatedAt); err != nil {
		return WelfareClaimExport{}, err
	}
	if threshold < 0 || cap < 0 || award <= 0 {
		return WelfareClaimExport{}, ErrInvariant
	}
	claim.Threshold = formatMilliPointsInt64(threshold)
	claim.Cap = formatMilliPointsInt64(cap)
	claim.Awarded = formatMilliPointsInt64(award)
	return claim, nil
}
