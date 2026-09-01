package rps

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/accountstream"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
)

func stringAmount(value *db.U128) *string {
	if value == nil {
		return nil
	}
	formatted := formatMilli(value.Big())
	return &formatted
}

func stringU128(value *db.U128) *string {
	if value == nil {
		return nil
	}
	formatted := value.Decimal()
	return &formatted
}

func funSnapshot(seat seatRecord, self bool) FunSnapshot {
	if seat.SnapshotCompletedCount == nil || seat.SnapshotProfitableCount == nil || seat.SnapshotRockCount == nil ||
		seat.SnapshotScissorsCount == nil || seat.SnapshotPaperCount == nil {
		return FunSnapshot{State: "none"}
	}
	completed := seat.SnapshotCompletedCount.Big()
	if !self && completed.Sign() == 0 {
		return FunSnapshot{State: "none"}
	}
	if !self && completed.Cmp(big.NewInt(10)) < 0 {
		return FunSnapshot{State: "insufficient", CompletedCount: completed.String()}
	}
	return FunSnapshot{
		State: "full", CompletedCount: completed.String(), ProfitableCount: seat.SnapshotProfitableCount.Decimal(),
		RockCount: seat.SnapshotRockCount.Decimal(), ScissorsCount: seat.SnapshotScissorsCount.Decimal(),
		PaperCount: seat.SnapshotPaperCount.Decimal(),
	}
}

func actorOptions(record sessionRecord, seat int) []string {
	if record.State != StateStarted || seat < 0 || seat > 2 || record.Seats[seat].DeletionState != "active" {
		return []string{}
	}
	value := record.Seats[seat]
	switch record.Phase {
	case PhaseGesture, PhasePaidPoolGesture, PhaseFreePoolGesture, PhaseUltimateGesture:
		if value.GestureEnvelope == nil {
			return []string{"gesture"}
		}
	case PhaseDealerRaise:
		if record.DealerSeat != nil && *record.DealerSeat == seat {
			return []string{"dealer_decision"}
		}
	case PhaseFollowers:
		if record.DealerSeat != nil && *record.DealerSeat != seat && value.FollowerAction == nil {
			return []string{"follower_decision"}
		}
	}
	return []string{}
}

