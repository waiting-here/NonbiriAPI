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

func routingClaim(t *testing.T, fixture *claimFixture, caller int64, key testKey, donationKeyID int64) Handle {
	t.Helper()
	request := fixture.acceptCharity(caller, 1)
	handle, err := fixture.service.Claim(context.Background(), ClaimInput{
		RequestID: request.ID, ActorUserID: caller, AttemptSeq: 1,
		Purpose: PurposeCharity, Candidate: key.candidate, DonationKeyID: donationKeyID,
	})
	if err != nil {
		t.Fatalf("reserve charity routing claim: %v", err)
	}
	return handle
}

func dispatchRoutingClaim(t *testing.T, fixture *claimFixture, handle Handle) {
	t.Helper()
	grant, err := fixture.service.TakeForDispatch(context.Background(), handle)
	if err != nil {
		t.Fatalf("dispatch charity routing claim: %v", err)
	}
	grant.Clear()
}

func routingFixture(t *testing.T, ttl int) (*claimFixture, int64, []testKey, []int64) {
	t.Helper()
	fixture := newClaimFixture(t)
	caller := fixture.seedUser("routing-caller", false)
	keys := make([]testKey, 3)
	donations := make([]int64, 3)
	for index, label := range []string{"routing-a", "routing-b", "routing-c"} {
		donor := fixture.seedUser(label, false)
		keys[index] = fixture.seedKey(donor, label)
		donations[index] = fixture.seedDonationKey(donor, keys[index], label, 0)
	}
	if _, err := fixture.db.Exec(`UPDATE charity_model_routing SET strategy='cache_balanced' WHERE model_id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Exec(`UPDATE charity_routing_settings SET revision=2,affinity_ttl_seconds=? WHERE model_id=1`, ttl); err != nil {
		t.Fatal(err)
	}
	return fixture, caller, keys, donations
}

func routingCounts(t *testing.T, fixture *claimFixture) (receipts, dispatches, affinities int64) {
	t.Helper()
	for _, item := range []struct {
		query string
		dest  *int64
	}{
		{`SELECT count(*) FROM charity_dispatch_receipts`, &receipts},
		{`SELECT COALESCE(sum(dispatch_count),0) FROM charity_dispatch_buckets`, &dispatches},
		{`SELECT count(*) FROM charity_key_affinities`, &affinities},
	} {
		if err := fixture.db.QueryRow(item.query).Scan(item.dest); err != nil {
			t.Fatal(err)
		}
	}
	return
}

func routingAffinityAttempt(t *testing.T, fixture *claimFixture) string {
	t.Helper()
	var attemptID string
	if err := fixture.db.QueryRow(`SELECT attempt_id FROM charity_key_affinities WHERE model_id=1`).Scan(&attemptID); err != nil {
		t.Fatal(err)
	}
	return attemptID
}

func TestCharityRoutingConcurrentReservationsSplitEmptyLoad(t *testing.T) {
	fixture, caller, keys, donations := routingFixture(t, 300)
	options := []BalancedCandidate{
		{Candidate: keys[0].candidate, DonationKeyID: donations[0]},
		{Candidate: keys[1].candidate, DonationKeyID: donations[1]},
	}
	firstRequest := fixture.acceptCharity(caller, 1)
	secondRequest := fixture.acceptCharity(caller, 1)
	claim := func(requestID string) (Handle, error) {
		return fixture.service.Claim(context.Background(), ClaimInput{
			RequestID: requestID, ActorUserID: caller, AttemptSeq: 1,
			Purpose: PurposeCharity, BalancedCandidates: options,
		})
	}
	start := make(chan struct{})
	type result struct {
		handle Handle
		err    error
	}
	results := make(chan result, 2)
	for _, requestID := range []string{firstRequest.ID, secondRequest.ID} {
		go func(id string) { <-start; handle, err := claim(id); results <- result{handle, err} }(requestID)
	}
	close(start)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("concurrent balanced claims: %v / %v", first.err, second.err)
	}
	if first.handle.EndpointKeyID() == second.handle.EndpointKeyID() ||
		(first.handle.EndpointKeyID() != keys[0].keyID && first.handle.EndpointKeyID() != keys[1].keyID) {
		t.Fatalf("pending load did not split two first claims: %d / %d", first.handle.EndpointKeyID(), second.handle.EndpointKeyID())
	}
	receipts, dispatches, affinities := routingCounts(t, fixture)
	if receipts != 2 || dispatches != 0 || affinities != 0 {
		t.Fatalf("pending state = receipts %d dispatches %d affinities %d", receipts, dispatches, affinities)
	}
}

func TestCharityRoutingRestartKeepsDispatchedLoadAndAffinity(t *testing.T) {
	fixture, caller, keys, donations := routingFixture(t, 300)
	dispatched := routingClaim(t, fixture, caller, keys[0], donations[0])
	dispatchRoutingClaim(t, fixture, dispatched)
	routingClaim(t, fixture, caller, keys[1], donations[1])
	if receipts, dispatches, affinities := routingCounts(t, fixture); receipts != 2 || dispatches != 1 || affinities != 1 {
		t.Fatalf("before restart = receipts %d dispatches %d affinities %d", receipts, dispatches, affinities)
	}
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
		t.Fatalf("recover charity claims after restart: %v", err)
	}
	if receipts, dispatches, affinities := routingCounts(t, fixture); receipts != 1 || dispatches != 1 || affinities != 1 {
		t.Fatalf("after restart = receipts %d dispatches %d affinities %d", receipts, dispatches, affinities)
	}
	request := fixture.acceptCharity(caller, 1)
	selected, err := restarted.Claim(context.Background(), ClaimInput{
		RequestID: request.ID, ActorUserID: caller, AttemptSeq: 1,
		Purpose: PurposeCharity, BalancedCandidates: []BalancedCandidate{
			{Candidate: keys[1].candidate, DonationKeyID: donations[1]},
			{Candidate: keys[0].candidate, DonationKeyID: donations[0]},
		},
	})
	if err != nil || selected.EndpointKeyID() != keys[0].keyID {
		t.Fatalf("restart lost physical association: key %d error %v", selected.EndpointKeyID(), err)
	}
}

func TestCharityRoutingAffinityPrecedesLoadAndStillRequiresAdmission(t *testing.T) {
	fixture, caller, keys, donations := routingFixture(t, 300)
	first := routingClaim(t, fixture, caller, keys[0], donations[0])
	dispatchRoutingClaim(t, fixture, first)
	options := []BalancedCandidate{
		{Candidate: keys[1].candidate, DonationKeyID: donations[1]},
		{Candidate: keys[0].candidate, DonationKeyID: donations[0]},
	}
	request := fixture.acceptCharity(caller, 1)
	preferred, err := fixture.service.Claim(context.Background(), ClaimInput{
		RequestID: request.ID, ActorUserID: caller, AttemptSeq: 1,
		Purpose: PurposeCharity, BalancedCandidates: options,
	})
	if err != nil || preferred.EndpointKeyID() != keys[0].keyID {
		t.Fatalf("valid association did not win before load: key %d error %v", preferred.EndpointKeyID(), err)
	}
	if _, err := fixture.service.ReleaseUndispatched(context.Background(), preferred); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Exec(`UPDATE endpoint_keys SET enabled=0 WHERE id=?`, keys[0].keyID); err != nil {
		t.Fatal(err)
	}
	request = fixture.acceptCharity(caller, 1)
	fallback, err := fixture.service.Claim(context.Background(), ClaimInput{
		RequestID: request.ID, ActorUserID: caller, AttemptSeq: 1,
		Purpose: PurposeCharity, BalancedCandidates: options,
	})
	if err != nil || fallback.EndpointKeyID() != keys[1].keyID {
		t.Fatalf("disabled association bypassed admission: key %d error %v", fallback.EndpointKeyID(), err)
	}
}

func TestCharityRoutingRevisionInvalidatesSelectionWithoutResettingLoad(t *testing.T) {
	fixture, caller, keys, donations := routingFixture(t, 300)
	first := routingClaim(t, fixture, caller, keys[0], donations[0])
	dispatchRoutingClaim(t, fixture, first)
	if _, err := fixture.db.Exec(`UPDATE charity_routing_settings SET revision=revision+1 WHERE model_id=1`); err != nil {
		t.Fatal(err)
	}
	request := fixture.acceptCharity(caller, 1)
	selected, err := fixture.service.Claim(context.Background(), ClaimInput{
		RequestID: request.ID, ActorUserID: caller, AttemptSeq: 1,
		Purpose: PurposeCharity, BalancedCandidates: []BalancedCandidate{
			{Candidate: keys[0].candidate, DonationKeyID: donations[0]},
			{Candidate: keys[1].candidate, DonationKeyID: donations[1]},
		},
	})
	if err != nil || selected.EndpointKeyID() != keys[1].keyID {
		t.Fatalf("stale revision won over lower-load resource: key %d error %v", selected.EndpointKeyID(), err)
	}
}

func TestCharityRoutingSameSecondCancellationSplicesAssociation(t *testing.T) {
	for _, order := range [][]int{{0, 1}, {1, 0}, {0, 2, 1}, {1, 0, 2}, {2, 1, 0}} {
		t.Run(string(rune('a'+order[0]))+string(rune('a'+order[len(order)-1])), func(t *testing.T) {
			fixture, caller, keys, donations := routingFixture(t, 300)
			handles := make([]Handle, len(order))
			for index := range handles {
				handles[index] = routingClaim(t, fixture, caller, keys[index], donations[index])
				dispatchRoutingClaim(t, fixture, handles[index])
			}
			if got := routingAffinityAttempt(t, fixture); got != handles[len(handles)-1].ClaimID() {
				t.Fatalf("latest association = %s", got)
			}
			for _, index := range order {
				if err := fixture.service.RevokeUndelivered(context.Background(), handles[index]); err != nil {
					t.Fatalf("revoke attempt %d: %v", index, err)
				}
			}
			receipts, dispatches, affinities := routingCounts(t, fixture)
			if receipts != 0 || dispatches != 0 || affinities != 0 {
				t.Fatalf("canceled state = receipts %d dispatches %d affinities %d", receipts, dispatches, affinities)
			}
		})
	}
}

func TestCharityRoutingRestoresOlderAssociationBeyondLoadWindow(t *testing.T) {
	fixture, caller, keys, donations := routingFixture(t, 86_400)
	first := routingClaim(t, fixture, caller, keys[0], donations[0])
	dispatchRoutingClaim(t, fixture, first)
	if _, err := fixture.service.CompleteAttempt(context.Background(), first, AttemptOutcome{
		Kind: ResultResponse, UpstreamStatus: 503, ProtocolSuccess: false,
		Usage: connectorcontract.Usage{Present: true},
	}); err != nil {
		t.Fatalf("complete failed upstream HTTP: %v", err)
	}
	fixture.clock.Store(1_400)
	tx, err := fixture.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanupRoutingTx(context.Background(), tx, fixture.clock.Load()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	receipts, dispatches, affinities := routingCounts(t, fixture)
	if receipts != 0 || dispatches != 0 || affinities != 1 {
		t.Fatalf("older retained affinity = receipts %d dispatches %d affinities %d", receipts, dispatches, affinities)
	}
	second := routingClaim(t, fixture, caller, keys[1], donations[1])
	dispatchRoutingClaim(t, fixture, second)
	if err := fixture.service.RevokeUndelivered(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	if got := routingAffinityAttempt(t, fixture); got != first.ClaimID() {
		t.Fatalf("restored association = %s, want %s", got, first.ClaimID())
	}
}

func TestCharityRoutingRevisionAndKeyDeletionInvalidateRollback(t *testing.T) {
	for _, change := range []string{"revision", "key_deleted"} {
		t.Run(change, func(t *testing.T) {
			fixture, caller, keys, donations := routingFixture(t, 300)
			first := routingClaim(t, fixture, caller, keys[0], donations[0])
			dispatchRoutingClaim(t, fixture, first)
			second := routingClaim(t, fixture, caller, keys[1], donations[1])
			dispatchRoutingClaim(t, fixture, second)
			var err error
			if change == "revision" {
				_, err = fixture.db.Exec(`UPDATE charity_routing_settings SET revision=revision+1 WHERE model_id=1`)
			} else {
				_, err = fixture.db.Exec(`UPDATE donation_keys
SET enabled=0,ended_at=?,ended_reason='withdrawn',report_match_until=?,updated_at=?
WHERE endpoint_key_id=?`, fixture.clock.Load(), fixture.clock.Load()+7_776_000,
					fixture.clock.Load(), keys[0].keyID)
				if err != nil {
					t.Fatal(err)
				}
				_, err = fixture.db.Exec(`DELETE FROM endpoint_keys WHERE id=?`, keys[0].keyID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := fixture.service.RevokeUndelivered(context.Background(), second); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := fixture.db.QueryRow(`SELECT count(*) FROM charity_key_affinities`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("invalid prior association remained: count %d err %v", count, err)
			}
		})
	}
}

func TestCharityRoutingPendingReceiptSurvivesLoadWindowAndCapacityFailsClosed(t *testing.T) {
	fixture, caller, keys, donations := routingFixture(t, 300)
	handle := routingClaim(t, fixture, caller, keys[0], donations[0])
	fixture.clock.Store(1_400)
	tx, err := fixture.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanupRoutingTx(context.Background(), tx, fixture.clock.Load()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	receipts, dispatches, affinities := routingCounts(t, fixture)
	if receipts != 1 || dispatches != 0 || affinities != 0 {
		t.Fatalf("old pending receipt was removed: %d/%d/%d", receipts, dispatches, affinities)
	}
	owner, err := NewCharityRoutingLifecycle(fixture.db)
	if err != nil {
		t.Fatal(err)
	}
	work, err := owner.Retain(context.Background(), fixture.clock.Load(), 1, time.Time{})
	if err != nil || work.Processed != 0 || work.More {
		t.Fatalf("active pending receipt caused cleanup continuation: %+v %v", work, err)
	}
	if _, err := fixture.db.Exec(`UPDATE charity_routing_capacity SET affinities=200000 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.TakeForDispatch(context.Background(), handle); !errors.Is(err, ErrRoutingBusy) {
		t.Fatalf("full association capacity = %v", err)
	}
	var state string
	if err := fixture.db.QueryRow(`SELECT state FROM dispatch_claims WHERE id=?`, handle.ClaimID()).Scan(&state); err != nil || state != "claimed" {
		t.Fatalf("capacity failure crossed dispatch marker: %q %v", state, err)
	}
}

func TestCharityRoutingExistingAssociationSurvivesUnrelatedFullCapacity(t *testing.T) {
	fixture, caller, keys, donations := routingFixture(t, 300)
	first := routingClaim(t, fixture, caller, keys[0], donations[0])
	dispatchRoutingClaim(t, fixture, first)
	if _, err := fixture.db.Exec(`UPDATE charity_routing_capacity SET affinities=200000 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	second := routingClaim(t, fixture, caller, keys[0], donations[0])
	dispatchRoutingClaim(t, fixture, second)
	if got := routingAffinityAttempt(t, fixture); got != second.ClaimID() {
		t.Fatalf("existing association was not refreshed at capacity: %s", got)
	}
}

func TestCharityRoutingActualFailedHTTPRemainsCounted(t *testing.T) {
	fixture, caller, keys, donations := routingFixture(t, 300)
	handle := routingClaim(t, fixture, caller, keys[0], donations[0])
	dispatchRoutingClaim(t, fixture, handle)
	if _, err := fixture.service.CompleteAttempt(context.Background(), handle, AttemptOutcome{
		Kind: ResultResponse, UpstreamStatus: 500, ProtocolSuccess: false,
		Usage: connectorcontract.Usage{Present: true},
	}); err != nil {
		t.Fatal(err)
	}
	receipts, dispatches, affinities := routingCounts(t, fixture)
	if receipts != 1 || dispatches != 1 || affinities != 1 {
		t.Fatalf("failed HTTP was rolled back: %d/%d/%d", receipts, dispatches, affinities)
	}
}

func TestCharityRoutingCountsAllStrategiesWithoutCreatingUnrequestedAffinity(t *testing.T) {
	for _, strategy := range []string{"ordered", "random", "expiry_weighted"} {
		t.Run(strategy, func(t *testing.T) {
			fixture, caller, keys, donations := routingFixture(t, 300)
			if _, err := fixture.db.Exec(`UPDATE charity_model_routing SET strategy=? WHERE model_id=1`, strategy); err != nil {
				t.Fatal(err)
			}
			handle := routingClaim(t, fixture, caller, keys[0], donations[0])
			dispatchRoutingClaim(t, fixture, handle)
			receipts, dispatches, affinities := routingCounts(t, fixture)
			if receipts != 1 || dispatches != 1 || affinities != 0 {
				t.Fatalf("strategy %s routing state = %d/%d/%d", strategy, receipts, dispatches, affinities)
			}
		})
	}
}

func TestCharityRoutingPhysicalLoadIsSharedAcrossModelsButNotPersonalCalls(t *testing.T) {
	fixture, caller, keys, donations := routingFixture(t, 300)
	first := routingClaim(t, fixture, caller, keys[0], donations[0])
	dispatchRoutingClaim(t, fixture, first)
	if _, err := fixture.db.Exec(`INSERT INTO charity_models(id,provider,model,full_name,enabled,pricing_mode,created_at,updated_at)
VALUES(2,'second','model','[公益]second/model',1,'per_request',1000,1000);
INSERT INTO charity_model_routing(model_id,strategy) VALUES(2,'cache_balanced');
INSERT INTO charity_routing_settings(model_id,revision,affinity_ttl_seconds) VALUES(2,1,300)`); err != nil {
		t.Fatal(err)
	}
	decisionNow := fixture.clock.Load()
	request, err := fixture.service.Accept(context.Background(), AcceptInput{
		UserID: caller, Route: RouteCharityChat, ModelSnapshot: "[公益]second/model",
		AttemptLimit: 1, ReservedMilli: 200, CharityModelID: 2, CharityDecisionNow: &decisionNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.service.Claim(context.Background(), ClaimInput{
		RequestID: request.ID, ActorUserID: caller, AttemptSeq: 1,
		Purpose: PurposeCharity, Candidate: keys[0].candidate, DonationKeyID: donations[0],
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatchRoutingClaim(t, fixture, second)
	receipts, dispatches, affinities := routingCounts(t, fixture)
	if receipts != 2 || dispatches != 2 || affinities != 2 {
		t.Fatalf("shared physical load = %d/%d/%d", receipts, dispatches, affinities)
	}
	var bucketRows int
	if err := fixture.db.QueryRow(`SELECT count(*) FROM charity_dispatch_buckets`).Scan(&bucketRows); err != nil || bucketRows != 1 {
		t.Fatalf("same key and UTC second split buckets: rows %d error %v", bucketRows, err)
	}
	var donor int64
	if err := fixture.db.QueryRow(`SELECT user_id FROM endpoints WHERE id=?`, keys[0].endpointID).Scan(&donor); err != nil {
		t.Fatal(err)
	}
	selfRequest := fixture.acceptSelf(donor, 1)
	self, err := fixture.service.Claim(context.Background(), ClaimInput{
		RequestID: selfRequest.ID, ActorUserID: donor, AttemptSeq: 1,
		Purpose: PurposeSelf, Candidate: keys[0].candidate,
	})
	if err != nil {
		t.Fatal(err)
	}
	selfGrant, err := fixture.service.TakeForDispatch(context.Background(), self)
	if err != nil {
		t.Fatal(err)
	}
	selfGrant.Clear()
	_, afterSelf, _ := routingCounts(t, fixture)
	if afterSelf != 2 {
		t.Fatalf("personal dispatch changed charity load to %d", afterSelf)
	}
}

func TestCharityRoutingLifecycleDeletionKeepsPhysicalCountUntilWindowEnds(t *testing.T) {
	fixture, caller, keys, donations := routingFixture(t, 300)
	handle := routingClaim(t, fixture, caller, keys[0], donations[0])
	dispatchRoutingClaim(t, fixture, handle)
	if _, err := fixture.service.CompleteAttempt(context.Background(), handle, AttemptOutcome{
		Kind: ResultResponse, UpstreamStatus: 500, ProtocolSuccess: false,
		Usage: connectorcontract.Usage{Present: true},
	}); err != nil {
		t.Fatal(err)
	}
	owner, err := NewCharityRoutingLifecycle(fixture.db)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := fixture.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.PrepareDelete(context.Background(), tx, lifecycle.DeleteRequest{UserID: caller, DecisionNow: fixture.clock.Load()}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	receipts, dispatches, affinities := routingCounts(t, fixture)
	if receipts != 1 || dispatches != 1 || affinities != 0 {
		t.Fatalf("deletion removed physical load or left affinity: %d/%d/%d", receipts, dispatches, affinities)
	}
	var receiptUser sql.NullInt64
	if err := fixture.db.QueryRow(`SELECT user_id FROM charity_dispatch_receipts WHERE attempt_id=?`, handle.ClaimID()).Scan(&receiptUser); err != nil || receiptUser.Valid {
		t.Fatalf("deletion retained receipt user: %+v %v", receiptUser, err)
	}
	fixture.clock.Store(1_301)
	work, err := owner.Retain(context.Background(), fixture.clock.Load(), 1, time.Time{})
	if err != nil || work.Processed != 1 || !work.More {
		t.Fatalf("first bounded routing retention = %+v %v", work, err)
	}
	work, err = owner.Retain(context.Background(), fixture.clock.Load(), 1, time.Time{})
	if err != nil || work.Processed != 1 || work.More {
		t.Fatalf("second bounded routing retention = %+v %v", work, err)
	}
	receipts, dispatches, affinities = routingCounts(t, fixture)
	if receipts != 0 || dispatches != 0 || affinities != 0 {
		t.Fatalf("expired routing state = %d/%d/%d", receipts, dispatches, affinities)
	}
}
