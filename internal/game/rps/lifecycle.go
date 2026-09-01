package rps

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/accountstream"
	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

var ErrPostCommitRequired = errors.New("rps: lifecycle post-commit finalizer required")

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

type ExportFinalizer struct {
	service         *Service
	events          []preparedAccountEvent
	facts           activities.PublishFacts
	publishActivity bool
	done            atomic.Bool
}

type preparedAccountEvent struct {
	userID int64
	event  accountstream.PublishedEvent
}

type RetentionResult struct {
	Processed int
	More      bool
}

// PrepareUserDeletion joins the caller-owned account deletion transaction.
// The shared StartLimiter deletion marker is owned by the L1 coordinator; this
// adapter intentionally does not acquire another one. decisionNow is the
// coordinator's transaction-wide frozen Unix second; the adapter must not read
// its own clock and thereby split one deletion decision across instants.
func (adapter *LifecycleAdapter) PrepareUserDeletion(ctx context.Context, tx *sql.Tx, userID, decisionNow int64) (*DeletionFinalizer, error) {
	return adapter.PrepareDeleteTx(ctx, tx, userID, decisionNow)
}

// PrepareDeleteTx joins the caller-owned account deletion transaction and uses
// only the coordinator's transaction-wide frozen decision time.
func (adapter *LifecycleAdapter) PrepareDeleteTx(ctx context.Context, tx *sql.Tx, userID, decisionNow int64) (*DeletionFinalizer, error) {
	if adapter == nil || adapter.service == nil || ctx == nil || tx == nil || userID <= 0 || decisionNow < 0 || decisionNow > 253402300799 {
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
	rows, err := tx.QueryContext(ctx, `SELECT own.session_id,own.seat_no,peer.user_id
	FROM game_rps_summary_seats own
	JOIN game_rps_summary_seats peer ON peer.session_id=own.session_id
	WHERE own.user_id=? ORDER BY own.session_id,peer.seat_no`, userID)
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
			if survivor.Int64 != userID {
				survivors[survivor.Int64] = struct{}{}
			}
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
	result, finalizer, err := adapter.ExportTx(ctx, tx, userID, now, 10000)
	if err != nil {
		return UserExport{}, err
	}
	if finalizer != nil {
		_ = finalizer.Abort()
		return UserExport{}, ErrPostCommitRequired
	}
	return result, nil
}

// ExportTx reads the safe RPS slice from the caller-owned transaction. Queue
// expiry and phase-deadline convergence use only decisionNow. Any resulting
// account-stream/activity publication is returned as a one-shot finalizer and
// must run only after the outer transaction commits.
func (adapter *LifecycleAdapter) ExportTx(
	ctx context.Context,
	tx *sql.Tx,
	userID, decisionNow int64,
	limit int,
) (UserExport, *ExportFinalizer, error) {
	if adapter == nil || adapter.service == nil || ctx == nil || tx == nil || userID <= 0 ||
		decisionNow < 0 || decisionNow > 253402300799 || limit < 1 || limit > 10000 {
		return UserExport{}, nil, ErrInvalidRequest
	}
	service := adapter.service
	result := UserExport{Summaries: []SummaryExport{}}
	var finalizer *ExportFinalizer
	if pending, found, err := loadPending(ctx, tx, userID); err != nil {
		return UserExport{}, nil, err
	} else if found {
		result.Pending = &pending
	}
	if record, found, err := loadSessionByUser(ctx, tx, userID); err != nil {
		return UserExport{}, nil, err
	} else if found {
		if record.State == StateStarted && record.PhaseDeadline != nil && decisionNow >= *record.PhaseDeadline {
			users := sessionUsers(&record)
			reduced := reducer{service: service, ctx: ctx, tx: tx, record: &record, expectedRevision: record.Revision, now: decisionNow}
			if err := reduced.applyDeadlineDefaults(); err != nil {
				return UserExport{}, nil, err
			}
			if reduced.terminal != nil {
				users = reduced.terminal.Users
			}
			finalizer, err = newExportFinalizer(ctx, tx, service, decisionNow, users, reduced.facts, true)
			if err != nil {
				return UserExport{}, nil, err
			}
		}
		home, err := service.projectHomeTx(ctx, tx, userID, decisionNow)
		if err != nil {
			return UserExport{}, nil, err
		}
		if home.Kind == "session" {
			result.Current = &home
		} else if home.Kind == "pending_result" {
			result.Pending = home.Result
		}
	} else if queue, found, err := loadQueueByUser(ctx, tx, userID); err != nil {
		return UserExport{}, nil, err
	} else if found {
		if decisionNow >= queue.Deadline {
			if err := service.releaseQueueTx(ctx, tx, queue, decisionNow, 0); err != nil {
				return UserExport{}, nil, err
			}
			finalizer, err = newExportFinalizer(
				ctx, tx, service, decisionNow, []int64{userID}, activities.PublishFacts{}, false,
			)
			if err != nil {
				return UserExport{}, nil, err
			}
		} else {
			view := queueView(queue, decisionNow)
			home := HomeState{Kind: "queue", Queue: &view}
			result.Current = &home
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT s.session_id,s.mode,s.terminal_reason,s.started_at,s.terminal_at,
seat.seat_no,seat.input,seat.returned,seat.wallet_net_sign,seat.wallet_net_mag,seat.timeout_count,
seat.rock_count,seat.scissors_count,seat.paper_count
FROM game_rps_summaries s JOIN game_rps_summary_seats seat ON seat.session_id=s.session_id
	WHERE seat.user_id=? AND s.delete_at>? ORDER BY s.terminal_at,s.session_id LIMIT ?`, userID, decisionNow, limit+1)
	if err != nil {
		return UserExport{}, nil, classifyDB(err)
	}
	for rows.Next() {
		var summary SummaryExport
		var seat SummarySeatExport
		var inputRaw, returnedRaw, walletRaw, timeoutRaw, rockRaw, scissorsRaw, paperRaw []byte
		var sign int
		if err := rows.Scan(&summary.SessionID, &summary.Mode, &summary.TerminalReason, &summary.StartedAt, &summary.TerminalAt,
			&seat.SeatNo, &inputRaw, &returnedRaw, &sign, &walletRaw, &timeoutRaw, &rockRaw, &scissorsRaw, &paperRaw); err != nil {
			_ = rows.Close()
			return UserExport{}, nil, classifyDB(err)
		}
		input, err := db.DecodeU256(inputRaw)
		if err != nil {
			_ = rows.Close()
			return UserExport{}, nil, ErrInvariant
		}
		returned, err := db.DecodeU256(returnedRaw)
		if err != nil {
			_ = rows.Close()
			return UserExport{}, nil, ErrInvariant
		}
		wide := [5]db.U128{}
		for index, raw := range [][]byte{walletRaw, timeoutRaw, rockRaw, scissorsRaw, paperRaw} {
			wide[index], err = db.DecodeU128(raw)
			if err != nil {
				_ = rows.Close()
				return UserExport{}, nil, ErrInvariant
			}
		}
		if sign < -1 || sign > 1 || !validTerminalReason(summary.TerminalReason) || game.ResolveMode(game.RPSID, summary.Mode) != nil ||
			seat.SeatNo < 0 || seat.SeatNo > 2 {
			_ = rows.Close()
			return UserExport{}, nil, ErrInvariant
		}
		seat.Input, seat.Returned = formatMilli(input.Big()), formatMilli(returned.Big())
		seat.WalletNet = formatSignedMilli(sign, wide[0].Big())
		seat.TimeoutCount, seat.RockCount = wide[1].Decimal(), wide[2].Decimal()
		seat.ScissorsCount, seat.PaperCount = wide[3].Decimal(), wide[4].Decimal()
		summary.OwnSeat = seat
		result.Summaries = append(result.Summaries, summary)
		if len(result.Summaries) > limit {
			_ = rows.Close()
			return UserExport{}, nil, ErrResourceLimit
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return UserExport{}, nil, classifyDB(err)
	}
	if err := rows.Close(); err != nil {
		return UserExport{}, nil, classifyDB(err)
	}
	var funRaw [5][]byte
	err = tx.QueryRowContext(ctx, `SELECT completed_count,profitable_count,rock_count,scissors_count,paper_count
FROM game_rps_fun_stats WHERE user_id=?`, userID).Scan(&funRaw[0], &funRaw[1], &funRaw[2], &funRaw[3], &funRaw[4])
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return UserExport{}, nil, classifyDB(err)
	}
	if err == nil {
		values := [5]db.U128{}
		for index, raw := range funRaw {
			values[index], err = db.DecodeU128(raw)
			if err != nil {
				return UserExport{}, nil, ErrInvariant
			}
		}
		result.FunStats = &FunStatsExport{CompletedCount: values[0].Decimal(), ProfitableCount: values[1].Decimal(),
			RockCount: values[2].Decimal(), ScissorsCount: values[3].Decimal(), PaperCount: values[4].Decimal()}
	}
	var tutorial int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(p.tutorial_rps_seen,0) FROM users u
	LEFT JOIN game_user_preferences p ON p.user_id=u.id WHERE u.id=?`, userID).Scan(&tutorial); err != nil {
		return UserExport{}, nil, classifyDB(err)
	}
	if tutorial != 0 && tutorial != 1 {
		return UserExport{}, nil, ErrInvariant
	}
	result.TutorialSeen = tutorial == 1
	return result, finalizer, nil
}

func newExportFinalizer(
	ctx context.Context,
	tx *sql.Tx,
	service *Service,
	decisionNow int64,
	users []int64,
	facts activities.PublishFacts,
	publishActivity bool,
) (*ExportFinalizer, error) {
	seen := make(map[int64]struct{}, len(users))
	ordered := make([]int64, 0, len(users))
	for _, userID := range users {
		if userID <= 0 {
			return nil, ErrInvariant
		}
		if _, exists := seen[userID]; exists {
			continue
		}
		seen[userID] = struct{}{}
		ordered = append(ordered, userID)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	events := make([]preparedAccountEvent, 0, len(ordered))
	for _, userID := range ordered {
		home, err := service.projectHomeTx(ctx, tx, userID, decisionNow)
		if err != nil {
			return nil, err
		}
		body, err := json.Marshal(home)
		if err != nil || len(body) > maxProjectedStateBytes {
			return nil, ErrResourceLimit
		}
		var revision, epoch *string
		if home.Session != nil {
			value, identity := home.Session.Revision, home.Session.IdentityEpoch
			revision, epoch = &value, &identity
		} else if home.Queue != nil {
			value := home.Queue.Revision
			revision = &value
		}
		events = append(events, preparedAccountEvent{userID: userID, event: accountstream.PublishedEvent{
			Channel: accountstream.ChannelRPS, Type: accountstream.TypeDelta,
			Revision: revision, IdentityEpoch: epoch, Data: body,
		}})
	}
	copyFacts := activities.PublishFacts{Global: facts.Global, AccountIDs: append([]int64(nil), facts.AccountIDs...)}
	return &ExportFinalizer{service: service, events: events, facts: copyFacts, publishActivity: publishActivity}, nil
}

func (finalizer *ExportFinalizer) Commit() bool {
	if finalizer == nil || finalizer.service == nil || !finalizer.done.CompareAndSwap(false, true) {
		return false
	}
	ctx := context.Background()
	for _, prepared := range finalizer.events {
		_, err := finalizer.service.accountEvents.PublishCommitted(ctx, prepared.userID, prepared.event)
		finalizer.service.reportPublish(err)
	}
	if finalizer.publishActivity {
		if err := finalizer.service.activityEvents.Publish(ctx, finalizer.facts); err != nil {
			finalizer.service.reportPublish(err)
		}
	}
	return true
}

func (finalizer *ExportFinalizer) Abort() bool {
	return finalizer != nil && finalizer.done.CompareAndSwap(false, true)
}

// Retain removes at most limit due rank facts, shared summaries, and technical
// lease rows in one domain-owned transaction. Session/queue convergence remains
// on the recovery rail and is not reimplemented here.
func (adapter *LifecycleAdapter) Retain(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budgetDeadline time.Time,
) (RetentionResult, error) {
	if adapter == nil || adapter.service == nil || ctx == nil || decisionNow < 0 || decisionNow > 253402300799 ||
		limit < 1 || limit > workerBatchSize {
		return RetentionResult{}, ErrInvalidRequest
	}
	service := adapter.service
	if service.closed.Load() || !service.recovered.Load() {
		return RetentionResult{}, ErrServiceUnavailable
	}
	workerCtx, cancel := context.WithDeadline(ctx, budgetDeadline)
	defer cancel()
	tx, err := service.database.BeginTx(workerCtx, nil)
	if err != nil {
		return RetentionResult{}, classifyDB(err)
	}
	defer tx.Rollback()
	processed := 0
	if !rpsRetentionBudgetExpired(budgetDeadline) {
		count, err := service.expireRankFactsBatchTxLimit(workerCtx, tx, decisionNow, limit-processed)
		if err != nil {
			return RetentionResult{}, err
		}
		processed += count
	}
	if processed < limit && !rpsRetentionBudgetExpired(budgetDeadline) {
		result, err := tx.ExecContext(workerCtx, `DELETE FROM game_rps_summaries WHERE session_id IN (
 SELECT session_id FROM game_rps_summaries WHERE delete_at<=? ORDER BY delete_at,session_id LIMIT ?
)`, decisionNow, limit-processed)
		if err != nil {
			return RetentionResult{}, classifyDB(err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return RetentionResult{}, classifyDB(err)
		}
		processed += int(changed)
	}
	if processed < limit && !rpsRetentionBudgetExpired(budgetDeadline) {
		result, err := tx.ExecContext(workerCtx, `DELETE FROM game_online_leases WHERE rowid IN (
 SELECT lease.rowid FROM game_online_leases lease
 WHERE substr(lease.session_id,1,4)='rps_' AND (lease.health_epoch<>? OR
  (lease.expires_at<=? AND NOT EXISTS(
   SELECT 1 FROM game_rps_sessions active WHERE active.id=lease.session_id AND active.state='started')))
 ORDER BY lease.expires_at,lease.session_id,lease.user_id LIMIT ?
)`, service.healthEpoch, decisionNow, limit-processed)
		if err != nil {
			return RetentionResult{}, classifyDB(err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return RetentionResult{}, classifyDB(err)
		}
		processed += int(changed)
	}
	var more int
	if err := tx.QueryRowContext(workerCtx, `SELECT EXISTS(
 SELECT 1 FROM game_rps_rank_facts WHERE aggregate_applied=1 AND expires_at<=?
) OR EXISTS(
 SELECT 1 FROM game_rps_summaries WHERE delete_at<=?
) OR EXISTS(
 SELECT 1 FROM game_online_leases lease
 WHERE substr(lease.session_id,1,4)='rps_' AND (lease.health_epoch<>? OR
  (lease.expires_at<=? AND NOT EXISTS(
   SELECT 1 FROM game_rps_sessions active WHERE active.id=lease.session_id AND active.state='started')))
)`, decisionNow, decisionNow, service.healthEpoch, decisionNow).Scan(&more); err != nil {
		return RetentionResult{}, classifyDB(err)
	}
	if more != 0 && more != 1 {
		return RetentionResult{}, ErrInvariant
	}
	if err := tx.Commit(); err != nil {
		return RetentionResult{}, classifyDB(err)
	}
	service.forgetExpiredLeases(decisionNow)
	return RetentionResult{Processed: processed, More: more == 1}, nil
}

func rpsRetentionBudgetExpired(deadline time.Time) bool {
	return !deadline.IsZero() && !time.Now().Before(deadline)
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