func projectState(record sessionRecord, userID int64, now int64) (State, error) {
	selfSeat := -1
	for index := range record.Seats {
		if record.Seats[index].UserID != nil && *record.Seats[index].UserID == userID && record.Seats[index].DeletionState == "active" {
			selfSeat = index
			break
		}
	}
	if selfSeat < 0 {
		return State{}, ErrNotFound
	}
	visible := [3]*string{}
	lastResult := (*string)(nil)
	if reveal, ok := latestReveal(record.RecentEvents); ok {
		code := reveal.ResultCode
		lastResult = &code
		for _, item := range reveal.Gestures {
			gesture := item.Gesture
			visible[item.SeatNo] = &gesture
		}
	}
	seats := make([]Seat, 0, 3)
	for index, persisted := range record.Seats {
		projected := Seat{
			SeatNo: index, Viewer: "opponent", DeletionState: persisted.DeletionState,
			StartingBalance: formatMilli(persisted.StartingBalance.Big()), CurrentBalance: formatMilli(persisted.CurrentBalance.Big()),
			CurrentRoundInput: formatMilli(persisted.CurrentRoundInput.Big()), CurrentAllIn: persisted.CurrentAllIn,
			TotalInput: formatMilli(persisted.TotalInput.Big()), TotalReturned: formatMilli(persisted.TotalReturned.Big()),
			TimeoutCount: persisted.TimeoutCount.Decimal(),
		}
		if persisted.DeletionState == "active" {
			if persisted.DisplayName == nil {
				return State{}, ErrInvariant
			}
			projected.DisplayName, projected.AvatarURL = *persisted.DisplayName, persisted.AvatarURL
			projected.FunSnapshot = funSnapshot(persisted, index == selfSeat)
			projected.VisibleGesture = visible[index]
			if index == selfSeat {
				projected.Viewer = "self"
				projected.FollowerAction = persisted.FollowerAction
			}
		}
		if persisted.TerminalReturn != nil {
			if persisted.WalletNetSign == nil || persisted.WalletNetMag == nil {
				return State{}, ErrInvariant
			}
			terminal := formatMilli(persisted.TerminalReturn.Big())
			projected.TerminalReturn = &terminal
			walletNet := formatSignedMilli(*persisted.WalletNetSign, persisted.WalletNetMag.Big())
			projected.WalletNet = &walletNet
		}
		seats = append(seats, projected)
	}
	state := State{
		SessionID: record.ID, Mode: record.Mode, State: record.State, Phase: record.Phase,
		PhaseSeq: record.PhaseSeq.Decimal(), Revision: record.Revision.Decimal(), IdentityEpoch: record.IdentityEpoch.Decimal(),
		ServerNow: now, Deadline: record.PhaseDeadline,
		RuleSnapshot: RuleSnapshot{
			RulesVersion: record.RulesVersion, Base: formatMilli(big.NewInt(record.BaseMilli)), PumpsBP: record.Pumps,
			GestureSeconds: record.GestureSeconds, DealerSeconds: record.DealerSeconds, FollowerSeconds: record.FollowerSeconds,
			StandardMultiplier: game.RPSStandardMultiplier, FreeTieReminder: game.RPSFreeTieReminder, FreeTieLimit: game.RPSFreeTieLimit,
		},
		Economy: Economy{
			PlayerPool: formatMilli(record.PlayerPool.Big()), PermanentMultiplier: record.PermanentMultiplier.Decimal(),
			PoolBaseMultiplier: stringU128(record.PoolBaseMultiplier), CurrentPlanMultiplier: stringU128(record.CurrentPlanMultiplier),
			DealerRaise: stringAmount(record.DealerRaise), Cuts: Cuts{
				Platform: formatMilli(record.PlatformCutTotal.Big()), Welfare: formatMilli(record.WelfareCutTotal.Big()),
				Thursday: formatMilli(record.ThursdayCutTotal.Big()),
			}, WelfareCarry: formatMilli(record.WelfareCarryTotal.Big()),
		},
		Seats: seats, CurrentActorOptions: actorOptions(record, selfSeat),
		RoundSummary: RoundSummary{
			BaseRoundCount: record.BaseRoundCount.Decimal(), PaidTieCount: record.PaidTieCount.Decimal(),
			FreeTieCount: record.FreeTieCount.Decimal(), PaidPoolStreak: record.PaidPoolStreak.Decimal(),
			FreePoolStreak: record.FreePoolStreak.Decimal(), ReminderActive: record.ReminderState == "active",
			LastRevealResult: lastResult,
		},
		RecentEvents: append([]RecentEvent(nil), record.RecentEvents...), FirstAvailableSeq: record.RecentFirstSeq.Decimal(),
		EventsTruncated: record.RecentFirstSeq.Big().Cmp(big.NewInt(1)) > 0,
	}
	if len(state.RecentEvents) == 0 {
		state.FirstAvailableSeq = "0"
	}
	for {
		body, err := json.Marshal(state)
		if err != nil {
			return State{}, ErrInvariant
		}
		if len(body) <= maxProjectedStateBytes {
			return state, nil
		}
		if len(state.RecentEvents) == 0 {
			return State{}, ErrResourceLimit
		}
		state.RecentEvents = state.RecentEvents[1:]
		state.EventsTruncated = true
		if len(state.RecentEvents) == 0 {
			state.FirstAvailableSeq = record.RecentLastSeq.Decimal()
		} else {
			state.FirstAvailableSeq = state.RecentEvents[0].Seq
		}
	}
}

