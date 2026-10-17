package logapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestAttemptFiltersMatchIndependentAttemptsWithoutDuplicateRows(t *testing.T) {
	f := newNumberedLogFixture(t)
	key, endpoint, model := int64(800), "https://filter.example/v1", "upstream%_literal"
	for id := int64(100); id < 123; id++ {
		// The three filters deliberately never match the same attempt. A
		// duplicate matching attempt must not change the logical row count.
		f.insertAttempt(id, 1, 201, key, "https://other.example/v1", "openai-compatible", "other", string(ResultResponse), 200, nil, nil, [4]int64{}, 0, 1001, 1002)
		f.insertAttempt(id, 2, 201, 999, endpoint, "openai-compatible", "other", string(ResultResponse), 200, nil, nil, [4]int64{}, 0, 1002, 1003)
		f.insertAttempt(id, 3, 201, 999, "https://other.example/v1", "openai-compatible", model, string(ResultResponse), 200, nil, nil, [4]int64{}, 0, 1003, 1004)
		f.insertAttempt(id, 4, 201, key, endpoint, "openai-compatible", "other", string(ResultResponse), 200, nil, nil, [4]int64{}, 0, 1004, 1005)
	}
	ctx := context.Background()
	for mask := 1; mask < 8; mask++ {
		t.Run(fmt.Sprintf("filters_%d", mask), func(t *testing.T) {
			filter := ListFilter{Page: &pagination.Request{Page: 999, Size: 10}}
			if mask&1 != 0 {
				filter.EndpointKeyID = &key
			}
			if mask&2 != 0 {
				filter.EndpointBaseURL = &endpoint
			}
			if mask&4 != 0 {
				filter.UpstreamModel = &model
			}
			admin, err := f.repo.ListAdmin(ctx, filter)
			if err != nil {
				t.Fatal(err)
			}
			requireLogPagination(t, admin.Pagination, 3, 10, 23)
			steward, err := f.repo.ListSteward(ctx, 6, filter, mismatchStewardAuthorizer{})
			if err != nil {
				t.Fatal(err)
			}
			requireLogPagination(t, steward.Pagination, 3, 10, 23)
			if len(admin.Data) != 3 || len(steward.Data) != 3 {
				t.Fatal("duplicate attempts changed page size")
			}
			for i := range admin.Data {
				want := logOpaqueID("req_", uint64(102-i))
				if admin.Data[i].ID != want || steward.Data[i].ID != want {
					t.Fatalf("tie order = %s / %s, want %s", admin.Data[i].ID, steward.Data[i].ID, want)
				}
			}
			if _, err := f.repo.ListSteward(ctx, 5, filter, mismatchStewardAuthorizer{}); !errors.Is(err, ErrForbidden) {
				t.Fatalf("level 5 read = %v", err)
			}
			if _, err := f.repo.ListUser(ctx, logUserOne, filter); !errors.Is(err, ErrInvalid) {
				t.Fatalf("private attempt filters exposed to ordinary user: %v", err)
			}
			filter.Page, filter.Limit = nil, 10
			seen := map[string]bool{}
			for {
				page, err := f.repo.ListAdmin(ctx, filter)
				if err != nil {
					t.Fatal(err)
				}
				for _, row := range page.Data {
					if seen[row.ID] {
						t.Fatalf("repeated cursor row %s", row.ID)
					}
					seen[row.ID] = true
				}
				if page.NextCursor == nil {
					break
				}
				filter.Cursor = *page.NextCursor
			}
			if len(seen) != 23 {
				t.Fatalf("cursor row count = %d", len(seen))
			}
			filter.Cursor, filter.Limit = "", 0
			exported, err := f.repo.ExportAdmin(ctx, filter)
			if err != nil || len(exported) != 23 {
				t.Fatalf("export = %d, %v", len(exported), err)
			}
		})
	}
	filter := ListFilter{EndpointKeyID: &key, EndpointBaseURL: &endpoint, UpstreamModel: &model, Page: &pagination.Request{Page: 1, Size: 10}}
	wrongKey := int64(9999)
	filter.EndpointKeyID = &wrongKey
	page, err := f.repo.ListAdmin(ctx, filter)
	if err != nil || len(page.Data) != 0 {
		t.Fatalf("unmatched AND filter = %+v, %v", page, err)
	}
	requireLogPagination(t, page.Pagination, 1, 10, 0)
	filter.EndpointKeyID, filter.UserID = &key, int64Ref(logUserTwo)
	page, err = f.repo.ListAdmin(ctx, filter)
	if err != nil || len(page.Data) != 0 {
		t.Fatalf("attempt filter bypassed owner filter: %+v, %v", page, err)
	}
	filter.UserID = nil
	missing := "not-present"
	filter.EndpointBaseURL = &missing
	page, err = f.repo.ListAdmin(ctx, filter)
	if err != nil || len(page.Data) != 0 {
		t.Fatalf("unmatched endpoint filter = %+v, %v", page, err)
	}
	filter.EndpointBaseURL, filter.UpstreamModel = &endpoint, &missing
	page, err = f.repo.ListAdmin(ctx, filter)
	if err != nil || len(page.Data) != 0 {
		t.Fatalf("unmatched upstream filter = %+v, %v", page, err)
	}
}

