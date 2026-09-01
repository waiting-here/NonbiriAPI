package rps

import (
	"context"
	"database/sql"
	"encoding/json"
	"math/big"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/accountstream"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type enqueueBody struct {
	Mode                string `json:"mode"`
	DeviceToken         string `json:"device_token"`
	DeathmatchConfirmed bool   `json:"deathmatch_confirmed"`
}

type cancelBody struct {
	ExpectedRevision string `json:"expected_revision"`
}

func requestDecision(userID int64, method, route string, pathIDs []string, body any) ([32]byte, [32]byte, error) {
	actor, err := actorHash(userID)
	if err != nil {
		return [32]byte{}, [32]byte{}, ErrInvalidRequest
	}
	canonical, err := idempotency.CanonicalJSON(body)
	if err != nil {
		return [32]byte{}, [32]byte{}, ErrInvalidRequest
	}
	digest, err := idempotency.RequestDigest(idempotency.DigestInput{
		ActorScopeHash: actor, Method: method, Route: route, PathResourceIDs: pathIDs, Body: canonical,
	})
	if err != nil {
		return [32]byte{}, [32]byte{}, ErrInvalidRequest
	}
	return actor, digest, nil
}

func replayQueue(decision idempotency.Decision) (QueueMutationResult, error) {
	if decision.Kind != idempotency.Replay || decision.HTTPStatus != http.StatusAccepted || len(decision.ResponseBody) == 0 {
		return QueueMutationResult{}, ErrInvariant
	}
	var queue Queue
	if json.Unmarshal(decision.ResponseBody, &queue) != nil || !db.ValidateOpaqueID(queue.ID, "rpsq_") || queue.State != "waiting" {
		return QueueMutationResult{}, ErrInvariant
	}
	return QueueMutationResult{Queue: queue, HTTPStatus: decision.HTTPStatus, IdempotentReplay: true}, nil
}

