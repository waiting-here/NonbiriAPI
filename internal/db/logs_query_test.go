package db

// Tests for the redesigned user/administrator request-log projections
// (logs_query.go): ownership inside the SQL, the charity privacy projection,
// note/base-URL semantics after resource deletion, exact-match filters,
// bounded options, and the fail-closed export row bound.

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// seedLogKeyWithNote seeds an endpoint (with base_url and note) plus one key
// (with note) for userID and returns the key id.
func seedLogKeyWithNote(t *testing.T, store *Store, userID int64, baseURL, endpointNote, keyNote string) int64 {
	t.Helper()
	now := time.Now().Unix()
	res, err := store.DB().Exec(
		`INSERT INTO endpoints (user_id, connector_type, base_url, note, created_at, updated_at) VALUES (?, 'openai-compatible', ?, ?, ?, ?)`,
		userID, baseURL, endpointNote, now, now)
	if err != nil {
		t.Fatalf("seed endpoint: %v", err)
	}
	endpointID, _ := res.LastInsertId()
	res, err = store.DB().Exec(
		`INSERT INTO endpoint_keys (endpoint_id, encrypted_secret, note, created_at, updated_at) VALUES (?, 'nbsec:v1:test', ?, ?, ?)`,
		endpointID, keyNote, now, now)
	if err != nil {
		t.Fatalf("seed key: %v", err)
	}
	keyID, _ := res.LastInsertId()
	return keyID
}

func logInput(userID, keyID int64, attempt, model, upstream string) RequestLogInput {
	base := time.Unix(1700000000, 0).UTC()
	return RequestLogInput{
		AttemptID:           attempt,
		UserID:              userID,
		Model:               model,
		EndpointKeyID:       keyID,
		UpstreamModelID:     upstream,
		StatusCode:          200,
		DurationMs:          10,
		StartedAt:           base,
		CompletedAt:         base.Add(10 * time.Millisecond),
		UncachedInputTokens: 1,
		OutputTokens:        2,
	}
}

func TestUserLogQueryOwnershipInSQL(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "logs.db"))
	defer store.Close()
	alice := seedUsageUser(t, store, "alice")
	bob := seedUsageUser(t, store, "bob")

	if err := store.RecordRequest(context.Background(), logInput(alice, 0, "own-a", "p/m", "up/a")); err != nil {
		t.Fatalf("RecordRequest alice: %v", err)
	}
	if err := store.RecordRequest(context.Background(), logInput(bob, 0, "own-b", "q/m", "up/b")); err != nil {
		t.Fatalf("RecordRequest bob: %v", err)
	}

	logs, hasMore, err := store.QueryUserRequestLogs(context.Background(), alice, UserLogQuery{PageSize: 10})
	if err != nil || hasMore {
		t.Fatalf("QueryUserRequestLogs = %+v hasMore=%v err=%v", logs, hasMore, err)
	}
	if len(logs) != 1 || logs[0].Model != "p/m" || logs[0].UpstreamModelID != "up/a" {
		t.Fatalf("alice rows = %+v", logs)
	}

	// Bob's query never returns Alice's row; an unknown user gets nothing.
	for _, uid := range []int64{bob, alice + 999} {
		logs, _, err = store.QueryUserRequestLogs(context.Background(), uid, UserLogQuery{PageSize: 100})
		if err != nil {
			t.Fatalf("QueryUserRequestLogs(%d): %v", uid, err)
		}
		for _, l := range logs {
			if l.Model == "p/m" {
				t.Fatalf("cross-user row leaked to user %d: %+v", uid, l)
			}
		}
	}
	if _, _, err := store.QueryUserRequestLogs(context.Background(), 0, UserLogQuery{}); err == nil {
		t.Fatal("user id 0 must be rejected")
	}
}

