package claim

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

func seedPersonalRoutingModel(t *testing.T, fixture *claimFixture, owner int64, name, strategy string, keys ...testKey) int64 {
	t.Helper()
	result, err := fixture.db.Exec(`INSERT INTO models(user_id,provider,model,full_name,route_strategy,created_at,updated_at)
VALUES(?,'public',?,?,?,1000,1000)`, owner, name, "public/"+name, strategy)
	if err != nil {
		t.Fatal(err)
	}
	modelID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	for index, key := range keys {
		if _, err := fixture.db.Exec(`INSERT OR IGNORE INTO model_pair_catalog
(endpoint_key_id,normalized_model_id,automatic_supports,manual_supports,automatic_revision,pair_revision,updated_at)
VALUES(?,?,0,1,0,1,1000)`, key.keyID, key.candidate.UpstreamModelID); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.db.Exec(`INSERT INTO model_bindings
(model_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at) VALUES(?,?,?,?,1000,1000)`,
			modelID, key.keyID, key.candidate.UpstreamModelID, index); err != nil {
			t.Fatal(err)
		}
	}
	return modelID
}

func personalRoutingFixture(t *testing.T, strategy string) (*claimFixture, int64, int64, []testKey) {
	t.Helper()
	fixture := newClaimFixture(t)
	owner := fixture.seedUser("personal-routing", false)
	keys := []testKey{fixture.seedKey(owner, "personal-a"), fixture.seedKey(owner, "personal-b"), fixture.seedKey(owner, "personal-c")}
	model := seedPersonalRoutingModel(t, fixture, owner, "model", strategy, keys...)
	return fixture, owner, model, keys
}

