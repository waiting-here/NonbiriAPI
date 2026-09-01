package rps

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type queueRecord struct {
	ID                     string
	UserID                 int64
	AccountID              int64
	Mode                   string
	Revision               db.U128
	ReservationOperationID string
	Reserved               db.U128
	LedgerRowsRemaining    db.U128
	DeviceHash             [32]byte
	IPHash                 [32]byte
	Deadline               int64
	CreatedAt              int64
}

const queueColumns = `id,user_id,account_id,mode,revision,reservation_operation_id,reserved,
ledger_rows_remaining,device_token_hash,source_ip_hash,deadline,created_at`

func scanQueue(scanner interface{ Scan(...any) error }) (queueRecord, error) {
	var record queueRecord
	var revisionRaw, reservedRaw, remainingRaw, deviceRaw, ipRaw []byte
	err := scanner.Scan(&record.ID, &record.UserID, &record.AccountID, &record.Mode, &revisionRaw,
		&record.ReservationOperationID, &reservedRaw, &remainingRaw, &deviceRaw, &ipRaw,
		&record.Deadline, &record.CreatedAt)
	if err != nil {
		return queueRecord{}, err
	}
	record.Revision, err = db.DecodeU128(revisionRaw)
	if err == nil {
		record.Reserved, err = db.DecodeU128(reservedRaw)
	}
	if err == nil {
		record.LedgerRowsRemaining, err = db.DecodeU128(remainingRaw)
	}
	if err != nil || len(deviceRaw) != 32 || len(ipRaw) != 32 {
		return queueRecord{}, ErrInvariant
	}
	copy(record.DeviceHash[:], deviceRaw)
	copy(record.IPHash[:], ipRaw)
	if !db.ValidateOpaqueID(record.ID, "rpsq_") || !db.ValidateOpaqueID(record.ReservationOperationID, "op_") ||
		record.UserID <= 0 || record.AccountID <= 0 || game.ResolveMode(game.RPSID, record.Mode) != nil ||
		record.Revision.Big().Sign() <= 0 || record.Reserved.Big().Sign() <= 0 ||
		record.LedgerRowsRemaining.Big().Cmp(bigOne) != 0 || record.CreatedAt < 0 ||
		record.Deadline < record.CreatedAt+30 || record.Deadline > record.CreatedAt+120 || record.Deadline > 253402300799 {
		return queueRecord{}, ErrInvariant
	}
	return record, nil
}

