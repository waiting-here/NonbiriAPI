package claim

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/headerfacts"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

func TestHeaderCompletionBatchUsesEachAttemptContextAndRecoveryRecordsNothing(t *testing.T) {
	f := newClaimFixture(t)
	owner := f.seedUser("headers", false)
	key := f.seedKey(owner, "headers")
	const count = 4
	handles := make([]Handle, count)
	contexts := make([]context.Context, count)
	ids := make([]string, count)
	for i := range handles {
		input := selfLimitInput(f, owner, key)
		ids[i] = input.RequestID
		handle, err := f.service.Claim(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		dispatchLimitClaim(t, f, handle)
		handles[i] = handle
		ctx, cancel := context.WithCancel(headerfacts.WithResponse(context.Background(), input.RequestID, 1))
		headerfacts.CaptureResponse(ctx, &http.Response{StatusCode: 200, Header: http.Header{"X-Request": {fmt.Sprint(i)}, "X-Page": {"first"}}})
		headerfacts.CaptureResponse(ctx, &http.Response{StatusCode: 503, Header: http.Header{"X-Request": {fmt.Sprint(i)}, "X-Page": {"second"}}})
		cancel()
		contexts[i] = context.WithoutCancel(ctx)
	}
	blocker, err := f.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	done := make(chan error, count)
	outcome := AttemptOutcome{Kind: ResultResponse, UpstreamStatus: 503, StreakDisposition: contract.StreakUpstreamFailure, FailureOrigin: contract.OriginUpstreamResponse}
	for i := range handles {
		go func() { _, err := f.service.CompleteAttempt(contexts[i], handles[i], outcome); done <- err }()
	}
	awaitCompletionCondition(t, func() bool { return pendingCompletions(f.service) >= count-1 })
	blocker.Close()
	for range count {
		if err := awaitCompletionResult(t, done); err != nil {
			t.Fatal(err)
		}
	}
	for i, id := range ids {
		var data []byte
		var state string
		if err := f.db.QueryRow("SELECT h.save_state,h.encoded FROM request_header_facts h JOIN request_logs l ON l.id=h.request_log_id WHERE l.logical_request_id=? AND h.attempt_seq=1", id).Scan(&state, &data); err != nil {
			t.Fatal(err)
		}
		view, err := headerfacts.DecodeCollection(state, data, false)
		if err != nil || len(view.Responses) != 2 {
			t.Fatal(view, err)
		}
		for _, response := range view.Responses {
			found := false
			for _, entry := range response.Headers {
				if entry.Name == "X-Request" {
					found = entry.Value == fmt.Sprint(i)
				}
			}
			if !found {
				t.Fatal("batch context crossed requests", i, view)
			}
		}
	}
	var before, after int64
	f.db.QueryRow("SELECT header_bytes FROM observability_state WHERE id=1").Scan(&before)
	if _, err := f.service.CompleteAttempt(contexts[0], handles[0], outcome); err != nil {
		t.Fatal(err)
	}
	f.db.QueryRow("SELECT header_bytes FROM observability_state WHERE id=1").Scan(&after)
	if before != after {
		t.Fatal("replay changed header count")
	}
	input := selfLimitInput(f, owner, key)
	handle, err := f.service.Claim(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	dispatchLimitClaim(t, f, handle)
	// A crash after headers but before completion has no saved diagnostic.
	ctx := headerfacts.WithResponse(context.Background(), input.RequestID, 1)
	headerfacts.CaptureResponse(ctx, &http.Response{StatusCode: 200, Header: http.Header{"X-Lost": {"in-memory-only"}}})
	if _, err := f.service.RecoverNonterminal(context.Background(), 20); err != nil {
		t.Fatal(err)
	}
	var saved int
	if err := f.db.QueryRow("SELECT count(*) FROM request_header_facts h JOIN request_logs l ON l.id=h.request_log_id WHERE l.logical_request_id=?", input.RequestID).Scan(&saved); err != nil || saved != 0 {
		t.Fatal("recovery manufactured header capture", saved, err)
	}
}

func BenchmarkHeaderFactTransaction(b *testing.B) {
	f := newClaimFixture(b)
	owner := f.seedUser("benchmark", false)
	request := f.acceptSelf(owner, 1)
	incoming := httptest.NewRequest("POST", "http://example.invalid", nil)
	incoming.Header = http.Header{"Authorization": {"private"}, "User-Agent": {"ordinary-client/1.0"}, "X-Trace": {"first", "second"}}
	captured := headerfacts.WithRequest(context.Background(), incoming)
	for _, save := range []bool{false, true} {
		b.Run(fmt.Sprint(save), func(b *testing.B) {
			ctx := context.Background()
			if save {
				ctx = captured
			}
			b.ReportAllocs()
			for b.Loop() {
				tx, err := f.db.BeginTx(ctx, nil)
				if err != nil {
					b.Fatal(err)
				}
				if _, err = tx.ExecContext(ctx, "UPDATE request_logs SET duration_ms=duration_ms WHERE logical_request_id=?", request.ID); err != nil {
					b.Fatal(err)
				}
				if err = observability.RecordRequestHeadersTx(ctx, tx, request.ID); err != nil {
					b.Fatal(err)
				}
				if err = tx.Rollback(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