func personalRoutingInput(t *testing.T, fixture *claimFixture, owner, model int64, keys ...testKey) ClaimInput {
	t.Helper()
	var name string
	if err := fixture.db.QueryRow(`SELECT full_name FROM models WHERE id=?`, model).Scan(&name); err != nil {
		t.Fatal(err)
	}
	request, err := fixture.service.Accept(context.Background(), AcceptInput{
		UserID: owner, Route: RouteOpenAIChat, ModelSnapshot: name, AttemptLimit: 1, ReservedMilli: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	input := ClaimInput{RequestID: request.ID, ActorUserID: owner, PersonalModelID: model, Purpose: PurposeSelf, AttemptSeq: 1}
	if len(keys) == 1 {
		input.Candidate = keys[0].candidate
	} else {
		for _, key := range keys {
			input.BalancedCandidates = append(input.BalancedCandidates, BalancedCandidate{Candidate: key.candidate})
		}
	}
	return input
}

func personalRoutingClaim(t *testing.T, fixture *claimFixture, owner, model int64, keys ...testKey) Handle {
	t.Helper()
	handle, err := fixture.service.Claim(context.Background(), personalRoutingInput(t, fixture, owner, model, keys...))
	if err != nil {
		t.Fatal(err)
	}
	return handle
}

func personalRoutingCounts(t *testing.T, fixture *claimFixture) (receipts, dispatches, affinities int64) {
	t.Helper()
	receipts, dispatches, _ = routingCounts(t, fixture)
	if err := fixture.db.QueryRow(`SELECT count(*) FROM personal_key_affinities`).Scan(&affinities); err != nil {
		t.Fatal(err)
	}
	return
}

func TestPersonalRoutingConcurrentReservationsSplitEmptyLoad(t *testing.T) {
	fixture, owner, model, keys := personalRoutingFixture(t, "cache_balanced")
	inputs := []ClaimInput{
		personalRoutingInput(t, fixture, owner, model, keys[:2]...),
		personalRoutingInput(t, fixture, owner, model, keys[:2]...),
	}
	type result struct {
		handle Handle
		err    error
	}
	start := make(chan struct{})
	results := make(chan result, len(inputs))
	for _, input := range inputs {
		go func() {
			<-start
			handle, err := fixture.service.Claim(context.Background(), input)
			results <- result{handle, err}
		}()
	}
	close(start)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil || first.handle.EndpointKeyID() == second.handle.EndpointKeyID() {
		t.Fatalf("concurrent reservations did not split: %+v / %+v", first, second)
	}
	if receipts, dispatches, affinities := personalRoutingCounts(t, fixture); receipts != 2 || dispatches != 0 || affinities != 0 {
		t.Fatalf("pending routing state = %d/%d/%d", receipts, dispatches, affinities)
	}
	if _, err := fixture.service.ReleaseUndispatched(context.Background(), first.handle); err != nil {
		t.Fatal(err)
	}
	if receipts, dispatches, affinities := personalRoutingCounts(t, fixture); receipts != 1 || dispatches != 0 || affinities != 0 {
		t.Fatalf("released routing state = %d/%d/%d", receipts, dispatches, affinities)
	}
}

func TestPersonalRoutingAffinityIsOwnerAndModelScoped(t *testing.T) {
	fixture, owner, model, keys := personalRoutingFixture(t, "cache_balanced")
	dispatchRoutingClaim(t, fixture, personalRoutingClaim(t, fixture, owner, model, keys[0]))
	preferred := personalRoutingClaim(t, fixture, owner, model, keys[1], keys[0])
	if preferred.EndpointKeyID() != keys[0].keyID {
		t.Fatal("valid association did not precede lower load")
	}
	if _, err := fixture.service.ReleaseUndispatched(context.Background(), preferred); err != nil {
		t.Fatal(err)
	}
	secondModel := seedPersonalRoutingModel(t, fixture, owner, "second", "cache_balanced", keys...)
	otherModel := personalRoutingClaim(t, fixture, owner, secondModel, keys[0], keys[1])
	if otherModel.EndpointKeyID() != keys[1].keyID {
		t.Fatal("association leaked across personal models")
	}
	otherOwner := fixture.seedUser("other-personal-owner", false)
	foreign := personalRoutingInput(t, fixture, otherOwner, model, keys[:2]...)
	if _, err := fixture.service.Claim(context.Background(), foreign); !errors.Is(err, ErrModelUnavailable) {
		t.Fatalf("foreign model claim = %v", err)
	}
	otherKey := fixture.seedKey(otherOwner, "foreign-personal-key")
	selection := personalRoutingClaim(t, fixture, owner, model, otherKey, keys[2])
	if selection.EndpointKeyID() != keys[2].keyID {
		t.Fatal("foreign endpoint key entered the candidate set")
	}
}

func TestPersonalRoutingAffinityRequiresCurrentAvailability(t *testing.T) {
	for _, change := range []string{"expiry", "revision", "disabled", "unbound"} {
		t.Run(change, func(t *testing.T) {
			fixture, owner, model, keys := personalRoutingFixture(t, "cache_balanced")
			dispatchRoutingClaim(t, fixture, personalRoutingClaim(t, fixture, owner, model, keys[0]))
			var err error
			switch change {
			case "expiry":
				fixture.clock.Store(1300)
			case "revision":
				_, err = fixture.db.Exec(`UPDATE models SET revision=revision+1 WHERE id=?`, model)
			case "disabled":
				_, err = fixture.db.Exec(`UPDATE endpoint_keys SET enabled=0 WHERE id=?`, keys[0].keyID)
			case "unbound":
				_, err = fixture.db.Exec(`DELETE FROM model_bindings WHERE model_id=? AND endpoint_key_id=?`, model, keys[0].keyID)
			}
			if err != nil {
				t.Fatal(err)
			}
			selected := personalRoutingClaim(t, fixture, owner, model, keys[1], keys[0])
			if selected.EndpointKeyID() != keys[1].keyID {
				t.Fatalf("%s association still selected key %d", change, selected.EndpointKeyID())
			}
		})
	}
}

func TestPersonalRoutingCountsEveryStrategyAndActualFailures(t *testing.T) {
	for _, strategy := range []string{"ordered", "random", "cache_balanced"} {
		t.Run(strategy, func(t *testing.T) {
			fixture, owner, model, keys := personalRoutingFixture(t, strategy)
			handle := personalRoutingClaim(t, fixture, owner, model, keys[0])
			dispatchRoutingClaim(t, fixture, handle)
			if _, err := fixture.service.CompleteAttempt(context.Background(), handle, AttemptOutcome{
				Kind: ResultResponse, UpstreamStatus: 500, ProtocolSuccess: false,
				Usage: connectorcontract.Usage{Present: true},
			}); err != nil {
				t.Fatal(err)
			}
			wantAffinity := int64(0)
			if strategy == "cache_balanced" {
				wantAffinity = 1
			}
			if receipts, dispatches, affinities := personalRoutingCounts(t, fixture); receipts != 1 || dispatches != 1 || affinities != wantAffinity {
				t.Fatalf("actual failed dispatch state = %d/%d/%d", receipts, dispatches, affinities)
			}
		})
	}
}

func TestPersonalRoutingUndeliveredRevokeIsIdempotentAndSplicesAffinity(t *testing.T) {
	fixture, owner, model, keys := personalRoutingFixture(t, "cache_balanced")
	handles := make([]Handle, len(keys))
	for index, key := range keys {
		handles[index] = personalRoutingClaim(t, fixture, owner, model, key)
		dispatchRoutingClaim(t, fixture, handles[index])
	}
	for _, index := range []int{1, 2, 0, 1} {
		if err := fixture.service.RevokeUndelivered(context.Background(), handles[index]); err != nil {
			t.Fatal(err)
		}
		if index == 2 {
			var affinity string
			if err := fixture.db.QueryRow(`SELECT attempt_id FROM personal_key_affinities WHERE user_id=? AND model_id=?`, owner, model).Scan(&affinity); err != nil || affinity != handles[0].ClaimID() {
				t.Fatalf("revoked predecessor restored: %q %v", affinity, err)
			}
		}
	}
	if receipts, dispatches, affinities := personalRoutingCounts(t, fixture); receipts != 0 || dispatches != 0 || affinities != 0 {
		t.Fatalf("undelivered dispatch state = %d/%d/%d", receipts, dispatches, affinities)
	}
}

func TestPersonalRoutingRevokeDoesNotRestoreRemovedBinding(t *testing.T) {
	fixture, owner, model, keys := personalRoutingFixture(t, "cache_balanced")
	first := personalRoutingClaim(t, fixture, owner, model, keys[0])
	dispatchRoutingClaim(t, fixture, first)
	second := personalRoutingClaim(t, fixture, owner, model, keys[1])
	dispatchRoutingClaim(t, fixture, second)
	if _, err := fixture.db.Exec(`DELETE FROM model_bindings WHERE model_id=? AND endpoint_key_id=?`, model, keys[0].keyID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.RevokeUndelivered(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	if receipts, dispatches, affinities := personalRoutingCounts(t, fixture); receipts != 1 || dispatches != 1 || affinities != 0 {
		t.Fatalf("removed binding restored affinity: %d/%d/%d", receipts, dispatches, affinities)
	}
}

func TestPersonalRoutingPreservesFrozenClaimWithoutRestoringRemovedResources(t *testing.T) {
	for _, removal := range []string{"model", "key"} {
		t.Run(removal, func(t *testing.T) {
			fixture, owner, model, keys := personalRoutingFixture(t, "cache_balanced")
			handle := personalRoutingClaim(t, fixture, owner, model, keys[0])
			var err error
			if removal == "model" {
				_, err = fixture.db.Exec(`DELETE FROM models WHERE id=?`, model)
			} else {
				_, err = fixture.db.Exec(`DELETE FROM endpoint_keys WHERE id=?`, keys[0].keyID)
			}
			if err != nil {
				t.Fatal(err)
			}
			dispatchRoutingClaim(t, fixture, handle)
			if receipts, dispatches, affinities := personalRoutingCounts(t, fixture); receipts != 1 || dispatches != 1 || affinities != 0 {
				t.Fatalf("frozen dispatch recreated removed association: %d/%d/%d", receipts, dispatches, affinities)
			}
			if err := fixture.service.RevokeUndelivered(context.Background(), handle); err != nil {
				t.Fatal(err)
			}
			if receipts, dispatches, affinities := personalRoutingCounts(t, fixture); receipts != 0 || dispatches != 0 || affinities != 0 {
				t.Fatalf("frozen dispatch could not be revoked: %d/%d/%d", receipts, dispatches, affinities)
			}
		})
	}
}

func TestPersonalRoutingCredentialFailureRetractsProvisionalDispatch(t *testing.T) {
	fixture, owner, model, keys := personalRoutingFixture(t, "cache_balanced")
	handle := personalRoutingClaim(t, fixture, owner, model, keys[0])
	fixture.codec.failOpen = true
	if grant, err := fixture.service.TakeForDispatch(context.Background(), handle); err == nil || grant != nil {
		t.Fatal("credential failure dispatched a grant")
	}
	if receipts, dispatches, affinities := personalRoutingCounts(t, fixture); receipts != 0 || dispatches != 0 || affinities != 0 {
		t.Fatalf("credential failure retained provisional load: %d/%d/%d", receipts, dispatches, affinities)
	}
}

func TestPersonalRoutingDeletionAndLateCompletionCannotReviveAffinity(t *testing.T) {
	for _, removal := range []string{"owner", "model", "key"} {
		t.Run(removal, func(t *testing.T) {
			fixture, owner, model, keys := personalRoutingFixture(t, "cache_balanced")
			handle := personalRoutingClaim(t, fixture, owner, model, keys[0])
			dispatchRoutingClaim(t, fixture, handle)
			lifecycleOwner, err := NewCharityRoutingLifecycle(fixture.db)
			if err != nil {
				t.Fatal(err)
			}
			switch removal {
			case "owner":
				tx, err := fixture.db.BeginTx(context.Background(), nil)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := lifecycleOwner.PrepareDelete(context.Background(), tx, lifecycle.DeleteRequest{UserID: owner, DecisionNow: fixture.clock.Load()}); err != nil {
					tx.Rollback()
					t.Fatal(err)
				}
				err = tx.Commit()
				if err != nil {
					t.Fatal(err)
				}
				var receiptOwner sql.NullInt64
				if err := fixture.db.QueryRow(`SELECT user_id FROM charity_dispatch_receipts WHERE attempt_id=?`, handle.ClaimID()).Scan(&receiptOwner); err != nil || receiptOwner.Valid {
					t.Fatalf("deleted owner retained in receipt: %+v %v", receiptOwner, err)
				}
			case "model":
				_, err = fixture.db.Exec(`DELETE FROM models WHERE id=?`, model)
			case "key":
				_, err = fixture.db.Exec(`DELETE FROM endpoint_keys WHERE id=?`, keys[0].keyID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.service.CompleteAttempt(context.Background(), handle, AttemptOutcome{
				Kind: ResultResponse, UpstreamStatus: 500, ProtocolSuccess: false,
				Usage: connectorcontract.Usage{Present: true},
			}); err != nil {
				t.Fatal(err)
			}
			if receipts, dispatches, affinities := personalRoutingCounts(t, fixture); receipts != 1 || dispatches != 1 || affinities != 0 {
				t.Fatalf("late completion changed routing state: %d/%d/%d", receipts, dispatches, affinities)
			}
			work, err := lifecycleOwner.Retain(context.Background(), 1300, 10, time.Time{})
			if err != nil || work.Processed != 2 || work.More {
				t.Fatalf("retention after window: %+v %v", work, err)
			}
			if receipts, dispatches, affinities := personalRoutingCounts(t, fixture); receipts != 0 || dispatches != 0 || affinities != 0 {
				t.Fatalf("expired anonymous routing state = %d/%d/%d", receipts, dispatches, affinities)
			}
		})
	}
}

func TestPersonalRoutingRestartReleasesPendingAndKeepsActualLoad(t *testing.T) {
	fixture, owner, model, keys := personalRoutingFixture(t, "cache_balanced")
	dispatchRoutingClaim(t, fixture, personalRoutingClaim(t, fixture, owner, model, keys[0]))
	personalRoutingClaim(t, fixture, owner, model, keys[1])
	restarted, err := New(Dependencies{
		DB: fixture.db, Secrets: fixture.codec, Accounting: fixture.accounting,
		Charity: fixture.charity, Acceptance: allowAcceptanceGate{},
		Now: func() time.Time { return time.Unix(fixture.clock.Load(), 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.service = restarted
	if _, err := restarted.RecoverNonterminal(context.Background(), MaxRecoveryBatch); err != nil {
		t.Fatal(err)
	}
	if receipts, dispatches, affinities := personalRoutingCounts(t, fixture); receipts != 1 || dispatches != 1 || affinities != 1 {
		t.Fatalf("recovered routing state = %d/%d/%d", receipts, dispatches, affinities)
	}
	selected := personalRoutingClaim(t, fixture, owner, model, keys[1], keys[0])
	if selected.EndpointKeyID() != keys[0].keyID {
		t.Fatal("restart lost valid personal association")
	}
}
