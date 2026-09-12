package rps

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"math/bits"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/accountstream"
	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/finance"
	rpsconfig "github.com/waiting-here/NonbiriAPI/internal/game/rps/config"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

const matchCandidateLimit = rpsconfig.RPSQueueCapacity

func mergePublishFacts(left, right activities.PublishFacts) activities.PublishFacts {
	result := activities.PublishFacts{Global: left.Global || right.Global}
	seen := make(map[int64]struct{}, len(left.AccountIDs)+len(right.AccountIDs))
	for _, source := range [][]int64{left.AccountIDs, right.AccountIDs} {
		for _, id := range source {
			if id <= 0 {
				continue
			}
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			result.AccountIDs = append(result.AccountIDs, id)
		}
	}
	return result
}

func (service *Service) loadMatchCandidates(ctx context.Context, tx *sql.Tx, mode string, now int64) ([]queueRecord, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+queueColumns+` FROM game_rps_queue
WHERE mode=? AND deadline>? ORDER BY created_at,id LIMIT ?`, mode, now, matchCandidateLimit)
	if err != nil {
		return nil, classifyDB(err)
	}
	defer rows.Close()
	result := make([]queueRecord, 0, matchCandidateLimit)
	for rows.Next() {
		record, err := scanQueue(rows)
		if err != nil {
			return nil, classifyDB(err)
		}
		result = append(result, record)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyDB(err)
	}
	return result, nil
}

func selectCompatibleMatch(candidates []queueRecord) ([3]queueRecord, bool) {
	var groups [2][]queueRecord
	for _, candidate := range candidates {
		groups[candidate.RulesVersion-1] = append(groups[candidate.RulesVersion-1], candidate)
	}
	var selected [3]queueRecord
	found := false
	for _, group := range groups {
		triple, ok := selectMatch(group)
		if ok && (!found || triple[0].CreatedAt < selected[0].CreatedAt ||
			triple[0].CreatedAt == selected[0].CreatedAt && triple[0].ID < selected[0].ID) {
			selected, found = triple, true
		}
	}
	return selected, found
}

func distinctDevices(values [3]queueRecord) bool {
	return !bytes.Equal(values[0].DeviceHash[:], values[1].DeviceHash[:]) &&
		!bytes.Equal(values[0].DeviceHash[:], values[2].DeviceHash[:]) &&
		!bytes.Equal(values[1].DeviceHash[:], values[2].DeviceHash[:])
}

func distinctIPs(values [3]queueRecord) bool {
	return !bytes.Equal(values[0].IPHash[:], values[1].IPHash[:]) &&
		!bytes.Equal(values[0].IPHash[:], values[2].IPHash[:]) &&
		!bytes.Equal(values[1].IPHash[:], values[2].IPHash[:])
}

func selectMatch(candidates []queueRecord) ([3]queueRecord, bool) {
	if len(candidates) < 3 {
		return [3]queueRecord{}, false
	}
	// Any device-safe triple can include the oldest queue entry: it exists if
	// and only if the candidate set contains two further device hashes. Keep
	// that oldest entry fixed so IP preference cannot starve queue fairness.
	second := -1
	var fallback [3]queueRecord
	for index := 1; index < len(candidates); index++ {
		if candidates[index].DeviceHash == candidates[0].DeviceHash {
			continue
		}
		if second < 0 {
			second = index
			continue
		}
		if candidates[index].DeviceHash != candidates[second].DeviceHash {
			fallback = [3]queueRecord{candidates[0], candidates[second], candidates[index]}
			break
		}
	}
	if fallback[0].ID == "" {
		return [3]queueRecord{}, false
	}

	// Prefer an all-distinct IP triple while retaining the same oldest entry.
	// Bitsets make the exhaustive lexicographic search bounded by
	// queue_capacity^2/word_size instead of cubic work at the 4,096-row cap.
	wordCount := (len(candidates) + 63) / 64
	deviceGroups := make(map[[32]byte][]uint64)
	ipGroups := make(map[[32]byte][]uint64)
	for index, candidate := range candidates {
		device := deviceGroups[candidate.DeviceHash]
		if device == nil {
			device = make([]uint64, wordCount)
			deviceGroups[candidate.DeviceHash] = device
		}
		device[index/64] |= uint64(1) << uint(index%64)
		ip := ipGroups[candidate.IPHash]
		if ip == nil {
			ip = make([]uint64, wordCount)
			ipGroups[candidate.IPHash] = ip
		}
		ip[index/64] |= uint64(1) << uint(index%64)
	}
	firstDevice, firstIP := deviceGroups[candidates[0].DeviceHash], ipGroups[candidates[0].IPHash]
	for candidateSecond := 1; candidateSecond+1 < len(candidates); candidateSecond++ {
		if candidates[candidateSecond].DeviceHash == candidates[0].DeviceHash || candidates[candidateSecond].IPHash == candidates[0].IPHash {
			continue
		}
		third := firstCompatibleIndex(candidateSecond+1, len(candidates), firstDevice,
			deviceGroups[candidates[candidateSecond].DeviceHash], firstIP, ipGroups[candidates[candidateSecond].IPHash])
		if third >= 0 {
			return [3]queueRecord{candidates[0], candidates[candidateSecond], candidates[third]}, true
		}
	}
	return fallback, true
}

