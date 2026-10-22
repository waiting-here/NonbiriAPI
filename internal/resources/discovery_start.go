package resources

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/headerfacts"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

type discoverySelection struct {
	source            connectionstore.SourceAuthorization
	ref               connectionstore.SourceAuthorizationRef
	endpointID, keyID int64
	authorize         func(context.Context, *sql.Tx) error
}

type discoveryStart[T any] struct {
	actorID   int64
	role      string
	stage     string
	wait      bool
	preflight func(context.Context, *sql.Tx) error
	resolve   func(context.Context, *sql.Tx) (discoverySelection, error)
	project   func(context.Context, *sql.Tx, discoverySelection, string, DiscoveryEvidence) (T, ResourceOperationResult, error)
}

func beginSourceDiscovery[T any](ctx context.Context, r *Repository, mutation ControlMutation, start discoveryStart[T]) (MutationResult[T], error) {
	now, err := r.nowUnix()
	if err != nil {
		return MutationResult[T]{}, err
	}
	actor, err := actorHash(start.actorID)
	if err != nil {
		return MutationResult[T]{}, err
	}
	digest, err := idempotency.RequestDigest(idempotency.DigestInput{ActorScopeHash: actor, Method: mutation.Method, Route: mutation.Route, PathResourceIDs: mutation.PathIDs, Query: mutation.Query, Body: mutation.CanonicalBody})
	if err != nil {
		return MutationResult[T]{}, ErrInvalidRequest
	}
	tx, err := beginTx(ctx, r.db)
	if err != nil {
		return MutationResult[T]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	if err := start.preflight(ctx, tx); err != nil {
		return MutationResult[T]{}, err
	}
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{Scope: idempotency.ScopeModelDiscovery, ActorHash: actor, Key: mutation.IdempotencyKey, RequestHash: digest, DecisionNow: now})
	if errors.Is(err, idempotency.ErrConflict) || errors.Is(err, idempotency.ErrInProgress) {
		return MutationResult[T]{}, ErrConflict
	}
	if err != nil {
		return MutationResult[T]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replayMutation[T](decision)
	}
	selected, err := start.resolve(ctx, tx)
	if err != nil {
		return MutationResult[T]{}, err
	}
	kind, err := discoveryConnectorType(selected.source.ListFormat)
	if err != nil {
		return MutationResult[T]{}, err
	}
	descriptor, ok := r.connectors.Descriptor(kind)
	if !ok || descriptor.Discoverer == nil {
		return MutationResult[T]{}, ErrInvalidRequest
	}
	var generation int64
	var active bool
	err = tx.QueryRowContext(ctx, `SELECT st.result_generation,EXISTS(SELECT 1 FROM discovery_runs run WHERE run.id=st.current_run_id AND run.state='checking') FROM discovery_source_states st WHERE st.source_revision_id=?`, selected.source.SourceRevisionID).Scan(&generation, &active)
	if err != nil {
		return MutationResult[T]{}, err
	}
	if active || generation == int64(^uint64(0)>>1) {
		return MutationResult[T]{}, ErrConflict
	}
	operationID, err := r.operationID()
	if err != nil || !db.ValidateOpaqueID(operationID, "op_") {
		return MutationResult[T]{}, ErrUnavailable
	}
	hash := sha256.Sum256([]byte(operationID))
	generation++
	job := discoveryJob{revision: generation, input: DiscoveryClaimInput{OperationID: operationID, OwnerUserID: selected.source.OwnerUserID, EndpointID: selected.endpointID, EndpointKeyID: selected.keyID, ConnectorType: kind, CanonicalBaseURL: selected.source.CanonicalBaseURL, Discoverer: descriptor.Discoverer, Authorize: selected.authorize,
		Source: selected.ref, SourceRevisionID: selected.source.SourceRevisionID, CredentialVersionID: selected.source.CredentialVersionID, UseApprovedRevisionID: selected.source.UseApprovedRevisionID}}
	reservation, admitted := r.discoveryWorker.ReserveDiscovery()
	if !admitted {
		return MutationResult[T]{}, errDiscoveryWorkerUnavailable
	}
	launched := false
	defer func() {
		if !launched {
			reservation.Release()
		}
	}()
	launch := reservation.Start
	if start.wait {
		cancellable, ok := reservation.(interface {
			StartContext(context.Context, func(context.Context))
		})
		if !ok {
			return MutationResult[T]{}, ErrUnavailable
		}
		launch = func(work func(context.Context)) { cancellable.StartContext(ctx, work) }
	}
	checkpoint := "source:" + strconv.FormatInt(selected.source.SourceRevisionID, 10) + ":" + strconv.FormatInt(generation, 10)
	if _, err := tx.ExecContext(ctx, `INSERT INTO accepted_operations(id,kind,actor_user_id,actor_role,payload_hash,state,checkpoint,last_error_class,created_at,terminal_at) VALUES(?,'model_discovery',?,?,?,'accepted',?,NULL,?,NULL)`, operationID, start.actorID, start.role, digest[:], checkpoint, now); err != nil {
		return MutationResult[T]{}, err
	}
	var useID, useRevision any
	if selected.ref.Purpose == connectionstore.SourceCharity {
		useID = selected.source.DonationUseID
		useRevision = selected.source.UseApprovedRevisionID
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO discovery_runs(source_revision_id,source_revision_snapshot,credential_version_id,operation_id,operation_hash,result_generation,authorization_kind,authorization_use_id,authorization_use_revision_id,state,safe_class,safe_diagnostic,started_at,model_count) VALUES(?,?,?,?,?,?,?,?,?,'checking','none','',?,0)`, selected.source.SourceRevisionID, selected.source.SourceRevision, selected.source.CredentialVersionID, operationID, hash[:], generation, string(selected.ref.Purpose), useID, useRevision, now)
	if err != nil {
		return MutationResult[T]{}, err
	}
	runID, err := result.LastInsertId()
	if err != nil {
		return MutationResult[T]{}, err
	}
	result, err = tx.ExecContext(ctx, `UPDATE discovery_source_states SET result_generation=?,current_run_id=?,updated_at=? WHERE source_revision_id=? AND result_generation=?`, generation, runID, now, selected.source.SourceRevisionID, generation-1)
	if err != nil {
		return MutationResult[T]{}, err
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		return MutationResult[T]{}, ErrConflict
	}
	if err := r.reconcileSourceKeysTx(ctx, tx, selected.source.OwnerUserID, selected.source.SourceID, now); err != nil {
		return MutationResult[T]{}, err
	}
	checking := DiscoveryEvidence{State: "checking", Revision: strconv.FormatInt(generation, 10), SafeClass: "none", ObservedAt: &now}
	item, receipt, err := start.project(ctx, tx, selected, operationID, checking)
	if err != nil {
		return MutationResult[T]{}, err
	}
	out, err := finishJSONMutation(ctx, tx, decision, http.StatusAccepted, item)
	if err != nil {
		return MutationResult[T]{}, err
	}
	if start.stage != "" {
		if err := recordResourceOperation(ctx, tx, start.actorID, mutation, idempotency.ScopeModelDiscovery, start.stage, receipt); err != nil {
			return MutationResult[T]{}, err
		}
	}
	if err := commitTx(tx, &committed); err != nil {
		return MutationResult[T]{}, err
	}
	launched = true
	done := make(chan error, 1)
	source, hasSource := observability.SourceFromContext(ctx)
	headers := headerfacts.Request(ctx)
	launch(func(work context.Context) {
		if hasSource {
			work = observability.WithSource(work, source)
		}
		work = headerfacts.WithCapturedRequest(work, headers)
		if start.wait {
			work = discoveryIdentityContext{Context: work, identity: ctx}
		}
		done <- r.runDiscovery(work, job)
	})
	if start.wait {
		select {
		case err := <-done:
			if ctx.Err() != nil {
				return MutationResult[T]{}, ctx.Err()
			}
			if err != nil {
				return MutationResult[T]{}, err
			}
		case <-ctx.Done():
			return MutationResult[T]{}, ctx.Err()
		}

		readTx, err := beginTx(ctx, r.db)
		if err != nil {
			return MutationResult[T]{}, err
		}
		defer readTx.Rollback()
		if selected.authorize != nil {
			if err := selected.authorize(ctx, readTx); err != nil {
				return MutationResult[T]{}, err
			}
		}
		live, err := connectionstore.LoadDiscoveryAuthorizationTx(ctx, readTx, selected.ref)
		if err != nil {
			return MutationResult[T]{}, mapConnectionStoreError(err)
		}
		if !sameSourceAuthorization(live, selected.source) {
			return MutationResult[T]{}, ErrConflict
		}
		evidence, err := discoveryEvidenceForRevisionTx(ctx, readTx, selected.source.SourceRevisionID)
		if err != nil {
			return MutationResult[T]{}, err
		}
		if evidence.revision != job.revision {
			return MutationResult[T]{}, ErrConflict
		}
		projection, err := evidence.evidence()
		if err != nil {
			return MutationResult[T]{}, err
		}
		value, _, err := start.project(ctx, readTx, selected, operationID, projection)
		if err != nil {
			return MutationResult[T]{}, err
		}
		out.Value = value
	}
	return out, nil
}
