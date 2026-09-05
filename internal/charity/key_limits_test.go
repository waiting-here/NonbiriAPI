package charity

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func TestKeyLimitRejectionRollsBackCharityReservationAndFailureSequence(t *testing.T) {
	e := newCharityTestEnv(t)
	ctx := context.Background()
	vault, err := secret.New(bytes.Repeat([]byte{0x43}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	rail, err := claim.New(claim.Dependencies{DB: e.store.DB(), Secrets: vault, Charity: e.service, Now: func() time.Time { return time.Unix(charityTestNow, 0) }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DB().Exec(`INSERT INTO endpoint_key_limits(endpoint_key_id,max_concurrency,max_rpm) VALUES(?,1,1)`, e.endpointKey); err != nil {
		t.Fatal(err)
	}
	request1 := e.accept(t, e.requestModel, 2400, 1)
	request2 := e.accept(t, e.requestModel, 2400, 1)
	input := claim.ClaimInput{RequestID: request1, ActorUserID: e.callerID, AttemptSeq: 1, Purpose: claim.PurposeCharity, DonationKeyID: e.donationKey, Candidate: claim.Candidate{EndpointID: e.endpointID, EndpointKeyID: e.endpointKey, ConnectorType: connectorcontract.TypeOpenAICompatible, CanonicalBaseURL: "https://charity.example.test/v1", UpstreamModelID: "upstream-model"}}
	if _, err := rail.Claim(ctx, input); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		t.Helper()
		var value string
		if err := e.store.DB().QueryRow(`SELECT hex(price_reserved_mag)||':'||hex(calls_reserved)||':'||hex(tokens_reserved)||':'||hex(next_claim_seq)||':'||hex(next_fold_seq)||':'||hex(failure_streak) FROM donation_keys WHERE id=?`, e.donationKey).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := snapshot()
	input.RequestID = request2
	if _, err := rail.Claim(ctx, input); !errors.Is(err, claim.ErrKeyRateLimited) {
		t.Fatalf("limit rejection=%v", err)
	}
	if after := snapshot(); after != before {
		t.Fatalf("rejected route changed charity reservation or failure counters: %s -> %s", before, after)
	}
	var count int
	if err := e.store.DB().QueryRow(`SELECT COUNT(*) FROM dispatch_claims WHERE logical_request_id=?`, request2).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected claim persisted=%d %v", count, err)
	}
	if err := e.store.DB().QueryRow(`SELECT COUNT(*) FROM donation_usage_reservations`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("reservation leaked=%d %v", count, err)
	}
}
