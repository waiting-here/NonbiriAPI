package rps

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/accountstream"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

type actionWireBody struct {
	PhaseSeq         string `json:"phase_seq"`
	ExpectedRevision string `json:"expected_revision"`
	Action           string `json:"action"`
	Payload          any    `json:"payload"`
}

type gestureActionPayload struct {
	Gesture string `json:"gesture"`
}

type dealerActionPayload struct {
	Decision string `json:"decision"`
	Amount   string `json:"amount,omitempty"`
}

type followerActionPayload struct {
	Decision string `json:"decision"`
}

func canonicalAction(input ActionInput) (actionWireBody, error) {
	result := actionWireBody{PhaseSeq: input.PhaseSeq, ExpectedRevision: input.ExpectedRevision, Action: input.Action}
	switch input.Action {
	case "gesture":
		if !validGesture(input.Gesture) || input.DealerDecision != "" || input.RaiseAmount != "" || input.FollowerDecision != "" {
			return actionWireBody{}, ErrInvalidRequest
		}
		result.Payload = gestureActionPayload{Gesture: input.Gesture}
	case "dealer_decision":
		if input.Gesture != "" || input.FollowerDecision != "" {
			return actionWireBody{}, ErrInvalidRequest
		}
		if input.DealerDecision == "no_raise" {
			if input.RaiseAmount != "" {
				return actionWireBody{}, ErrInvalidRequest
			}
			result.Payload = dealerActionPayload{Decision: "no_raise"}
		} else if input.DealerDecision == "raise" {
			if _, err := parsePositiveCreditInteger(input.RaiseAmount); err != nil {
				return actionWireBody{}, err
			}
			result.Payload = dealerActionPayload{Decision: "raise", Amount: input.RaiseAmount}
		} else {
			return actionWireBody{}, ErrInvalidRequest
		}
	case "follower_decision":
		if input.Gesture != "" || input.DealerDecision != "" || input.RaiseAmount != "" ||
			(input.FollowerDecision != FollowerCall && input.FollowerDecision != FollowerSurrender) {
			return actionWireBody{}, ErrInvalidRequest
		}
		result.Payload = followerActionPayload{Decision: input.FollowerDecision}
	default:
		return actionWireBody{}, ErrInvalidRequest
	}
	return result, nil
}

func seatForUser(record *sessionRecord, userID int64) (int, bool) {
	if record == nil {
		return -1, false
	}
	for seat := range record.Seats {
		if record.Seats[seat].UserID != nil && *record.Seats[seat].UserID == userID && record.Seats[seat].DeletionState == "active" {
			return seat, true
		}
	}
	return -1, false
}

func sessionUsers(record *sessionRecord) []int64 {
	result := make([]int64, 0, 3)
	if record == nil {
		return result
	}
	for _, seat := range record.Seats {
		if seat.UserID != nil && seat.DeletionState == "active" {
			result = append(result, *seat.UserID)
		}
	}
	return result
}

