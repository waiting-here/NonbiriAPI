package activities

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

func beginControlMutation(ctx context.Context, tx *sql.Tx, actorKind string, actorID int64, input ControlMutation, now int64) (idempotency.Decision, error) {
	if actorID <= 0 || (actorKind != "user" && actorKind != "admin") || len(input.CanonicalBody) > idempotency.MaxControlBodyBytes {
		return idempotency.Decision{}, ErrInvalidRequest
	}
	actor, err := idempotency.ActorScopeHash(actorKind, strconv.FormatInt(actorID, 10))
	if err != nil {
		return idempotency.Decision{}, ErrInvalidRequest
	}
	digest, err := idempotency.RequestDigest(idempotency.DigestInput{
		ActorScopeHash: actor, Method: input.Method, Route: input.Route,
		PathResourceIDs: input.PathIDs, Query: input.Query, Body: input.CanonicalBody,
	})
	if err != nil {
		return idempotency.Decision{}, ErrInvalidRequest
	}
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{
		Scope: idempotency.ScopeControlMutation, ActorHash: actor,
		Key: input.IdempotencyKey, RequestHash: digest, DecisionNow: now,
	})
	if errors.Is(err, idempotency.ErrConflict) || errors.Is(err, idempotency.ErrInProgress) {
		return idempotency.Decision{}, ErrConflict
	}
	if err != nil {
		return idempotency.Decision{}, classifyDatabaseError("accept control mutation", err)
	}
	return decision, nil
}

func replayMutation[T any](decision idempotency.Decision) (MutationResult[T], error) {
	var value T
	if len(decision.ResponseBody) > 0 {
		if err := json.Unmarshal(decision.ResponseBody, &value); err != nil {
			return MutationResult[T]{}, fmt.Errorf("%w: decode idempotency response", ErrInvariant)
		}
	}
	return MutationResult[T]{
		Value: value, Status: decision.HTTPStatus,
		Body: append([]byte(nil), decision.ResponseBody...), Replayed: true,
	}, nil
}

func finishJSONMutation[T any](ctx context.Context, tx *sql.Tx, decision idempotency.Decision, status int, value T) (MutationResult[T], error) {
	body, err := json.Marshal(value)
	if err != nil || len(body) > idempotency.MaxResponseBytes {
		return MutationResult[T]{}, fmt.Errorf("%w: encode mutation response", ErrInvariant)
	}
	if err := idempotency.Complete(ctx, tx, decision, status, body); err != nil {
		return MutationResult[T]{}, classifyDatabaseError("complete control mutation", err)
	}
	return MutationResult[T]{Value: value, Status: status, Body: body}, nil
}