func firstCompatibleIndex(start, count int, excluded ...[]uint64) int {
	if start < 0 || start >= count || len(excluded) == 0 {
		return -1
	}
	wordCount := (count + 63) / 64
	for word := start / 64; word < wordCount; word++ {
		blocked := uint64(0)
		for _, group := range excluded {
			if len(group) != wordCount {
				return -1
			}
			blocked |= group[word]
		}
		available := ^blocked
		if word == start/64 {
			available &= ^uint64(0) << uint(start%64)
		}
		if word == wordCount-1 && count%64 != 0 {
			available &= (uint64(1) << uint(count%64)) - 1
		}
		if available != 0 {
			return word*64 + bits.TrailingZeros64(available)
		}
	}
	return -1
}

type frozenIdentity struct {
	DisplayName string
	AvatarURL   *string
	Stats       [5]db.U128
}

func loadFrozenIdentity(ctx context.Context, tx *sql.Tx, userID, now int64) (frozenIdentity, error) {
	var username, avatar, discordID, guildNick, guildAvatar string
	var banned, admin int
	var bannedUntil sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT username,avatar,COALESCE(discord_id,''),guild_nick,guild_avatar_url,is_banned,is_admin,banned_until
FROM users WHERE id=?`, userID).Scan(&username, &avatar, &discordID, &guildNick, &guildAvatar, &banned, &admin, &bannedUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return frozenIdentity{}, ErrForbidden
	}
	if err != nil {
		return frozenIdentity{}, classifyDB(err)
	}
	if admin != 0 || banned != 0 && (!bannedUntil.Valid || bannedUntil.Int64 > now) || discordID == "" {
		return frozenIdentity{}, ErrForbidden
	}
	display := safeDisplayName(guildNick)
	if display == "" {
		display = safeDisplayName(username)
	}
	if display == "" {
		return frozenIdentity{}, ErrInvariant
	}
	avatarURL := safeAvatar(guildAvatar)
	if avatarURL == nil && discordID != "" && avatar != "" && safePathAtom(discordID) && safePathAtom(avatar) {
		candidate := "https://cdn.discordapp.com/avatars/" + discordID + "/" + avatar + ".png"
		avatarURL = safeAvatar(candidate)
	}
	result := frozenIdentity{DisplayName: display, AvatarURL: avatarURL}
	var raw [5][]byte
	err = tx.QueryRowContext(ctx, `SELECT completed_count,profitable_count,rock_count,scissors_count,paper_count
FROM game_rps_fun_stats WHERE user_id=?`, userID).Scan(&raw[0], &raw[1], &raw[2], &raw[3], &raw[4])
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return frozenIdentity{}, classifyDB(err)
	}
	for index := range raw {
		value, err := db.DecodeU128(raw[index])
		if err != nil {
			return frozenIdentity{}, ErrInvariant
		}
		result.Stats[index] = value
	}
	return result, nil
}

func safePathAtom(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index := range value {
		character := value[index]
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func (service *Service) MatchOnce(ctx context.Context, mode string) (bool, error) {
	if service == nil || service.closed.Load() || !service.recovered.Load() || rpsconfig.Descriptor().ResolveMode(mode) != nil {
		return false, ErrInvalidRequest
	}
	now, err := service.decisionNow()
	if err != nil {
		return false, err
	}
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return false, classifyDB(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	maintenance, err := maintenanceEnabled(ctx, tx)
	if err != nil {
		return false, err
	}
	snapshot, err := service.readSnapshot(ctx, tx)
	if err != nil {
		return false, err
	}
	config := snapshot.RPS.Modes[mode]
	if maintenance || !snapshot.GamesEnabled || !snapshot.RPS.Enabled || !config.Enabled {
		return false, nil
	}
	candidates, err := service.loadMatchCandidates(ctx, tx, mode, now)
	if err != nil {
		return false, err
	}
	selected, found := selectCompatibleMatch(candidates)
	if !found {
		return false, nil
	}
	invalid := make([]queueRecord, 0, len(selected))
	for _, queue := range selected {
		if !queueCompatibleWithConfig(queue, config) {
			invalid = append(invalid, queue)
		}
	}
	if len(invalid) != 0 {
		users := make([]int64, 0, len(invalid))
		for _, queue := range invalid {
			if err := service.releaseQueueTx(ctx, tx, queue, now, 0); err != nil {
				return false, err
			}
			users = append(users, queue.UserID)
		}
		if err := tx.Commit(); err != nil {
			return false, classifyDB(err)
		}
		committed = true
		service.publishUsers(ctx, users, accountstream.TypeDelta)
		return false, nil
	}
	users, facts, err := service.startMatchTx(ctx, tx, selected, config, now)
	if err != nil {
		return false, fmt.Errorf("rps: start match: %w", err)
	}
	if service.beforeMatchCommit != nil {
		if err := service.beforeMatchCommit(); err != nil {
			return false, ErrServiceUnavailable
		}
	}
	if err := tx.Commit(); err != nil {
		return false, classifyDB(err)
	}
	committed = true
	service.publishUsers(ctx, users, accountstream.TypeDelta)
	if err := service.activityEvents.Publish(ctx, facts); err != nil {
		service.reportPublish(err)
	}
	return true, nil
}

func queueCompatibleWithConfig(queue queueRecord, config rpsconfig.RPSModeConfig) bool {
	base := big.NewInt(config.BaseMilli)
	if base.Sign() <= 0 || queue.Reserved.Big().Sign() <= 0 {
		return false
	}
	switch queue.Mode {
	case game.RPSModeQuick:
		return queue.Reserved.Big().Cmp(base) == 0
	case game.RPSModeStandard:
		return queue.Reserved.Big().Cmp(new(big.Int).Mul(base, big.NewInt(rpsconfig.RPSStandardMultiplier))) == 0
	case game.RPSModeDeathmatch:
		return queue.Reserved.Big().Cmp(base) >= 0
	default:
		return false
	}
}

func (service *Service) startMatchTx(ctx context.Context, tx *sql.Tx, selected [3]queueRecord, config rpsconfig.RPSModeConfig, now int64) ([]int64, activities.PublishFacts, error) {
	mode := selected[0].Mode
	for _, queue := range selected {
		if queue.Mode != mode || queue.RulesVersion != selected[0].RulesVersion || queue.Deadline <= now {
			return nil, activities.PublishFacts{}, ErrConflict
		}
	}
	service.randomMu.Lock()
	order, err := randomSeatOrder(service.random)
	dealer, dealerErr := randomIndex(service.random, 3)
	service.randomMu.Unlock()
	if err != nil || dealerErr != nil {
		return nil, activities.PublishFacts{}, ErrServiceUnavailable
	}
	if mode == game.RPSModeQuick {
		dealer = -1
	}
	identities := [3]frozenIdentity{}
	queuesBySeat := [3]queueRecord{}
	users := make([]int64, 0, 3)
	for seat, candidateIndex := range order {
		queue := selected[candidateIndex]
		identity, err := loadFrozenIdentity(ctx, tx, queue.UserID, now)
		if err != nil {
			return nil, activities.PublishFacts{}, err
		}
		queuesBySeat[seat], identities[seat] = queue, identity
		users = append(users, queue.UserID)
	}
	sessionID, err := service.generate("rps_")
	if err != nil {
		return nil, activities.PublishFacts{}, err
	}
	operationID, err := service.generate("op_")
	if err != nil {
		return nil, activities.PublishFacts{}, err
	}
	futureRows, err := sessionFutureRows(now)
	if err != nil {
		return nil, activities.PublishFacts{}, err
	}
	one, _ := u128(bigOne)
	deadline := now + int64(config.GestureSeconds)
	if deadline < now || deadline > 253402300799 {
		return nil, activities.PublishFacts{}, ErrServiceUnavailable
	}
	planInputs := [3]finance.QueueInput{}
	record := sessionRecord{
		ID: sessionID, Mode: mode, RulesVersion: selected[0].RulesVersion,
		State: StateStarted, Phase: PhaseGesture, Revision: one, PhaseSeq: one, IdentityEpoch: one,
		LedgerRowsRemaining: futureRows, BaseMilli: config.BaseMilli,
		Pumps: PumpsBP(config.PumpsBP), GestureSeconds: config.GestureSeconds, DealerSeconds: config.DealerSeconds,
		FollowerSeconds: config.FollowerSeconds, PermanentMultiplier: one, CurrentPlanMultiplier: &one,
		ReminderState: "none", PhaseDeadline: &deadline, HealthEpoch: service.healthEpoch, StartedAt: now,
		Presentation: sessionPresentation{PoolTieCount: new(db.U128)},
	}
	if dealer >= 0 {
		record.DealerSeat = &dealer
	}
	for seat, queue := range queuesBySeat {
		planAmount, amountErr := ledger.AmountFromBig(queue.Reserved.Big())
		if amountErr != nil {
			return nil, activities.PublishFacts{}, ErrInvariant
		}
		gamePaid, err := ledger.AmountFromBig(queue.GamePaid.Big())
		if err != nil {
			return nil, activities.PublishFacts{}, ErrInvariant
		}
		planInputs[seat] = finance.QueueInput{QueueID: queue.ID, UserID: queue.UserID, Amount: planAmount, GamePaid: gamePaid}
		identity := identities[seat]
		userID := queue.UserID
		record.Seats[seat] = seatRecord{
			SeatNo: seat, UserID: &userID, DeletionState: "active", DisplayName: &identity.DisplayName, AvatarURL: identity.AvatarURL,
			StartingBalance: queue.Reserved, CurrentBalance: queue.Reserved, GameBuyIn: queue.GamePaid, GameRemaining: queue.GamePaid,
			SnapshotCompletedCount: &identity.Stats[0], SnapshotProfitableCount: &identity.Stats[1], SnapshotRockCount: &identity.Stats[2],
			SnapshotScissorsCount: &identity.Stats[3], SnapshotPaperCount: &identity.Stats[4],
		}
	}
	inputs := [3]*big.Int{}
	planMultiplier := bigOne
	if mode == game.RPSModeStandard || mode == game.RPSModeDeathmatch {
		planMultiplier = record.CurrentPlanMultiplier.Big()
	}
	planned := new(big.Int).Mul(big.NewInt(record.BaseMilli), planMultiplier)
	allIn := mode == game.RPSModeDeathmatch
	for seat := range record.Seats {
		value := planned
		if mode == game.RPSModeQuick || mode == game.RPSModeStandard {
			value = big.NewInt(record.BaseMilli)
		}
		if value.Cmp(record.Seats[seat].CurrentBalance.Big()) > 0 {
			value = record.Seats[seat].CurrentBalance.Big()
		}
		inputs[seat] = cloneBig(value)
		allIn = allIn && value.Cmp(record.Seats[seat].CurrentBalance.Big()) == 0
	}
	if allIn {
		record.Phase = PhaseUltimateGesture
		record.DealerSeat = nil
	}
	if err := appendEvent(&record, EventPhaseChanged, phaseChangedPayload{Phase: record.Phase, Deadline: record.PhaseDeadline}); err != nil {
		return nil, activities.PublishFacts{}, err
	}
	if err := service.finance.SessionStart(ctx, tx, finance.SessionStart{Meta: ledger.Meta{OperationID: operationID, CreatedAt: now}, SessionID: sessionID, FutureRows: futureRows, Queues: planInputs}, func(ctx context.Context, tx *sql.Tx, accountID int64) error {
		record.AccountID = accountID
		return service.insertStartedSessionTx(ctx, tx, &record, queuesBySeat)
	}); err != nil {
		return nil, activities.PublishFacts{}, fmt.Errorf("session start ledger: %w", mapLedger(err))
	}
	facts, err := service.applyInputsTx(ctx, tx, &record, record.Revision, inputs, now, 0)
	if err != nil {
		return nil, activities.PublishFacts{}, fmt.Errorf("initial inputs: %w", err)
	}
	for _, userID := range users {
		if err := recordGameActivityTx(ctx, tx, userID, now); err != nil {
			return nil, activities.PublishFacts{}, err
		}
	}
	facts = mergePublishFacts(facts, activities.PublishFacts{AccountIDs: users})
	return users, facts, nil
}

func (service *Service) insertStartedSessionTx(ctx context.Context, tx *sql.Tx, record *sessionRecord, queues [3]queueRecord) error {
	events, err := json.Marshal(record.RecentEvents)
	if err != nil {
		return ErrInvariant
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO game_rps_sessions(
id,account_id,mode,rules_version,state,phase,revision,phase_seq,identity_epoch,cut_seq,ledger_rows_remaining,
dealer_seat,base_milli,platform_bp,welfare_bp,thursday_bp,gesture_seconds,dealer_seconds,follower_seconds,player_pool,
permanent_multiplier,pool_base_multiplier,current_plan_multiplier,dealer_raise,base_round_count,paid_tie_count,free_tie_count,
paid_pool_streak,free_pool_streak,platform_cut_total,welfare_cut_total,thursday_cut_total,welfare_carry_total,reminder_state,
phase_deadline,health_epoch,recent_events_blob,recent_first_seq,recent_last_seq,recent_event_count,terminal_operation_id,
terminal_retry_attempt_count,terminal_next_retry_at,terminal_last_error_class,started_at,terminal_reason)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		record.ID, record.AccountID, record.Mode, record.RulesVersion, record.State, record.Phase, db.EncodeU128(record.Revision),
		db.EncodeU128(record.PhaseSeq), db.EncodeU128(record.IdentityEpoch), db.EncodeU128(record.CutSeq), db.EncodeU128(record.LedgerRowsRemaining),
		nullableInt(record.DealerSeat), record.BaseMilli, record.Pumps.Platform, record.Pumps.Welfare, record.Pumps.Thursday,
		record.GestureSeconds, record.DealerSeconds, record.FollowerSeconds, db.EncodeU128(record.PlayerPool),
		db.EncodeU128(record.PermanentMultiplier), nullableU128(record.PoolBaseMultiplier), nullableU128(record.CurrentPlanMultiplier),
		nullableU128(record.DealerRaise), db.EncodeU128(record.BaseRoundCount), db.EncodeU128(record.PaidTieCount),
		db.EncodeU128(record.FreeTieCount), db.EncodeU128(record.PaidPoolStreak), db.EncodeU128(record.FreePoolStreak),
		db.EncodeU128(record.PlatformCutTotal), db.EncodeU128(record.WelfareCutTotal), db.EncodeU128(record.ThursdayCutTotal),
		db.EncodeU128(record.WelfareCarryTotal), record.ReminderState, nullableInt64(record.PhaseDeadline), record.HealthEpoch,
		events, db.EncodeU128(record.RecentFirstSeq), db.EncodeU128(record.RecentLastSeq), len(record.RecentEvents), nil,
		db.EncodeU128(record.TerminalRetryAttemptCount), nil, nil, record.StartedAt, nil); err != nil {
		return classifyDB(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO game_rps_presentation(session_id, pool_tie_count, quick_seat0_gesture, quick_seat1_gesture, quick_seat2_gesture) VALUES(?, ?, NULL, NULL, NULL)`, record.ID, db.EncodeU128(db.U128{})); err != nil {
		return classifyDB(err)
	}
	for seat, value := range record.Seats {
		if value.UserID == nil || value.DisplayName == nil {
			return ErrInvariant
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO game_rps_seats(
session_id,seat_no,user_id,deletion_state,display_name_snapshot,avatar_url_snapshot,starting_balance,current_balance,
current_round_input,current_all_in,current_gesture_envelope,current_gesture_phase_seq,follower_action,last_action_phase_seq,
total_input,total_returned,terminal_return,wallet_net_sign,wallet_net_mag,rock_count,scissors_count,paper_count,timeout_count,
snapshot_completed_count,snapshot_profitable_count,snapshot_rock_count,snapshot_scissors_count,snapshot_paper_count,stats_applied,game_buy_in,game_remaining)
VALUES(?,?,?,?,?,?,?,?,?,0,NULL,NULL,NULL,NULL,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0,?,?)`,
			record.ID, seat, *value.UserID, value.DeletionState, *value.DisplayName, nullableString(value.AvatarURL),
			db.EncodeU128(value.StartingBalance), db.EncodeU128(value.CurrentBalance), db.EncodeU128(value.CurrentRoundInput),
			db.EncodeU256(value.TotalInput), db.EncodeU256(value.TotalReturned), nil, nil, nil,
			db.EncodeU128(value.RockCount), db.EncodeU128(value.ScissorsCount), db.EncodeU128(value.PaperCount), db.EncodeU128(value.TimeoutCount),
			nullableU128(value.SnapshotCompletedCount), nullableU128(value.SnapshotProfitableCount), nullableU128(value.SnapshotRockCount),
			nullableU128(value.SnapshotScissorsCount), nullableU128(value.SnapshotPaperCount), db.EncodeU128(value.GameBuyIn), db.EncodeU128(value.GameRemaining)); err != nil {
			return classifyDB(err)
		}
		if record.RulesVersion == 2 {
			if err := service.finance.TransferOnboarding(ctx, tx, finance.QueueOnboardingTransfer{QueueID: queues[seat].ID, SessionID: record.ID, UserID: *value.UserID, SeatNo: seat}); err != nil {
				return mapLedger(err)
			}
		}
		result, err := tx.ExecContext(ctx, `UPDATE game_rps_user_slots SET queue_id=NULL,session_id=? WHERE user_id=? AND queue_id=? AND session_id IS NULL`,
			record.ID, *value.UserID, queues[seat].ID)
		if err != nil {
			return classifyDB(err)
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			return ErrConflict
		}
	}
	for _, queue := range queues {
		result, err := tx.ExecContext(ctx, `DELETE FROM game_rps_queue WHERE id=? AND revision=?`, queue.ID, db.EncodeU128(queue.Revision))
		if err != nil {
			return classifyDB(err)
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			return ErrConflict
		}
	}
	return nil
}

func (service *Service) applyInputsTx(ctx context.Context, tx *sql.Tx, record *sessionRecord, expectedRevision db.U128, inputs [3]*big.Int, now, actorUserID int64) (activities.PublishFacts, error) {
	if record == nil || record.State != StateStarted {
		return activities.PublishFacts{}, ErrInvariant
	}
	totalCuts := cutBreakdown{}
	gameInputTotal := new(big.Int)
	for seat, input := range inputs {
		if input == nil || input.Sign() < 0 || input.Cmp(record.Seats[seat].CurrentBalance.Big()) > 0 {
			return activities.PublishFacts{}, ErrInvariant
		}
		if input.Sign() == 0 {
			continue
		}
		cut, err := cutsForInput(input, record.Pumps)
		if err != nil {
			return activities.PublishFacts{}, err
		}
		addCut(&totalCuts, cut)
		balance, err := u128(new(big.Int).Sub(record.Seats[seat].CurrentBalance.Big(), input))
		if err != nil {
			return activities.PublishFacts{}, err
		}
		round, err := u128(new(big.Int).Add(record.Seats[seat].CurrentRoundInput.Big(), input))
		if err != nil {
			return activities.PublishFacts{}, err
		}
		gameInput := record.Seats[seat].GameRemaining.Big()
		if gameInput.Cmp(input) > 0 {
			gameInput.Set(input)
		}
		gameRemaining, err := u128(new(big.Int).Sub(record.Seats[seat].GameRemaining.Big(), gameInput))
		if err != nil {
			return activities.PublishFacts{}, err
		}
		record.Seats[seat].GameRemaining = gameRemaining
		gameInputTotal.Add(gameInputTotal, gameInput)
		record.Seats[seat].CurrentBalance = balance
		record.Seats[seat].CurrentRoundInput = round
		record.Seats[seat].CurrentAllIn = balance.Big().Sign() == 0
		record.Seats[seat].TotalInput, err = addU256Value(record.Seats[seat].TotalInput, input)
		if err != nil {
			return activities.PublishFacts{}, err
		}
		pool, err := u128(new(big.Int).Add(record.PlayerPool.Big(), cut.Net))
		if err != nil {
			return activities.PublishFacts{}, err
		}
		record.PlayerPool = pool
	}
	if totalCuts.Platform == nil {
		if err := persistSessionTx(ctx, tx, record, expectedRevision); err != nil {
			return activities.PublishFacts{}, err
		}
		return activities.PublishFacts{}, nil
	}
	platformTotal, err := u128(new(big.Int).Add(record.PlatformCutTotal.Big(), totalCuts.Platform))
	if err != nil {
		return activities.PublishFacts{}, err
	}
	welfareTotal, err := u128(new(big.Int).Add(record.WelfareCutTotal.Big(), totalCuts.Welfare))
	if err != nil {
		return activities.PublishFacts{}, err
	}
	thursdayTotal, err := u128(new(big.Int).Add(record.ThursdayCutTotal.Big(), totalCuts.Thursday))
	if err != nil {
		return activities.PublishFacts{}, err
	}
	record.PlatformCutTotal, record.WelfareCutTotal, record.ThursdayCutTotal = platformTotal, welfareTotal, thursdayTotal
	cutTotal := new(big.Int).Add(totalCuts.Platform, totalCuts.Welfare)
	cutTotal.Add(cutTotal, totalCuts.Thursday)
	if cutTotal.Sign() == 0 && gameInputTotal.Sign() == 0 {
		if err := persistSessionTx(ctx, tx, record, expectedRevision); err != nil {
			return activities.PublishFacts{}, err
		}
		return activities.PublishFacts{}, nil
	}
	cutSeq, err := incU128(record.CutSeq)
	if err != nil {
		return activities.PublishFacts{}, err
	}
	one, _ := u128(bigOne)
	remaining, err := subU128(record.LedgerRowsRemaining, one)
	if err != nil {
		return activities.PublishFacts{}, err
	}
	record.CutSeq, record.LedgerRowsRemaining = cutSeq, remaining
	operationID, err := service.generate("op_")
	if err != nil {
		return activities.PublishFacts{}, err
	}
	var welfare, thursday activities.PoolDestination
	if record.RulesVersion == 1 || totalCuts.Welfare.Sign() > 0 {
		welfare, err = service.pools.WelfareDestination(ctx, tx)
		if err != nil {
			return activities.PublishFacts{}, classifyDB(err)
		}
	}
	if record.RulesVersion == 1 || totalCuts.Thursday.Sign() > 0 {
		thursday, err = service.pools.ThursdayDestination(ctx, tx, now)
		if err != nil {
			return activities.PublishFacts{}, classifyDB(err)
		}
	}
	platformAmount, err := ledger.AmountFromBig(totalCuts.Platform)
	if err != nil {
		return activities.PublishFacts{}, ErrInvariant
	}
	welfareAmount, err := ledger.AmountFromBig(totalCuts.Welfare)
	if err != nil {
		return activities.PublishFacts{}, ErrInvariant
	}
	thursdayAmount, err := ledger.AmountFromBig(totalCuts.Thursday)
	if err != nil {
		return activities.PublishFacts{}, ErrInvariant
	}
	gameInputAmount, err := ledger.AmountFromBig(gameInputTotal)
	if err != nil {
		return activities.PublishFacts{}, ErrInvariant
	}
	if err := service.finance.RoundCut(ctx, tx, finance.RoundCut{GameInput: gameInputAmount, Meta: ledger.Meta{OperationID: operationID, ActorUserID: actorUserID, CreatedAt: now}, SessionID: record.ID, Sequence: cutSeq, WelfareAccountID: welfare.AccountID, ThursdayAccountID: thursday.AccountID, Amounts: ledger.RPSCutAmounts{Platform: platformAmount, Welfare: welfareAmount, Thursday: thursdayAmount}}, func(ctx context.Context, tx *sql.Tx) error {
		if err := persistSessionRowsTx(ctx, tx, record, expectedRevision); err != nil {
			return fmt.Errorf("persist after round cut: %w", err)
		}
		return nil
	}); err != nil {
		return activities.PublishFacts{}, fmt.Errorf("round cut ledger: %w", mapLedger(err))
	}
	if err := validateSession(ctx, tx, record); err != nil {
		return activities.PublishFacts{}, fmt.Errorf("validate after round cut: %w", err)
	}
	destinations := make([]activities.PoolDestination, 0, 2)
	if totalCuts.Welfare.Sign() > 0 {
		destinations = append(destinations, welfare)
	}
	if totalCuts.Thursday.Sign() > 0 {
		destinations = append(destinations, thursday)
	}
	if len(destinations) == 0 {
		return activities.PublishFacts{}, nil
	}
	facts, err := service.pools.RecordPoolTransfers(ctx, tx, now, destinations...)
	if err != nil {
		return activities.PublishFacts{}, classifyDB(err)
	}
	return facts, nil
}

func recordGameActivityTx(ctx context.Context, tx *sql.Tx, userID, now int64) error {
	var raw sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key=?`, db.SiteTimezoneKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (!raw.Valid || raw.String == "") {
		return nil
	}
	if err != nil {
		return classifyDB(err)
	}
	offset, err := strconv.ParseInt(raw.String, 10, 32)
	if err != nil || strconv.FormatInt(offset, 10) != raw.String || !db.ValidSiteTimezoneOffset(int(offset)) {
		return ErrInvariant
	}
	day := db.SiteDayKey(now, offset)
	result, err := tx.ExecContext(ctx, `UPDATE user_activity_daily SET game_active=1,game_rounds=game_rounds+1,updated_at=?
WHERE day=? AND user_id=? AND game_rounds<9223372036854775807`, now, day, userID)
	if err != nil {
		return classifyDB(err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return classifyDB(err)
	}
	if changed == 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_activity_daily(day,user_id,game_active,game_rounds,updated_at) VALUES(?,?,1,1,?)`, day, userID, now); err != nil {
			return classifyDB(err)
		}
	}
	var roundsRaw []byte
	err = tx.QueryRowContext(ctx, `SELECT game_rounds FROM site_activity_daily WHERE day=?`, day).Scan(&roundsRaw)
	if errors.Is(err, sql.ErrNoRows) {
		one, _ := u128(bigOne)
		zero := db.EncodeU128(db.U128{})
		_, err = tx.ExecContext(ctx, `INSERT INTO site_activity_daily(
day,product_active,api_requests,uncached_input_tokens,cache_write_input_tokens,cache_read_input_tokens,
output_tokens,checkins,console_writes,game_active,game_rounds,distinct_product_users,updated_at)
VALUES(?,0,?,?,?,?,?,?,?,1,?,?,?)`, day, zero, zero, zero, zero, zero, zero, zero, db.EncodeU128(one), zero, now)
		return classifyDB(err)
	}
	if err != nil {
		return classifyDB(err)
	}
	rounds, err := db.DecodeU128(roundsRaw)
	if err != nil {
		return ErrInvariant
	}
	next, err := addCounter(rounds, 1)
	if err != nil {
		return err
	}
	result, err = tx.ExecContext(ctx, `UPDATE site_activity_daily SET game_active=1,game_rounds=?,updated_at=?
WHERE day=? AND game_rounds=?`, db.EncodeU128(next), now, day, roundsRaw)
	if err != nil {
		return classifyDB(err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		if err != nil {
			return classifyDB(err)
		}
		return ErrConflict
	}
	return nil
}
