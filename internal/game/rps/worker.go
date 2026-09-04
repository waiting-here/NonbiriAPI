package rps

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/accountstream"
	"github.com/waiting-here/NonbiriAPI/internal/game"
)

func (service *Service) RecoverBeforeListen(ctx context.Context) error {
	if service == nil || service.closed.Load() {
		return ErrClosed
	}
	service.recovered.Store(false)
	now, err := service.decisionNow()
	if err != nil {
		return err
	}
	if err := service.ValidatePersistedState(ctx); err != nil {
		return err
	}
	if _, err := service.database.ExecContext(ctx, `DELETE FROM game_online_leases WHERE substr(session_id,1,4)='rps_'`); err != nil {
		return classifyDB(err)
	}
	service.leaseMu.Lock()
	clear(service.leaseBindings)
	service.leaseMu.Unlock()
	if err := service.recoverHealthEpoch(ctx, now); err != nil {
		return err
	}
	for {
		count, err := service.sweepQueues(ctx, now)
		if err != nil {
			return err
		}
		if count < workerBatchSize {
			break
		}
	}
	for {
		count, err := service.runTerminalRetries(ctx, now)
		if err != nil {
			return err
		}
		if count < workerBatchSize {
			break
		}
	}
	if _, err := service.expireRankFacts(ctx, now); err != nil {
		return err
	}
	service.recovered.Store(true)
	return nil
}