func loadPending(ctx context.Context, tx *sql.Tx, userID int64) (PendingResult, bool, error) {
	var record PendingResult
	var inputRaw, returnedRaw, walletRaw []byte
	var sign int
	var outcomes [3]string
	err := tx.QueryRowContext(ctx, `SELECT session_id_text,mode,terminal_reason,own_seat_no,own_input,own_returned,
own_wallet_net_sign,own_wallet_net_mag,seat0_result,seat1_result,seat2_result,created_at
FROM game_rps_pending_results WHERE user_id=?`, userID).Scan(
		&record.SessionID, &record.Mode, &record.TerminalReason, &record.OwnSeatNo, &inputRaw, &returnedRaw,
		&sign, &walletRaw, &outcomes[0], &outcomes[1], &outcomes[2], &record.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PendingResult{}, false, nil
	}
	if err != nil {
		return PendingResult{}, false, classifyDB(err)
	}
	input, err := db.DecodeU256(inputRaw)
	if err != nil {
		return PendingResult{}, false, ErrInvariant
	}
	returned, err := db.DecodeU256(returnedRaw)
	if err != nil {
		return PendingResult{}, false, ErrInvariant
	}
	wallet, err := db.DecodeU128(walletRaw)
	if err != nil || sign < -1 || sign > 1 || !validTerminalReason(record.TerminalReason) || record.OwnSeatNo < 0 || record.OwnSeatNo > 2 {
		return PendingResult{}, false, ErrInvariant
	}
	record.OwnInput, record.OwnReturned = formatMilli(input.Big()), formatMilli(returned.Big())
	record.OwnWalletNet = formatSignedMilli(sign, wallet.Big())
	for seat, outcome := range outcomes {
		if outcome != "win" && outcome != "loss" && outcome != "tie" && outcome != "deidentified" {
			return PendingResult{}, false, ErrInvariant
		}
		record.Seats = append(record.Seats, PendingSeat{SeatNo: seat, Result: outcome})
	}
	return record, true, nil
}

func idleModes(snapshot game.ConfigSnapshot) map[string]ModeConfig {
	result := make(map[string]ModeConfig, 3)
	for _, mode := range []string{game.RPSModeQuick, game.RPSModeStandard, game.RPSModeDeathmatch} {
		value := snapshot.RPS.Modes[mode]
		result[mode] = ModeConfig{
			Enabled: snapshot.GamesEnabled && snapshot.RPS.Enabled && value.Enabled,
			Base:    formatMilli(big.NewInt(value.BaseMilli)), PumpsBP: PumpsBP(value.PumpsBP), QueueSeconds: value.QueueSeconds,
			GestureSeconds: value.GestureSeconds, DealerSeconds: value.DealerSeconds, FollowerSeconds: value.FollowerSeconds,
			QueueCapacity: game.RPSQueueCapacity,
		}
	}
	return result
}

