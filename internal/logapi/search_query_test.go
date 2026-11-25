package logapi

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func fixedSearchWindow(ids []int64, meta pagination.Metadata) func(int64) ([]int64, pagination.Metadata, error) {
	return func(int64) ([]int64, pagination.Metadata, error) { return ids, meta, nil }
}

func TestLogSearchSnapshotAndCurrentProjection(t *testing.T) {
	f, reader := newSeparateLogReaders(t, logReadAuthFunc(func(ctx context.Context, tx *sql.Tx, role string, id int64) error {
		return authorizePageAccount(ctx, tx, role, id, 1_000_000)
	}))
	ctx := t.Context()
	request := pagination.Request{Page: 2, Size: 10}
	status := 201
	scope := logSearchScope{role: "user", actorID: logUserOne}
	filter := ListFilter{Status: &status, Page: &request}
	var ids []int64
	err := f.repo.collectSearchIDs(ctx, scope, filter, func(id, expiresAt int64) error {
		if expiresAt != 1010+requestLogRetentionSeconds {
			t.Fatalf("unexpected expiry %d", expiresAt)
		}
		if len(ids) == 0 {
			// A WAL writer can modify membership while the worker is streaming.
			f.mustExec("UPDATE request_logs SET caller_status=202 WHERE id=100")
			f.insertLog(123, logOpaqueID("req_", 123), logUserOne, "addition", string(RouteOpenAIChat), string(ResultSuccess), 201, nil, 1000, 1010, [4]int64{}, 0, 0)
		}
		ids = append(ids, id)
		return nil
	})
	if err != nil || len(ids) != 23 {
		t.Fatal(ids, err)
	}
	for index, id := range ids {
		if id != int64(122-index) {
			t.Fatalf("snapshot order %v", ids)
		}
	}
	metadata, offset, err := request.Window(int64(len(ids)))
	if err != nil {
		t.Fatal(err)
	}
	pageIDs := ids[offset : offset+int64(request.Size)]
	entry, err := f.repo.readSearchPage(ctx, scope, fixedSearchWindow(pageIDs, metadata))
	if err != nil {
		t.Fatal(err)
	}
	page := entry.(Page[UserLogRow])
	requireLogPagination(t, page.Pagination, 2, 10, 23)
	if len(page.Data) != 10 || page.Data[0].(UserSelfLogRow).ID != logOpaqueID("req_", 112) {
		t.Fatal(page)
	}
	// Membership and order are fixed, while the visible fields are current.
	entry, err = f.repo.readSearchPage(ctx, scope, fixedSearchWindow([]int64{100}, metadata))
	if err != nil || *entry.(Page[UserLogRow]).Data[0].(UserSelfLogRow).CallerStatus != 202 {
		t.Fatal(entry, err)
	}
	f.mustExec("UPDATE request_logs SET user_id=? WHERE id=100", logUserTwo)
	if _, err = f.repo.readSearchPage(ctx, scope, fixedSearchWindow([]int64{100}, metadata)); !errors.Is(err, ErrSearchExpired) {
		t.Fatal("owner change", err)
	}
	f.clock.Advance(time.Duration(1010+requestLogRetentionSeconds-f.clock.Now().Unix()) * time.Second)
	if _, err = f.repo.readSearchPage(ctx, scope, fixedSearchWindow(pageIDs, metadata)); !errors.Is(err, ErrSearchExpired) {
		t.Fatal("retention boundary", err)
	}
	f.mustExec("UPDATE users SET is_banned=1 WHERE id=?", logUserOne)
	if err = f.repo.authorizeSearch(ctx, scope); !errors.Is(err, ErrForbidden) {
		t.Fatal("status authorization", err)
	}
	if _, err = f.repo.readSearchPage(ctx, scope, fixedSearchWindow(nil, metadata)); !errors.Is(err, ErrForbidden) {
		t.Fatal("empty-page authorization", err)
	}
	if n := reader.Stats().InUse; n != 0 {
		t.Fatalf("readers held: %d", n)
	}
}

func TestLogSearchHistoricalModelsAndIndependentAttemptMatches(t *testing.T) {
	f := newNumberedLogFixture(t)
	ctx := t.Context()
	model := "historical_%"
	f.mustExec("UPDATE request_logs SET model='Historical_% retired' WHERE id=2")
	// The key matches the first attempt and the URL and model match the second.
	f.mustExec("UPDATE request_attempts SET endpoint_key_id_snapshot=989 WHERE request_log_id=2 AND attempt_seq=1")
	f.mustExec("UPDATE request_attempts SET canonical_base_url='https://special.example/v1' WHERE request_log_id=2 AND attempt_seq=2")
	key, endpoint, upstream := int64(989), "https://special.example/v1", "charity-up-2"
	filter := ListFilter{CharityModel: &model, EndpointKeyID: &key, EndpointBaseURL: &endpoint, UpstreamModel: &upstream}
	for _, scope := range []logSearchScope{{role: "admin"}, {role: "steward", actorID: 999, steward: allowLogStewardRead{}}} {
		var ids []int64
		err := f.repo.collectSearchIDs(ctx, scope, filter, func(id, expiry int64) error {
			ids = append(ids, id)
			if expiry != 220+requestLogRetentionSeconds {
				t.Fatal(expiry)
			}
			return nil
		})
		if err != nil || !slices.Equal(ids, []int64{2}) {
			t.Fatal(scope.role, ids, err)
		}
		meta, _, err := pagination.Default().Window(1)
		if err != nil {
			t.Fatal(err)
		}
		page, err := f.repo.readSearchPage(ctx, scope, fixedSearchWindow(ids, meta))
		if err != nil {
			t.Fatal(err)
		}
		switch scope.role {
		case "admin":
			if page.(Page[AdminLogRow]).Data[0].ID != f.charityID {
				t.Fatal(page)
			}
		case "steward":
			if page.(Page[StewardLogRow]).Data[0].ID != f.charityID {
				t.Fatal(page)
			}
		}
	}
	scope := logSearchScope{role: "steward", actorID: 999, steward: &logStewardTestAuthorizer{results: []error{ErrForbidden}}}
	if err := f.repo.authorizeSearch(ctx, scope); !errors.Is(err, ErrForbidden) {
		t.Fatal("missing steward grant", err)
	}
}

