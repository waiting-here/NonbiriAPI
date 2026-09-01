package rps

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strconv"
	"sync/atomic"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

type LifecycleAdapter struct{ service *Service }

func (service *Service) Lifecycle() *LifecycleAdapter { return &LifecycleAdapter{service: service} }

func NewLifecycleAdapter(service *Service) *LifecycleAdapter {
	if service == nil {
		return &LifecycleAdapter{}
	}
	return service.Lifecycle()
}

type DeletionFinalizer struct {
	service   *Service
	userID    int64
	survivors []int64
	done      atomic.Bool
}

// PrepareUserDeletion joins the caller-owned account deletion transaction.
// The shared StartLimiter deletion marker is owned by the L1 coordinator; this
// adapter intentionally does not acquire another one. decisionNow is the
// coordinator's transaction-wide frozen Unix second; the adapter must not read
// its own clock and thereby split one deletion decision across instants.
func (adapter *LifecycleAdapter) PrepareUserDeletion(ctx context.Context, tx *sql.Tx, userID, decisionNow int64) (*DeletionFinalizer, error) {
	if adapter == nil || adapter.service == nil || tx == nil || userID <= 0 || decisionNow < 0 || decisionNow > 253402300799 {
		return nil, ErrInvalidRequest
	}
	service := adapter.service
	finalizer := &DeletionFinalizer{service: service, userID: userID}
	survivors := map[int64]struct{}{}
	if queue, found, err := loadQueueByUser(ctx, tx, userID); err != nil {
		return nil, err
	} else if found {
		if err := service.releaseQueueTx(ctx, tx, queue, decisionNow, 0); err != nil {
			return nil, err
		}
	}
	if record, found, err := loadSessionByUser(ctx, tx, userID); err != nil {
		return nil, err
	} else if found {
		seatNo, owner := seatForUser(&record, userID)
		if !owner {
			return nil, ErrInvariant
		}
		expected := record.Revision
		record.Revision, err = incU128(record.Revision)
		if err != nil {
			return nil, err
		}
		if err := clearEventsForIdentity(&record, seatNo); err != nil {
			return nil, err
		}
		seat := &record.Seats[seatNo]
		seat.UserID, seat.DisplayName, seat.AvatarURL = nil, nil, nil
		seat.SnapshotCompletedCount, seat.SnapshotProfitableCount = nil, nil
		seat.SnapshotRockCount, seat.SnapshotScissorsCount, seat.SnapshotPaperCount = nil, nil, nil
		seat.DeletionState = "deletion_pending"
		if err := persistSessionTx(ctx, tx, &record, expected); err != nil {
			return nil, err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM game_rps_user_slots WHERE user_id=? AND session_id=?`, userID, record.ID)
		if err != nil {
			return nil, classifyDB(err)
		}
		if changed, err := result.RowsAffected(); err != nil || changed != 1 {
			if err != nil {
				return nil, classifyDB(err)
			}
			return nil, ErrConflict
		}
		for _, survivor := range sessionUsers(&record) {
			if survivor != userID {
				survivors[survivor] = struct{}{}
			}
		}
	}
	if err := detachTerminalIdentityTx(ctx, tx, userID, survivors); err != nil {
		return nil, err
	}
	for _, statement := range []string{
		`DELETE FROM game_online_leases WHERE user_id=? AND substr(session_id,1,4)='rps_'`,
		`DELETE FROM game_rps_pending_results WHERE user_id=?`,
		`DELETE FROM game_rps_rank_facts WHERE user_id=?`,
		`DELETE FROM game_rps_rank_aggregates WHERE user_id=?`,
		`DELETE FROM game_rps_fun_stats WHERE user_id=?`,
	} {
		if _, err := tx.ExecContext(ctx, statement, userID); err != nil {
			return nil, classifyDB(err)
		}
	}
	actor, err := actorHash(userID)
	if err != nil {
		return nil, ErrInvariant
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM idempotency_records WHERE scope=? AND actor_scope_hash=?`, idempotency.ScopeGameRPS, actor[:]); err != nil {
		return nil, classifyDB(err)
	}
	for survivor := range survivors {
		finalizer.survivors = append(finalizer.survivors, survivor)
	}
	sort.Slice(finalizer.survivors, func(i, j int) bool { return finalizer.survivors[i] < finalizer.survivors[j] })
	return finalizer, nil
}

func detachTerminalIdentityTx(ctx context.Context, tx *sql.Tx, userID int64, survivors map[int64]struct{}) error {
	rows, err := tx.QueryContext(ctx, `SELECT own.session_id,own.seat_no,p.user_id
FROM game_rps_summary_seats own
LEFT JOIN game_rps_pending_results p ON p.session_id_text=own.session_id AND p.user_id<>?
WHERE own.user_id=? ORDER BY own.session_id,p.user_id`, userID, userID)
	if err != nil {
		return classifyDB(err)
	}
	seats := map[string]int{}
	for rows.Next() {
		var sessionID string
		var seatNo int
		var survivor sql.NullInt64
		if err := rows.Scan(&sessionID, &seatNo, &survivor); err != nil {
			_ = rows.Close()
			return classifyDB(err)
		}
		if !db.ValidateOpaqueID(sessionID, "rps_") || seatNo < 0 || seatNo > 2 {
			_ = rows.Close()
			return ErrInvariant
		}
		if previous, exists := seats[sessionID]; exists && previous != seatNo {
			_ = rows.Close()
			return ErrInvariant
		}
		seats[sessionID] = seatNo
		if survivor.Valid {
			if survivor.Int64 <= 0 {
				_ = rows.Close()
				return ErrInvariant
			}
			survivors[survivor.Int64] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return classifyDB(err)
	}
	if err := rows.Close(); err != nil {
		return classifyDB(err)
	}
	for sessionID, seatNo := range seats {
		column := "seat0_result"
		if seatNo == 1 {
			column = "seat1_result"
		} else if seatNo == 2 {
			column = "seat2_result"
		}
		if _, err := tx.ExecContext(ctx, `UPDATE game_rps_pending_results SET `+column+`='deidentified'
WHERE session_id_text=? AND user_id<>? AND `+column+`<>'deidentified'`, sessionID, userID); err != nil {
			return classifyDB(err)
		}
		result, err := tx.ExecContext(ctx, `UPDATE game_rps_summary_seats SET user_id=NULL
WHERE session_id=? AND seat_no=? AND user_id=?`, sessionID, seatNo, userID)
		if err != nil {
			return classifyDB(err)
		}
		if changed, err := result.RowsAffected(); err != nil || changed != 1 {
			if err != nil {
				return classifyDB(err)
			}
			return ErrConflict
		}
	}
	return nil
}

func (finalizer *DeletionFinalizer) Commit() bool {
	if finalizer == nil || finalizer.service == nil || !finalizer.done.CompareAndSwap(false, true) {
		return false
	}
	service := finalizer.service
	service.forgetUserMemory(finalizer.userID)
	ctx := context.Background()
	// Only the removed account receives the permanent tombstone. Live peers are
	// synchronously discarded without a tombstone before any snapshot rebuild,
	// so a failed purge cannot make an old identity frame deliverable and peers
	// remain able to reconnect for a later authoritative rebuild.
	if err := service.accountEvents.ForgetAccounts(ctx, []int64{finalizer.userID}); err != nil {
		service.reportPublish(err)
		return true
	}
	if len(finalizer.survivors) != 0 {
		if err := service.accountEvents.DiscardAccounts(ctx, finalizer.survivors); err != nil {
			service.reportPublish(err)
			return true
		}
		if err := service.accountEvents.PurgeAccounts(ctx, finalizer.survivors); err != nil {
			service.reportPublish(err)
		}
	}
	return true
}

func (finalizer *DeletionFinalizer) Abort() bool {
	return finalizer != nil && finalizer.done.CompareAndSwap(false, true)
}

// HomeSummaryTx returns the RPS-owned slices consumed by the root home
// summary aggregator. The caller owns authorization and the transaction so
// all game providers can be read from one snapshot.
func (service *Service) HomeSummaryTx(ctx context.Context, tx *sql.Tx, userID int64) (HomeSummary, error) {
	if service == nil || service.closed.Load() || tx == nil || userID <= 0 {
		return HomeSummary{}, ErrInvalidRequest
	}
	result := HomeSummary{Continue: []HomeContinueItem{}, PendingResults: []HomePendingItem{}}
	var sessionID, state string
	err := tx.QueryRowContext(ctx, `SELECT session.id,session.state
FROM game_rps_user_slots slot JOIN game_rps_sessions session ON session.id=slot.session_id
WHERE slot.user_id=? AND slot.session_id IS NOT NULL`, userID).Scan(&sessionID, &state)
	if err == nil {
		if !db.ValidateOpaqueID(sessionID, "rps_") || state != StateStarted && state != StateTerminalProcessing {
			return HomeSummary{}, ErrInvariant
		}
		result.Continue = append(result.Continue, HomeContinueItem{
			Game: game.RPSID, ResourceID: sessionID, State: state, RouteID: "game-rps",
		})
	} else if !errors.Is(err, sql.ErrNoRows) {
		return HomeSummary{}, classifyDB(err)
	}
	var pendingID string
	var createdAt int64
	err = tx.QueryRowContext(ctx, `SELECT session_id_text,created_at FROM game_rps_pending_results WHERE user_id=?`, userID).
		Scan(&pendingID, &createdAt)
	if err == nil {
		if !db.ValidateOpaqueID(pendingID, "rps_") || createdAt < 0 || createdAt > 253402300799 {
			return HomeSummary{}, ErrInvariant
		}
		result.PendingResults = append(result.PendingResults, HomePendingItem{
			Game: game.RPSID, ResourceID: pendingID, CreatedAt: createdAt, RouteID: "game-rps",
		})
	} else if !errors.Is(err, sql.ErrNoRows) {
		return HomeSummary{}, classifyDB(err)
	}
	if len(result.Continue) != 0 && len(result.PendingResults) != 0 {
		return HomeSummary{}, ErrInvariant
	}
	return result, nil
}

func (adapter *LifecycleAdapter) ExportUser(ctx context.Context, tx *sql.Tx, userID, now int64) (UserExport, error) {
	if adapter == nil || adapter.service == nil || tx == nil || userID <= 0 || now < 0 || now > 253402300799 {
		return UserExport{}, ErrInvalidRequest
	}
	service := adapter.service
	result := UserExport{Summaries: []SummaryExport{}}
	if pending, found, err := loadPending(ctx, tx, userID); err != nil {
		return UserExport{}, err
	} else if found {
		result.Pending = &pending
	}
	if record, found, err := loadSessionByUser(ctx, tx, userID); err != nil {
		return UserExport{}, err
	} else if found {
		if record.State == StateStarted && record.PhaseDeadline != nil && now >= *record.PhaseDeadline {
			reduced := reducer{service: service, ctx: ctx, tx: tx, record: &record, expectedRevision: record.Revision, now: now}
			if err := reduced.applyDeadlineDefaults(); err != nil {
				return UserExport{}, err
			}
		}
		home, err := service.projectHomeTx(ctx, tx, userID, now)
		if err != nil {
			return UserExport{}, err
		}
		if home.Kind == "session" {
			result.Current = &home
		} else if home.Kind == "pending_result" {
			result.Pending = home.Result
		}
	} else if queue, found, err := loadQueueByUser(ctx, tx, userID); err != nil {
		return UserExport{}, err
	} else if found {
		view := queueView(queue, now)
		home := HomeState{Kind: "queue", Queue: &view}
		result.Current = &home
	}
	rows, err := tx.QueryContext(ctx, `SELECT s.session_id,s.mode,s.terminal_reason,s.started_at,s.terminal_at,
seat.seat_no,seat.input,seat.returned,seat.wallet_net_sign,seat.wallet_net_mag,seat.timeout_count,
seat.rock_count,seat.scissors_count,seat.paper_count
FROM game_rps_summaries s JOIN game_rps_summary_seats seat ON seat.session_id=s.session_id
WHERE seat.user_id=? AND s.delete_at>? ORDER BY s.terminal_at,s.session_id LIMIT 10001`, userID, now)
	if err != nil {
		return UserExport{}, classifyDB(err)
	}
	for rows.Next() {
		var summary SummaryExport
		var seat SummarySeatExport
		var inputRaw, returnedRaw, walletRaw, timeoutRaw, rockRaw, scissorsRaw, paperRaw []byte
		var sign int
		if err := rows.Scan(&summary.SessionID, &summary.Mode, &summary.TerminalReason, &summary.StartedAt, &summary.TerminalAt,
			&seat.SeatNo, &inputRaw, &returnedRaw, &sign, &walletRaw, &timeoutRaw, &rockRaw, &scissorsRaw, &paperRaw); err != nil {
			_ = rows.Close()
			return UserExport{}, classifyDB(err)
		}
		input, err := db.DecodeU256(inputRaw)
		if err != nil {
			_ = rows.Close()
			return UserExport{}, ErrInvariant
		}
		returned, err := db.DecodeU256(returnedRaw)
		if err != nil {
			_ = rows.Close()
			return UserExport{}, ErrInvariant
		}
		wide := [5]db.U128{}
		for index, raw := range [][]byte{walletRaw, timeoutRaw, rockRaw, scissorsRaw, paperRaw} {
			wide[index], err = db.DecodeU128(raw)
			if err != nil {
				_ = rows.Close()
				return UserExport{}, ErrInvariant
			}
		}
		if sign < -1 || sign > 1 || !validTerminalReason(summary.TerminalReason) || game.ResolveMode(game.RPSID, summary.Mode) != nil ||
			seat.SeatNo < 0 || seat.SeatNo > 2 {
			_ = rows.Close()
			return UserExport{}, ErrInvariant
		}
		seat.Input, seat.Returned = formatMilli(input.Big()), formatMilli(returned.Big())
		seat.WalletNet = formatSignedMilli(sign, wide[0].Big())
		seat.TimeoutCount, seat.RockCount = wide[1].Decimal(), wide[2].Decimal()
		seat.ScissorsCount, seat.PaperCount = wide[3].Decimal(), wide[4].Decimal()
		summary.OwnSeat = seat
		result.Summaries = append(result.Summaries, summary)
		if len(result.Summaries) > 10000 {
			_ = rows.Close()
			return UserExport{}, ErrResourceLimit
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return UserExport{}, classifyDB(err)
	}
	if err := rows.Close(); err != nil {
		return UserExport{}, classifyDB(err)
	}
	var funRaw [5][]byte
	err = tx.QueryRowContext(ctx, `SELECT completed_count,profitable_count,rock_count,scissors_count,paper_count
FROM game_rps_fun_stats WHERE user_id=?`, userID).Scan(&funRaw[0], &funRaw[1], &funRaw[2], &funRaw[3], &funRaw[4])
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return UserExport{}, classifyDB(err)
	}
	if err == nil {
		values := [5]db.U128{}
		for index, raw := range funRaw {
			values[index], err = db.DecodeU128(raw)
			if err != nil {
				return UserExport{}, ErrInvariant
			}
		}
		result.FunStats = &FunStatsExport{CompletedCount: values[0].Decimal(), ProfitableCount: values[1].Decimal(),
			RockCount: values[2].Decimal(), ScissorsCount: values[3].Decimal(), PaperCount: values[4].Decimal()}
	}
	var tutorial int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(p.tutorial_rps_seen,0) FROM users u
LEFT JOIN game_user_preferences p ON p.user_id=u.id WHERE u.id=?`, userID).Scan(&tutorial); err != nil {
		return UserExport{}, classifyDB(err)
	}
	if tutorial != 0 && tutorial != 1 {
		return UserExport{}, ErrInvariant
	}
	result.TutorialSeen = tutorial == 1
	return result, nil
}

func (adapter *LifecycleAdapter) Cleanup(ctx context.Context, now int64) (int, error) {
	if adapter == nil || adapter.service == nil || now < 0 || now > 253402300799 {
		return 0, ErrInvalidRequest
	}
	service := adapter.service
	if service.closed.Load() || !service.recovered.Load() {
		return 0, ErrServiceUnavailable
	}
	for {
		count, err := service.runDeadlineSessions(ctx, now)
		if err != nil {
			return 0, err
		}
		if count < workerBatchSize {
			break
		}
	}
	for {
		count, err := service.runTerminalRetries(ctx, now)
		if err != nil {
			return 0, err
		}
		if count < workerBatchSize {
			break
		}
	}
	if _, err := service.expireRankFacts(ctx, now); err != nil {
		return 0, err
	}
	result, err := service.database.ExecContext(ctx, `DELETE FROM game_rps_summaries WHERE session_id IN (
SELECT session_id FROM game_rps_summaries WHERE delete_at<=? ORDER BY delete_at,session_id LIMIT ?)`, now, workerBatchSize)
	if err != nil {
		return 0, classifyDB(err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return 0, classifyDB(err)
	}
	if _, err := service.pruneLeases(ctx, now); err != nil {
		return int(changed), err
	}
	return int(changed), nil
}

func (service *Service) ActiveCounts(ctx context.Context) (ActiveCounts, error) {
	if service == nil || service.closed.Load() {
		return ActiveCounts{}, ErrClosed
	}
	if !service.recovered.Load() {
		return ActiveCounts{}, ErrServiceUnavailable
	}
	now, err := service.decisionNow()
	if err != nil {
		return ActiveCounts{}, err
	}
	for {
		count, err := service.sweepQueues(ctx, now)
		if err != nil {
			return ActiveCounts{}, err
		}
		if count < workerBatchSize {
			break
		}
	}
	for {
		count, err := service.runDeadlineSessions(ctx, now)
		if err != nil {
			return ActiveCounts{}, err
		}
		if count < workerBatchSize {
			break
		}
	}
	for {
		count, err := service.runTerminalRetries(ctx, now)
		if err != nil {
			return ActiveCounts{}, err
		}
		if count < workerBatchSize {
			break
		}
	}
	sessionCounts := map[string]int64{}
	rows, err := service.database.QueryContext(ctx, `SELECT mode,phase,COUNT(*) FROM game_rps_sessions GROUP BY mode,phase`)
	if err != nil {
		return ActiveCounts{}, classifyDB(err)
	}
	for rows.Next() {
		var mode, phase string
		var count int64
		if err := rows.Scan(&mode, &phase, &count); err != nil {
			_ = rows.Close()
			return ActiveCounts{}, classifyDB(err)
		}
		if game.ResolveMode(game.RPSID, mode) != nil || !validPersistentPhase(phase) || count < 0 {
			_ = rows.Close()
			return ActiveCounts{}, ErrInvariant
		}
		sessionCounts[mode+"\x00"+phase] = count
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return ActiveCounts{}, classifyDB(err)
	}
	if err := rows.Close(); err != nil {
		return ActiveCounts{}, classifyDB(err)
	}
	queueCounts := map[string]int64{}
	rows, err = service.database.QueryContext(ctx, `SELECT mode,COUNT(*) FROM game_rps_queue GROUP BY mode`)
	if err != nil {
		return ActiveCounts{}, classifyDB(err)
	}
	for rows.Next() {
		var mode string
		var count int64
		if err := rows.Scan(&mode, &count); err != nil {
			_ = rows.Close()
			return ActiveCounts{}, classifyDB(err)
		}
		if game.ResolveMode(game.RPSID, mode) != nil || count < 0 {
			_ = rows.Close()
			return ActiveCounts{}, ErrInvariant
		}
		queueCounts[mode] = count
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return ActiveCounts{}, classifyDB(err)
	}
	if err := rows.Close(); err != nil {
		return ActiveCounts{}, classifyDB(err)
	}
	result := ActiveCounts{Sessions: []ActiveCount{}, Queues: []QueueCount{}}
	phases := []string{PhaseGesture, PhaseDealerRaise, PhaseFollowers, PhasePaidPoolGesture, PhaseFreePoolGesture, PhaseUltimateGesture, PhaseTerminal}
	for _, mode := range []string{game.RPSModeQuick, game.RPSModeStandard, game.RPSModeDeathmatch} {
		for _, phase := range phases {
			result.Sessions = append(result.Sessions, ActiveCount{Mode: mode, Phase: phase, Count: strconv.FormatInt(sessionCounts[mode+"\x00"+phase], 10)})
		}
		result.Queues = append(result.Queues, QueueCount{Mode: mode, Count: strconv.FormatInt(queueCounts[mode], 10)})
	}
	return result, nil
}

func validPersistentPhase(phase string) bool {
	switch phase {
	case PhaseGesture, PhaseDealerRaise, PhaseFollowers, PhasePaidPoolGesture, PhaseFreePoolGesture, PhaseUltimateGesture, PhaseTerminal:
		return true
	default:
		return false
	}
}