func (service *Service) projectHomeTx(ctx context.Context, tx *sql.Tx, userID, now int64) (HomeState, error) {
	if pending, found, err := loadPending(ctx, tx, userID); err != nil {
		return HomeState{}, err
	} else if found {
		return HomeState{Kind: "pending_result", Result: &pending}, nil
	}
	if record, found, err := loadSessionByUser(ctx, tx, userID); err != nil {
		return HomeState{}, err
	} else if found {
		state, err := projectState(record, userID, now)
		if err != nil {
			return HomeState{}, err
		}
		return HomeState{Kind: "session", Session: &state}, nil
	}
	if queue, found, err := loadQueueByUser(ctx, tx, userID); err != nil {
		return HomeState{}, err
	} else if found {
		view := queueView(queue, now)
		return HomeState{Kind: "queue", Queue: &view}, nil
	}
	var tutorial int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(p.tutorial_rps_seen,0) FROM users u
LEFT JOIN game_user_preferences p ON p.user_id=u.id WHERE u.id=? AND u.is_admin=0`, userID).Scan(&tutorial); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return HomeState{}, ErrUnauthorized
		}
		return HomeState{}, classifyDB(err)
	}
	if tutorial != 0 && tutorial != 1 {
		return HomeState{}, ErrInvariant
	}
	snapshot, err := service.readSnapshot(ctx, tx)
	if err != nil {
		return HomeState{}, err
	}
	return HomeState{Kind: "idle", TutorialSeen: tutorial == 1, Modes: idleModes(snapshot)}, nil
}

func (service *Service) projectHome(ctx context.Context, userID int64) (HomeState, error) {
	now, err := service.decisionNow()
	if err != nil {
		return HomeState{}, err
	}
	tx, err := service.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return HomeState{}, classifyDB(err)
	}
	defer tx.Rollback()
	return service.projectHomeTx(ctx, tx, userID, now)
}

func (service *Service) Snapshot(ctx context.Context, accountID int64, channel accountstream.Channel) (accountstream.Snapshot, error) {
	if channel != accountstream.ChannelRPS || accountID <= 0 {
		return accountstream.Snapshot{}, ErrInvalidRequest
	}
	home, err := service.projectHome(ctx, accountID)
	if err != nil {
		return accountstream.Snapshot{}, err
	}
	body, err := json.Marshal(home)
	if err != nil || len(body) > maxProjectedStateBytes {
		return accountstream.Snapshot{}, ErrResourceLimit
	}
	var revision, epoch *string
	if home.Session != nil {
		value, identity := home.Session.Revision, home.Session.IdentityEpoch
		revision, epoch = &value, &identity
	} else if home.Queue != nil {
		value := home.Queue.Revision
		revision = &value
	}
	return accountstream.Snapshot{Revision: revision, IdentityEpoch: epoch, Data: body}, nil
}

func (service *Service) CurrentIdentityEpoch(ctx context.Context, accountID int64) (*string, error) {
	if accountID <= 0 {
		return nil, ErrInvalidRequest
	}
	var raw []byte
	err := service.database.QueryRowContext(ctx, `SELECT s.identity_epoch FROM game_rps_user_slots us
JOIN game_rps_sessions s ON s.id=us.session_id WHERE us.user_id=? AND us.session_id IS NOT NULL`, accountID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, classifyDB(err)
	}
	epoch, err := db.DecodeU128(raw)
	if err != nil || epoch.Big().Sign() <= 0 {
		return nil, ErrInvariant
	}
	value := epoch.Decimal()
	return &value, nil
}

func (service *Service) publishUsers(ctx context.Context, users []int64, eventType accountstream.EventType) {
	seen := make(map[int64]struct{}, len(users))
	for _, userID := range users {
		if userID <= 0 {
			continue
		}
		if _, exists := seen[userID]; exists {
			continue
		}
		seen[userID] = struct{}{}
		snapshot, err := service.Snapshot(ctx, userID, accountstream.ChannelRPS)
		if err != nil {
			service.reportPublish(err)
			continue
		}
		_, err = service.accountEvents.PublishCommitted(ctx, userID, accountstream.PublishedEvent{
			Channel: accountstream.ChannelRPS, Type: eventType, Revision: snapshot.Revision,
			IdentityEpoch: snapshot.IdentityEpoch, Data: snapshot.Data,
		})
		service.reportPublish(err)
	}
}

func decodeHomeState(body []byte) (HomeState, error) {
	if rpsValidateStrictResponseJSON(body) != nil {
		return HomeState{}, ErrInvariant
	}
	var discriminator struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal(body, &discriminator) != nil {
		return HomeState{}, ErrInvariant
	}
	var result HomeState
	switch discriminator.Kind {
	case "idle":
		var value struct {
			Kind         string                `json:"kind"`
			TutorialSeen bool                  `json:"tutorial_seen"`
			Modes        map[string]ModeConfig `json:"modes"`
		}
		if !rpsDecodeStrictResponseBytes(body, &value) {
			return HomeState{}, ErrInvariant
		}
		result = HomeState{Kind: value.Kind, TutorialSeen: value.TutorialSeen, Modes: value.Modes}
	case "queue":
		var value struct {
			Kind  string `json:"kind"`
			Queue Queue  `json:"queue"`
		}
		if !rpsDecodeStrictResponseBytes(body, &value) {
			return HomeState{}, ErrInvariant
		}
		result = HomeState{Kind: value.Kind, Queue: &value.Queue}
	case "session":
		var value struct {
			Kind    string `json:"kind"`
			Session State  `json:"session"`
		}
		if !rpsDecodeStrictResponseBytes(body, &value) {
			return HomeState{}, ErrInvariant
		}
		result = HomeState{Kind: value.Kind, Session: &value.Session}
	case "pending_result":
		var value struct {
			Kind   string        `json:"kind"`
			Result PendingResult `json:"result"`
		}
		if !rpsDecodeStrictResponseBytes(body, &value) {
			return HomeState{}, ErrInvariant
		}
		result = HomeState{Kind: value.Kind, Result: &value.Result}
	default:
		return HomeState{}, ErrInvariant
	}
	canonical, err := json.Marshal(result)
	if err != nil || len(canonical) > maxProjectedStateBytes || !bytes.Equal(canonical, body) {
		return HomeState{}, ErrInvariant
	}
	return result, nil
}