func TestCharityConsumerProjectionHidesDonorResources(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "logs.db"))
	defer store.Close()
	donor := seedUsageUser(t, store, "donor")
	consumer := seedUsageUser(t, store, "consumer")

	// The donor owns a named endpoint/key. A charity consumer's log row is
	// written with no endpoint_key_id at all (the write path only stores a key
	// owned by the logging user), so no donor identifier can be attached.
	donorKey := seedLogKeyWithNote(t, store, donor, "https://donor.example", "donor endpoint note", "donor key note")
	if err := store.RecordRequest(context.Background(), logInput(consumer, 0, "charity-c1", "[公益]p/m", "up/charity")); err != nil {
		t.Fatalf("RecordRequest consumer: %v", err)
	}
	if err := store.RecordRequest(context.Background(), logInput(donor, donorKey, "charity-d1", "d/m", "up/donor")); err != nil {
		t.Fatalf("RecordRequest donor: %v", err)
	}

	// The consumer sees their own row with empty notes and no donor material.
	logs, _, err := store.QueryUserRequestLogs(context.Background(), consumer, UserLogQuery{PageSize: 10})
	if err != nil || len(logs) != 1 {
		t.Fatalf("consumer rows = %+v err=%v", logs, err)
	}
	row := logs[0]
	if row.KeyNote != "" || row.EndpointNote != "" || row.EndpointBaseURL != "" || row.EndpointKeyID != 0 {
		t.Fatalf("consumer row leaks donor resources: %+v", row)
	}

	// The admin projection has no note field to leak by construction; it also
	// must not resolve the donor's base URL for the consumer's row.
	adminLogs, _, err := store.QueryAdminRequestLogs(context.Background(), AdminLogQuery{UserID: consumer, PageSize: 10})
	if err != nil || len(adminLogs) != 1 {
		t.Fatalf("admin consumer rows = %+v err=%v", adminLogs, err)
	}
	if adminLogs[0].EndpointBaseURL != "" || adminLogs[0].EndpointKeyID != 0 {
		t.Fatalf("admin consumer row leaks donor resources: %+v", adminLogs[0])
	}
}

func TestUserLogNotesJoinCurrentValuesAndSnapshotSurvivesDeletion(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "logs.db"))
	defer store.Close()
	userID := seedUsageUser(t, store, "u1")
	keyID := seedLogKeyWithNote(t, store, userID, "https://ep.example/v1", "my endpoint", "my key")

	input := logInput(userID, keyID, "del-1", "p/m", "up/m")
	if err := store.RecordRequest(context.Background(), input); err != nil {
		t.Fatalf("RecordRequest: %v", err)
	}

	logs, _, err := store.QueryUserRequestLogs(context.Background(), userID, UserLogQuery{PageSize: 10})
	if err != nil || len(logs) != 1 {
		t.Fatalf("rows = %+v err=%v", logs, err)
	}
	if logs[0].KeyNote != "my key" || logs[0].EndpointNote != "my endpoint" {
		t.Fatalf("notes = %q/%q, want current values", logs[0].KeyNote, logs[0].EndpointNote)
	}

	// Snapshot semantics: the dispatch-time base URL column is what both
	// projections show (empty until the dispatch rail writes it).
	if _, err := store.DB().Exec(`UPDATE request_logs SET endpoint_base_url = 'https://ep.example/v1' WHERE attempt_id = 'del-1'`); err != nil {
		t.Fatalf("set snapshot: %v", err)
	}

	// Deleting the key (which cascades the endpoint reference and can remove
	// the endpoint) keeps the log row; notes become empty while the snapshot
	// survives.
	if _, err := store.DB().Exec(`DELETE FROM endpoints WHERE id = (SELECT endpoint_id FROM endpoint_keys WHERE id = ?)`, keyID); err != nil {
		t.Fatalf("delete endpoint: %v", err)
	}
	logs, _, err = store.QueryUserRequestLogs(context.Background(), userID, UserLogQuery{PageSize: 10})
	if err != nil || len(logs) != 1 {
		t.Fatalf("rows after delete = %+v err=%v", logs, err)
	}
	if logs[0].KeyNote != "" || logs[0].EndpointNote != "" {
		t.Fatalf("notes after delete = %q/%q, want empty", logs[0].KeyNote, logs[0].EndpointNote)
	}
	if logs[0].EndpointBaseURL != "https://ep.example/v1" {
		t.Fatalf("snapshot after delete = %q, want the recorded value", logs[0].EndpointBaseURL)
	}
	if logs[0].EndpointKeyID != 0 {
		t.Fatalf("key ref after delete = %d, want 0", logs[0].EndpointKeyID)
	}
}