func (service *Service) validatePersistedState(ctx context.Context) error {
	if err := service.validateQueues(ctx); err != nil {
		return err
	}
	cursor := ""
	for {
		rows, err := service.database.QueryContext(ctx, `SELECT id FROM game_rps_sessions WHERE id>? ORDER BY id LIMIT ?`, cursor, workerBatchSize)
		if err != nil {
			return classifyDB(err)
		}
		ids := make([]string, 0, workerBatchSize)
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return classifyDB(err)
			}
			ids = append(ids, id)
			cursor = id
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return classifyDB(err)
		}
		if err := rows.Close(); err != nil {
			return classifyDB(err)
		}
		for _, id := range ids {
			tx, err := service.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				return classifyDB(err)
			}
			_, found, loadErr := loadSessionByID(ctx, tx, id)
			_ = tx.Rollback()
			if loadErr != nil || !found {
				if loadErr == nil {
					loadErr = ErrInvariant
				}
				service.recordInvariantAlert(ctx, id)
				return loadErr
			}
		}
		if len(ids) < workerBatchSize {
			break
		}
	}
	var invalid int
	if err := service.database.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM game_rps_rank_facts WHERE aggregate_applied<>1)`).Scan(&invalid); err != nil {
		return classifyDB(err)
	}
	if invalid != 0 {
		service.recordInvariantAlert(ctx, "rps-rank")
		return ErrInvariant
	}
	return nil
}

func (service *Service) validateQueues(ctx context.Context) error {
	cursor := ""
	for {
		rows, err := service.database.QueryContext(ctx, `SELECT `+queueColumns+` FROM game_rps_queue WHERE id>? ORDER BY id LIMIT ?`, cursor, workerBatchSize)
		if err != nil {
			return classifyDB(err)
		}
		count := 0
		for rows.Next() {
			record, err := scanQueue(rows)
			if err != nil {
				_ = rows.Close()
				service.recordInvariantAlert(ctx, "rps-queue")
				return ErrInvariant
			}
			cursor = record.ID
			count++
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return classifyDB(err)
		}
		if err := rows.Close(); err != nil {
			return classifyDB(err)
		}
		if count < workerBatchSize {
			return nil
		}
	}
}

func (service *Service) recordInvariantAlert(ctx context.Context, ref string) {
	now, err := service.decisionNow()
	if err != nil {
		return
	}
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	var exists int
	if tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM admin_alerts WHERE kind='invariant_violation' AND ref=? AND resolved=0)`, ref).Scan(&exists) != nil || exists != 0 {
		return
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO admin_alerts(kind,message,ref,created_at,resolved)
VALUES('invariant_violation','RPS persisted state failed validation',?,?,0)`, ref, now); err != nil {
		return
	}
	_ = tx.Commit()
}

func (service *Service) recoverHealthEpoch(ctx context.Context, now int64) error {
	for {
		count, err := service.recoverHealthEpochBatch(ctx, now, workerBatchSize)
		if err != nil {
			return err
		}
		if count < workerBatchSize {
			return nil
		}
	}
}

func (service *Service) recoverHealthEpochBatch(ctx context.Context, now int64, limit int) (int, error) {
	rows, err := service.database.QueryContext(ctx, `SELECT id FROM game_rps_sessions WHERE health_epoch<>? ORDER BY id LIMIT ?`, service.healthEpoch, limit)
	if err != nil {
		return 0, classifyDB(err)
	}
	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, classifyDB(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, classifyDB(err)
	}
	if err := rows.Close(); err != nil {
		return 0, classifyDB(err)
	}
	for _, id := range ids {
		if err := service.recoverOneHealthEpoch(ctx, id, now); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

func (service *Service) recoverOneHealthEpoch(ctx context.Context, sessionID string, now int64) error {
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return classifyDB(err)
	}
	defer tx.Rollback()
	record, found, err := loadSessionByID(ctx, tx, sessionID)
	if err != nil || !found {
		return err
	}
	if record.HealthEpoch == service.healthEpoch {
		return nil
	}
	expected := record.Revision
	record.HealthEpoch = service.healthEpoch
	record.Revision, err = incU128(record.Revision)
	if err != nil {
		return err
	}
	if record.State == StateStarted {
		seconds, err := phaseSeconds(&record, record.Phase)
		if err != nil {
			return err
		}
		deadline := now + int64(seconds)
		if deadline < now || deadline > 253402300799 {
			return ErrInvariant
		}
		record.PhaseDeadline = &deadline
	} else if record.State == StateTerminalProcessing {
		if record.TerminalNextRetryAt != nil && *record.TerminalNextRetryAt > now {
			value := now
			record.TerminalNextRetryAt = &value
		}
	} else {
		return ErrInvariant
	}
	if err := persistSessionTx(ctx, tx, &record, expected); err != nil {
		return err
	}
	return classifyDB(tx.Commit())
}

func (service *Service) sweepQueues(ctx context.Context, now int64) (int, error) {
	return service.sweepQueuesBatch(ctx, now, workerBatchSize)
}

func (service *Service) sweepQueuesBatch(ctx context.Context, now int64, limit int) (int, error) {
	ids, err := service.recoveryQueueIDs(ctx, now, limit)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := service.releaseQueueSystem(ctx, id, now); err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrConflict) {
			return 0, err
		}
	}
	return len(ids), nil
}

func (service *Service) recoveryQueueIDs(ctx context.Context, now int64, limit int) ([]string, error) {
	tx, err := service.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, classifyDB(err)
	}
	maintenance, err := maintenanceEnabled(ctx, tx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	snapshot, err := service.readSnapshot(ctx, tx)
	_ = tx.Rollback()
	if err != nil {
		return nil, err
	}
	disabled := map[string]bool{}
	for _, mode := range []string{game.RPSModeQuick, game.RPSModeStandard, game.RPSModeDeathmatch} {
		disabled[mode] = maintenance || !snapshot.GamesEnabled || !snapshot.RPS.Enabled || !snapshot.RPS.Modes[mode].Enabled
	}
	rows, err := service.database.QueryContext(ctx, `SELECT q.id FROM game_rps_queue q JOIN users u ON u.id=q.user_id
WHERE q.deadline<=? OR u.is_admin<>0 OR (u.is_banned<>0 AND (u.banned_until IS NULL OR u.banned_until>?))
OR (q.mode='quick' AND ?) OR (q.mode='standard' AND ?) OR (q.mode='deathmatch' AND ?)
ORDER BY q.deadline,q.created_at,q.id LIMIT ?`, now, now, disabled[game.RPSModeQuick], disabled[game.RPSModeStandard], disabled[game.RPSModeDeathmatch], limit)
	if err != nil {
		return nil, classifyDB(err)
	}
	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, classifyDB(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, classifyDB(err)
	}
	if err := rows.Close(); err != nil {
		return nil, classifyDB(err)
	}
	return ids, nil
}

func (service *Service) releaseQueueSystem(ctx context.Context, queueID string, now int64) error {
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return classifyDB(err)
	}
	defer tx.Rollback()
	record, err := scanQueue(tx.QueryRowContext(ctx, `SELECT `+queueColumns+` FROM game_rps_queue WHERE id=?`, queueID))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return classifyDB(err)
	}
	if err := service.releaseQueueTx(ctx, tx, record, now, 0); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return classifyDB(err)
	}
	service.publishUsers(ctx, []int64{record.UserID}, accountstream.TypeDelta)
	return nil
}

func (service *Service) runDeadlineSessions(ctx context.Context, now int64) (int, error) {
	return service.runDeadlineSessionsBatch(ctx, now, workerBatchSize)
}

func (service *Service) runDeadlineSessionsBatch(ctx context.Context, now int64, limit int) (int, error) {
	rows, err := service.database.QueryContext(ctx, `SELECT id FROM game_rps_sessions
WHERE state='started' AND phase_deadline<=? ORDER BY phase_deadline,id LIMIT ?`, now, limit)
	if err != nil {
		return 0, classifyDB(err)
	}
	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, classifyDB(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, classifyDB(err)
	}
	if err := rows.Close(); err != nil {
		return 0, classifyDB(err)
	}
	for _, id := range ids {
		if _, err := service.runDeadlineOne(ctx, id, now); err != nil && !errors.Is(err, ErrConflict) && !errors.Is(err, ErrNotFound) {
			return 0, err
		}
	}
	return len(ids), nil
}

func (service *Service) runDeadlineOne(ctx context.Context, sessionID string, now int64) (bool, error) {
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return false, classifyDB(err)
	}
	defer tx.Rollback()
	record, found, err := loadSessionByID(ctx, tx, sessionID)
	if err != nil || !found {
		return false, err
	}
	if record.State != StateStarted || record.PhaseDeadline == nil || now < *record.PhaseDeadline {
		return false, nil
	}
	if maintenance, err := maintenanceEnabled(ctx, tx); err != nil {
		return false, err
	} else if maintenance {
		if err := service.authorizeSystemContinuation(ctx, tx, record, ActionDeadline); err != nil {
			return false, err
		}
	}
	users := sessionUsers(&record)
	reduced := reducer{service: service, ctx: ctx, tx: tx, record: &record, expectedRevision: record.Revision, now: now}
	if err := reduced.applyDeadlineDefaults(); err != nil {
		if errors.Is(err, ErrInvariant) {
			_ = tx.Rollback()
			service.recordInvariantAlert(ctx, sessionID)
		}
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, classifyDB(err)
	}
	if reduced.terminal != nil {
		users = reduced.terminal.Users
	}
	service.publishUsers(ctx, users, accountstream.TypeDelta)
	if err := service.activityEvents.Publish(ctx, reduced.facts); err != nil {
		service.reportPublish(err)
	}
	return true, nil
}

func (service *Service) runTerminalRetries(ctx context.Context, now int64) (int, error) {
	return service.runTerminalRetriesBatch(ctx, now, workerBatchSize)
}

func (service *Service) runTerminalRetriesBatch(ctx context.Context, now int64, limit int) (int, error) {
	rows, err := service.database.QueryContext(ctx, `SELECT id FROM game_rps_sessions
WHERE state='terminal_processing' AND (terminal_next_retry_at IS NULL OR terminal_next_retry_at<=?)
ORDER BY COALESCE(terminal_next_retry_at,0),id LIMIT ?`, now, limit)
	if err != nil {
		return 0, classifyDB(err)
	}
	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, classifyDB(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, classifyDB(err)
	}
	if err := rows.Close(); err != nil {
		return 0, classifyDB(err)
	}
	for _, id := range ids {
		if _, err := service.runTerminalOne(ctx, id, now); err != nil && !errors.Is(err, ErrConflict) && !errors.Is(err, ErrNotFound) {
			return 0, err
		}
	}
	return len(ids), nil
}

func (service *Service) runTerminalOne(ctx context.Context, sessionID string, now int64) (bool, error) {
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return false, classifyDB(err)
	}
	defer tx.Rollback()
	record, found, err := loadSessionByID(ctx, tx, sessionID)
	if err != nil || !found {
		return false, err
	}
	if record.State != StateTerminalProcessing || record.TerminalNextRetryAt != nil && *record.TerminalNextRetryAt > now {
		return false, nil
	}
	if maintenance, err := maintenanceEnabled(ctx, tx); err != nil {
		return false, err
	} else if maintenance {
		if err := service.authorizeSystemContinuation(ctx, tx, record, ActionTerminal); err != nil {
			return false, err
		}
	}
	result, err := service.finalizeTerminalTx(ctx, tx, &record, now)
	if err != nil {
		if retryErr := service.scheduleTerminalRetryTx(ctx, tx, &record, now, err); retryErr != nil {
			return false, retryErr
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return false, classifyDB(commitErr)
		}
		service.publishUsers(ctx, sessionUsers(&record), accountstream.TypeDelta)
		return false, nil
	}
	if err := tx.Commit(); err != nil {
		return false, classifyDB(err)
	}
	service.publishUsers(ctx, result.Users, accountstream.TypeDelta)
	if err := service.activityEvents.Publish(ctx, result.Facts); err != nil {
		service.reportPublish(err)
	}
	return true, nil
}

func (service *Service) shortenDisconnected(ctx context.Context, now int64) (int, error) {
	rows, err := service.database.QueryContext(ctx, `SELECT DISTINCT g.id FROM game_rps_sessions g
JOIN game_rps_seats seat ON seat.session_id=g.id
WHERE g.state='started' AND g.phase_deadline>? AND seat.user_id IS NOT NULL AND seat.deletion_state='active'
AND EXISTS(SELECT 1 FROM game_online_leases old WHERE old.session_id=g.id AND old.user_id=seat.user_id AND old.health_epoch=?)
AND NOT EXISTS(SELECT 1 FROM game_online_leases live WHERE live.session_id=g.id AND live.user_id=seat.user_id AND live.health_epoch=? AND live.expires_at>?)
ORDER BY g.id LIMIT ?`, now+5, service.healthEpoch, service.healthEpoch, now, workerBatchSize)
	if err != nil {
		return 0, classifyDB(err)
	}
	ids := make([]string, 0, workerBatchSize)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, classifyDB(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, classifyDB(err)
	}
	if err := rows.Close(); err != nil {
		return 0, classifyDB(err)
	}
	for _, id := range ids {
		if err := service.shortenOneDisconnected(ctx, id, now); err != nil && !errors.Is(err, ErrConflict) {
			return 0, err
		}
	}
	return len(ids), nil
}

func (service *Service) shortenOneDisconnected(ctx context.Context, sessionID string, now int64) error {
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return classifyDB(err)
	}
	defer tx.Rollback()
	record, found, err := loadSessionByID(ctx, tx, sessionID)
	if err != nil || !found || record.State != StateStarted || record.PhaseDeadline == nil {
		return err
	}
	deadline := now + 5
	if *record.PhaseDeadline <= deadline {
		return nil
	}
	var disconnected int
	for _, seat := range record.Seats {
		if seat.UserID == nil || seat.DeletionState != "active" || !seatNeedsAction(record, seat.SeatNo) {
			continue
		}
		var had, live int
		if err := tx.QueryRowContext(ctx, `SELECT
EXISTS(SELECT 1 FROM game_online_leases WHERE session_id=? AND user_id=? AND health_epoch=?),
EXISTS(SELECT 1 FROM game_online_leases WHERE session_id=? AND user_id=? AND health_epoch=? AND expires_at>?)`,
			record.ID, *seat.UserID, service.healthEpoch, record.ID, *seat.UserID, service.healthEpoch, now).Scan(&had, &live); err != nil {
			return classifyDB(err)
		}
		if had == 1 && live == 0 {
			disconnected = 1
			break
		}
	}
	if disconnected == 0 {
		return nil
	}
	expected := record.Revision
	record.Revision, err = incU128(record.Revision)
	if err != nil {
		return err
	}
	record.PhaseDeadline = &deadline
	if err := persistSessionTx(ctx, tx, &record, expected); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return classifyDB(err)
	}
	service.publishUsers(ctx, sessionUsers(&record), accountstream.TypeDelta)
	return nil
}

func seatNeedsAction(record sessionRecord, seat int) bool {
	if seat < 0 || seat > 2 {
		return false
	}
	value := record.Seats[seat]
	switch record.Phase {
	case PhaseGesture, PhasePaidPoolGesture, PhaseFreePoolGesture, PhaseUltimateGesture:
		return value.GestureEnvelope == nil
	case PhaseDealerRaise:
		return record.DealerSeat != nil && *record.DealerSeat == seat
	case PhaseFollowers:
		return record.DealerSeat != nil && *record.DealerSeat != seat && value.FollowerAction == nil
	default:
		return false
	}
}

func (service *Service) pruneLeases(ctx context.Context, now int64) (int, error) {
	result, err := service.database.ExecContext(ctx, `DELETE FROM game_online_leases WHERE rowid IN (
SELECT lease.rowid FROM game_online_leases lease
WHERE substr(lease.session_id,1,4)='rps_' AND (lease.health_epoch<>? OR
 (lease.expires_at<=? AND NOT EXISTS(
  SELECT 1 FROM game_rps_sessions active WHERE active.id=lease.session_id AND active.state='started')))
ORDER BY expires_at,session_id,user_id LIMIT ?)`, service.healthEpoch, now, workerBatchSize)
	if err != nil {
		return 0, classifyDB(err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return 0, classifyDB(err)
	}
	service.forgetExpiredLeases(now)
	return int(changed), nil
}

func (service *Service) StartWorker(parent context.Context) error {
	if service == nil || service.closed.Load() {
		return ErrClosed
	}
	if parent == nil || !service.recovered.Load() {
		return errors.New("rps: recovery must complete before worker start")
	}
	service.workerMu.Lock()
	defer service.workerMu.Unlock()
	if service.workerCancel != nil {
		return errors.New("rps: worker already started")
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	service.workerCancel, service.workerDone = cancel, done
	go service.workerLoop(ctx, done)
	return nil
}

func (service *Service) workerLoop(ctx context.Context, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(service.workerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		now, err := service.decisionNow()
		if err != nil {
			continue
		}
		_, _ = service.sweepQueues(ctx, now)
		for _, mode := range []string{game.RPSModeQuick, game.RPSModeStandard, game.RPSModeDeathmatch} {
			for count := 0; count < workerBatchSize; count++ {
				matched, err := service.MatchOnce(ctx, mode)
				if err != nil || !matched {
					break
				}
			}
		}
		_, _ = service.runDeadlineSessions(ctx, now)
		_, _ = service.runTerminalRetries(ctx, now)
		_, _ = service.shortenDisconnected(ctx, now)
		_, _ = service.pruneLeases(ctx, now)
		_, _ = service.expireRankFactsBatch(ctx, now)
	}
}

func (service *Service) expireRankFactsBatch(ctx context.Context, now int64) (int, error) {
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return 0, classifyDB(err)
	}
	defer tx.Rollback()
	count, err := service.expireRankFactsBatchTx(ctx, tx, now)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, classifyDB(err)
	}
	return count, nil
}