func loadQueueByUser(ctx context.Context, tx *sql.Tx, userID int64) (queueRecord, bool, error) {
	record, err := scanQueue(tx.QueryRowContext(ctx, `SELECT `+queueColumns+` FROM game_rps_queue WHERE user_id=?`, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return queueRecord{}, false, nil
	}
	if err != nil {
		return queueRecord{}, false, classifyDB(err)
	}
	return record, true, nil
}

func loadQueueByID(ctx context.Context, tx *sql.Tx, queueID string, userID int64) (queueRecord, bool, error) {
	record, err := scanQueue(tx.QueryRowContext(ctx, `SELECT `+queueColumns+` FROM game_rps_queue WHERE id=? AND user_id=?`, queueID, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return queueRecord{}, false, nil
	}
	if err != nil {
		return queueRecord{}, false, classifyDB(err)
	}
	return record, true, nil
}

type seatRecord struct {
	SeatNo                  int
	UserID                  *int64
	DeletionState           string
	DisplayName             *string
	AvatarURL               *string
	StartingBalance         db.U128
	CurrentBalance          db.U128
	CurrentRoundInput       db.U128
	CurrentAllIn            bool
	GestureEnvelope         []byte
	GesturePhaseSeq         *db.U128
	FollowerAction          *string
	LastActionPhaseSeq      *db.U128
	TotalInput              db.U256
	TotalReturned           db.U256
	TerminalReturn          *db.U128
	WalletNetSign           *int
	WalletNetMag            *db.U128
	RockCount               db.U128
	ScissorsCount           db.U128
	PaperCount              db.U128
	TimeoutCount            db.U128
	SnapshotCompletedCount  *db.U128
	SnapshotProfitableCount *db.U128
	SnapshotRockCount       *db.U128
	SnapshotScissorsCount   *db.U128
	SnapshotPaperCount      *db.U128
	StatsApplied            bool
}

type sessionRecord struct {
	ID                        string
	AccountID                 int64
	Mode                      string
	RulesVersion              int
	State                     string
	Phase                     string
	Revision                  db.U128
	PhaseSeq                  db.U128
	IdentityEpoch             db.U128
	CutSeq                    db.U128
	LedgerRowsRemaining       db.U128
	DealerSeat                *int
	BaseMilli                 int64
	Pumps                     PumpsBP
	GestureSeconds            int
	DealerSeconds             int
	FollowerSeconds           int
	PlayerPool                db.U128
	PermanentMultiplier       db.U128
	PoolBaseMultiplier        *db.U128
	CurrentPlanMultiplier     *db.U128
	DealerRaise               *db.U128
	BaseRoundCount            db.U128
	PaidTieCount              db.U128
	FreeTieCount              db.U128
	PaidPoolStreak            db.U128
	FreePoolStreak            db.U128
	PlatformCutTotal          db.U128
	WelfareCutTotal           db.U128
	ThursdayCutTotal          db.U128
	WelfareCarryTotal         db.U128
	ReminderState             string
	PhaseDeadline             *int64
	HealthEpoch               int64
	RecentEvents              []RecentEvent
	RecentFirstSeq            db.U128
	RecentLastSeq             db.U128
	TerminalOperationID       *string
	TerminalRetryAttemptCount db.U128
	TerminalNextRetryAt       *int64
	TerminalLastErrorClass    *string
	StartedAt                 int64
	TerminalReason            *string
	Seats                     [3]seatRecord
}

const sessionColumns = `id,account_id,mode,rules_version,state,phase,revision,phase_seq,identity_epoch,cut_seq,
ledger_rows_remaining,dealer_seat,base_milli,platform_bp,welfare_bp,thursday_bp,gesture_seconds,dealer_seconds,
follower_seconds,player_pool,permanent_multiplier,pool_base_multiplier,current_plan_multiplier,dealer_raise,
base_round_count,paid_tie_count,free_tie_count,paid_pool_streak,free_pool_streak,platform_cut_total,welfare_cut_total,
thursday_cut_total,welfare_carry_total,reminder_state,phase_deadline,health_epoch,recent_events_blob,recent_first_seq,
recent_last_seq,recent_event_count,terminal_operation_id,terminal_retry_attempt_count,terminal_next_retry_at,
terminal_last_error_class,started_at,terminal_reason`

func decodeRequiredU128(raw []byte) (db.U128, error) {
	value, err := db.DecodeU128(raw)
	if err != nil {
		return db.U128{}, ErrInvariant
	}
	return value, nil
}

func decodeOptionalU128(raw []byte) (*db.U128, error) {
	if raw == nil {
		return nil, nil
	}
	value, err := decodeRequiredU128(raw)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func scanSession(scanner interface{ Scan(...any) error }) (sessionRecord, error) {
	var record sessionRecord
	var dealer sql.NullInt64
	var deadline, retryAt sql.NullInt64
	var terminalOperation, retryClass, terminalReason sql.NullString
	var revisionRaw, phaseRaw, identityRaw, cutRaw, remainingRaw []byte
	var poolRaw, permanentRaw, poolBaseRaw, planRaw, raiseRaw []byte
	var baseCountRaw, paidCountRaw, freeCountRaw, paidStreakRaw, freeStreakRaw []byte
	var platformRaw, welfareRaw, thursdayRaw, carryRaw []byte
	var eventBlob, firstRaw, lastRaw, retryCountRaw []byte
	var eventCount int
	err := scanner.Scan(
		&record.ID, &record.AccountID, &record.Mode, &record.RulesVersion, &record.State, &record.Phase,
		&revisionRaw, &phaseRaw, &identityRaw, &cutRaw, &remainingRaw, &dealer, &record.BaseMilli,
		&record.Pumps.Platform, &record.Pumps.Welfare, &record.Pumps.Thursday, &record.GestureSeconds,
		&record.DealerSeconds, &record.FollowerSeconds, &poolRaw, &permanentRaw, &poolBaseRaw, &planRaw,
		&raiseRaw, &baseCountRaw, &paidCountRaw, &freeCountRaw, &paidStreakRaw, &freeStreakRaw,
		&platformRaw, &welfareRaw, &thursdayRaw, &carryRaw, &record.ReminderState, &deadline,
		&record.HealthEpoch, &eventBlob, &firstRaw, &lastRaw, &eventCount, &terminalOperation,
		&retryCountRaw, &retryAt, &retryClass, &record.StartedAt, &terminalReason,
	)
	if err != nil {
		return sessionRecord{}, err
	}
	required := []struct {
		raw []byte
		out *db.U128
	}{
		{revisionRaw, &record.Revision}, {phaseRaw, &record.PhaseSeq}, {identityRaw, &record.IdentityEpoch},
		{cutRaw, &record.CutSeq}, {remainingRaw, &record.LedgerRowsRemaining}, {poolRaw, &record.PlayerPool},
		{permanentRaw, &record.PermanentMultiplier}, {baseCountRaw, &record.BaseRoundCount},
		{paidCountRaw, &record.PaidTieCount}, {freeCountRaw, &record.FreeTieCount},
		{paidStreakRaw, &record.PaidPoolStreak}, {freeStreakRaw, &record.FreePoolStreak},
		{platformRaw, &record.PlatformCutTotal}, {welfareRaw, &record.WelfareCutTotal},
		{thursdayRaw, &record.ThursdayCutTotal}, {carryRaw, &record.WelfareCarryTotal},
		{firstRaw, &record.RecentFirstSeq}, {lastRaw, &record.RecentLastSeq},
		{retryCountRaw, &record.TerminalRetryAttemptCount},
	}
	for _, value := range required {
		decoded, decodeErr := decodeRequiredU128(value.raw)
		if decodeErr != nil {
			return sessionRecord{}, decodeErr
		}
		*value.out = decoded
	}
	if record.PoolBaseMultiplier, err = decodeOptionalU128(poolBaseRaw); err == nil {
		record.CurrentPlanMultiplier, err = decodeOptionalU128(planRaw)
	}
	if err == nil {
		record.DealerRaise, err = decodeOptionalU128(raiseRaw)
	}
	if err != nil {
		return sessionRecord{}, err
	}
	if dealer.Valid {
		value := int(dealer.Int64)
		record.DealerSeat = &value
	}
	if deadline.Valid {
		value := deadline.Int64
		record.PhaseDeadline = &value
	}
	if retryAt.Valid {
		value := retryAt.Int64
		record.TerminalNextRetryAt = &value
	}
	if terminalOperation.Valid {
		value := terminalOperation.String
		record.TerminalOperationID = &value
	}
	if retryClass.Valid {
		value := retryClass.String
		record.TerminalLastErrorClass = &value
	}
	if terminalReason.Valid {
		value := terminalReason.String
		record.TerminalReason = &value
	}
	if len(eventBlob) == 0 {
		record.RecentEvents = []RecentEvent{}
	} else if json.Unmarshal(eventBlob, &record.RecentEvents) != nil {
		return sessionRecord{}, ErrInvariant
	}
	if len(record.RecentEvents) != eventCount || len(record.RecentEvents) > maxRecentEvents {
		return sessionRecord{}, ErrInvariant
	}
	if err := validateSessionHeader(record); err != nil {
		return sessionRecord{}, err
	}
	return record, nil
}

func validateSessionHeader(record sessionRecord) error {
	if !db.ValidateOpaqueID(record.ID, "rps_") || record.AccountID <= 0 || game.ResolveMode(game.RPSID, record.Mode) != nil ||
		record.RulesVersion < 1 || record.Revision.Big().Sign() <= 0 || record.PhaseSeq.Big().Sign() <= 0 ||
		record.IdentityEpoch.Big().Sign() <= 0 || record.BaseMilli <= 0 || record.BaseMilli > game.MaxMoneyMilli ||
		record.Pumps.Platform < 0 || record.Pumps.Welfare < 0 || record.Pumps.Thursday < 0 ||
		record.Pumps.Platform+record.Pumps.Welfare+record.Pumps.Thursday >= 10000 ||
		record.GestureSeconds < 5 || record.GestureSeconds > 20 || record.DealerSeconds < 5 || record.DealerSeconds > 15 ||
		record.FollowerSeconds < 5 || record.FollowerSeconds > 15 || record.PermanentMultiplier.Big().Sign() <= 0 ||
		record.StartedAt < 0 || record.StartedAt > 253402300799 || record.HealthEpoch < 0 {
		return ErrInvariant
	}
	if record.Mode == game.RPSModeStandard && record.BaseMilli > game.MaxMoneyMilli/game.RPSStandardMultiplier {
		return ErrInvariant
	}
	if record.State == StateStarted {
		if record.PhaseDeadline == nil || record.TerminalOperationID != nil || record.TerminalReason != nil ||
			record.TerminalRetryAttemptCount.Big().Sign() != 0 || record.TerminalNextRetryAt != nil || record.TerminalLastErrorClass != nil {
			return ErrInvariant
		}
		switch record.Phase {
		case PhaseGesture, PhaseDealerRaise, PhaseFollowers, PhasePaidPoolGesture, PhaseFreePoolGesture, PhaseUltimateGesture:
		default:
			return ErrInvariant
		}
	} else if record.State == StateTerminalProcessing {
		if record.Phase != PhaseTerminal || record.PhaseDeadline != nil || record.TerminalOperationID == nil ||
			record.TerminalReason == nil || !validTerminalReason(*record.TerminalReason) {
			return ErrInvariant
		}
	} else {
		return ErrInvariant
	}
	if len(record.RecentEvents) == 0 {
		if record.RecentFirstSeq.Big().Sign() != 0 || record.RecentLastSeq.Big().Sign() != 0 {
			return ErrInvariant
		}
	} else {
		if record.RecentEvents[0].Seq != record.RecentFirstSeq.Decimal() ||
			record.RecentEvents[len(record.RecentEvents)-1].Seq != record.RecentLastSeq.Decimal() {
			return ErrInvariant
		}
		previous := new(big.Int)
		for index, event := range record.RecentEvents {
			sequence, err := db.ParseU128Decimal(event.Seq)
			phase, phaseErr := db.ParseU128Decimal(event.PhaseSeq)
			if err != nil || phaseErr != nil || phase.Big().Cmp(record.PhaseSeq.Big()) > 0 ||
				event.IdentityEpoch != record.IdentityEpoch.Decimal() || !validEvent(event) ||
				(index > 0 && sequence.Big().Cmp(new(big.Int).Add(previous, bigOne)) != 0) {
				return ErrInvariant
			}
			previous.Set(sequence.Big())
		}
	}
	return nil
}

func validTerminalReason(reason string) bool {
	switch reason {
	case TerminalQuickResolved, TerminalStandardRoundLimit, TerminalStandardInsufficient,
		TerminalDeathmatchExhausted, TerminalUltimateResolved, TerminalFreeTieLimit:
		return true
	default:
		return false
	}
}

func validEvent(event RecentEvent) bool {
	sequence, err := db.ParseU128Decimal(event.Seq)
	if err != nil || sequence.Big().Sign() <= 0 {
		return false
	}
	epoch, err := db.ParseU128Decimal(event.IdentityEpoch)
	if err != nil || epoch.Big().Sign() <= 0 {
		return false
	}
	phase, err := db.ParseU128Decimal(event.PhaseSeq)
	if err != nil || phase.Big().Sign() <= 0 || len(event.SafePayload) == 0 || !json.Valid(event.SafePayload) {
		return false
	}
	if event.Kind == EventIdentityReset {
		var payload identityResetPayload
		if !rpsDecodeStrictBytes(event.SafePayload, &payload) || payload.IdentityEpoch != event.IdentityEpoch {
			return false
		}
	}
	return validEventPayload(event.Kind, event.SafePayload)
}

func scanSeat(scanner interface{ Scan(...any) error }) (seatRecord, error) {
	var record seatRecord
	var user sql.NullInt64
	var display, avatar, follower sql.NullString
	var allIn, statsApplied int
	var startRaw, currentRaw, roundRaw, gesturePhaseRaw, lastActionRaw []byte
	var totalInputRaw, totalReturnedRaw, terminalRaw, walletRaw []byte
	var walletSign sql.NullInt64
	var rockRaw, scissorsRaw, paperRaw, timeoutRaw []byte
	var snapshotCompleted, snapshotProfitable, snapshotRock, snapshotScissors, snapshotPaper []byte
	err := scanner.Scan(&record.SeatNo, &user, &record.DeletionState, &display, &avatar, &startRaw, &currentRaw,
		&roundRaw, &allIn, &record.GestureEnvelope, &gesturePhaseRaw, &follower, &lastActionRaw,
		&totalInputRaw, &totalReturnedRaw, &terminalRaw, &walletSign, &walletRaw,
		&rockRaw, &scissorsRaw, &paperRaw, &timeoutRaw, &snapshotCompleted, &snapshotProfitable,
		&snapshotRock, &snapshotScissors, &snapshotPaper, &statsApplied)
	if err != nil {
		return seatRecord{}, err
	}
	required := []struct {
		raw []byte
		out *db.U128
	}{{startRaw, &record.StartingBalance}, {currentRaw, &record.CurrentBalance}, {roundRaw, &record.CurrentRoundInput},
		{rockRaw, &record.RockCount}, {scissorsRaw, &record.ScissorsCount}, {paperRaw, &record.PaperCount}, {timeoutRaw, &record.TimeoutCount}}
	for _, value := range required {
		decoded, decodeErr := decodeRequiredU128(value.raw)
		if decodeErr != nil {
			return seatRecord{}, decodeErr
		}
		*value.out = decoded
	}
	var errDecode error
	if record.TotalInput, errDecode = db.DecodeU256(totalInputRaw); errDecode == nil {
		record.TotalReturned, errDecode = db.DecodeU256(totalReturnedRaw)
	}
	if errDecode != nil {
		return seatRecord{}, ErrInvariant
	}
	if record.GesturePhaseSeq, errDecode = decodeOptionalU128(gesturePhaseRaw); errDecode == nil {
		record.LastActionPhaseSeq, errDecode = decodeOptionalU128(lastActionRaw)
	}
	if errDecode == nil {
		record.TerminalReturn, errDecode = decodeOptionalU128(terminalRaw)
	}
	if errDecode == nil {
		record.WalletNetMag, errDecode = decodeOptionalU128(walletRaw)
	}
	if errDecode != nil {
		return seatRecord{}, errDecode
	}
	optionals := []struct {
		raw []byte
		out **db.U128
	}{{snapshotCompleted, &record.SnapshotCompletedCount}, {snapshotProfitable, &record.SnapshotProfitableCount},
		{snapshotRock, &record.SnapshotRockCount}, {snapshotScissors, &record.SnapshotScissorsCount}, {snapshotPaper, &record.SnapshotPaperCount}}
	for _, value := range optionals {
		decoded, decodeErr := decodeOptionalU128(value.raw)
		if decodeErr != nil {
			return seatRecord{}, decodeErr
		}
		*value.out = decoded
	}
	if user.Valid {
		value := user.Int64
		record.UserID = &value
	}
	if display.Valid {
		value := display.String
		record.DisplayName = &value
	}
	if avatar.Valid {
		value := avatar.String
		record.AvatarURL = &value
	}
	if follower.Valid {
		value := follower.String
		record.FollowerAction = &value
	}
	if walletSign.Valid {
		value := int(walletSign.Int64)
		record.WalletNetSign = &value
	}
	record.CurrentAllIn = allIn == 1
	record.StatsApplied = statsApplied == 1
	if record.SeatNo < 0 || record.SeatNo > 2 || record.StartingBalance.Big().Sign() <= 0 ||
		allIn < 0 || allIn > 1 || statsApplied < 0 || statsApplied > 1 ||
		(record.GestureEnvelope == nil) != (record.GesturePhaseSeq == nil) ||
		(record.TerminalReturn == nil) != (record.WalletNetSign == nil) ||
		(record.TerminalReturn == nil) != (record.WalletNetMag == nil) {
		return seatRecord{}, ErrInvariant
	}
	if record.DeletionState == "active" {
		if record.UserID == nil || record.DisplayName == nil || *record.DisplayName == "" ||
			record.SnapshotCompletedCount == nil || record.SnapshotProfitableCount == nil || record.SnapshotRockCount == nil ||
			record.SnapshotScissorsCount == nil || record.SnapshotPaperCount == nil {
			return seatRecord{}, ErrInvariant
		}
	} else if record.DeletionState == "deletion_pending" || record.DeletionState == "deidentified" {
		if record.DisplayName != nil || record.AvatarURL != nil || record.SnapshotCompletedCount != nil ||
			record.SnapshotProfitableCount != nil || record.SnapshotRockCount != nil ||
			record.SnapshotScissorsCount != nil || record.SnapshotPaperCount != nil {
			return seatRecord{}, ErrInvariant
		}
	} else {
		return seatRecord{}, ErrInvariant
	}
	return record, nil
}

const seatColumns = `seat_no,user_id,deletion_state,display_name_snapshot,avatar_url_snapshot,starting_balance,
current_balance,current_round_input,current_all_in,current_gesture_envelope,current_gesture_phase_seq,follower_action,
last_action_phase_seq,total_input,total_returned,terminal_return,wallet_net_sign,wallet_net_mag,rock_count,scissors_count,
paper_count,timeout_count,snapshot_completed_count,snapshot_profitable_count,snapshot_rock_count,snapshot_scissors_count,
snapshot_paper_count,stats_applied`

func loadSessionByID(ctx context.Context, tx *sql.Tx, sessionID string) (sessionRecord, bool, error) {
	record, err := scanSession(tx.QueryRowContext(ctx, `SELECT `+sessionColumns+` FROM game_rps_sessions WHERE id=?`, sessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return sessionRecord{}, false, nil
	}
	if err != nil {
		return sessionRecord{}, false, classifyDB(err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+seatColumns+` FROM game_rps_seats WHERE session_id=? ORDER BY seat_no`, sessionID)
	if err != nil {
		return sessionRecord{}, false, classifyDB(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		seat, scanErr := scanSeat(rows)
		if scanErr != nil || seat.SeatNo != count || count >= 3 {
			return sessionRecord{}, false, ErrInvariant
		}
		record.Seats[count] = seat
		count++
	}
	if err := rows.Err(); err != nil {
		return sessionRecord{}, false, classifyDB(err)
	}
	if count != 3 {
		return sessionRecord{}, false, ErrInvariant
	}
	if err := validateSession(ctx, tx, &record); err != nil {
		return sessionRecord{}, false, err
	}
	return record, true, nil
}

func loadSessionByUser(ctx context.Context, tx *sql.Tx, userID int64) (sessionRecord, bool, error) {
	var sessionID string
	err := tx.QueryRowContext(ctx, `SELECT session_id FROM game_rps_user_slots WHERE user_id=? AND session_id IS NOT NULL`, userID).Scan(&sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return sessionRecord{}, false, nil
	}
	if err != nil {
		return sessionRecord{}, false, classifyDB(err)
	}
	record, found, err := loadSessionByID(ctx, tx, sessionID)
	if err != nil || !found {
		if err == nil {
			err = ErrInvariant
		}
		return sessionRecord{}, false, err
	}
	for _, seat := range record.Seats {
		if seat.UserID != nil && *seat.UserID == userID && seat.DeletionState == "active" {
			return record, true, nil
		}
	}
	return sessionRecord{}, false, ErrInvariant
}

func validateSessionShape(record *sessionRecord) error {
	if record == nil {
		return fmt.Errorf("nil session: %w", ErrInvariant)
	}
	if record.DealerSeat != nil && (*record.DealerSeat < 0 || *record.DealerSeat > 2) {
		return fmt.Errorf("dealer seat: %w", ErrInvariant)
	}
	gesturePhase := record.Phase == PhaseGesture || record.Phase == PhasePaidPoolGesture || record.Phase == PhaseFreePoolGesture || record.Phase == PhaseUltimateGesture
	for seat := range record.Seats {
		value := &record.Seats[seat]
		if value.SeatNo != seat {
			return fmt.Errorf("seat %d ordinal: %w", seat, ErrInvariant)
		}
		if (value.GestureEnvelope == nil) != (value.GesturePhaseSeq == nil) {
			return fmt.Errorf("seat %d gesture pair: %w", seat, ErrInvariant)
		}
		if gesturePhase {
			if value.FollowerAction != nil || value.GestureEnvelope == nil && value.LastActionPhaseSeq != nil ||
				value.GestureEnvelope != nil && (value.GesturePhaseSeq == nil || value.LastActionPhaseSeq == nil ||
					value.GesturePhaseSeq.Big().Cmp(record.PhaseSeq.Big()) != 0 || value.LastActionPhaseSeq.Big().Cmp(record.PhaseSeq.Big()) != 0) {
				return fmt.Errorf("seat %d gesture matrix: %w", seat, ErrInvariant)
			}
		} else if record.Phase == PhaseDealerRaise {
			if value.GestureEnvelope == nil || value.GesturePhaseSeq == nil || value.GesturePhaseSeq.Big().Cmp(record.PhaseSeq.Big()) >= 0 ||
				value.FollowerAction != nil || value.LastActionPhaseSeq != nil {
				return fmt.Errorf("seat %d dealer matrix: %w", seat, ErrInvariant)
			}
		} else if record.Phase == PhaseFollowers {
			if record.DealerSeat == nil || value.GestureEnvelope == nil || value.GesturePhaseSeq == nil || value.GesturePhaseSeq.Big().Cmp(record.PhaseSeq.Big()) >= 0 {
				return fmt.Errorf("seat %d follower gesture matrix: %w", seat, ErrInvariant)
			}
			if seat == *record.DealerSeat {
				if value.FollowerAction != nil || value.LastActionPhaseSeq != nil {
					return fmt.Errorf("seat %d dealer follower matrix: %w", seat, ErrInvariant)
				}
			} else if (value.FollowerAction == nil) != (value.LastActionPhaseSeq == nil) ||
				value.LastActionPhaseSeq != nil && value.LastActionPhaseSeq.Big().Cmp(record.PhaseSeq.Big()) != 0 {
				return fmt.Errorf("seat %d follower action matrix: %w", seat, ErrInvariant)
			}
		} else if record.Phase == PhaseTerminal {
			if value.GestureEnvelope != nil || value.GesturePhaseSeq != nil || value.FollowerAction != nil || value.LastActionPhaseSeq != nil ||
				value.TerminalReturn == nil || value.WalletNetSign == nil || value.WalletNetMag == nil ||
				value.TerminalReturn.Big().Cmp(value.CurrentBalance.Big()) != 0 {
				return fmt.Errorf("seat %d terminal matrix: %w", seat, ErrInvariant)
			}
		}
	}
	return nil
}

func validateSession(ctx context.Context, tx *sql.Tx, record *sessionRecord) error {
	if err := validateSessionShape(record); err != nil {
		return err
	}
	account, err := ledger.ReadAccount(ctx, tx, record.AccountID)
	if err != nil || account.Kind != ledger.AccountPlatform || account.Code != "rps-session:"+record.ID || account.Balance.Sign() < 0 {
		return fmt.Errorf("session account: %w", ErrInvariant)
	}
	currentTotal := cloneBig(record.PlayerPool.Big())
	startingTotal := new(big.Int)
	for index, seat := range record.Seats {
		expectedCurrent := new(big.Int).Sub(seat.StartingBalance.Big(), seat.TotalInput.Big())
		expectedCurrent.Add(expectedCurrent, seat.TotalReturned.Big())
		if expectedCurrent.Sign() < 0 || expectedCurrent.Cmp(seat.CurrentBalance.Big()) != 0 {
			return fmt.Errorf("seat %d balance start-input+returned=%s current=%s: %w",
				index, expectedCurrent, seat.CurrentBalance.Big(), ErrInvariant)
		}
		if record.State == StateTerminalProcessing {
			sign, magnitude, err := walletNet(seat.CurrentBalance.Big(), seat.StartingBalance.Big())
			if err != nil || seat.WalletNetSign == nil || seat.WalletNetMag == nil ||
				*seat.WalletNetSign != sign || seat.WalletNetMag.Big().Cmp(magnitude.Big()) != 0 {
				return fmt.Errorf("seat %d terminal wallet net: %w", index, ErrInvariant)
			}
		}
		currentTotal.Add(currentTotal, seat.CurrentBalance.Big())
		startingTotal.Add(startingTotal, seat.StartingBalance.Big())
	}
	if currentTotal.Cmp(account.Balance.Big()) != 0 {
		return fmt.Errorf("session balance got=%s account=%s: %w", currentTotal, account.Balance.Big(), ErrInvariant)
	}
	accounted := cloneBig(currentTotal)
	accounted.Add(accounted, record.PlatformCutTotal.Big())
	accounted.Add(accounted, record.WelfareCutTotal.Big())
	accounted.Add(accounted, record.ThursdayCutTotal.Big())
	if startingTotal.Cmp(accounted) != 0 {
		return fmt.Errorf("session conservation start=%s accounted=%s: %w", startingTotal, accounted, ErrInvariant)
	}
	if record.State == StateTerminalProcessing && record.WelfareCarryTotal.Big().Cmp(record.PlayerPool.Big()) != 0 {
		return fmt.Errorf("terminal carry: %w", ErrInvariant)
	}
	return nil
}

func nullableU128(value *db.U128) any {
	if value == nil {
		return nil
	}
	return db.EncodeU128(*value)
}

func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func persistSessionRowsTx(ctx context.Context, tx *sql.Tx, record *sessionRecord, expectedRevision db.U128) error {
	if record == nil || tx == nil {
		return ErrInvariant
	}
	if err := validateSessionShape(record); err != nil {
		return err
	}
	events, err := json.Marshal(record.RecentEvents)
	if err != nil || len(events) > 131072 {
		return ErrInvariant
	}
	if len(record.RecentEvents) == 0 {
		events = []byte{}
	}
	result, err := tx.ExecContext(ctx, `UPDATE game_rps_sessions SET
state=?,phase=?,revision=?,phase_seq=?,identity_epoch=?,cut_seq=?,ledger_rows_remaining=?,dealer_seat=?,
player_pool=?,permanent_multiplier=?,pool_base_multiplier=?,current_plan_multiplier=?,dealer_raise=?,base_round_count=?,
paid_tie_count=?,free_tie_count=?,paid_pool_streak=?,free_pool_streak=?,platform_cut_total=?,welfare_cut_total=?,
thursday_cut_total=?,welfare_carry_total=?,reminder_state=?,phase_deadline=?,health_epoch=?,recent_events_blob=?,
recent_first_seq=?,recent_last_seq=?,recent_event_count=?,terminal_operation_id=?,terminal_retry_attempt_count=?,
terminal_next_retry_at=?,terminal_last_error_class=?,terminal_reason=? WHERE id=? AND revision=?`,
		record.State, record.Phase, db.EncodeU128(record.Revision), db.EncodeU128(record.PhaseSeq),
		db.EncodeU128(record.IdentityEpoch), db.EncodeU128(record.CutSeq), db.EncodeU128(record.LedgerRowsRemaining),
		nullableInt(record.DealerSeat), db.EncodeU128(record.PlayerPool), db.EncodeU128(record.PermanentMultiplier),
		nullableU128(record.PoolBaseMultiplier), nullableU128(record.CurrentPlanMultiplier), nullableU128(record.DealerRaise),
		db.EncodeU128(record.BaseRoundCount), db.EncodeU128(record.PaidTieCount), db.EncodeU128(record.FreeTieCount),
		db.EncodeU128(record.PaidPoolStreak), db.EncodeU128(record.FreePoolStreak), db.EncodeU128(record.PlatformCutTotal),
		db.EncodeU128(record.WelfareCutTotal), db.EncodeU128(record.ThursdayCutTotal), db.EncodeU128(record.WelfareCarryTotal),
		record.ReminderState, nullableInt64(record.PhaseDeadline), record.HealthEpoch, events,
		db.EncodeU128(record.RecentFirstSeq), db.EncodeU128(record.RecentLastSeq), len(record.RecentEvents),
		nullableString(record.TerminalOperationID), db.EncodeU128(record.TerminalRetryAttemptCount),
		nullableInt64(record.TerminalNextRetryAt), nullableString(record.TerminalLastErrorClass), nullableString(record.TerminalReason),
		record.ID, db.EncodeU128(expectedRevision))
	if err != nil {
		return classifyDB(err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return classifyDB(err)
	}
	if changed != 1 {
		return ErrConflict
	}
	for seat := range record.Seats {
		value := &record.Seats[seat]
		var user any
		if value.UserID != nil {
			user = *value.UserID
		}
		allIn, statsApplied := 0, 0
		if value.CurrentAllIn {
			allIn = 1
		}
		if value.StatsApplied {
			statsApplied = 1
		}
		result, err = tx.ExecContext(ctx, `UPDATE game_rps_seats SET
user_id=?,deletion_state=?,display_name_snapshot=?,avatar_url_snapshot=?,current_balance=?,current_round_input=?,
current_all_in=?,current_gesture_envelope=?,current_gesture_phase_seq=?,follower_action=?,last_action_phase_seq=?,
total_input=?,total_returned=?,terminal_return=?,wallet_net_sign=?,wallet_net_mag=?,rock_count=?,scissors_count=?,
paper_count=?,timeout_count=?,snapshot_completed_count=?,snapshot_profitable_count=?,snapshot_rock_count=?,
snapshot_scissors_count=?,snapshot_paper_count=?,stats_applied=? WHERE session_id=? AND seat_no=?`,
			user, value.DeletionState, nullableString(value.DisplayName), nullableString(value.AvatarURL),
			db.EncodeU128(value.CurrentBalance), db.EncodeU128(value.CurrentRoundInput), allIn, nullableBytes(value.GestureEnvelope),
			nullableU128(value.GesturePhaseSeq), nullableString(value.FollowerAction), nullableU128(value.LastActionPhaseSeq),
			db.EncodeU256(value.TotalInput), db.EncodeU256(value.TotalReturned), nullableU128(value.TerminalReturn),
			nullableInt(value.WalletNetSign), nullableU128(value.WalletNetMag), db.EncodeU128(value.RockCount),
			db.EncodeU128(value.ScissorsCount), db.EncodeU128(value.PaperCount), db.EncodeU128(value.TimeoutCount),
			nullableU128(value.SnapshotCompletedCount), nullableU128(value.SnapshotProfitableCount),
			nullableU128(value.SnapshotRockCount), nullableU128(value.SnapshotScissorsCount),
			nullableU128(value.SnapshotPaperCount), statsApplied, record.ID, seat)
		if err != nil {
			return classifyDB(err)
		}
		changed, err = result.RowsAffected()
		if err != nil || changed != 1 {
			return ErrInvariant
		}
	}
	return nil
}

func persistSessionTx(ctx context.Context, tx *sql.Tx, record *sessionRecord, expectedRevision db.U128) error {
	if err := persistSessionRowsTx(ctx, tx, record, expectedRevision); err != nil {
		return err
	}
	return validateSession(ctx, tx, record)
}

func nullableBytes(value []byte) any {
	if value == nil {
		return nil
	}
	return value
}

func safeDisplayName(value string) string {
	if !utf8.ValidString(value) {
		return ""
	}
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(value))
	runes := []rune(value)
	if len(runes) > 128 {
		runes = runes[:128]
	}
	return string(runes)
}

func safeAvatar(value string) *string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil
	}
	copy := value
	return &copy
}

func queueView(record queueRecord, now int64) Queue {
	return Queue{ID: record.ID, Mode: record.Mode, State: "waiting", Revision: record.Revision.Decimal(), Deadline: record.Deadline, ServerNow: now}
}
