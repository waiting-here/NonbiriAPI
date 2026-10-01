package claim

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/credits"
)

// The test charity adapter applies the production four-bucket price formula to
// each committed attempt. The claim service still uses its real SQLite ledger.
type mismatchPricedCharity struct {
	*testCharity
	prices credits.TokenPrices
}

func (c *mismatchPricedCharity) price(usage connectorcontract.Usage) (int64, error) {
	if !usage.Present {
		return 0, nil
	}
	return credits.PriceTokenUsage(credits.TokenUsage{
		UncachedInput: usage.UncachedInputTokens, CacheWriteInput: usage.CacheWriteInputTokens,
		CacheReadInput: usage.CacheReadInputTokens, Output: usage.OutputTokens,
	}, c.prices)
}

func (c *mismatchPricedCharity) PrepareAttempt(_ context.Context, _ *sql.Tx, input CharityAttemptInput) (CharityActual, error) {
	price, err := c.price(input.Usage)
	return CharityActual{PriceMilli: price}, err
}

func (c *mismatchPricedCharity) RequestCharge(_ context.Context, _ *sql.Tx, requestID string, disposition AccountingDisposition) (int64, error) {
	if disposition == AccountingRelease {
		return 0, nil
	}
	c.mu.Lock()
	attempts := append([]CharityAttemptInput(nil), c.attempts...)
	c.mu.Unlock()
	var total int64
	for _, attempt := range attempts {
		if attempt.RequestID != requestID {
			continue
		}
		price, err := c.price(attempt.Usage)
		if err != nil {
			return 0, err
		}
		total += price
	}
	return total, nil
}

func TestMismatchPersistsAcrossRetriesAndBillsActualBucketsOnce(t *testing.T) {
	f := newClaimFixture(t)
	consumerID := f.seedUser("usage-mismatch-consumer", false)
	donorID := f.seedUser("usage-mismatch-donor", false)
	f.seedLedgerUser(consumerID, 1_000)
	f.seedLedgerUser(donorID, 0)
	key := f.seedKey(donorID, "usage-mismatch")
	donationKeyID := f.seedDonationKey(donorID, key, "usage-mismatch", 0)
	priced := &mismatchPricedCharity{
		testCharity: f.charity,
		prices:      credits.TokenPrices{UncachedInput: 1_000_000, Output: 1_000_000},
	}
	service, err := New(Dependencies{
		DB: f.db, Secrets: f.codec, Accounting: NewLedgerAccounting(), Charity: priced,
		Acceptance: allowAcceptanceGate{},
		Now:        func() time.Time { return time.Unix(f.clock.Load(), 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	f.service = service
	request := f.acceptCharity(consumerID, 2)
	ctx := context.Background()

	complete := func(seq int, outcome AttemptOutcome) Attempt {
		t.Helper()
		f.clock.Add(1)
		handle, err := f.service.Claim(ctx, ClaimInput{
			RequestID: request.ID, ActorUserID: consumerID, AttemptSeq: seq,
			Purpose: PurposeCharity, Candidate: key.candidate, DonationKeyID: donationKeyID,
		})
		if err != nil {
			t.Fatalf("claim attempt %d: %v", seq, err)
		}
		dispatch, err := f.service.TakeForDispatch(ctx, handle)
		if err != nil {
			t.Fatalf("dispatch attempt %d: %v", seq, err)
		}
		dispatch.Clear()
		f.clock.Add(1)
		attempt, err := f.service.CompleteAttempt(ctx, handle, outcome)
		if err != nil {
			t.Fatalf("complete attempt %d: %v", seq, err)
		}
		// A retry with contradictory usage cannot replace the immutable attempt or
		// create an additional ledger operation.
		replayed, err := f.service.CompleteAttempt(ctx, handle, AttemptOutcome{
			Kind: ResultSynthetic, UpstreamStatus: 502, Usage: connectorcontract.Usage{},
		})
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("idempotent attempt %d = %+v, %v; original %+v", seq, replayed, err, attempt)
		}
		if same, err := f.service.CompleteAttempt(ctx, handle, outcome); err != nil || same.Usage != attempt.Usage {
			t.Fatalf("same replay: %+v %v", same, err)
		}
		return attempt
	}

	first := complete(1, AttemptOutcome{
		Kind: ResultResponse, UpstreamStatus: 503, ResponseStarted: true,
		Usage: connectorcontract.Usage{
			Present: true, TotalMismatch: true, UncachedInputTokens: 2, OutputTokens: 3,
		},
	})
	if !first.Usage.Present || !first.Usage.TotalMismatch || first.Usage.UncachedInputTokens != 2 || first.Usage.OutputTokens != 3 {
		t.Fatalf("first attempt=%+v", first)
	}
	second := complete(2, AttemptOutcome{
		Kind: ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true,
		Usage: connectorcontract.Usage{TotalMismatch: true}, // prior stream comparison, then malformed component
	})
	if second.Usage.Present || !second.Usage.TotalMismatch {
		t.Fatalf("unknown final attempt lost independent marker: %+v", second)
	}

	charge, err := priced.RequestCharge(ctx, nil, request.ID, AccountingCommit)
	if err != nil || charge != 5 {
		t.Fatalf("validated bucket charge = %d, %v", charge, err)
	}
	if _, err := f.service.CompleteRequest(ctx, CompleteRequestInput{
		RequestID: request.ID, Caller: CallerResult{Class: ResultSuccess, Status: 200},
		Disposition: AccountingCommit, ActualChargeMilli: charge,
	}); err != nil {
		t.Fatalf("settle real ledger: %v", err)
	}
	f.requireLedgerBalanceForUser(consumerID, "995")
	f.requireLedgerBalanceForCode(platformAccountCode, "5")
	f.validateRealLedger()
	var mismatch, unknown, attempts int
	var uncached, output int64
	if err := f.db.QueryRow(`SELECT usage_total_mismatch,usage_unknown,attempt_count,uncached_input_tokens,output_tokens
FROM request_logs WHERE logical_request_id=?`, request.ID).Scan(&mismatch, &unknown, &attempts, &uncached, &output); err != nil {
		t.Fatal(err)
	}
	if mismatch != 1 || unknown != 1 || attempts != 2 || uncached != 2 || output != 3 {
		t.Fatalf("aggregate mismatch=%d unknown=%d attempts=%d buckets=%d,%d", mismatch, unknown, attempts, uncached, output)
	}
	var mismatchedAttempts int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM request_attempts a
JOIN request_logs l ON l.id=a.request_log_id
WHERE l.logical_request_id=? AND a.usage_total_mismatch=1`, request.ID).Scan(&mismatchedAttempts); err != nil {
		t.Fatal(err)
	}
	if mismatchedAttempts != 2 {
		t.Fatalf("persisted mismatched attempts = %d", mismatchedAttempts)
	}
}