func TestLogSearchPrivacyAndCanceledCollection(t *testing.T) {
	f := domainLogFixture(t)
	scope := logSearchScope{role: "user", actorID: logUserOne}
	meta, _, err := pagination.Default().Window(1)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := f.repo.readSearchPage(t.Context(), scope, fixedSearchWindow([]int64{1}, meta))
	if err != nil || entry.(Page[UserLogRow]).Data[0].(UserSelfLogRow).Model != "[redacted]/[redacted]" {
		t.Fatal(entry, err)
	}
	f.mustExec("INSERT INTO mainstream_channels VALUES('registered',1,'active')")
	f.mustExec("INSERT INTO mainstream_channel_entries VALUES(1,'registered','openai-compatible','https://api.example.com/v1',1)")
	entry, err = f.repo.readSearchPage(t.Context(), scope, fixedSearchWindow([]int64{1}, meta))
	if err != nil || entry.(Page[UserLogRow]).Data[0].(UserSelfLogRow).Model != "example/[redacted]" {
		t.Fatal(entry, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err = f.repo.collectSearchIDs(ctx, scope, ListFilter{}, func(id, expiry int64) error {
		if id != 6 || expiry != 0 {
			t.Fatal("first result should be pending", id, expiry)
		}
		cancel()
		return context.Canceled
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := f.repo.authorizeSearch(t.Context(), scope); err != nil {
		t.Fatal("reader not reusable", err)
	}
}

func TestLogSearchUsesCallerBudgetAfterPromotion(t *testing.T) {
	deadline := time.Now().Add(30 * time.Second)
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	defer cancel()
	var budgets []time.Time
	f, _ := newSeparateLogReaders(t, logReadAuthFunc(func(ctx context.Context, tx *sql.Tx, role string, id int64) error {
		actual, _ := ctx.Deadline()
		budgets = append(budgets, actual)
		return authorizePageAccount(ctx, tx, role, id, 1_000_000)
	}))
	scope := logSearchScope{role: "user", actorID: logUserOne}
	request := pagination.Default()
	filter := ListFilter{Page: &request}
	metadata, _, err := request.Window(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.authorizeSearch(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.collectSearchIDs(ctx, scope, filter, func(int64, int64) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.readSearchPage(ctx, scope, fixedSearchWindow([]int64{1}, metadata)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.ListUser(ctx, logUserOne, filter); err != nil {
		t.Fatal(err)
	}
	if len(budgets) != 4 || !budgets[0].Equal(deadline) || !budgets[1].Equal(deadline) || !budgets[2].Equal(deadline) || budgets[3].IsZero() || budgets[3].After(time.Now().Add(logPageTimeout)) {
		t.Fatal(budgets)
	}
	cancel()
	if _, err := f.repo.readSearchPage(ctx, scope, fixedSearchWindow([]int64{1}, metadata)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestLogSearchSelectsWindowAfterAuthorization(t *testing.T) {
	const expiry = 110 + requestLogRetentionSeconds
	clock := newLogTestClock(expiry - 1)
	f, _ := newSeparateLogReaders(t, logReadAuthFunc(func(ctx context.Context, tx *sql.Tx, role string, id int64) error {
		clock.Advance(time.Second)
		return authorizePageAccount(ctx, tx, role, id, clock.Now().Unix())
	}))
	f.repo.now = clock.Now
	page, err := f.repo.readSearchPage(t.Context(), logSearchScope{role: "user", actorID: logUserOne}, func(now int64) ([]int64, pagination.Metadata, error) {
		// The older completed row just expired while obtaining the snapshot.
		ids := []int64{6}
		if now < expiry {
			ids = append(ids, 1)
		}
		meta, _, err := pagination.Default().Window(int64(len(ids)))
		return ids, meta, err
	})
	if err != nil {
		t.Fatal(err)
	}
	result := page.(Page[UserLogRow])
	requireLogPagination(t, result.Pagination, 1, 20, 1)
	if len(result.Data) != 1 || result.Data[0].(UserSelfLogRow).ID != f.pendingID {
		t.Fatal(result)
	}
}
