package logapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestSearchMatchesDeepJumpAndRollingRetention(t *testing.T) {
	m, err := newSearchMatches()
	if err != nil {
		t.Fatal(err)
	}
	defer m.close()
	const n = searchBlockRows*4 + 13
	for id := int64(n); id > 0; id-- {
		if err := m.add(id, id%4); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	for now := int64(0); now <= 4; now++ {
		var want []int64
		for id := int64(n); id > 0; id-- {
			if id%4 == 0 || id%4 > now {
				want = append(want, id)
			}
		}
		for _, pg := range []int64{1, 7, 14, pagination.MaxPage} {
			request := pagination.Request{Page: pg, Size: 100}
			ids, meta, err := m.window(t.Context(), request, now)
			wantMeta, offset, _ := request.Window(int64(len(want)))
			wantIDs := want[offset:min(int(offset)+request.Size, len(want))]
			if err != nil || meta != wantMeta || !slices.Equal(ids, wantIDs) {
				t.Fatalf("window at %d page %d: %+v (%d IDs), %v", now, pg, meta, len(ids), err)
			}
		}
	}
	// An earlier page read may reach the scratch file after a later one.
	_, meta, err := m.window(t.Context(), pagination.Default(), 2)
	if err != nil || meta.TotalItems != "515" {
		t.Fatalf("out-of-order read restored expired rows: %+v, %v", meta, err)
	}
}

func TestSearchQueueCancelAndClose(t *testing.T) {
	repo := &Repository{}
	t.Cleanup(repo.CloseSearches)
	owner := logSearchOwner{role: "user", actorID: 123, session: "a"}
	started, stopped := make(chan struct{}), make(chan struct{})
	first, err := repo.searches.start(t.Context(), owner, func(ctx context.Context, _ func(int64, int64) error) error {
		close(started)
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	queued := make(chan struct{})
	collect := func(_ context.Context, add func(int64, int64) error) error {
		close(queued)
		return add(7, 0)
	}
	second, err := repo.searches.start(t.Context(), owner, collect)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.searches.start(t.Context(), owner, collect); !errors.Is(err, errSearchBusy) {
		t.Fatalf("queue admission: %v", err)
	}
	other := owner
	other.session = "b"
	repo.searches.forget(first, other)
	if _, err := repo.searches.lookup(first, other); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-session lookup: %v", err)
	}
	repo.searches.forget(first, owner)
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("active search was not canceled")
	}
	select {
	case <-queued:
	case <-time.After(2 * time.Second):
		t.Fatal("queued search was not admitted after cancellation")
	}
	repo.CloseSearches()
	if _, err := repo.searches.lookup(second, owner); !errors.Is(err, ErrNotFound) {
		t.Fatalf("shutdown kept result: %v", err)
	}
}

func TestHTTPLogSearchContinuesAfterFiveSeconds(t *testing.T) {
	f, reader := newSeparateLogReaders(t, logReadAuthFunc(func(ctx context.Context, tx *sql.Tx, role string, id int64) error {
		return authorizePageAccount(ctx, tx, role, id, 1_000_000)
	}))
	t.Cleanup(f.repo.CloseSearches)
	api, err := NewHTTPAPI(f.repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	held, err := reader.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	request := httptest.NewRequest(http.MethodGet, "/api/logs?status=201&page=1&page_size=10", nil)
	request.Header.Set("Prefer", "respond-async")
	response := httptest.NewRecorder()
	start := time.Now()
	api.userList(response, request, UserPrincipal{UserID: logUserOne})
	var pending struct {
		Status string `json:"status"`
		ID     string `json:"search_id"`
	}
	if response.Code != http.StatusAccepted || json.Unmarshal(response.Body.Bytes(), &pending) != nil ||
		pending.Status != "running" || pending.ID == "" || time.Since(start) < logPageTimeout {
		t.Fatalf("promotion after %s: %d %s", time.Since(start), response.Code, response.Body)
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	scope := logSearchScope{role: "user", actorID: logUserOne}
	poll := func(method, params string, scope logSearchScope) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/log-searches/"+pending.ID+params, nil)
		r.SetPathValue("search_id", pending.ID)
		w := httptest.NewRecorder()
		api.searchResource(w, r, scope)
		return w
	}
	var page struct {
		Pagination *pagination.Metadata `json:"pagination"`
		Data       []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	end := time.Now().Add(4 * time.Second)
	for {
		w := poll(http.MethodGet, "?page=99&page_size=10", scope)
		if w.Code == http.StatusOK {
			if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
			break
		}
		if w.Code != http.StatusAccepted || time.Now().After(end) {
			t.Fatalf("poll: %d %s", w.Code, w.Body)
		}
		time.Sleep(time.Millisecond)
	}
	requireLogPagination(t, page.Pagination, 3, 10, 23)
	if len(page.Data) != 3 || page.Data[0].ID != logOpaqueID("req_", 102) {
		t.Fatalf("wrong last page: %+v", page.Data)
	}
	f.insertLog(150, logOpaqueID("req_", 150), logUserOne, "later", string(RouteOpenAIChat),
		string(ResultSuccess), 201, nil, 1000, 1010, [4]int64{}, 0, 0)
	w := poll(http.MethodGet, "?page=1&page_size=10", scope)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Pagination.TotalItems != "23" || page.Data[0].ID != logOpaqueID("req_", 122) {
		t.Fatalf("new insertion changed captured membership: %d %s", w.Code, w.Body)
	}
	if w := poll(http.MethodGet, "", logSearchScope{role: "user", actorID: logUserTwo}); w.Code != 404 {
		t.Fatalf("cross-owner poll: %d", w.Code)
	}
	f.mustExec("UPDATE users SET is_banned=1 WHERE id=?", logUserOne)
	if w := poll(http.MethodGet, "", scope); w.Code != 403 {
		t.Fatalf("revoked reader: %d %s", w.Code, w.Body)
	}
	if w := poll(http.MethodDelete, "", scope); w.Code != 204 {
		t.Fatalf("cancel: %d %s", w.Code, w.Body)
	}
	f.mustExec("UPDATE users SET is_banned=0 WHERE id=?", logUserOne)
	if w := poll(http.MethodGet, "", scope); w.Code != 404 {
		t.Fatalf("deleted result: %d %s", w.Code, w.Body)
	}
}

func TestCancelStopsAnAuthorizedSearchPollWaitingForItsRead(t *testing.T) {
	f := newNumberedLogFixture(t)
	t.Cleanup(f.repo.CloseSearches)
	entered := make(chan struct{})
	f.repo.readAuth = logReadAuthFunc(func(ctx context.Context, _ *sql.Tx, _ string, _ int64) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	})
	api, err := NewHTTPAPI(f.repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	scope := logSearchScope{role: "user", actorID: logUserOne}
	id, err := f.repo.searches.start(t.Context(), searchOwner(t.Context(), scope), func(ctx context.Context, _ func(int64, int64) error) error {
		<-ctx.Done()
		return ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	call := func(method string) int {
		req := httptest.NewRequestWithContext(t.Context(), method, "/api/log-searches/"+id, nil)
		req.SetPathValue("search_id", id)
		response := httptest.NewRecorder()
		api.searchResource(response, req, scope)
		return response.Code
	}
	status := make(chan int, 1)
	go func() { status <- call(http.MethodGet) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("poll never reached authorization")
	}
	if code := call(http.MethodDelete); code != http.StatusNoContent {
		t.Fatalf("cancel status = %d", code)
	}
	select {
	case code := <-status:
		if code != http.StatusNotFound {
			t.Fatalf("canceled poll returned %d", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled poll continued waiting")
	}
}
