package rps

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"sort"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type terminalResult struct {
	Users []int64
	Facts activities.PublishFacts
}

func (service *Service) transitionTerminal(record *sessionRecord, reason string, now int64) error {
	if record == nil || record.State != StateStarted || !validTerminalReason(reason) {
		return ErrInvariant
	}
	operationID, err := service.generate("op_")
	if err != nil {
		return err
	}
	phaseSeq, err := incU128(record.PhaseSeq)
	if err != nil {
		return err
	}
	record.State, record.Phase, record.PhaseSeq = StateTerminalProcessing, PhaseTerminal, phaseSeq
	record.PhaseDeadline, record.DealerSeat = nil, nil
	record.PoolBaseMultiplier, record.CurrentPlanMultiplier, record.DealerRaise = nil, nil, nil
	record.ReminderState = "none"
	record.TerminalOperationID, record.TerminalReason = &operationID, &reason
	record.TerminalRetryAttemptCount = db.U128{}
	record.TerminalNextRetryAt, record.TerminalLastErrorClass = nil, nil
	record.WelfareCarryTotal = record.PlayerPool
	for seat := range record.Seats {
		value := &record.Seats[seat]
		terminal := value.CurrentBalance
		sign, magnitude, err := walletNet(terminal.Big(), value.StartingBalance.Big())
		if err != nil {
			return err
		}
		value.TerminalReturn, value.WalletNetSign, value.WalletNetMag = &terminal, &sign, &magnitude
		value.GestureEnvelope, value.GesturePhaseSeq, value.FollowerAction, value.LastActionPhaseSeq = nil, nil, nil, nil
	}
	return appendEvent(record, EventTerminal, terminalPayload{TerminalReason: reason})
}

func terminalErrorClass(err error) string {
	switch {
	case errors.Is(err, ErrInvariant):
		return "invariant_violation"
	case errors.Is(err, ErrServiceUnavailable):
		return "db_busy"
	default:
		return "internal_retryable"
	}
}

func terminalBackoff(attempt *big.Int) int64 {
	if attempt == nil || attempt.Sign() <= 0 {
		return 1
	}
	if !attempt.IsInt64() || attempt.Int64() >= 12 {
		return 3600
	}
	seconds := int64(1) << uint(attempt.Int64()-1)
	if seconds > 3600 {
		return 3600
	}
	return seconds
}

func (service *Service) scheduleTerminalRetryTx(ctx context.Context, tx *sql.Tx, record *sessionRecord, now int64, cause error) error {
	if record == nil || record.State != StateTerminalProcessing {
		return ErrInvariant
	}
	attempt, err := incU128(record.TerminalRetryAttemptCount)
	if err != nil {
		return err
	}
	next := now + terminalBackoff(attempt.Big())
	if next < now || next > 253402300799 {
		next = 253402300799
	}
	class := terminalErrorClass(cause)
	record.TerminalRetryAttemptCount, record.TerminalNextRetryAt, record.TerminalLastErrorClass = attempt, &next, &class
	if err := persistSessionTx(ctx, tx, record, record.Revision); err != nil {
		return err
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM admin_alerts
WHERE kind='rps_terminal_retrying' AND ref=? AND resolved=0)`, record.ID).Scan(&exists); err != nil {
		return classifyDB(err)
	}
	if exists == 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO admin_alerts(kind,message,ref,created_at,resolved)
VALUES('rps_terminal_retrying','RPS terminal transaction will retry',?,?,0)`, record.ID, now); err != nil {
			return classifyDB(err)
		}
	}
	return nil
}

func terminalOutcome(seat seatRecord) (string, error) {
	if seat.DeletionState != "active" {
		return "deidentified", nil
	}
	if seat.WalletNetSign == nil {
		return "", ErrInvariant
	}
	switch *seat.WalletNetSign {
	case -1:
		return "loss", nil
	case 0:
		return "tie", nil
	case 1:
		return "win", nil
	default:
		return "", ErrInvariant
	}
}

