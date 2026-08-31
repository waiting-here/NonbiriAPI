package activities

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/accountstream"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func (r *Repository) GetActivities(ctx context.Context, userID int64) (ActivitiesSnapshot, error) {
	projection, err := r.ProjectActivities(ctx, userID)
	return projection.Snapshot, err
}

func (r *Repository) GetThursday(ctx context.Context, userID int64) (ThursdayView, error) {
	projection, err := r.ProjectActivities(ctx, userID)
	return projection.Snapshot.Thursday, err
}

// ProjectActivities rebuilds the complete account-safe activity projection.
// It never includes participant IDs, operation/source facts, or other users.
func (r *Repository) ProjectActivities(ctx context.Context, userID int64) (SnapshotProjection, error) {
	if r == nil || ctx == nil || userID <= 0 {
		return SnapshotProjection{}, ErrInvalidRequest
	}
	now, err := r.workerNow()
	if err != nil {
		return SnapshotProjection{}, err
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return SnapshotProjection{}, classifyDatabaseError("begin activities projection", err)
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=?)`, userID).Scan(&exists); err != nil {
		return SnapshotProjection{}, classifyDatabaseError("read activities account", err)
	}
	if exists != 1 {
		return SnapshotProjection{}, ErrNotFound
	}
	config, err := readActivityConfigTx(ctx, tx)
	if err != nil {
		return SnapshotProjection{}, err
	}
	snapshot := ActivitiesSnapshot{}
	if config.masterEnabled {
		snapshot.Master = MasterView{Enabled: true, Available: true, Reason: MasterReasonAvailable}
	} else {
		snapshot.Master = MasterView{Enabled: false, Available: false, Reason: MasterReasonDisabled}
	}
	if err := projectWelfareTx(ctx, tx, userID, now, config, &snapshot.Welfare); err != nil {
		return SnapshotProjection{}, err
	}
	if err := projectThursdayTx(ctx, tx, userID, now, config, &snapshot.Thursday); err != nil {
		return SnapshotProjection{}, err
	}
	revision, err := activitiesProjectionRevisionTx(ctx, tx, userID, config.revision)
	if err != nil {
		return SnapshotProjection{}, err
	}
	data, err := json.Marshal(snapshot)
	if err != nil || len(data) > accountstream.MaxSnapshotBytes {
		return SnapshotProjection{}, ErrResourceLimit
	}
	if err := tx.Commit(); err != nil {
		return SnapshotProjection{}, classifyDatabaseError("commit activities projection", err)
	}
	return SnapshotProjection{Snapshot: snapshot, Revision: revision, Data: data}, nil
}

func projectWelfareTx(ctx context.Context, tx *sql.Tx, userID, now int64, config activityConfig, view *WelfareView) error {
	if view == nil {
		return ErrInvariant
	}
	view.Enabled = config.welfareEnabled
	view.Threshold = formatMilliPointsInt64(config.welfareThreshold)
	view.Cap = formatMilliPointsInt64(config.welfareCap)
	destination := PoolDestination{}
	var poolID string
	if err := tx.QueryRowContext(ctx, `SELECT id,account_id FROM shared_pools WHERE pool_type='welfare' AND state='open'`).Scan(&poolID, &destination.AccountID); err != nil {
		return classifyDatabaseError("read projected welfare pool", err)
	}
	account, err := ledger.ReadAccount(ctx, tx, destination.AccountID)
	if err != nil {
		return classifyLedgerError("read projected welfare pool balance", err)
	}
	if account.Kind != ledger.AccountPool || account.Code != "pool:"+poolID || account.Balance.Sign() < 0 {
		return ErrInvariant
	}
	view.PoolBalance = formatMilliPoints(account.Balance.Big())
	if config.timezoneSet {
		view.SiteDay, err = siteDay(now, config.timezoneMinutes)
		if err != nil {
			return err
		}
		view.ClaimedToday, err = readWelfareClaimTx(ctx, tx, userID, view.SiteDay)
		if err != nil {
			return err
		}
	}
	if !config.masterEnabled || !config.welfareEnabled {
		view.State = WelfareStateUnavailable
		return nil
	}
	if !config.timezoneSet {
		view.State = WelfareStateConfigurationError
		return nil
	}
	if view.ClaimedToday {
		view.State = WelfareStateClaimed
		return nil
	}
	assets, _, err := welfareAssetsTx(ctx, tx, userID)
	if errors.Is(err, ErrResourceLimit) {
		view.State = WelfareStateIneligible
		return nil
	}
	if err != nil {
		return err
	}
	if assets.Cmp(big.NewInt(config.welfareThreshold)) >= 0 {
		view.State = WelfareStateIneligible
		return nil
	}
	award := new(big.Int).Quo(account.Balance.Big(), big.NewInt(10))
	if award.Cmp(big.NewInt(config.welfareCap)) > 0 {
		award.SetInt64(config.welfareCap)
	}
	if award.Sign() == 0 {
		view.State = WelfareStateEmpty
		return nil
	}
	view.State = WelfareStateAvailable
	return nil
}

func projectThursdayTx(ctx context.Context, tx *sql.Tx, userID, now int64, config activityConfig, view *ThursdayView) error {
	if view == nil {
		return ErrInvariant
	}
	view.Enabled = config.thursdayEnabled
	view.ServerNow = now
	periods, err := readProjectionPeriodsTx(ctx, tx)
	if err != nil {
		return err
	}
	var current, next *periodRecord
	var latestSettled *periodRecord
	configurationError := false
	for index := range periods {
		period := &periods[index]
		if period.state == PeriodStateConfigurationErr {
			configurationError = true
			continue
		}
		if period.state == PeriodStateSettled && (latestSettled == nil || period.opensAt > latestSettled.opensAt) {
			latestSettled = period
		}
		if period.state == PeriodStateConfigured && now < period.opensAt {
			if next == nil || period.opensAt < next.opensAt {
				next = period
			}
			continue
		}
		if period.state == PeriodStateSettling || (period.state == PeriodStateConfigured || period.state == PeriodStateOpen) && now >= period.opensAt {
			if current == nil || period.opensAt > current.opensAt {
				current = period
			}
		}
	}
	if current != nil {
		poolBalance, err := poolBalanceTx(ctx, tx, current.currentPoolID)
		if err != nil {
			return err
		}
		participant, found, err := readParticipantForUserTx(ctx, tx, current.id, userID)
		if err != nil {
			return err
		}
		myCount, myContributed := "0", "0"
		if found {
			myCount, myContributed = participant.count.Decimal(), formatMilliPoints(participant.contributed.Big())
		}
		view.Current = &ThursdayCurrent{
			PeriodID: current.id, Revision: strconv.FormatInt(current.revision, 10),
			OpensAt: current.opensAt, ClosesAt: current.closesAt,
			Literature: current.literature, Entry: formatMilliPointsInt64(current.entryMilli),
			PerUserLimit: current.perUserLimit, PoolBalance: poolBalance,
			MyCount: myCount, MyContributed: myContributed,
		}
	}
	if next != nil {
		poolBalance, err := poolBalanceTx(ctx, tx, next.currentPoolID)
		if err != nil {
			return err
		}
		view.Next = &ThursdayNext{PeriodID: next.id, OpensAt: next.opensAt, PoolBalance: poolBalance}
	}
	openNow := current != nil && now >= current.opensAt && now < current.closesAt &&
		(current.state == PeriodStateConfigured || current.state == PeriodStateOpen)
	if !openNow && latestSettled != nil && resultVisible(now, latestSettled.opensAt) {
		participant, found, err := readParticipantForUserTx(ctx, tx, latestSettled.id, userID)
		if err != nil {
			return err
		}
		if found && participant.settled {
			var reason *string
			if participant.unpaidReason.Valid {
				value := participant.unpaidReason.String
				reason = &value
			}
			view.LastResult = &ThursdayLastResult{
				PeriodID: latestSettled.id, MyCount: participant.count.Decimal(),
				MyContributed: formatMilliPoints(participant.contributed.Big()),
				Payout:        formatMilliPoints(participant.payout.Big()),
				UnpaidReason:  reason,
			}
		}
	}
	if !config.masterEnabled || !config.thursdayEnabled {
		view.State = ThursdayStateUnavailable
		return nil
	}
	if configurationError {
		view.State = ThursdayStateConfigurationError
		return nil
	}
	switch {
	case openNow:
		view.State = ThursdayStateOpen
	case current != nil && (current.state == PeriodStateSettling || now >= current.closesAt && current.state != PeriodStateSettled):
		view.State = ThursdayStateSettling
	case latestSettled != nil:
		view.State = ThursdayStateEnded
	case next != nil:
		view.State = ThursdayStateNotOpen
	default:
		view.State = ThursdayStateConfigurationError
	}
	return nil
}

func readProjectionPeriodsTx(ctx context.Context, tx *sql.Tx) ([]periodRecord, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+periodColumns+` FROM thursday_periods ORDER BY opens_at DESC,id DESC LIMIT 4`)
	if err != nil {
		return nil, classifyDatabaseError("read projected Thursday periods", err)
	}
	defer rows.Close()
	periods := make([]periodRecord, 0, 4)
	for rows.Next() {
		period, err := scanPeriodRecord(rows)
		if err != nil {
			return nil, classifyDatabaseError("scan projected Thursday periods", err)
		}
		periods = append(periods, period)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyDatabaseError("iterate projected Thursday periods", err)
	}
	return periods, nil
}

func poolBalanceTx(ctx context.Context, tx *sql.Tx, poolID string) (string, error) {
	pool, err := readPoolRecordTx(ctx, tx, poolID)
	if err != nil {
		return "", err
	}
	account, err := ledger.ReadAccount(ctx, tx, pool.accountID)
	if err != nil {
		return "", classifyLedgerError("read projected pool balance", err)
	}
	if account.Kind != ledger.AccountPool || account.Code != "pool:"+pool.id || account.Balance.Sign() < 0 {
		return "", ErrInvariant
	}
	return formatMilliPoints(account.Balance.Big()), nil
}

func activitiesProjectionRevisionTx(ctx context.Context, tx *sql.Tx, userID, configRevision int64) (string, error) {
	if configRevision < 1 {
		return "", ErrInvariant
	}
	total := big.NewInt(configRevision)
	var capacityRevision, userRevision []byte
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM credit_capacity WHERE id=1`).Scan(&capacityRevision); err != nil {
		return "", classifyDatabaseError("read activities capacity revision", err)
	}
	capacity, err := decodeU128(capacityRevision)
	if err != nil {
		return "", err
	}
	total.Add(total, capacity.Big())
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM users WHERE id=?`, userID).Scan(&userRevision); err != nil {
		return "", classifyDatabaseError("read activities user revision", err)
	}
	user, err := decodeU128(userRevision)
	if err != nil {
		return "", err
	}
	total.Add(total, user.Big())
	for _, query := range []string{
		`SELECT revision FROM shared_pools
                 WHERE state='open' OR period_id IN (
                   SELECT id FROM thursday_periods ORDER BY opens_at DESC,id DESC LIMIT 4)
                 ORDER BY id`,
		`SELECT revision FROM thursday_periods ORDER BY opens_at DESC,id DESC LIMIT 4`,
	} {
		rows, err := tx.QueryContext(ctx, query)
		if err != nil {
			return "", classifyDatabaseError("read activities projection revision", err)
		}
		for rows.Next() {
			var revision int64
			if err := rows.Scan(&revision); err != nil || revision < 1 {
				rows.Close()
				if err != nil {
					return "", classifyDatabaseError("scan activities projection revision", err)
				}
				return "", ErrInvariant
			}
			total.Add(total, big.NewInt(revision))
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return "", classifyDatabaseError("iterate activities projection revision", err)
		}
		if err := rows.Close(); err != nil {
			return "", classifyDatabaseError("close activities projection revision", err)
		}
	}
	return total.String(), nil
}

// Snapshot is the activities-only accountstream adapter. The root compositor
// combines it with the RPS provider; unknown channels are rejected.
func (r *Repository) Snapshot(ctx context.Context, accountID int64, channel accountstream.Channel) (accountstream.Snapshot, error) {
	if channel != accountstream.ChannelActivities {
		return accountstream.Snapshot{}, accountstream.ErrSnapshot
	}
	projection, err := r.ProjectActivities(ctx, accountID)
	if err != nil {
		return accountstream.Snapshot{}, err
	}
	return projection.AccountstreamSnapshot(), nil
}
