package charity

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type forceReviewAuthority struct{}

func (forceReviewAuthority) AuthorizeAdminMutation(ctx context.Context, tx *sql.Tx, id int64) error {
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT is_admin FROM users WHERE id=?`, id).Scan(&valid)
	if err != nil || !valid {
		return donation.ErrForbidden
	}
	return nil
}
func (forceReviewAuthority) AuthorizeStewardMutation(context.Context, *sql.Tx, int64) error {
	return donation.ErrForbidden
}

func prepareForceReview(t *testing.T, e *charityTestEnv) func() error {
	t.Helper()
	ctx := context.Background()
	vault, err := secret.New(bytes.Repeat([]byte{0x43}, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { vault.Close() })
	review, err := secret.NewDonationReview(vault)
	if err != nil {
		t.Fatal(err)
	}
	if err = review.Initialize(ctx, e.store.DB()); err != nil {
		t.Fatal(err)
	}
	tx := beginTestTx(t, e.store.DB())
	if _, err = review.CaptureDonationKey(ctx, tx, e.donationKey, e.endpointKey, time.Unix(charityTestNow, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE donations SET first_approval_origin='auto' WHERE id=?`, e.donationID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO donation_handling(donation_id,state,revision,created_at,updated_at) VALUES(?,'pending',1,?,?)`, e.donationID, charityTestNow, charityTestNow); err != nil {
		t.Fatal(err)
	}
	commitTestTx(t, tx)
	if _, err = e.store.DB().Exec(`INSERT INTO users(discord_id,username,is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(NULL,'review manager',1,zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),?,?)`, charityTestNow, charityTestNow); err != nil {
		t.Fatal(err)
	}
	service, err := donation.New(donation.Config{Store: e.store, Review: review, RoleAuth: forceReviewAuthority{}, Now: func() time.Time { return time.Unix(charityTestNow+2, 0) }})
	if err != nil {
		t.Fatal(err)
	}
	input := donation.ReviewInput{Decision: "force_reject", ExpectedRevision: 1, Reason: "original approval requires review"}
	body, err := idempotency.CanonicalJSON(map[string]any{"decision": input.Decision, "expected_revision": "1", "reason": input.Reason})
	if err != nil {
		t.Fatal(err)
	}
	mutation := resources.ControlMutation{IdempotencyKey: strings.Repeat("R", 22), Method: http.MethodPost, Route: "/admin/api/donations/{id}/review", PathIDs: []string{fmt.Sprint(e.donationID)}, CanonicalBody: body}
	return func() error { _, err := service.ReviewAdmin(ctx, mutation, e.donationID, input); return err }
}

func TestForceRejectDispatchLinearizationAndSentSettlement(t *testing.T) {
	e := newCharityTestEnv(t)
	force := prepareForceReview(t, e)
	request := e.accept(t, e.requestModel, 2400, 1)
	claimed := e.claim(t, request, 1, false)
	if _, err := e.store.DB().Exec(`INSERT INTO request_logs(logical_request_id,user_id,endpoint_key_id,upstream_model_id,route_kind,started_at) VALUES(?,?,?,'upstream-model','charity_chat_completions',?)`, request, e.callerID, e.endpointKey, charityTestNow); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	reviewResult := make(chan error, 1)
	dispatchResult := make(chan error, 1)
	go func() { <-start; reviewResult <- force() }()
	go func() {
		<-start
		ctx := context.Background()
		tx, err := e.store.DB().BeginTx(ctx, nil)
		if err != nil {
			dispatchResult <- err
			return
		}
		defer tx.Rollback()
		err = e.service.PrepareDispatch(ctx, tx, claim.CharityDispatch{RequestID: request, ClaimID: claimed.id, ActorUserID: e.callerID, DispatchedAt: charityTestNow + 2})
		if err == nil {
			_, err = tx.Exec(`UPDATE dispatch_claims SET state='dispatched',dispatched_at=? WHERE id=? AND state='claimed'`, charityTestNow+2, claimed.id)
		}
		if err == nil {
			err = tx.Commit()
		}
		dispatchResult <- err
	}()
	close(start)
	if err := <-reviewResult; err != nil {
		t.Fatal(err)
	}
	dispatchErr := <-dispatchResult
	if dispatchErr != nil && !errors.Is(dispatchErr, claim.ErrNotFound) {
		t.Fatalf("dispatch result: %v", dispatchErr)
	}
	var state string
	if err := e.store.DB().QueryRow(`SELECT state FROM dispatch_claims WHERE id=?`, claimed.id).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if dispatchErr == nil {
		if state != "dispatched" {
			t.Fatal("successful dispatch was not committed")
		}
		e.completeAttempt(t, request, claimed, claim.CharityAttemptInput{ReceiverUserID: &e.donorID, Usage: connectorcontract.Usage{Present: true}, ProtocolSuccess: true, ResponseStarted: true, CompletedAt: charityTestNow + 10}, claim.RewardPosted, claim.CharityActual{PriceMilli: 3000, RewardMilli: 1250})
	} else if state != "claimed" {
		t.Fatal("rejected dispatch changed claim state")
	}
	tx := beginTestTx(t, e.store.DB())
	err := e.service.PrepareDispatch(context.Background(), tx, claim.CharityDispatch{RequestID: request, ClaimID: claimed.id, ActorUserID: e.callerID, DispatchedAt: charityTestNow + 11})
	tx.Rollback()
	if !errors.Is(err, claim.ErrNotFound) {
		t.Fatalf("dispatch after rejection: %v", err)
	}
}

func TestForceRejectAlreadyDispatchedSettlesNormally(t *testing.T) {
	e := newCharityTestEnv(t)
	force := prepareForceReview(t, e)
	request := e.accept(t, e.requestModel, 2400, 1)
	claimed := e.claim(t, request, 1, true)
	if err := force(); err != nil {
		t.Fatal(err)
	}
	e.completeAttempt(t, request, claimed, claim.CharityAttemptInput{ReceiverUserID: &e.donorID, Usage: connectorcontract.Usage{Present: true}, ProtocolSuccess: true, ResponseStarted: true, CompletedAt: charityTestNow + 10}, claim.RewardPosted, claim.CharityActual{PriceMilli: 3000, RewardMilli: 1250})
	charge, err := e.service.CalculateRequestCharge(context.Background(), request, claim.AccountingCommit)
	if err != nil || charge != 2400 {
		t.Fatalf("sent charge %d %v", charge, err)
	}
}
