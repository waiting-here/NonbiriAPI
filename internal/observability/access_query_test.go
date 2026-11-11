package observability

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestAccessGenerationCountUsesBoundedIndexes(t *testing.T) {
	r, userID := newObservedDatabase(t)
	for _, tc := range []struct {
		name, suffix, index string
		args                []any
	}{
		{"global", "", "idx_request_sources_scan", []any{1_799_913_600, 1_800_000_000}},
		{"owner", " AND user_id=?", "idx_request_sources_user_time", []any{1_799_913_600, 1_800_000_000, userID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := r.db.Query("EXPLAIN QUERY PLAN "+accessGenerationCountQuery+tc.suffix, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var plan []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				plan = append(plan, detail)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			detail := strings.Join(plan, "\n")
			if !strings.Contains(detail, "SEARCH request_source_facts USING INDEX "+tc.index) ||
				!strings.Contains(detail, "occurred_at>? AND occurred_at<?") || strings.Contains(detail, "SCAN request_source_facts") {
				t.Fatalf("generation count lost its bounded index: %s", detail)
			}
			t.Log(detail)
		})
	}
}

func insertAccessQueryEvent(t *testing.T, r *Repository, userID, at int64, path, response string, status int, generation any) string {
	t.Helper()
	id, err := db.GenerateOpaqueID("aev_")
	if err != nil {
		t.Fatal(err)
	}
	var keyUser any
	if generation != nil {
		keyUser = userID
	}
	if _, err := r.db.Exec(`INSERT INTO audit_access_events(id,user_id,caller_key_user_id,caller_key_generation,path_kind,method,http_status,response_kind,effective_ip,ip_quality,source_json,occurred_at)
 VALUES(?,?,?,?,?,'GET',?,?,'192.0.2.72','direct_peer','{"effective_ip":"192.0.2.72","ip_quality":"direct_peer"}',?)`, id, userID, keyUser, generation, path, status, response, at); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAccessSummaryPreservesWindowAndDenominator(t *testing.T) {
	r, userID := newObservedDatabase(t)
	now, from := r.now().Unix(), r.now().Unix()-86400+17
	for _, item := range []struct {
		at    int64
		kind  string
		owner any
	}{
		{from - 1, "self", userID}, {from, "self", userID}, {now - 4, "charity", userID},
		{now - 3, "unclassified", userID}, {now - 2, "discovery", userID}, {now - 1, "self", nil}, {now, "self", userID},
	} {
		_, logID := observedLog(t, r, userID, item.at)
		if _, err := r.db.Exec(`INSERT INTO request_source_facts(request_log_id,user_id,kind,effective_ip,ip_quality,source_json,occurred_at)
 VALUES(?,?,?,'192.0.2.72','direct_peer','{}',?)`, logID, item.owner, item.kind, item.at); err != nil {
			t.Fatal(err)
		}
	}
	insertAccessQueryEvent(t, r, userID, from-1, "models", "api_json", 200, 3)
	insertAccessQueryEvent(t, r, userID, from, "models", "api_json", 200, 3)
	insertAccessQueryEvent(t, r, userID, now-3, "models", "api_json", 429, 3)
	insertAccessQueryEvent(t, r, userID, now-2, "billing_usage", "spa_fallback", 200, 3)
	insertAccessQueryEvent(t, r, userID, now-1, "models", "api_json", 200, nil)
	insertAccessQueryEvent(t, r, userID, now, "models", "api_json", 200, 3)
	for _, item := range []struct {
		at            int64
		path          string
		status, count int
	}{
		{from - from%60 - 60, "models", 2, 11}, {from - from%60, "models", 2, 2},
		{now - 60, "models", 4, 3}, {now - 60, "billing_usage", 2, 4}, {now, "models", 2, 13},
	} {
		if _, err := r.db.Exec(`INSERT INTO anonymous_access_minutes(minute_at,path_kind,method,status_class,count) VALUES(?,?,'GET',?,?)`, item.at, item.path, item.status, item.count); err != nil {
			t.Fatal(err)
		}
	}
	generation := int64(3)
	for _, tc := range []struct {
		name                                       string
		filter                                     AccessFilter
		authenticated, anonymous, models, requests int64
	}{
		{"global", AccessFilter{}, 4, 9, 3, 3},
		{"owner", AccessFilter{UserID: userID}, 4, 0, 3, 2},
		{"key", AccessFilter{UserID: userID, KeyGeneration: &generation}, 3, 0, 2, 2},
		{"model_status", AccessFilter{PathKind: "models", StatusClass: 4}, 1, 3, 1, 3},
		{"billing", AccessFilter{PathKind: "billing_usage"}, 1, 4, 0, 3},
		{"unknown_owner", AccessFilter{UserID: userID + 100}, 0, 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filter := tc.filter
			filter.From, filter.To, filter.Limit = from, now, 20
			tx, err := r.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			got, err := SummarizeAccessTx(t.Context(), tx, filter)
			if err != nil {
				t.Fatal(err)
			}
			if got.AuthenticatedEvents != tc.authenticated || got.AnonymousEvents != tc.anonymous || got.ModelListEvents != tc.models || got.GenerationRequests != tc.requests {
				t.Fatalf("unexpected summary: %+v", got)
			}
			if tc.requests == 0 {
				if got.ModelGenerationRatio != nil {
					t.Fatal("zero denominator has ratio")
				}
			} else if got.ModelGenerationRatio == nil || *got.ModelGenerationRatio != float64(tc.models)/float64(tc.requests) {
				t.Fatalf("ratio: %v", got.ModelGenerationRatio)
			}
			if tc.name == "global" {
				want := []AccessPathSummary{{"billing_usage", "spa_fallback", 1, 0}, {"models", "api_json", 3, 0}, {"billing_usage", "other", 0, 4}, {"models", "other", 0, 5}}
				if !reflect.DeepEqual(got.Paths, want) {
					t.Fatalf("paths: %+v", got.Paths)
				}
			}
		})
	}
}

func TestAccessCursorAndNumberedPagesKeepTieOrder(t *testing.T) {
	r, userID := newObservedDatabase(t)
	type event struct {
		id string
		at int64
	}
	var want []event
	for n := range 85 {
		at := r.now().Unix() - 100 + int64(n/3)
		want = append(want, event{insertAccessQueryEvent(t, r, userID, at, "models", "api_json", 200, 3), at})
	}
	sort.Slice(want, func(i, j int) bool {
		return want[i].at > want[j].at || want[i].at == want[j].at && want[i].id > want[j].id
	})
	tx, err := r.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	filter := AccessFilter{From: r.now().Unix() - 86400, To: r.now().Unix(), Limit: 20, UserID: userID}
	var got []event
	for pageNumber := int64(1); ; pageNumber++ {
		cursor, err := ListAccessTx(t.Context(), tx, filter)
		if err != nil {
			t.Fatal(err)
		}
		numberedFilter := filter
		numberedFilter.Cursor, numberedFilter.Page = "", pageNumber
		numbered, err := ListAccessNumberedTx(t.Context(), tx, numberedFilter)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(cursor.Data, numbered.Data) {
			t.Fatalf("page %d differs", pageNumber)
		}
		for _, item := range cursor.Data {
			got = append(got, event{item.ID, item.OccurredAt})
		}
		if cursor.NextCursor == nil {
			break
		}
		if pageNumber > 5 {
			t.Fatal("cursor failed to advance")
		}
		filter.Cursor = *cursor.NextCursor
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("tied timestamps lost, duplicated, or reordered rows")
	}
}

func TestAccessPageCancelsSparseTailBeforeReturningRows(t *testing.T) {
	r, userID := newObservedDatabase(t)
	insertAccessQueryEvent(t, r, userID, r.now().Unix()-2, "models", "api_json", 200, 3)
	first := insertAccessQueryEvent(t, r, userID, r.now().Unix()-1, "models", "api_json", 200, 3)
	for _, numbered := range []bool{false, true} {
		tx, err := r.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		// The newest row is immediate. Finding the next match must remain inside
		// QueryContext, where the SQLite driver's interrupt hook is installed.
		where := `occurred_at>=? AND occurred_at<? AND (id=? OR (WITH RECURSIVE n(x) AS
 (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<10000000) SELECT SUM(x) FROM n)=0)`
		args := []any{r.now().Unix() - 86400, r.now().Unix(), first, 2}
		if numbered {
			args = append(args, 0)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		rows, err := tx.QueryContext(ctx, accessPageQuery(where, numbered), args...)
		if rows != nil {
			rows.Close()
		}
		cancel()
		tx.Rollback()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("numbered=%v exposed first row before sparse selection completed: %v", numbered, err)
		}
		if got := scalar(t, r.db, `SELECT 7`); got != 7 {
			t.Fatal("connection not reusable", got)
		}
	}
}