func int64Ref(value int64) *int64 { return &value }

type canceledLogAuthorizer struct{ err error }

func (a canceledLogAuthorizer) AuthorizeStewardRead(context.Context, *sql.Tx, int64) error {
	return a.err
}

func TestLogContextErrorsKeepTheirIdentityAndHTTPClassification(t *testing.T) {
	f := newNumberedLogFixture(t)
	for _, want := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(want.Error(), func(t *testing.T) {
			_, err := f.repo.ListSteward(context.Background(), 6, ListFilter{}, canceledLogAuthorizer{fmt.Errorf("authorize: %w", want)})
			if !errors.Is(err, want) || errors.Is(err, ErrForbidden) {
				t.Fatalf("authorization cancellation = %v", err)
			}
			recorder := httptest.NewRecorder()
			writeLogError(recorder, err)
			if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), httperr.CodeServiceUnavailable) {
				t.Fatalf("context HTTP result = %d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
	connection, err := f.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	for _, role := range []string{"user", "admin", "steward"} {
		t.Run("waiting_"+role, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			filter := ListFilter{Page: &pagination.Request{Page: 1, Size: 10}}
			var readErr error
			switch role {
			case "user":
				_, readErr = f.repo.ListUser(ctx, logUserOne, filter)
			case "admin":
				_, readErr = f.repo.ListAdmin(ctx, filter)
			case "steward":
				_, readErr = f.repo.ListSteward(ctx, 6, filter, allowLogStewardRead{})
			}
			if !errors.Is(readErr, context.DeadlineExceeded) {
				t.Fatalf("connection wait = %v", readErr)
			}
			recorder := httptest.NewRecorder()
			writeLogError(recorder, readErr)
			if recorder.Code != http.StatusServiceUnavailable {
				t.Fatalf("connection wait HTTP = %d", recorder.Code)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := translateReadError(ctx, errors.New("interrupted (9)")); !errors.Is(err, context.Canceled) {
		t.Fatalf("SQLite interrupt lost cancellation: %v", err)
	}
	for _, err := range []error{ErrForbidden, authz.ErrMFARequired, errors.New("unrelated database failure")} {
		recorder := httptest.NewRecorder()
		writeLogError(recorder, err)
		want := http.StatusForbidden
		if !errors.Is(err, ErrForbidden) && !errors.Is(err, authz.ErrMFARequired) {
			want = http.StatusInternalServerError
		}
		if recorder.Code != want {
			t.Fatalf("existing error %v mapped to %d, want %d", err, recorder.Code, want)
		}
	}
}

func TestLogSQLCountCancellationAndStewardHTTPPrecheck(t *testing.T) {
	f := newNumberedLogFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, _, _, err := logPageQuery(ctx, f.db, `SELECT 1`,
		`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<1000000000) SELECT SUM(x) FROM n`, "", nil,
		&pagination.Request{Page: 1, Size: 10}, 10)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("running SQL count deadline = %v", err)
	}
	for _, failure := range []error{context.Canceled, context.DeadlineExceeded, authz.ErrMFARequired} {
		api, err := NewHTTPAPI(f.repo, canceledLogAuthorizer{failure})
		if err != nil {
			t.Fatal(err)
		}
		recorder := httptest.NewRecorder()
		api.stewardList(recorder, httptest.NewRequest(http.MethodGet, "/api/steward/logs?page=1", nil), UserPrincipal{UserID: 6})
		want := http.StatusServiceUnavailable
		if errors.Is(failure, authz.ErrMFARequired) {
			want = http.StatusForbidden
			if !strings.Contains(recorder.Body.String(), httperr.CodeMFARequired) {
				t.Fatalf("MFA precheck lost classification: %s", recorder.Body.String())
			}
		}
		if recorder.Code != want {
			t.Fatalf("steward precheck %v = %d %s", failure, recorder.Code, recorder.Body.String())
		}
	}
}
