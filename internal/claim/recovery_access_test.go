package claim

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRequestRecoveryUsesStateIndexAndPreservesOrder(t *testing.T) {
	fixture := newClaimFixture(t)
	seedRecoveryTerminalHistory(t, fixture, 1_000)
	for _, query := range []string{requestRecoveryCandidateSQL, claimRecoveryPendingSQL} {
		rows, err := fixture.db.Query("EXPLAIN QUERY PLAN " + query)
		if err != nil {
			t.Fatal(err)
		}
		var details []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			details = append(details, detail)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
		plan := strings.Join(details, "\n")
		if !strings.Contains(plan, "USING COVERING INDEX idx_logical_requests_state (state=?)") ||
			strings.Contains(plan, "SCAN r") || strings.Contains(plan, "SCAN logical_requests") {
			t.Fatalf("recovery scans terminal history:\n%s", plan)
		}
	}

	ctx := context.Background()
	userID := fixture.seedUser("recovery-order", false)
	key := fixture.seedKey(userID, "recovery-order")
	key.candidate.UpstreamModelID = ""
	// The oldest request has an active claim and cannot be finalized yet.
	blocked, _, err := fixture.service.ClaimDiscovery(ctx, DiscoveryClaimInput{ActorUserID: userID, Candidate: key.candidate})
	if err != nil {
		t.Fatal(err)
	}
	fixture.clock.Store(2_000)
	running, handle, err := fixture.service.ClaimDiscovery(ctx, DiscoveryClaimInput{ActorUserID: userID, Candidate: key.candidate})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.ReleaseUndispatched(ctx, handle); err != nil {
		t.Fatal(err)
	}
	fixture.clock.Store(3_000)
	accepted := fixture.acceptSelf(userID, 1)
	for _, want := range []string{running.ID, accepted.ID} {
		var selected string
		if err := fixture.db.QueryRow(requestRecoveryCandidateSQL).Scan(&selected); err != nil || selected != want {
			t.Fatalf("recovery order = %q, %v; want %q", selected, err, want)
		}
		if completed, err := fixture.service.recoverOneRequestAt(ctx, 4_000); err != nil || !completed {
			t.Fatalf("recover selected request = %v, %v", completed, err)
		}
	}
	if completed, err := fixture.service.recoverOneRequestAt(ctx, 4_000); !errors.Is(err, sql.ErrNoRows) || completed {
		t.Fatalf("active claim exclusion = %v, %v", completed, err)
	}
	var blockedState string
	if err := fixture.db.QueryRow(`SELECT state FROM logical_requests WHERE id=?`, blocked.ID).Scan(&blockedState); err != nil || blockedState != "running" {
		t.Fatalf("active request state = %q, %v", blockedState, err)
	}
	if more, err := fixture.service.hasClaimRecoveryWork(ctx); err != nil || !more {
		t.Fatalf("active backlog = %v, %v", more, err)
	}
	report, err := fixture.service.RecoverNonterminalAt(ctx, 4_000, MaxRecoveryBatch, time.Time{})
	if err != nil || report.ReleasedClaims != 1 || report.CompletedRequests != 1 || report.More {
		t.Fatalf("remaining recovery = %+v, %v", report, err)
	}
}

func BenchmarkRecoveryTerminalHistory(b *testing.B) {
	for _, history := range []int{0, 100_000} {
		b.Run(fmt.Sprintf("terminal_%d", history), func(b *testing.B) {
			fixture := newClaimFixture(b)
			seedRecoveryTerminalHistory(b, fixture, history)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				report, err := fixture.service.RecoverNonterminalAt(context.Background(), 2_000, MaxRecoveryBatch, time.Time{})
				if err != nil || report != (RecoveryReport{}) {
					b.Fatalf("empty recovery = %+v, %v", report, err)
				}
			}
		})
	}
}

func seedRecoveryTerminalHistory(t testing.TB, fixture *claimFixture, count int) {
	t.Helper()
	if count == 0 {
		return
	}
	userID := fixture.seedUser("recovery-history", false)
	_, err := fixture.db.Exec(`WITH RECURSIVE history(n) AS (
VALUES(1) UNION ALL SELECT n+1 FROM history WHERE n<?
) INSERT INTO logical_requests(id,user_id,route_kind,state,attempt_limit,caller_result_class,
caller_status,accounting_state,settlement_destination,ledger_rows_remaining,created_at,terminal_at)
SELECT printf('req_%021dA',n),?,'model_discovery','terminal',1,'success',202,'none','user',zeroblob(16),0,0
FROM history`, count, userID)
	if err != nil {
		t.Fatalf("seed terminal recovery history: %v", err)
	}
}