func TestAdminLogFiltersExactMatch(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "logs.db"))
	defer store.Close()
	alice := seedUsageUser(t, store, "alice")
	bob := seedUsageUser(t, store, "bob")

	a1 := logInput(alice, 0, "flt-a1", "p/m", "up/alpha")
	a1.ErrorCode = "upstream"
	if err := store.RecordRequest(context.Background(), a1); err != nil {
		t.Fatalf("RecordRequest: %v", err)
	}
	if err := store.RecordRequest(context.Background(), logInput(bob, 0, "flt-b1", "q/m", "up/beta")); err != nil {
		t.Fatalf("RecordRequest: %v", err)
	}
	// Give alice's row a dispatch-time base URL snapshot.
	if _, err := store.DB().Exec(`UPDATE request_logs SET endpoint_base_url = 'https://alpha.example/v1' WHERE attempt_id = 'flt-a1'`); err != nil {
		t.Fatalf("set snapshot: %v", err)
	}

	cases := []struct {
		name  string
		query AdminLogQuery
		want  int
	}{
		{"user", AdminLogQuery{UserID: alice, PageSize: 10}, 1},
		{"base url exact", AdminLogQuery{EndpointBaseURL: "https://alpha.example/v1", PageSize: 10}, 1},
		{"base url no substring", AdminLogQuery{EndpointBaseURL: "alpha.example", PageSize: 10}, 0},
		{"upstream exact", AdminLogQuery{UpstreamModel: "up/beta", PageSize: 10}, 1},
		{"error code", AdminLogQuery{ErrorCode: "upstream", PageSize: 10}, 1},
		{"unknown error code matches nothing", AdminLogQuery{ErrorCode: "nope", PageSize: 10}, 0},
		{"status", AdminLogQuery{Status: 200, PageSize: 10}, 2},
		{"combined", AdminLogQuery{UserID: alice, UpstreamModel: "up/alpha", Status: 200, PageSize: 10}, 1},
	}
	for _, tc := range cases {
		rows, _, err := store.QueryAdminRequestLogs(context.Background(), tc.query)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(rows) != tc.want {
			t.Errorf("%s: rows = %d, want %d (%+v)", tc.name, len(rows), tc.want, rows)
		}
	}

	invalid := []AdminLogQuery{
		{UserID: -1},
		{EndpointBaseURL: " leading-space"},
		{EndpointBaseURL: "x\x01y"},
		{UpstreamModel: "trailing-space "},
		{ErrorCode: "bad\nline"},
		{Status: 99},
		{Status: 600},
		{FromUnix: -1},
		{FromUnix: 20, ToUnix: 10},
	}
	for i, q := range invalid {
		if _, _, err := store.QueryAdminRequestLogs(context.Background(), q); err == nil {
			t.Errorf("invalid query %d (%+v) accepted", i, q)
		}
	}
}

func TestStewardResourceFiltersExcludeCharitySnapshots(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "logs.db"))
	defer store.Close()
	consumer := seedUsageUser(t, store, "consumer")
	personal := logInput(consumer, 0, "steward-filter-personal", "p/m", "personal/up")
	if err := store.RecordRequest(context.Background(), personal); err != nil {
		t.Fatalf("RecordRequest personal: %v", err)
	}
	charity := logInput(consumer, 0, "steward-filter-charity", "[公益]p/m", "donor/up")
	if err := store.RecordRequest(context.Background(), charity); err != nil {
		t.Fatalf("RecordRequest charity: %v", err)
	}
	if _, err := store.DB().Exec(`UPDATE request_logs SET route_kind='charity', endpoint_base_url='https://donor.example/v1' WHERE attempt_id='steward-filter-charity'`); err != nil {
		t.Fatalf("mark charity row: %v", err)
	}
	if _, err := store.DB().Exec(`UPDATE request_logs SET endpoint_base_url='https://personal.example/v1' WHERE attempt_id='steward-filter-personal'`); err != nil {
		t.Fatalf("set personal snapshot: %v", err)
	}

	for _, tc := range []struct {
		name  string
		query AdminLogQuery
		want  string
	}{
		{
			name:  "donor base URL does not match charity",
			query: AdminLogQuery{EndpointBaseURL: "https://donor.example/v1", PageSize: 10},
		},
		{
			name:  "donor upstream model does not match charity",
			query: AdminLogQuery{UpstreamModel: "donor/up", PageSize: 10},
		},
		{
			name:  "personal base URL remains filterable",
			query: AdminLogQuery{EndpointBaseURL: "https://personal.example/v1", PageSize: 10},
			want:  "steward-filter-personal",
		},
		{
			name:  "personal upstream model remains filterable",
			query: AdminLogQuery{UpstreamModel: "personal/up", PageSize: 10},
			want:  "steward-filter-personal",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, _, err := store.QueryStewardRequestLogs(context.Background(), tc.query)
			if err != nil {
				t.Fatalf("QueryStewardRequestLogs: %v", err)
			}
			if tc.want == "" {
				if len(rows) != 0 {
					t.Fatalf("rows = %+v, donor charity row must not match", rows)
				}
				return
			}
			if len(rows) != 1 || rows[0].AttemptID != tc.want || rows[0].RouteKind != "personal" {
				t.Fatalf("rows = %+v, want personal row %q", rows, tc.want)
			}
		})
	}
}

