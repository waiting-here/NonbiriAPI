package fatfish

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

type mutationDecision struct {
	idempotency.Decision
}

func (s *Service) beginMutationTx(ctx context.Context, tx *sql.Tx, actorKind string, actorID int64, key, method, route string, pathIDs []string, body any, nowMS int64) (mutationDecision, error) {
	if actorID <= 0 || nowMS < 0 {
		return mutationDecision{}, ErrInvalid
	}
	actor, err := idempotency.ActorScopeHash(actorKind, strconv.FormatInt(actorID, 10))
	if err != nil {
		return mutationDecision{}, ErrInvalid
	}
	raw, err := idempotency.CanonicalJSON(body)
	if err != nil {
		return mutationDecision{}, ErrInvalid
	}
	digest, err := idempotency.RequestDigest(idempotency.DigestInput{ActorScopeHash: actor, Method: method, Route: route, PathResourceIDs: pathIDs, Body: raw})
	if err != nil {
		return mutationDecision{}, ErrInvalid
	}
	secret, err := s.keys.DeriveGenerationTwoSubkey([]byte("NonbiriAPI/fatfish-idempotency/v1"))
	if err != nil || len(secret) != 32 {
		clear(secret)
		return mutationDecision{}, ErrInvariant
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(digest[:])
	copy(digest[:], mac.Sum(nil))
	clear(secret)
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{Scope: idempotency.ScopeControlMutation, ActorHash: actor, Key: key, RequestHash: digest, DecisionNow: nowMS / 1000})
	if errors.Is(err, idempotency.ErrConflict) || errors.Is(err, idempotency.ErrInProgress) {
		return mutationDecision{}, ErrConflict
	}
	if err != nil {
		return mutationDecision{}, err
	}
	return mutationDecision{decision}, nil
}

func completeMutationTx(ctx context.Context, tx *sql.Tx, decision mutationDecision, value any, status int) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return idempotency.Complete(ctx, tx, decision.Decision, status, raw)
}

func replayMutation[T any](decision mutationDecision) (T, bool, error) {
	var value T
	if decision.Kind != idempotency.Replay {
		return value, false, nil
	}
	if decision.HTTPStatus != http.StatusOK && decision.HTTPStatus != http.StatusAccepted {
		return value, false, ErrInvariant
	}
	if err := json.Unmarshal(decision.ResponseBody, &value); err != nil {
		return value, false, ErrInvariant
	}
	return value, true, nil
}