func (service *Service) Enqueue(ctx context.Context, input EnqueueInput) (QueueMutationResult, error) {
	if service == nil || service.closed.Load() {
		return QueueMutationResult{}, ErrClosed
	}
	if input.UserID <= 0 || game.ResolveMode(game.RPSID, input.Mode) != nil ||
		(input.Mode == game.RPSModeDeathmatch) != input.DeathmatchConfirmed {
		return QueueMutationResult{}, ErrInvalidRequest
	}
	if err := (game.StartContract{Game: game.RPSID, Version: game.RPSVersion, Mode: input.Mode}).Validate(); err != nil {
		return QueueMutationResult{}, ErrInvalidRequest
	}
	deviceHash, err := hashDeviceToken(service.keys.device, input.DeviceToken)
	if err != nil {
		return QueueMutationResult{}, err
	}
	ipHash := hashCanonicalIP(service.keys.ip, input.CanonicalSourceIP)
	if _, err := idempotency.KeyHash(input.IdempotencyKey); err != nil {
		return QueueMutationResult{}, ErrInvalidRequest
	}
	body := enqueueBody{Mode: input.Mode, DeviceToken: input.DeviceToken, DeathmatchConfirmed: input.DeathmatchConfirmed}
	actor, digest, err := requestDecision(input.UserID, http.MethodPost, RouteQueue, nil, body)
	if err != nil {
		return QueueMutationResult{}, err
	}
	now, err := service.decisionNow()
	if err != nil {
		return QueueMutationResult{}, err
	}
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return QueueMutationResult{}, classifyDB(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := service.userAuthorizer.AuthorizeUserMutation(ctx, tx, input.UserID); err != nil {
		return QueueMutationResult{}, mapAuthorization(err)
	}
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{
		Scope: idempotency.ScopeGameRPS, ActorHash: actor, Key: input.IdempotencyKey, RequestHash: digest, DecisionNow: now,
	})
	if err != nil {
		return QueueMutationResult{}, mapIdempotency(err)
	}
	if decision.Kind == idempotency.Replay {
		return replayQueue(decision)
	}
	maintenance, err := maintenanceEnabled(ctx, tx)
	if err != nil {
		return QueueMutationResult{}, err
	}
	if maintenance {
		return QueueMutationResult{}, ErrMaintenance
	}
	if _, found, err := loadPending(ctx, tx, input.UserID); err != nil {
		return QueueMutationResult{}, err
	} else if found {
		return QueueMutationResult{}, ErrConflict
	}
	var slotExists int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM game_rps_user_slots WHERE user_id=?)`, input.UserID).Scan(&slotExists); err != nil {
		return QueueMutationResult{}, classifyDB(err)
	}
	if slotExists != 0 {
		return QueueMutationResult{}, ErrConflict
	}
	snapshot, err := service.readSnapshot(ctx, tx)
	if err != nil {
		return QueueMutationResult{}, err
	}
	config := snapshot.RPS.Modes[input.Mode]
	if !snapshot.GamesEnabled || !snapshot.RPS.Enabled || !config.Enabled {
		return QueueMutationResult{}, ErrFeatureDisabled
	}
	var queueCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_rps_queue WHERE mode=?`, input.Mode).Scan(&queueCount); err != nil {
		return QueueMutationResult{}, classifyDB(err)
	}
	if queueCount < 0 || queueCount >= game.RPSQueueCapacity {
		return QueueMutationResult{}, ErrResourceLimit
	}
	startReservation, _, err := service.limiter.Reserve(input.UserID)
	if err != nil {
		return QueueMutationResult{}, mapLimiter(err)
	}
	limiterCommitted := false
	defer func() {
		if !limiterCommitted {
			startReservation.Release()
		}
	}()
	account, err := ledger.UserAccount(ctx, tx, input.UserID)
	if err != nil {
		return QueueMutationResult{}, mapLedger(err)
	}
	reserveBig := big.NewInt(config.BaseMilli)
	if input.Mode == game.RPSModeStandard {
		reserveBig.Mul(reserveBig, big.NewInt(game.RPSStandardMultiplier))
	} else if input.Mode == game.RPSModeDeathmatch {
		reserveBig = account.Balance.Big()
	}
	if reserveBig.Sign() <= 0 || reserveBig.Cmp(big.NewInt(config.BaseMilli)) < 0 {
		return QueueMutationResult{}, ErrInsufficientCredits
	}
	reserved, err := u128(reserveBig)
	if err != nil {
		return QueueMutationResult{}, ErrInvariant
	}
	amount, err := ledger.AmountFromBig(reserveBig)
	if err != nil {
		return QueueMutationResult{}, ErrInvariant
	}
	queueID, err := service.generate("rpsq_")
	if err != nil {
		return QueueMutationResult{}, err
	}
	operationID, err := service.generate("op_")
	if err != nil {
		return QueueMutationResult{}, err
	}
	queueAccount, err := ledger.CreateRPSQueueAccount(ctx, tx, queueID, now)
	if err != nil {
		return QueueMutationResult{}, mapLedger(err)
	}
	one, _ := u128(bigOne)
	deadline := now + int64(config.QueueSeconds)
	if deadline < now || deadline > 253402300799 {
		return QueueMutationResult{}, ErrServiceUnavailable
	}
	ref, err := ledger.RPSQueueReservation(queueID)
	if err != nil {
		return QueueMutationResult{}, ErrInvariant
	}
	if err := ledger.Reserve(ctx, tx, ref, one, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO game_rps_queue(
id,user_id,account_id,mode,revision,reservation_operation_id,reserved,ledger_rows_remaining,
device_token_hash,source_ip_hash,deadline,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			queueID, input.UserID, queueAccount.ID, input.Mode, db.EncodeU128(one), operationID,
			db.EncodeU128(reserved), db.EncodeU128(one), deviceHash[:], ipHash[:], deadline, now); err != nil {
			return classifyDB(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO game_rps_user_slots(user_id,queue_id,session_id,created_at) VALUES(?,?,NULL,?)`, input.UserID, queueID, now); err != nil {
			return classifyDB(err)
		}
		return nil
	}); err != nil {
		return QueueMutationResult{}, mapLedger(err)
	}
	plan, err := ledger.NewRPSQueueReserve(ledger.Meta{OperationID: operationID, ActorUserID: input.UserID, CreatedAt: now},
		queueID, account.ID, queueAccount.ID, amount)
	if err != nil {
		return QueueMutationResult{}, ErrInvariant
	}
	if _, err := ledger.Apply(ctx, tx, plan); err != nil {
		return QueueMutationResult{}, mapLedger(err)
	}
	record := queueRecord{
		ID: queueID, UserID: input.UserID, AccountID: queueAccount.ID, Mode: input.Mode, Revision: one,
		ReservationOperationID: operationID, Reserved: reserved, LedgerRowsRemaining: one,
		DeviceHash: deviceHash, IPHash: ipHash, Deadline: deadline, CreatedAt: now,
	}
	queue := queueView(record, now)
	response, err := json.Marshal(queue)
	if err != nil {
		return QueueMutationResult{}, ErrInvariant
	}
	if err := idempotency.Complete(ctx, tx, decision, http.StatusAccepted, response); err != nil {
		return QueueMutationResult{}, mapIdempotency(err)
	}
	if err := tx.Commit(); err != nil {
		return QueueMutationResult{}, classifyDB(err)
	}
	committed = true
	startReservation.Commit()
	limiterCommitted = true
	service.publishUsers(ctx, []int64{input.UserID}, accountstream.TypeDelta)
	return QueueMutationResult{Queue: queue, HTTPStatus: http.StatusAccepted}, nil
}

func (service *Service) Cancel(ctx context.Context, input CancelInput) (EmptyMutationResult, error) {
	expected, err := db.ParseU128Decimal(input.ExpectedRevision)
	if service == nil || service.closed.Load() || input.UserID <= 0 || !db.ValidateOpaqueID(input.QueueID, "rpsq_") ||
		err != nil || expected.Big().Sign() <= 0 {
		return EmptyMutationResult{}, ErrInvalidRequest
	}
	if _, err := idempotency.KeyHash(input.IdempotencyKey); err != nil {
		return EmptyMutationResult{}, ErrInvalidRequest
	}
	actor, digest, err := requestDecision(input.UserID, http.MethodDelete, RouteQueueItem, []string{input.QueueID}, cancelBody{ExpectedRevision: input.ExpectedRevision})
	if err != nil {
		return EmptyMutationResult{}, err
	}
	now, err := service.decisionNow()
	if err != nil {
		return EmptyMutationResult{}, err
	}
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return EmptyMutationResult{}, classifyDB(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := service.userAuthorizer.AuthorizeUserMutation(ctx, tx, input.UserID); err != nil {
		return EmptyMutationResult{}, mapAuthorization(err)
	}
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{
		Scope: idempotency.ScopeGameRPS, ActorHash: actor, Key: input.IdempotencyKey, RequestHash: digest, DecisionNow: now,
	})
	if err != nil {
		return EmptyMutationResult{}, mapIdempotency(err)
	}
	if decision.Kind == idempotency.Replay {
		if decision.HTTPStatus != http.StatusNoContent || len(decision.ResponseBody) != 0 {
			return EmptyMutationResult{}, ErrInvariant
		}
		return EmptyMutationResult{HTTPStatus: http.StatusNoContent, IdempotentReplay: true}, nil
	}
	record, found, err := loadQueueByID(ctx, tx, input.QueueID, input.UserID)
	if err != nil {
		return EmptyMutationResult{}, err
	}
	if !found {
		return EmptyMutationResult{}, ErrNotFound
	}
	if record.Revision != expected {
		return EmptyMutationResult{}, ErrConflict
	}
	if err := service.releaseQueueTx(ctx, tx, record, now, input.UserID); err != nil {
		return EmptyMutationResult{}, err
	}
	if err := idempotency.Complete(ctx, tx, decision, http.StatusNoContent, nil); err != nil {
		return EmptyMutationResult{}, mapIdempotency(err)
	}
	if err := tx.Commit(); err != nil {
		return EmptyMutationResult{}, classifyDB(err)
	}
	committed = true
	service.publishUsers(ctx, []int64{input.UserID}, accountstream.TypeDelta)
	return EmptyMutationResult{HTTPStatus: http.StatusNoContent}, nil
}

func (service *Service) releaseQueueTx(ctx context.Context, tx *sql.Tx, record queueRecord, now, actorUserID int64) error {
	operationID, err := service.generate("op_")
	if err != nil {
		return err
	}
	userAccount, err := ledger.UserAccount(ctx, tx, record.UserID)
	if err != nil {
		return mapLedger(err)
	}
	amount, err := ledger.AmountFromBig(record.Reserved.Big())
	if err != nil {
		return ErrInvariant
	}
	plan, err := ledger.NewRPSQueueRelease(ledger.Meta{OperationID: operationID, ActorUserID: actorUserID, CreatedAt: now},
		record.ID, record.AccountID, userAccount.ID, amount)
	if err != nil {
		return ErrInvariant
	}
	ref, _ := ledger.RPSQueueReservation(record.ID)
	_, err = ledger.ConsumeReserved(ctx, tx, ref, plan, func(ctx context.Context, tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM game_rps_queue WHERE id=? AND user_id=? AND revision=? AND ledger_rows_remaining=?`,
			record.ID, record.UserID, db.EncodeU128(record.Revision), db.EncodeU128(record.LedgerRowsRemaining))
		if err != nil {
			return classifyDB(err)
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			return ErrConflict
		}
		return nil
	})
	return mapLedger(err)
}
