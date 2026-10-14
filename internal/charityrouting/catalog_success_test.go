package charityrouting

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/observability"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestCatalogSuccessAcrossPagesWithoutWrites(t *testing.T) {
	service, userID := newCatalogSuccessFixture(t, 12, 0)
	tx, err := service.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for index, sample := range []struct {
		modelID int64
		result  string
	}{{1, "success"}, {1, "failure"}, {1, "cancelled"}, {10, "cancelled"}} {
		insertCatalogOutcome(t, tx, int64(index+1), sample.modelID, routingTestNow-1, sample.result)
	}
	for index := range 20 {
		insertCatalogOutcome(t, tx, int64(index+5), 11, routingTestNow-1, "success")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	base := observability.SuccessRate{
		WindowStart: routingTestNow - 86400, AsOf: routingTestNow,
		InsufficientSample: true, CaptureStartedAt: routingTestNow - 3600,
	}
	half, one := 0.5, 1.0
	mixed, cancelled, sufficient := base, base, base
	mixed.Success, mixed.Failure, mixed.Cancelled, mixed.SampleCount, mixed.Rate = 1, 1, 1, 2, &half
	cancelled.Cancelled = 1
	sufficient.Success, sufficient.SampleCount, sufficient.Rate, sufficient.InsufficientSample = 20, 20, &one, false
	want := map[string]observability.SuccessRate{"1": mixed, "10": cancelled, "11": sufficient}
	var before, after int64
	if err := service.db.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, pageNumber := range []int64{1, 99} {
		page, err := service.Catalog(context.Background(), userID, CatalogFilter{}, pagination.Request{Page: pageNumber, Size: 10})
		if err != nil {
			t.Fatal(err)
		}
		wantPage, wantModels, firstModel := "1", 10, "model-000"
		if pageNumber == 99 {
			wantPage, wantModels, firstModel = "2", 2, "model-010"
		}
		if page.Pagination.Page != wantPage || page.Pagination.TotalItems != "12" || page.Pagination.TotalPages != "2" ||
			len(page.Models) != wantModels || page.Models[0].Model != firstModel || page.ServerNow != routingTestNow {
			t.Fatalf("page: %+v", page)
		}
		for _, model := range page.Models {
			expected, ok := want[model.ID]
			if !ok {
				expected = base
			}
			if !reflect.DeepEqual(model.RecentSuccess, expected) || model.Availability != "no_usable_key" {
				t.Fatalf("model %s: got %+v, want %+v", model.ID, model, expected)
			}
		}
	}
	if err := service.db.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil || before != after {
		t.Fatalf("catalog wrote state: before=%d after=%d err=%v", before, after, err)
	}
}

func TestEmptyCatalogDoesNotRequireSuccessState(t *testing.T) {
	service, userID := newCatalogSuccessFixture(t, 1, 0)
	if _, err := service.db.Exec(`DELETE FROM observability_state WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	page, err := service.Catalog(context.Background(), userID, CatalogFilter{Query: "no such model"}, pagination.Default())
	if err != nil || len(page.Models) != 0 || page.Pagination.TotalItems != "0" || page.Pagination.TotalPages != "1" {
		t.Fatalf("empty catalog: %+v %v", page, err)
	}
}

func insertCatalogOutcome(t *testing.T, tx *sql.Tx, sequence, modelID, at int64, result string) {
	t.Helper()
	requestID := fmt.Sprintf("req_%021dA", sequence)
	if _, err := tx.Exec(`INSERT INTO logical_requests(id,route_kind,model_snapshot,state,attempt_limit,caller_result_class,caller_status,accounting_state,settlement_destination,ledger_rows_remaining,created_at,terminal_at)
VALUES(?,'charity_chat_completions','synthetic model','terminal',1,'success',200,'none','user',zeroblob(16),?,?)`, requestID, at, at); err != nil {
		t.Fatal(err)
	}
	log, err := tx.Exec(`INSERT INTO request_logs(logical_request_id,route_kind,started_at,completed_at,caller_result_class,caller_status,status_code)
VALUES(?,'charity_chat_completions',?,?,'success',200,200)`, requestID, at, at)
	if err != nil {
		t.Fatal(err)
	}
	id, err := log.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO charity_request_outcomes(request_log_id,model_id,completed_at,dispatched,result) VALUES(?,?,?,1,?)`, id, modelID, at, result); err != nil {
		t.Fatal(err)
	}
}
