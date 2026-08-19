package db

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func seedUsageUser(t *testing.T, store *Store, discordID string) int64 {
	t.Helper()
	now := time.Now().Unix()
	res, err := store.DB().Exec(`INSERT INTO users (discord_id, username, created_at, updated_at) VALUES (?, ?, ?, ?)`, discordID, discordID, now, now)
	if err != nil {
		t.Fatalf("seed user %s: %v", discordID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed user %s last id: %v", discordID, err)
	}
	return id
}

func seedUsageAdmin(t *testing.T, store *Store) int64 {
	t.Helper()
	now := time.Now().Unix()
	res, err := store.DB().Exec(`INSERT INTO users (discord_id, username, is_admin, created_at, updated_at) VALUES (NULL, 'admin', 1, ?, ?)`, now, now)
	if err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed admin last id: %v", err)
	}
	return id
}

func seedUsageKey(t *testing.T, store *Store, userID int64) int64 {
	t.Helper()
	now := time.Now().Unix()
	if _, err := store.DB().Exec(`INSERT INTO endpoints (user_id, connector_type, base_url, created_at, updated_at) VALUES (?, 'openai-compatible', 'https://upstream.example', ?, ?)`, userID, now, now); err != nil {
		t.Fatalf("seed endpoint: %v", err)
	}
	res, err := store.DB().Exec(`INSERT INTO endpoint_keys (endpoint_id, encrypted_secret, created_at, updated_at) VALUES ((SELECT id FROM endpoints WHERE user_id=? LIMIT 1), 'nbsec:v1:test', ?, ?)`, userID, now, now)
	if err != nil {
		t.Fatalf("seed endpoint key: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed endpoint key last id: %v", err)
	}
	return id
}

func usageInput(userID, keyID int64) RequestLogInput {
	return RequestLogInput{
		AttemptID:        "attempt-usage-1",
		UserID:           userID,
		Model:            "opaque/provider/model",
		EndpointKeyID:    keyID,
		UpstreamModelID:  "upstream/model",
		StatusCode:       200,
		DurationMs:       12,
		StartedAt:        time.Unix(1700000000, 0).UTC(),
		CompletedAt:      time.Unix(1700000000, 0).Add(12 * time.Millisecond).UTC(),
		PromptTokens:     2,
		CompletionTokens: 3,
		TotalTokens:      5,
		UsageUnknown:     false,
	}
}