func validateActionReplayTx(ctx context.Context, tx *sql.Tx, input ActionInput, home HomeState, body []byte) error {
	switch home.Kind {
	case "session":
		if home.Session == nil || home.Session.SessionID != input.SessionID {
			return ErrInvariant
		}
		var epochRaw []byte
		err := tx.QueryRowContext(ctx, `SELECT identity_epoch FROM game_rps_sessions WHERE id=?`, input.SessionID).Scan(&epochRaw)
		if err == nil {
			epoch, err := db.DecodeU128(epochRaw)
			if err != nil || epoch.Big().Sign() <= 0 {
				return ErrInvariant
			}
			if home.Session.IdentityEpoch != epoch.Decimal() {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return classifyDB(err)
		}
		var seats, deidentified int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(CASE WHEN user_id IS NULL THEN 1 ELSE 0 END),0)
FROM game_rps_summary_seats WHERE session_id=?`, input.SessionID).Scan(&seats, &deidentified); err != nil {
			return classifyDB(err)
		}
		if seats != 3 {
			return ErrInvariant
		}
		if deidentified != 0 {
			return ErrConflict
		}
		return nil
	case "pending_result":
		if home.Result == nil || home.Result.SessionID != input.SessionID {
			return ErrInvariant
		}
		pending, found, err := loadPending(ctx, tx, input.UserID)
		if err != nil {
			return err
		}
		if !found || pending.SessionID != input.SessionID {
			return ErrConflict
		}
		canonical, err := json.Marshal(HomeState{Kind: "pending_result", Result: &pending})
		if err != nil {
			return ErrInvariant
		}
		if !bytes.Equal(canonical, body) {
			return ErrConflict
		}
		return nil
	default:
		return ErrInvariant
	}
}

func (service *Service) Action(ctx context.Context, input ActionInput) (MutationResult, error) {
	phaseSeq, phaseErr := db.ParseU128Decimal(input.PhaseSeq)
	expected, revisionErr := db.ParseU128Decimal(input.ExpectedRevision)
	wire, wireErr := canonicalAction(input)
	if service == nil || service.closed.Load() || input.UserID <= 0 || input.SessionBinding == "" ||
		!db.ValidateOpaqueID(input.SessionID, "rps_") || phaseErr != nil || revisionErr != nil || wireErr != nil ||
		phaseSeq.Big().Sign() <= 0 || expected.Big().Sign() <= 0 {
		return MutationResult{}, ErrInvalidRequest
	}
	if _, err := idempotency.KeyHash(input.IdempotencyKey); err != nil {
		return MutationResult{}, ErrInvalidRequest
	}
	actor, digest, err := requestDecision(input.UserID, http.MethodPost, RouteActions, []string{input.SessionID}, wire)
	if err != nil {
		return MutationResult{}, err
	}
	now, err := service.decisionNow()
	if err != nil {
		return MutationResult{}, err
	}
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return MutationResult{}, classifyDB(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := service.userAuthorizer.AuthorizeUserMutation(ctx, tx, input.UserID); err != nil {
		return MutationResult{}, mapAuthorization(err)
	}
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{
		Scope: idempotency.ScopeGameRPS, ActorHash: actor, Key: input.IdempotencyKey, RequestHash: digest, DecisionNow: now,
	})
	if err != nil {
		return MutationResult{}, mapIdempotency(err)
	}
	if decision.Kind == idempotency.Replay {
		if decision.HTTPStatus == http.StatusConflict {
			return MutationResult{}, ErrConflict
		}
		if decision.HTTPStatus != http.StatusOK {
			return MutationResult{}, ErrInvariant
		}
		home, err := decodeHomeState(decision.ResponseBody)
		if err != nil {
			return MutationResult{}, err
		}
		if err := validateActionReplayTx(ctx, tx, input, home, decision.ResponseBody); err != nil {
			return MutationResult{}, err
		}
		return MutationResult{State: home, HTTPStatus: decision.HTTPStatus, IdempotentReplay: true}, nil
	}
	record, found, err := loadSessionByID(ctx, tx, input.SessionID)
	if err != nil {
		return MutationResult{}, err
	}
	if !found {
		return MutationResult{}, ErrNotFound
	}
	seat, owner := seatForUser(&record, input.UserID)
	if !owner {
		return MutationResult{}, ErrNotFound
	}
	maintenance, err := maintenanceEnabled(ctx, tx)
	if err != nil {
		return MutationResult{}, err
	}
	if maintenance {
		if err := service.authorizeMaintenanceContinuation(ctx, tx, record, input.UserID, input.SessionBinding, ActionPlay, now); err != nil {
			return MutationResult{}, err
		}
	}
	users := sessionUsers(&record)
	if record.State != StateStarted || record.PhaseDeadline == nil {
		return MutationResult{}, ErrConflict
	}
	if now >= *record.PhaseDeadline {
		if maintenance {
			if err := service.authorizeSystemContinuation(ctx, tx, record, ActionDeadline); err != nil {
				return MutationResult{}, err
			}
		}
		reducer := reducer{service: service, ctx: ctx, tx: tx, record: &record, expectedRevision: record.Revision, now: now}
		if err := reducer.applyDeadlineDefaults(); err != nil {
			if errors.Is(err, ErrInvariant) {
				_ = tx.Rollback()
				service.recordInvariantAlert(ctx, record.ID)
			}
			return MutationResult{}, err
		}
		if err := idempotency.Complete(ctx, tx, decision, http.StatusConflict, nil); err != nil {
			return MutationResult{}, mapIdempotency(err)
		}
		if err := tx.Commit(); err != nil {
			return MutationResult{}, classifyDB(err)
		}
		committed = true
		if reducer.terminal != nil {
			users = reducer.terminal.Users
		}
		service.publishUsers(ctx, users, accountstream.TypeDelta)
		if err := service.activityEvents.Publish(ctx, reducer.facts); err != nil {
			service.reportPublish(err)
		}
		return MutationResult{}, ErrConflict
	}
	if record.Revision != expected || record.PhaseSeq != phaseSeq {
		return MutationResult{}, ErrConflict
	}
	releaseRate, err := service.reserveAction(input.UserID)
	if err != nil {
		return MutationResult{}, err
	}
	rateCommitted := false
	defer func() { releaseRate(rateCommitted) }()
	nextRevision, err := incU128(record.Revision)
	if err != nil {
		return MutationResult{}, err
	}
	record.Revision = nextRevision
	reducer := reducer{
		service: service, ctx: ctx, tx: tx, record: &record, expectedRevision: expected, now: now, actorUserID: input.UserID,
	}
	if err := reducer.applyManual(seat, input); err != nil {
		if errors.Is(err, ErrInvariant) {
			_ = tx.Rollback()
			service.recordInvariantAlert(ctx, record.ID)
		}
		return MutationResult{}, err
	}
	home, err := service.projectHomeTx(ctx, tx, input.UserID, now)
	if err != nil {
		return MutationResult{}, err
	}
	body, err := json.Marshal(home)
	if err != nil || len(body) > idempotency.MaxResponseBytes {
		return MutationResult{}, ErrResourceLimit
	}
	if err := idempotency.Complete(ctx, tx, decision, http.StatusOK, body); err != nil {
		return MutationResult{}, mapIdempotency(err)
	}
	if err := tx.Commit(); err != nil {
		return MutationResult{}, classifyDB(err)
	}
	committed, rateCommitted = true, true
	if reducer.terminal != nil {
		users = reducer.terminal.Users
	}
	service.publishUsers(ctx, users, accountstream.TypeDelta)
	if err := service.activityEvents.Publish(ctx, reducer.facts); err != nil {
		service.reportPublish(err)
	}
	return MutationResult{State: home, HTTPStatus: http.StatusOK}, nil
}

func (value *reducer) applyManual(seat int, input ActionInput) error {
	switch value.record.Phase {
	case PhaseGesture, PhasePaidPoolGesture, PhaseFreePoolGesture, PhaseUltimateGesture:
		if input.Action != "gesture" || value.record.Seats[seat].GestureEnvelope != nil {
			return ErrConflict
		}
		value.service.randomMu.Lock()
		envelope, err := sealGesture(value.service.random, value.service.keys.gesture, value.record.ID, seat, value.record.PhaseSeq, value.record.RulesVersion, input.Gesture)
		value.service.randomMu.Unlock()
		if err != nil {
			return err
		}
		phase := value.record.PhaseSeq
		value.record.Seats[seat].GestureEnvelope = envelope
		value.record.Seats[seat].GesturePhaseSeq, value.record.Seats[seat].LastActionPhaseSeq = &phase, &phase
		if err := appendEvent(value.record, EventActionLocked, actionLockedPayload{SeatNo: seat, ActionKind: "gesture"}); err != nil {
			return err
		}
		for _, persisted := range value.record.Seats {
			if persisted.GestureEnvelope == nil {
				return value.persist()
			}
		}
		if err := value.persist(); err != nil {
			return err
		}
		if value.record.Phase == PhaseGesture && value.record.Mode != game.RPSModeQuick {
			for index := range value.record.Seats {
				value.record.Seats[index].LastActionPhaseSeq = nil
			}
			if err := value.advancePhase(PhaseDealerRaise); err != nil {
				return err
			}
			return value.persist()
		}
		return value.revealAndReduce()
	case PhaseDealerRaise:
		if input.Action != "dealer_decision" || value.record.DealerSeat == nil || *value.record.DealerSeat != seat {
			return ErrConflict
		}
		if err := appendEvent(value.record, EventActionLocked, actionLockedPayload{SeatNo: seat, ActionKind: "dealer_decision"}); err != nil {
			return err
		}
		if input.DealerDecision == "no_raise" {
			return value.revealAndReduce()
		}
		raise, err := parsePositiveCreditInteger(input.RaiseAmount)
		if err != nil {
			return err
		}
		minimum := cloneBig(value.record.Seats[0].CurrentBalance.Big())
		for index := 1; index < 3; index++ {
			if value.record.Seats[index].CurrentBalance.Big().Cmp(minimum) < 0 {
				minimum.Set(value.record.Seats[index].CurrentBalance.Big())
			}
		}
		if raise.Cmp(minimum) > 0 {
			return ErrConflict
		}
		raiseValue, err := u128(raise)
		if err != nil {
			return err
		}
		value.record.DealerRaise = &raiseValue
		if err := value.advancePhase(PhaseFollowers); err != nil {
			return err
		}
		inputs := [3]*big.Int{new(big.Int), new(big.Int), new(big.Int)}
		inputs[seat] = raise
		return value.applyInputs(inputs)
	case PhaseFollowers:
		if input.Action != "follower_decision" || value.record.DealerSeat == nil || *value.record.DealerSeat == seat ||
			value.record.Seats[seat].FollowerAction != nil || value.record.DealerRaise == nil {
			return ErrConflict
		}
		decision := input.FollowerDecision
		phase := value.record.PhaseSeq
		value.record.Seats[seat].FollowerAction, value.record.Seats[seat].LastActionPhaseSeq = &decision, &phase
		if err := appendEvent(value.record, EventActionLocked, actionLockedPayload{SeatNo: seat, ActionKind: "follower_decision"}); err != nil {
			return err
		}
		inputs := [3]*big.Int{new(big.Int), new(big.Int), new(big.Int)}
		if decision == FollowerCall {
			inputs[seat] = value.record.DealerRaise.Big()
			if inputs[seat].Cmp(value.record.Seats[seat].CurrentBalance.Big()) > 0 {
				return ErrInvariant
			}
		}
		complete := true
		for other := range value.record.Seats {
			if other != *value.record.DealerSeat && value.record.Seats[other].FollowerAction == nil {
				complete = false
			}
		}
		if decision == FollowerCall {
			if err := value.applyInputs(inputs); err != nil {
				return err
			}
		} else if err := value.persist(); err != nil {
			return err
		}
		if !complete {
			return nil
		}
		return value.revealAndReduce()
	default:
		return ErrConflict
	}
}