func TestUserLogFiltersAndOptionsBounded(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "logs.db"))
	defer store.Close()
	userID := seedUsageUser(t, store, "u1")
	other := seedUsageUser(t, store, "u2")

	models := []string{"a/model", "b/model", "c/model", "d/model"}
	for i, m := range models {
		input := logInput(userID, 0, fmt.Sprintf("opt-%d", i), m, "up/m")
		if i == 3 {
			input.Model = "" // unattributed row: never a candidate
		}
		if err := store.RecordRequest(context.Background(), input); err != nil {
			t.Fatalf("RecordRequest: %v", err)
		}
	}
	if err := store.RecordRequest(context.Background(), logInput(other, 0, "opt-other", "z/model", "up/z")); err != nil {
		t.Fatalf("RecordRequest other: %v", err)
	}

	// Options: own distinct non-empty models, ascending, bounded.
	got, err := store.ListUserLogModelOptions(context.Background(), userID)
	if err != nil {
		t.Fatalf("ListUserLogModelOptions: %v", err)
	}
	want := []string{"a/model", "b/model", "c/model"}
	if len(got) != len(want) {
		t.Fatalf("options = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("options = %v, want %v", got, want)
		}
	}

	// Filters: model/error_code/status/time exact per frozen semantics.
	rows, _, err := store.QueryUserRequestLogs(context.Background(), userID, UserLogQuery{Model: "b/model", PageSize: 10})
	if err != nil || len(rows) != 1 {
		t.Fatalf("model filter = %+v err=%v", rows, err)
	}
	rows, _, err = store.QueryUserRequestLogs(context.Background(), userID, UserLogQuery{ErrorCode: "internal", PageSize: 10})
	if err != nil || len(rows) != 0 {
		t.Fatalf("error_code filter = %+v err=%v", rows, err)
	}
	bad := []UserLogQuery{
		{Model: " lead"},
		{Model: "trail "},
		{ErrorCode: "multi\nline"},
		{Status: 50},
		{FromUnix: -5},
		{FromUnix: 30, ToUnix: 20},
	}
	for i, q := range bad {
		if _, _, err := store.QueryUserRequestLogs(context.Background(), userID, q); err == nil {
			t.Errorf("invalid user query %d (%+v) accepted", i, q)
		}
	}
}

func TestExportAdminRequestLogsRowBoundFailsClosed(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "logs.db"))
	defer store.Close()
	userID := seedUsageUser(t, store, "u1")

	total := MaxLogExportRows + 1
	base := time.Unix(1700000000, 0).UTC()
	tx, err := store.DB().Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	for i := 0; i < total; i++ {
		if _, err := tx.Exec(`
INSERT INTO request_logs (attempt_id, user_id, status_code, started_at, completed_at)
VALUES (?, ?, 200, ?, ?)`,
			fmt.Sprintf("exp-%d", i), userID, base.Add(time.Duration(i)*time.Second).Unix(),
			base.Add(time.Duration(i)*time.Second).Unix()); err != nil {
			t.Fatalf("seed row %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if _, err := store.ExportAdminRequestLogs(context.Background(), AdminLogQuery{}); err != ErrLogExportTooLarge {
		t.Fatalf("over-bound export err = %v, want ErrLogExportTooLarge", err)
	}

	// At the bound exactly, the export succeeds completely.
	if _, err := store.DB().Exec(`DELETE FROM request_logs WHERE attempt_id = 'exp-0'`); err != nil {
		t.Fatalf("trim: %v", err)
	}
	rows, err := store.ExportAdminRequestLogs(context.Background(), AdminLogQuery{})
	if err != nil {
		t.Fatalf("bound export: %v", err)
	}
	if len(rows) != MaxLogExportRows {
		t.Fatalf("rows = %d, want %d", len(rows), MaxLogExportRows)
	}
	// Stable oldest-first export order.
	if rows[0].ID > rows[len(rows)-1].ID {
		t.Fatalf("export order not ascending: %d..%d", rows[0].ID, rows[len(rows)-1].ID)
	}
}

func TestRetentionCleanupStillBoundsLogQueries(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "logs.db"))
	defer store.Close()
	userID := seedUsageUser(t, store, "u1")

	old := time.Unix(1000000000, 0).UTC() // far beyond the 30-day retention window
	input := logInput(userID, 0, "old-1", "p/m", "up/m")
	input.StartedAt = old
	input.CompletedAt = old
	if err := store.RecordRequest(context.Background(), input); err != nil {
		t.Fatalf("RecordRequest: %v", err)
	}
	if n, err := store.DeleteRequestLogsBefore(context.Background(), time.Now().Unix(), MaxCleanupBatch); err != nil || n != 1 {
		t.Fatalf("DeleteRequestLogsBefore = %d err=%v", n, err)
	}
	log, _, err := store.QueryUserRequestLogs(context.Background(), userID, UserLogQuery{PageSize: 10})
	if err != nil || len(log) != 0 {
		t.Fatalf("retained rows after cleanup = %+v err=%v", log, err)
	}
	adminRows, err := store.ExportAdminRequestLogs(context.Background(), AdminLogQuery{})
	if err != nil || len(adminRows) != 0 {
		t.Fatalf("export after cleanup = %+v err=%v", adminRows, err)
	}
}