func countRequestLogs(t *testing.T, store *Store) int {
	t.Helper()
	var n int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM request_logs`).Scan(&n); err != nil {
		t.Fatalf("count request logs: %v", err)
	}
	return n
}

func TestRecordRequestValidUsageAndAccumulators(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "usage.db"))
	defer store.Close()
	userID := seedUsageUser(t, store, "u1")
	keyID := seedUsageKey(t, store, userID)

	if err := store.RecordRequest(context.Background(), usageInput(userID, keyID)); err != nil {
		t.Fatalf("RecordRequest: %v", err)
	}

	totals, err := store.GetUserUsage(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetUserUsage: %v", err)
	}
	if totals.TotalRequests != 1 || totals.TotalPromptTokens != 2 || totals.TotalCompletionTokens != 3 || totals.TotalUnknownUsageRequests != 0 {
		t.Fatalf("totals = %+v", totals)
	}
	if countRequestLogs(t, store) != 1 {
		t.Fatal("log row count != 1")
	}

	logs, _, err := store.QueryRequestLogs(context.Background(), LogQuery{UserID: userID, PageSize: 10})
	if err != nil {
		t.Fatalf("QueryRequestLogs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("logs = %+v", logs)
	}
	log := logs[0]
	if log.UserID != userID || log.Model != "opaque/provider/model" || log.EndpointKeyID != keyID ||
		log.UpstreamModelID != "upstream/model" || log.StatusCode != 200 || log.DurationMs != 12 ||
		log.PromptTokens != 2 || log.CompletionTokens != 3 || log.TotalTokens != 5 ||
		log.UsageUnknown || log.ErrorCode != "" || log.ErrorDiag != "" ||
		!log.StartedAt.Equal(time.Unix(1700000000, 0).UTC()) {
		t.Fatalf("log row = %+v", log)
	}
}

func TestRecordRequestUsageUnknownNoFabricatedTokens(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "usage.db"))
	defer store.Close()
	userID := seedUsageUser(t, store, "u1")

	input := usageInput(userID, 0)
	input.AttemptID = "attempt-unknown"
	input.UsageUnknown = true
	input.PromptTokens, input.CompletionTokens, input.TotalTokens = 0, 0, 0
	if err := store.RecordRequest(context.Background(), input); err != nil {
		t.Fatalf("RecordRequest: %v", err)
	}
	totals, err := store.GetUserUsage(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetUserUsage: %v", err)
	}
	if totals.TotalRequests != 1 || totals.TotalPromptTokens != 0 || totals.TotalCompletionTokens != 0 || totals.TotalUnknownUsageRequests != 1 {
		t.Fatalf("totals = %+v", totals)
	}
	logs, _, _ := store.QueryRequestLogs(context.Background(), LogQuery{PageSize: 10})
	if len(logs) != 1 || !logs[0].UsageUnknown || logs[0].PromptTokens != 0 || logs[0].TotalTokens != 0 {
		t.Fatalf("logs = %+v", logs)
	}
}

func TestRecordRequestInconsistentTripleStoredAsIs(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "usage.db"))
	defer store.Close()
	userID := seedUsageUser(t, store, "u1")

	input := usageInput(userID, 0)
	input.AttemptID = "attempt-inconsistent"
	input.PromptTokens, input.CompletionTokens, input.TotalTokens = 2, 3, 999
	if err := store.RecordRequest(context.Background(), input); err != nil {
		t.Fatalf("RecordRequest: %v", err)
	}
	totals, _ := store.GetUserUsage(context.Background(), userID)
	if totals.TotalPromptTokens != 2 || totals.TotalCompletionTokens != 3 {
		t.Fatalf("totals = %+v", totals)
	}
	logs, _, _ := store.QueryRequestLogs(context.Background(), LogQuery{PageSize: 10})
	if logs[0].PromptTokens != 2 || logs[0].CompletionTokens != 3 || logs[0].TotalTokens != 999 {
		t.Fatalf("log = %+v", logs[0])
	}
}

func TestRecordRequestDuplicateAttemptIDIsAtMostOnce(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "usage.db"))
	defer store.Close()
	userID := seedUsageUser(t, store, "u1")

	input := usageInput(userID, 0)
	for i := 0; i < 3; i++ {
		if err := store.RecordRequest(context.Background(), input); err != nil {
			t.Fatalf("RecordRequest #%d: %v", i, err)
		}
	}
	totals, _ := store.GetUserUsage(context.Background(), userID)
	if totals.TotalRequests != 1 || totals.TotalPromptTokens != 2 {
		t.Fatalf("duplicate delivery double-accumulated: %+v", totals)
	}
	if countRequestLogs(t, store) != 1 {
		t.Fatal("duplicate delivery inserted extra log rows")
	}
}

func TestRecordRequestLateUserDeletionIsAtomicNoOp(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "usage.db"))
	defer store.Close()

	// Late callback: the account was deleted before the hook write arrives.
	deleted := seedUsageUser(t, store, "gone")
	if _, err := store.DB().Exec(`DELETE FROM users WHERE id=?`, deleted); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	input := usageInput(deleted, 0)
	if err := store.RecordRequest(context.Background(), input); err != nil {
		t.Fatalf("late RecordRequest must not fail: %v", err)
	}
	if countRequestLogs(t, store) != 0 {
		t.Fatal("late callback inserted a log row for a deleted account")
	}

	// Written before deletion: the cascade removes the log row with the user.
	live := seedUsageUser(t, store, "live")
	if err := store.RecordRequest(context.Background(), usageInput(live, 0)); err != nil {
		t.Fatalf("RecordRequest: %v", err)
	}
	if _, err := store.DB().Exec(`DELETE FROM users WHERE id=?`, live); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if countRequestLogs(t, store) != 0 {
		t.Fatal("user deletion did not cascade to request logs")
	}
}

func TestRecordRequestDeletedKeyKeepsLogRow(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "usage.db"))
	defer store.Close()
	userID := seedUsageUser(t, store, "u1")
	keyID := seedUsageKey(t, store, userID)
	if _, err := store.DB().Exec(`DELETE FROM endpoint_keys WHERE id=?`, keyID); err != nil {
		t.Fatalf("delete key: %v", err)
	}
	if err := store.RecordRequest(context.Background(), usageInput(userID, keyID)); err != nil {
		t.Fatalf("RecordRequest with a deleted key must keep the log: %v", err)
	}
	logs, _, _ := store.QueryRequestLogs(context.Background(), LogQuery{PageSize: 10})
	if len(logs) != 1 || logs[0].EndpointKeyID != 0 {
		t.Fatalf("logs = %+v", logs)
	}
	totals, _ := store.GetUserUsage(context.Background(), userID)
	if totals.TotalRequests != 1 {
		t.Fatalf("totals = %+v", totals)
	}
}

func TestRecordRequestOverflowFailsClosed(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "usage.db"))
	defer store.Close()
	userID := seedUsageUser(t, store, "u1")
	seedUsageKey(t, store, userID)

	// Requests accumulator at the ceiling: the update must match zero rows,
	// roll back, and leave no log row behind (fail closed, no partial state).
	if _, err := store.DB().Exec(`UPDATE users SET total_requests=? WHERE id=?`, int64(math.MaxInt64), userID); err != nil {
		t.Fatalf("seed ceiling: %v", err)
	}
	if err := store.RecordRequest(context.Background(), usageInput(userID, 0)); err == nil {
		t.Fatal("overflowing total_requests must fail")
	} else if !strings.Contains(err.Error(), "overflow") {
		t.Fatalf("unexpected error: %v", err)
	}
	if countRequestLogs(t, store) != 0 {
		t.Fatal("overflow failure left a partial log row")
	}
	var requests int64
	if err := store.DB().QueryRow(`SELECT total_requests FROM users WHERE id=?`, userID).Scan(&requests); err != nil || requests != math.MaxInt64 {
		t.Fatalf("overflow failure mutated the accumulator: %d err=%v", requests, err)
	}

	// Token accumulator at the ceiling: same fail-closed behavior.
	if _, err := store.DB().Exec(`UPDATE users SET total_requests=0, total_prompt_tokens=? WHERE id=?`, int64(math.MaxInt64), userID); err != nil {
		t.Fatalf("seed token ceiling: %v", err)
	}
	if err := store.RecordRequest(context.Background(), usageInput(userID, 0)); err == nil {
		t.Fatal("overflowing total_prompt_tokens must fail")
	}
	if countRequestLogs(t, store) != 0 {
		t.Fatal("token overflow failure left a partial log row")
	}
}

func TestRecordRequestValidationRejects(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "usage.db"))
	defer store.Close()
	userID := seedUsageUser(t, store, "u1")
	base := usageInput(userID, 0)

	tests := []struct {
		name   string
		mutate func(*RequestLogInput)
	}{
		{"empty attempt id", func(i *RequestLogInput) { i.AttemptID = "" }},
		{"oversized attempt id", func(i *RequestLogInput) { i.AttemptID = strings.Repeat("x", MaxAttemptIDLen+1) }},
		{"control in attempt id", func(i *RequestLogInput) { i.AttemptID = "bad\nattempt" }},
		{"zero user", func(i *RequestLogInput) { i.UserID = 0 }},
		{"negative key id", func(i *RequestLogInput) { i.EndpointKeyID = -1 }},
		{"bad model", func(i *RequestLogInput) { i.Model = "model\nwith-newline" }},
		{"oversized model", func(i *RequestLogInput) { i.Model = strings.Repeat("m", 600) }},
		{"bad upstream id", func(i *RequestLogInput) { i.UpstreamModelID = " \tupstream " }},
		{"status 600", func(i *RequestLogInput) { i.StatusCode = 600 }},
		{"negative status", func(i *RequestLogInput) { i.StatusCode = -1 }},
		{"negative duration", func(i *RequestLogInput) { i.DurationMs = -1 }},
		{"oversized duration", func(i *RequestLogInput) { i.DurationMs = MaxDurationMs + 1 }},
		{"zero started", func(i *RequestLogInput) { i.StartedAt = time.Time{} }},
		{"completed before started", func(i *RequestLogInput) { i.CompletedAt = i.StartedAt.Add(-time.Second) }},
		{"negative tokens", func(i *RequestLogInput) { i.PromptTokens = -1 }},
		{"oversized tokens", func(i *RequestLogInput) { i.TotalTokens = MaxTokenDelta + 1 }},
		{"unstable error code", func(i *RequestLogInput) { i.ErrorCode = "selector" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := base
			input.AttemptID += "-" + strings.ReplaceAll(test.name, " ", "-")
			test.mutate(&input)
			if err := store.RecordRequest(context.Background(), input); err == nil {
				t.Fatal("invalid input was accepted")
			}
		})
	}
	if countRequestLogs(t, store) != 0 {
		t.Fatal("rejected inputs wrote log rows")
	}
}

func TestRecordRequestBoundsDiagnostic(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "usage.db"))
	defer store.Close()
	userID := seedUsageUser(t, store, "u1")

	input := usageInput(userID, 0)
	input.AttemptID = "attempt-diag"
	input.ErrorCode = "upstream"
	input.ErrorDiag = strings.Repeat("a", 5000) + "\nforged-line\r\n\t" + string([]byte{0xff, 0xfe})
	if err := store.RecordRequest(context.Background(), input); err != nil {
		t.Fatalf("RecordRequest: %v", err)
	}
	logs, _, _ := store.QueryRequestLogs(context.Background(), LogQuery{PageSize: 10})
	if len(logs) != 1 {
		t.Fatal("missing log row")
	}
	if len(logs[0].ErrorDiag) > 4096 {
		t.Fatalf("stored diag exceeds 4096 bytes: %d", len(logs[0].ErrorDiag))
	}
	if strings.ContainsAny(logs[0].ErrorDiag, "\r\n\t") {
		t.Fatalf("stored diag contains a line separator: %q", logs[0].ErrorDiag)
	}
	if !strings.Contains(logs[0].ErrorDiag, "[truncated]") {
		t.Fatalf("stored diag lacks truncation marker: %q", logs[0].ErrorDiag)
	}
}

func TestQueryRequestLogsFiltersAndOffsetPagination(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "usage.db"))
	defer store.Close()
	alice := seedUsageUser(t, store, "alice")
	bob := seedUsageUser(t, store, "bob")

	base := time.Unix(1700000000, 0).UTC()
	for i := 0; i < 5; i++ {
		input := RequestLogInput{
			AttemptID:       fmt.Sprintf("attempt-%d", i),
			UserID:          alice,
			Model:           "opaque/provider/model",
			UpstreamModelID: "upstream/model",
			StatusCode:      200,
			StartedAt:       base.Add(time.Duration(i) * time.Minute),
			CompletedAt:     base.Add(time.Duration(i)*time.Minute + time.Second),
		}
		if err := store.RecordRequest(context.Background(), input); err != nil {
			t.Fatalf("RecordRequest %d: %v", i, err)
		}
	}
	bobInput := RequestLogInput{
		AttemptID:       "attempt-bob",
		UserID:          bob,
		Model:           "other/model",
		UpstreamModelID: "upstream/other",
		StatusCode:      502,
		StartedAt:       base.Add(10 * time.Minute),
		CompletedAt:     base.Add(10*time.Minute + time.Second),
	}
	if err := store.RecordRequest(context.Background(), bobInput); err != nil {
		t.Fatalf("RecordRequest bob: %v", err)
	}

	// User filter: only alice's rows, newest first.
	logs, _, err := store.QueryRequestLogs(context.Background(), LogQuery{UserID: alice, PageSize: 10})
	if err != nil {
		t.Fatalf("QueryRequestLogs: %v", err)
	}
	if len(logs) != 5 || logs[0].ID <= logs[4].ID {
		t.Fatalf("user filter order = %+v", logs)
	}
	for _, log := range logs {
		if log.UserID != alice {
			t.Fatalf("cross-user row leaked: %+v", log)
		}
	}

	// Status filter.
	status, _, err := store.QueryRequestLogs(context.Background(), LogQuery{Status: 502, PageSize: 10})
	if err != nil || len(status) != 1 || status[0].UserID != bob {
		t.Fatalf("status filter = %+v err=%v", status, err)
	}

	// Time-range filter: started_at in [base+2min, base+4min).
	ranged, _, err := store.QueryRequestLogs(context.Background(), LogQuery{
		FromUnix: base.Add(2 * time.Minute).Unix(),
		ToUnix:   base.Add(4 * time.Minute).Unix(),
		PageSize: 10,
	})
	if err != nil || len(ranged) != 2 {
		t.Fatalf("range filter = %+v err=%v", ranged, err)
	}

	// Offset pagination: page of 2, then the next pages.
	page1, hasMore1, err := store.QueryRequestLogs(context.Background(), LogQuery{PageSize: 2})
	if err != nil || len(page1) != 2 || !hasMore1 {
		t.Fatalf("page1 = %+v err=%v hasMore=%v", page1, err, hasMore1)
	}
	page2, hasMore2, err := store.QueryRequestLogs(context.Background(), LogQuery{Page: 2, PageSize: 2})
	if err != nil || len(page2) != 2 || !hasMore2 {
		t.Fatalf("page2 = %+v err=%v hasMore=%v", page2, err, hasMore2)
	}
	page3, hasMore3, err := store.QueryRequestLogs(context.Background(), LogQuery{Page: 3, PageSize: 2})
	if err != nil || len(page3) != 2 || hasMore3 {
		t.Fatalf("page3 = %+v err=%v hasMore=%v", page3, err, hasMore3)
	}
	seen := make(map[int64]bool)
	for _, log := range append(append([]RequestLog(nil), page1...), append(page2, page3...)...) {
		if seen[log.ID] {
			t.Fatalf("pages overlap at id %d", log.ID)
		}
		seen[log.ID] = true
	}
	if len(seen) != 6 {
		t.Fatalf("pages missed rows: %d", len(seen))
	}

	// Page-size clamp: never exceeds the page bound.
	huge, _, err := store.QueryRequestLogs(context.Background(), LogQuery{PageSize: 1_000_000})
	if err != nil || len(huge) > MaxLogPageLimit {
		t.Fatalf("page-size clamp = %d err=%v", len(huge), err)
	}
}

func TestUsageQueriesExcludeAdminAndBoundPages(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "usage.db"))
	defer store.Close()
	alice := seedUsageUser(t, store, "alice")
	bob := seedUsageUser(t, store, "bob")
	admin := seedUsageAdmin(t, store)

	for i, id := range []int64{alice, bob} {
		input := usageInput(id, 0)
		input.AttemptID = fmt.Sprintf("attempt-queries-%d", i)
		if err := store.RecordRequest(context.Background(), input); err != nil {
			t.Fatalf("RecordRequest: %v", err)
		}
	}
	if _, err := store.DB().Exec(`UPDATE users SET total_requests=9999, total_prompt_tokens=99999 WHERE id=?`, admin); err != nil {
		t.Fatalf("seed admin totals: %v", err)
	}

	totals, err := store.AggregateUsage(context.Background())
	if err != nil {
		t.Fatalf("AggregateUsage: %v", err)
	}
	if totals.TotalRequests != 2 || totals.TotalPromptTokens != 4 {
		t.Fatalf("site-wide totals include the admin: %+v", totals)
	}

	rows, err := store.AggregateUsageByUser(context.Background(), 0)
	if err != nil {
		t.Fatalf("AggregateUsageByUser: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("by-user rows = %+v", rows)
	}
	for _, row := range rows {
		if row.UserID == admin {
			t.Fatalf("admin leaked into by-user aggregation: %+v", rows)
		}
	}

	bounded, err := store.AggregateUsageByUser(context.Background(), 1)
	if err != nil || len(bounded) != 1 || bounded[0].UserID != alice {
		t.Fatalf("bounded by-user = %+v err=%v", bounded, err)
	}
}

func TestRecordRequestConcurrentAccumulate(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "usage.db"))
	defer store.Close()
	userID := seedUsageUser(t, store, "u1")

	const workers = 24
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			input := usageInput(userID, 0)
			input.AttemptID = fmt.Sprintf("attempt-concurrent-%d", i)
			if err := store.RecordRequest(context.Background(), input); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent RecordRequest: %v", err)
	}

	totals, err := store.GetUserUsage(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetUserUsage: %v", err)
	}
	if totals.TotalRequests != workers || totals.TotalPromptTokens != 2*workers || totals.TotalCompletionTokens != 3*workers {
		t.Fatalf("concurrent accumulation lost updates: %+v", totals)
	}
	if countRequestLogs(t, store) != workers {
		t.Fatal("concurrent accumulation lost log rows")
	}
}