func (service *Service) finalizeTerminalTx(ctx context.Context, tx *sql.Tx, record *sessionRecord, now int64) (terminalResult, error) {
	if record == nil || record.State != StateTerminalProcessing || record.TerminalOperationID == nil || record.TerminalReason == nil {
		return terminalResult{}, ErrInvariant
	}
	external, err := ledger.CodedAccount(ctx, tx, "external")
	if err != nil {
		return terminalResult{}, ErrInvariant
	}
	welfare, err := service.pools.WelfareDestination(ctx, tx)
	if err != nil {
		return terminalResult{}, classifyDB(err)
	}
	payouts := make([]ledger.RPSTerminalPayout, 0, 3)
	users := make([]int64, 0, 3)
	deletedAmount := new(big.Int)
	for _, seat := range record.Seats {
		if seat.TerminalReturn == nil || seat.WalletNetSign == nil || seat.WalletNetMag == nil {
			return terminalResult{}, ErrInvariant
		}
		if seat.DeletionState == "active" && seat.UserID != nil {
			account, err := ledger.UserAccount(ctx, tx, *seat.UserID)
			if err != nil {
				return terminalResult{}, ErrInvariant
			}
			amount, err := ledger.AmountFromBig(seat.TerminalReturn.Big())
			if err != nil {
				return terminalResult{}, ErrInvariant
			}
			payouts = append(payouts, ledger.RPSTerminalPayout{UserAccountID: account.ID, Amount: amount})
			users = append(users, *seat.UserID)
		} else {
			deletedAmount.Add(deletedAmount, seat.TerminalReturn.Big())
		}
	}
	deleted, err := ledger.AmountFromBig(deletedAmount)
	if err != nil {
		return terminalResult{}, ErrInvariant
	}
	carry, err := ledger.AmountFromBig(record.PlayerPool.Big())
	if err != nil {
		return terminalResult{}, ErrInvariant
	}
	plan, err := ledger.NewRPSTerminal(ledger.Meta{OperationID: *record.TerminalOperationID, CreatedAt: now}, record.ID,
		record.AccountID, external.ID, welfare.AccountID, payouts, deleted, carry)
	if err != nil {
		return terminalResult{}, ErrInvariant
	}
	ref, _ := ledger.RPSSessionReservation(record.ID)
	_, err = ledger.ConsumeReserved(ctx, tx, ref, plan, func(ctx context.Context, tx *sql.Tx) error {
		if err := service.insertTerminalFactsTx(ctx, tx, record, now); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM game_rps_sessions WHERE id=? AND state='terminal_processing' AND revision=?`,
			record.ID, db.EncodeU128(record.Revision))
		if err != nil {
			return classifyDB(err)
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			return ErrConflict
		}
		return nil
	})
	if err != nil {
		return terminalResult{}, mapLedger(err)
	}
	var facts activities.PublishFacts
	if record.PlayerPool.Big().Sign() > 0 {
		facts, err = service.pools.RecordPoolTransfers(ctx, tx, now, welfare)
		if err != nil {
			return terminalResult{}, classifyDB(err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE admin_alerts SET resolved=1,resolved_at=?
WHERE kind='rps_terminal_retrying' AND ref=? AND resolved=0`, now, record.ID); err != nil {
		return terminalResult{}, classifyDB(err)
	}
	sort.Slice(users, func(i, j int) bool { return users[i] < users[j] })
	return terminalResult{Users: users, Facts: facts}, nil
}

func (service *Service) insertTerminalFactsTx(ctx context.Context, tx *sql.Tx, record *sessionRecord, now int64) error {
	deleteAt := now + summaryWindowSeconds
	if deleteAt < now || deleteAt > 253402300799 {
		return ErrInvariant
	}
	totalTimeout, totalRock, totalScissors, totalPaper := new(big.Int), new(big.Int), new(big.Int), new(big.Int)
	for _, seat := range record.Seats {
		totalTimeout.Add(totalTimeout, seat.TimeoutCount.Big())
		totalRock.Add(totalRock, seat.RockCount.Big())
		totalScissors.Add(totalScissors, seat.ScissorsCount.Big())
		totalPaper.Add(totalPaper, seat.PaperCount.Big())
	}
	timeout, err := u128(totalTimeout)
	if err != nil {
		return err
	}
	rock, err := u128(totalRock)
	if err != nil {
		return err
	}
	scissors, err := u128(totalScissors)
	if err != nil {
		return err
	}
	paper, err := u128(totalPaper)
	if err != nil {
		return err
	}
	welfareTotal, err := u128(new(big.Int).Add(record.WelfareCutTotal.Big(), record.WelfareCarryTotal.Big()))
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO game_rps_summaries(
session_id,mode,rules_version,base_milli,platform_bp,welfare_bp,thursday_bp,started_at,terminal_at,terminal_reason,
base_round_count,paid_tie_count,free_tie_count,total_timeout_count,total_rock_count,total_scissors_count,total_paper_count,
platform_total,welfare_total,thursday_total,delete_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		record.ID, record.Mode, record.RulesVersion, record.BaseMilli, record.Pumps.Platform, record.Pumps.Welfare,
		record.Pumps.Thursday, record.StartedAt, now, *record.TerminalReason, db.EncodeU128(record.BaseRoundCount),
		db.EncodeU128(record.PaidTieCount), db.EncodeU128(record.FreeTieCount), db.EncodeU128(timeout), db.EncodeU128(rock),
		db.EncodeU128(scissors), db.EncodeU128(paper), db.EncodeU128(record.PlatformCutTotal), db.EncodeU128(welfareTotal),
		db.EncodeU128(record.ThursdayCutTotal), deleteAt); err != nil {
		return classifyDB(err)
	}
	outcomes := [3]string{}
	for index, seat := range record.Seats {
		outcome, err := terminalOutcome(seat)
		if err != nil {
			return err
		}
		outcomes[index] = outcome
		var user any
		if seat.DeletionState == "active" && seat.UserID != nil {
			user = *seat.UserID
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO game_rps_summary_seats(
session_id,seat_no,user_id,input,returned,wallet_net_sign,wallet_net_mag,timeout_count,rock_count,scissors_count,paper_count)
VALUES(?,?,?,?,?,?,?,?,?,?,?)`, record.ID, index, user, db.EncodeU256(seat.TotalInput), db.EncodeU256(seat.TotalReturned),
			*seat.WalletNetSign, db.EncodeU128(*seat.WalletNetMag), db.EncodeU128(seat.TimeoutCount), db.EncodeU128(seat.RockCount),
			db.EncodeU128(seat.ScissorsCount), db.EncodeU128(seat.PaperCount)); err != nil {
			return classifyDB(err)
		}
	}
	for index, seat := range record.Seats {
		if seat.DeletionState != "active" || seat.UserID == nil {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO game_rps_pending_results(
user_id,session_id_text,mode,terminal_reason,own_seat_no,own_input,own_returned,own_wallet_net_sign,own_wallet_net_mag,
seat0_result,seat1_result,seat2_result,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			*seat.UserID, record.ID, record.Mode, *record.TerminalReason, index, db.EncodeU256(seat.TotalInput),
			db.EncodeU256(seat.TotalReturned), *seat.WalletNetSign, db.EncodeU128(*seat.WalletNetMag),
			outcomes[0], outcomes[1], outcomes[2], now); err != nil {
			return classifyDB(err)
		}
		if err := service.applyFunStatsTx(ctx, tx, *seat.UserID, seat, now); err != nil {
			return err
		}
		if err := service.applyRankFactTx(ctx, tx, record.ID, *seat.UserID, record.Mode, now, *seat.WalletNetSign, *seat.WalletNetMag); err != nil {
			return err
		}
	}
	return nil
}

func (service *Service) applyFunStatsTx(ctx context.Context, tx *sql.Tx, userID int64, seat seatRecord, now int64) error {
	one, _ := u128(bigOne)
	profitable := db.U128{}
	if seat.WalletNetSign != nil && *seat.WalletNetSign > 0 {
		profitable = one
	}
	var completedRaw, profitableRaw, rockRaw, scissorsRaw, paperRaw []byte
	err := tx.QueryRowContext(ctx, `SELECT completed_count,profitable_count,rock_count,scissors_count,paper_count
FROM game_rps_fun_stats WHERE user_id=?`, userID).Scan(&completedRaw, &profitableRaw, &rockRaw, &scissorsRaw, &paperRaw)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `INSERT INTO game_rps_fun_stats(
user_id,completed_count,profitable_count,rock_count,scissors_count,paper_count,updated_at) VALUES(?,?,?,?,?,?,?)`,
			userID, db.EncodeU128(one), db.EncodeU128(profitable), db.EncodeU128(seat.RockCount), db.EncodeU128(seat.ScissorsCount),
			db.EncodeU128(seat.PaperCount), now)
		return classifyDB(err)
	}
	if err != nil {
		return classifyDB(err)
	}
	oldRaw := [5][]byte{completedRaw, profitableRaw, rockRaw, scissorsRaw, paperRaw}
	additions := [5]db.U128{one, profitable, seat.RockCount, seat.ScissorsCount, seat.PaperCount}
	next := [5]db.U128{}
	for index := range oldRaw {
		old, err := db.DecodeU128(oldRaw[index])
		if err != nil {
			return ErrInvariant
		}
		next[index], err = addU128(old, additions[index])
		if err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE game_rps_fun_stats SET
completed_count=?,profitable_count=?,rock_count=?,scissors_count=?,paper_count=?,updated_at=?
WHERE user_id=? AND completed_count=? AND profitable_count=? AND rock_count=? AND scissors_count=? AND paper_count=?`,
		db.EncodeU128(next[0]), db.EncodeU128(next[1]), db.EncodeU128(next[2]), db.EncodeU128(next[3]), db.EncodeU128(next[4]), now,
		userID, completedRaw, profitableRaw, rockRaw, scissorsRaw, paperRaw)
	if err != nil {
		return classifyDB(err)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return ErrConflict
	}
	return nil
}

type rankAggregate struct {
	SessionCount, ProfitableCount db.U128
	NetSign                       int
	NetMag                        db.U128
	ProfitRate                    int
	ProfitAchieved, NetAchieved   int64
	ProfitKey, NetKey             [32]byte
	Revision                      db.U128
}

func loadRankAggregate(ctx context.Context, tx *sql.Tx, userID int64, mode string) (rankAggregate, bool, error) {
	var record rankAggregate
	var sessionsRaw, profitableRaw, netRaw, revisionRaw, profitKey, netKey []byte
	var eligible int
	err := tx.QueryRowContext(ctx, `SELECT session_count,profitable_count,net_profit_sign,net_profit_mag,eligible,
profit_rate_bp,profit_rate_achieved_at,net_profit_achieved_at,profit_public_tie_key,net_public_tie_key,revision
FROM game_rps_rank_aggregates WHERE user_id=? AND mode=?`, userID, mode).Scan(
		&sessionsRaw, &profitableRaw, &record.NetSign, &netRaw, &eligible, &record.ProfitRate,
		&record.ProfitAchieved, &record.NetAchieved, &profitKey, &netKey, &revisionRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return rankAggregate{}, false, nil
	}
	if err != nil {
		return rankAggregate{}, false, classifyDB(err)
	}
	if record.SessionCount, err = db.DecodeU128(sessionsRaw); err == nil {
		record.ProfitableCount, err = db.DecodeU128(profitableRaw)
	}
	if err == nil {
		record.NetMag, err = db.DecodeU128(netRaw)
	}
	if err == nil {
		record.Revision, err = db.DecodeU128(revisionRaw)
	}
	if err != nil || len(profitKey) != 32 || len(netKey) != 32 || eligible < 0 || eligible > 1 {
		return rankAggregate{}, false, ErrInvariant
	}
	copy(record.ProfitKey[:], profitKey)
	copy(record.NetKey[:], netKey)
	return record, true, nil
}

func (service *Service) applyRankFactTx(ctx context.Context, tx *sql.Tx, sessionID string, userID int64, mode string, now int64, sign int, magnitude db.U128) error {
	expires := now + summaryWindowSeconds
	profitable := 0
	if sign > 0 {
		profitable = 1
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO game_rps_rank_facts(
session_id_text,user_id,mode,terminal_at,expires_at,wallet_net_sign,wallet_net_mag,profitable,aggregate_applied)
VALUES(?,?,?,?,?,?,?,?,1)`, sessionID, userID, mode, now, expires, sign, db.EncodeU128(magnitude), profitable); err != nil {
		return classifyDB(err)
	}
	record, found, err := loadRankAggregate(ctx, tx, userID, mode)
	if err != nil {
		return err
	}
	one, _ := u128(bigOne)
	if !found {
		profitKey, err := leaderboardTieKey(service.keys.leaderboard, "profit_rate", mode, userID)
		if err != nil {
			return err
		}
		netKey, err := leaderboardTieKey(service.keys.leaderboard, "net_profit", mode, userID)
		if err != nil {
			return err
		}
		profitCount := db.U128{}
		if profitable == 1 {
			profitCount = one
		}
		rate, _ := profitRate(bigOne, profitCount.Big())
		eligible := 0
		if _, err := tx.ExecContext(ctx, `INSERT INTO game_rps_rank_aggregates(
user_id,mode,session_count,profitable_count,net_profit_sign,net_profit_mag,eligible,profit_rate_bp,
profit_rate_achieved_at,net_profit_achieved_at,profit_public_tie_key,net_public_tie_key,revision,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, userID, mode, db.EncodeU128(one), db.EncodeU128(profitCount), sign,
			db.EncodeU128(magnitude), eligible, rate, now, now, profitKey[:], netKey[:], db.EncodeU128(one), now); err != nil {
			return classifyDB(err)
		}
		return nil
	}
	nextSessions, err := addCounter(record.SessionCount, 1)
	if err != nil {
		return err
	}
	nextProfitable, err := addCounter(record.ProfitableCount, int64(profitable))
	if err != nil {
		return err
	}
	nextSign, nextMag, err := signedAdd(record.NetSign, record.NetMag.Big(), sign, magnitude.Big())
	if err != nil {
		return err
	}
	rate, err := profitRate(nextSessions.Big(), nextProfitable.Big())
	if err != nil {
		return err
	}
	profitAchieved, netAchieved := record.ProfitAchieved, record.NetAchieved
	if rate != record.ProfitRate {
		profitAchieved = now
	}
	if nextSign != record.NetSign || nextMag != record.NetMag {
		netAchieved = now
	}
	nextRevision, err := incU128(record.Revision)
	if err != nil {
		return err
	}
	eligible := 0
	if nextSessions.Big().Cmp(big.NewInt(10)) >= 0 {
		eligible = 1
	}
	result, err := tx.ExecContext(ctx, `UPDATE game_rps_rank_aggregates SET
session_count=?,profitable_count=?,net_profit_sign=?,net_profit_mag=?,eligible=?,profit_rate_bp=?,
profit_rate_achieved_at=?,net_profit_achieved_at=?,revision=?,updated_at=? WHERE user_id=? AND mode=? AND revision=?`,
		db.EncodeU128(nextSessions), db.EncodeU128(nextProfitable), nextSign, db.EncodeU128(nextMag), eligible, rate,
		profitAchieved, netAchieved, db.EncodeU128(nextRevision), now, userID, mode, db.EncodeU128(record.Revision))
	if err != nil {
		return classifyDB(err)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return ErrConflict
	}
	return nil
}
