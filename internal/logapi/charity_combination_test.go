package logapi

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestCharityModelCombinationReusesSnapshotAcrossManagementReads(t *testing.T) {
	f, reader := newSeparateLogReaders(t, logReadAuthFunc(func(ctx context.Context, tx *sql.Tx, role string, id int64) error {
		return authorizePageAccount(ctx, tx, role, id, 1_000_000)
	}))
	f.mustExec(db.CharityLogCoveringIndexSQL)
	f.mustExec(`UPDATE request_logs SET model='Shared_%/Historic',route_kind='charity_chat_completions',caller_result_class='failed',caller_status=400,caller_error_code='upstream',usage_total_mismatch=1,origin_user_id=?,origin_discord_id='123456789012345678' WHERE id IN (2,4)`, logUserOne)
	f.mustExec(`UPDATE request_attempts SET endpoint_key_id_snapshot=302,canonical_base_url='https://shared.example/v1',upstream_model_id='shared-upstream' WHERE request_log_id IN (2,4)`)
	f.mustExec(`INSERT INTO request_attempt_diagnostics(claim_id,request_log_id,attempt_seq,category,phase,details,completed_at) SELECT claim_id,request_log_id,attempt_seq,'transport','connect','{}',completed_at FROM request_attempts WHERE request_log_id IN (2,4)`)
	model, discord, url, upstream, code := "shared_%", "123456789012345678", "https://shared.example/v1", "shared-upstream", "upstream"
	owner, key, from, to, status, mismatch := int64(logUserOne), int64(302), int64(0), int64(1_000_000), 400, true
	filter := ListFilter{Operation: "chat_completions", CharityModel: &model, Status: &status, From: &from, To: &to, UserID: &owner, DiscordID: &discord, EndpointKeyID: &key, EndpointBaseURL: &url, UpstreamModel: &upstream, ErrorCode: &code, UsageTotalMismatch: &mismatch, Phase: "handler", ErrorCategory: "transport", ErrorPhase: "connect"}
	// The writer pool is unavailable throughout every filtered list and export.
	held, err := f.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	performance := observability.NewPerformance()
	ctx := performance.Context(t.Context())
	checkRead := func(role string, call func() ([]routeKindRow, error), want []routeKindRow) {
		t.Helper()
		rows, err := call()
		if err != nil || !slices.Equal(rows, want) {
			t.Fatalf("%s combined read=%+v want=%+v err=%v", role, rows, want, err)
		}
		found := false
		for _, phase := range performance.Drain() {
			if phase.Route == "log_read."+role+".models" {
				found = true
				if phase.Completed != 1 || phase.Active != 0 || phase.Failures != 0 {
					t.Fatalf("model lookup was not reused: %+v", phase)
				}
			}
		}
		if !found || reader.Stats().InUse != 0 {
			t.Fatal("combination skipped its snapshot lookup or kept reader")
		}
	}
	expected := []routeKindRow{{f.otherID, RouteCharityChat}, {f.charityID, RouteCharityChat}}
	for _, role := range []string{"admin", "steward"} {
		numbered := filter
		numbered.Page = &pagination.Request{Page: 99, Size: 10}
		checkRead(role, func() ([]routeKindRow, error) {
			if role == "admin" {
				out, err := f.repo.ListAdmin(ctx, numbered)
				if err == nil {
					requireLogPagination(t, out.Pagination, 1, 10, 2)
				}
				return routeKindRows(t, out.Data), err
			}
			out, err := f.repo.ListSteward(ctx, 999, numbered, allowLogStewardRead{})
			if err == nil {
				requireLogPagination(t, out.Pagination, 1, 10, 2)
			}
			return routeKindRows(t, out.Data), err
		}, expected)
		cursor := filter
		cursor.Limit = 1
		for i := range 2 {
			checkRead(role, func() ([]routeKindRow, error) {
				if role == "admin" {
					out, err := f.repo.ListAdmin(ctx, cursor)
					if out.NextCursor != nil {
						cursor.Cursor = *out.NextCursor
					}
					return routeKindRows(t, out.Data), err
				}
				out, err := f.repo.ListSteward(ctx, 999, cursor, allowLogStewardRead{})
				if out.NextCursor != nil {
					cursor.Cursor = *out.NextCursor
				}
				return routeKindRows(t, out.Data), err
			}, expected[i:i+1])
		}
		checkRead(role, func() ([]routeKindRow, error) {
			var out []AdminLogRow
			var err error
			if role == "admin" {
				out, err = f.repo.ExportAdmin(ctx, filter)
			} else {
				out, err = f.repo.ExportSteward(ctx, 999, filter, allowLogStewardRead{})
			}
			slices.Reverse(out)
			return routeKindRows(t, out), err
		}, expected)
	}
	if _, err := f.repo.ListUser(ctx, logUserOne, ListFilter{CharityModel: &model, Status: &status}); !errors.Is(err, ErrInvalid) {
		t.Fatal("user gained management filter", err)
	}
}

func TestCombinedCharityModelCountAndPageSeekHistoricalNames(t *testing.T) {
	database := indexedCharityQueryDatabase(t, 6400)
	var sqliteVersion string
	if err := database.QueryRow("SELECT sqlite_version()").Scan(&sqliteVersion); err != nil {
		t.Fatal(err)
	}
	t.Logf("sqlite=%s", sqliteVersion)
	status, from, to := 200, int64(1_800_000_000), int64(1_800_000_200)
	pattern := "MODEL-001"
	tx, err := database.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	predicate, args, err := charityModelFilter(t.Context(), tx, ListFilter{CharityModel: &pattern, Status: &status, From: &from, To: &to})
	if err != nil {
		t.Fatal(err)
	}
	where := "WHERE (l.completed_at IS NULL OR l.completed_at>?) AND l.caller_status=? AND l.started_at>=? AND l.started_at<?" + predicate
	args = append([]any{int64(1_799_000_000), status, from, to}, args...)
	legacyWhere, legacyArgs := charityQueryWhere(pattern, from, to)
	legacyWhere += " AND l.caller_status=?"
	legacyArgs = append(legacyArgs, status)
	expected := queryLogIDs(t, tx, "SELECT l.id FROM request_logs l "+legacyWhere+" ORDER BY l.started_at DESC,l.id DESC", legacyArgs...)
	query, pageArgs, metadata, err := logListPageQuery(t.Context(), tx, "l.id", "", where, args, &pagination.Request{Page: 1, Size: 10}, 10)
	if err != nil {
		t.Fatal(err)
	}
	requireLogPagination(t, metadata, 1, 10, int64(len(expected)))
	if got := queryLogIDs(t, tx, query, pageArgs...); !slices.Equal(got, expected[:min(10, len(expected))]) {
		t.Fatal("combined page changed IDs", got, expected)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []struct {
		query string
		args  []any
	}{{"SELECT COUNT(*) FROM request_logs l " + where, args}, {query, pageArgs}} {
		plan := charityQueryPlan(t, database, statement.query, statement.args)
		if !strings.Contains(plan, "idx_request_logs_charity_model (route_kind=? AND model=?)") {
			t.Fatal("combination lost model equality seeks", plan)
		}
		t.Log(plan)
	}
}
