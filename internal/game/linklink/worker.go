package linklink

import (
	"context"
	"database/sql"
	"errors"
	"time"
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
	for {
		removed, err := service.pruneLeases(ctx, now)
		if err != nil {
			return err
		}
		if removed < workerBatchSize {
			break
		}
	}
	for {
		processed, err := service.runDue(ctx, now)
		if err != nil {
			return err
		}
		if processed < workerBatchSize {
			break
		}
	}
	service.recovered.Store(true)
	return nil
}

func (service *Service) validatePersistedSessions(ctx context.Context) error {
	cursor := ""
	for {
		rows, err := service.database.QueryContext(ctx, `SELECT `+sessionColumns+`
FROM game_linklink_sessions WHERE id>? ORDER BY id LIMIT ?`, cursor, workerBatchSize)
		if err != nil {
			return classifyDB(err)
		}
		count := 0
		for rows.Next() {
			record, scanErr := scanSession(rows)
			if scanErr != nil {
				_ = rows.Close()
				return classifyDB(scanErr)
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

func (service *Service) StartWorker(parent context.Context) error {
	if service == nil || service.closed.Load() {
		return ErrClosed
	}
	if parent == nil || !service.recovered.Load() {
		return errors.New("linklink: recovery must complete before worker start")
	}
	service.workerMu.Lock()
	defer service.workerMu.Unlock()
	if service.workerCancel != nil {
		return errors.New("linklink: worker already started")
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
		_, _ = service.runDue(ctx, now)
		_, _ = service.pruneLeases(ctx, now)
	}
}

func (service *Service) runDue(ctx context.Context, now int64) (int, error) {
	rows, err := service.database.QueryContext(ctx, `
SELECT id FROM game_linklink_sessions WHERE deadline<=? ORDER BY deadline,id LIMIT ?`, now, workerBatchSize)
	if err != nil {
		return 0, classifyDB(err)
	}
	ids := make([]string, 0, workerBatchSize)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, classifyDB(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, classifyDB(err)
	}
	if err := rows.Close(); err != nil {
		return 0, classifyDB(err)
	}
	for _, sessionID := range ids {
		_, err := service.timeoutOne(ctx, sessionID, now)
		if err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

func (service *Service) timeoutOne(ctx context.Context, sessionID string, now int64) (bool, error) {
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return false, classifyDB(err)
	}
	defer tx.Rollback()
	record, found, err := loadSessionForWorker(ctx, tx, sessionID)
	if err != nil || !found {
		return false, err
	}
	if now < record.Deadline {
		return false, nil
	}
	maintenance, err := maintenanceEnabled(ctx, tx)
	if err != nil {
		return false, err
	}
	if maintenance {
		if err := service.authorizeSystemTimeout(ctx, tx, record); err != nil {
			return false, err
		}
	}
	if _, err := terminalize(ctx, tx, record, TerminalTimedOut, now); err != nil {
		if errors.Is(err, ErrConflict) {
			return false, nil
		}
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, classifyDB(err)
	}
	service.forgetSession(sessionID)
	return true, nil
}

func loadSessionForWorker(ctx context.Context, tx *sql.Tx, sessionID string) (sessionRecord, bool, error) {
	record, err := scanSession(tx.QueryRowContext(ctx, `SELECT `+sessionColumns+` FROM game_linklink_sessions WHERE id=?`, sessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return sessionRecord{}, false, nil
	}
	if err != nil {
		return sessionRecord{}, false, classifyDB(err)
	}
	return record, true, nil
}

func (service *Service) pruneLeases(ctx context.Context, now int64) (int, error) {
	result, err := service.database.ExecContext(ctx, `
DELETE FROM game_online_leases WHERE rowid IN (
 SELECT rowid FROM game_online_leases
 WHERE substr(session_id,1,3)='ll_' AND (expires_at<=? OR health_epoch<>?)
 ORDER BY expires_at,session_id LIMIT ?
)`, now, service.healthEpoch, workerBatchSize)
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
