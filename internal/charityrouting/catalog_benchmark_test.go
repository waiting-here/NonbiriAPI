package charityrouting

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

// The fixture has 100 recent outcomes per model; additional history lies
// outside the window. Setup and startup validation are excluded from timing.
func BenchmarkCatalogRecentSuccess(b *testing.B) {
	for _, history := range []int{100, 1000} {
		b.Run(fmt.Sprintf("history_per_model=%d", history), func(b *testing.B) {
			service, userID := newCatalogSuccessFixture(b, 100, history)
			for _, size := range []int{10, 50, 100} {
				b.Run(fmt.Sprintf("page_size=%d", size), func(b *testing.B) {
					page := pagination.Request{Page: 1, Size: size}
					checked, err := service.Catalog(context.Background(), userID, CatalogFilter{}, page)
					if err != nil || len(checked.Models) != size {
						b.Fatalf("fixture catalog: models=%d err=%v", len(checked.Models), err)
					}
					for _, model := range checked.Models {
						rate := model.RecentSuccess
						if rate.Success != 30 || rate.Failure != 30 || rate.Cancelled != 30 || rate.SampleCount != 60 {
							b.Fatalf("fixture outcome counts: %+v", rate)
						}
					}
					b.ReportAllocs()
					b.ResetTimer()
					for b.Loop() {
						result, err := service.Catalog(context.Background(), userID, CatalogFilter{}, page)
						if err != nil || len(result.Models) != size {
							b.Fatalf("catalog: models=%d err=%v", len(result.Models), err)
						}
					}
				})
			}
		})
	}
}

func newCatalogSuccessFixture(t testing.TB, modelCount, historyPerModel int) (*Service, int64) {
	t.Helper()
	master := bytes.Repeat([]byte{0x53}, secret.MasterKeyBytes)
	vault, err := secret.New(master)
	clear(master)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "catalog.sqlite")
	dbfixture.Materialize(t, path, vault)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	database := store.DB()
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	exec := func(query string, args ...any) sql.Result {
		t.Helper()
		result, err := tx.Exec(query, args...)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	exec(`UPDATE site_config SET value='1' WHERE key='charity_enabled'`)
	exec(`UPDATE observability_state SET capture_started_at=?,last_access_gap_at=NULL WHERE id=1`, routingTestNow-3600)
	user := exec(`INSERT INTO users(discord_id,username,donation_credit_mag,total_requests,total_uncached_input_tokens,
total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at)
VALUES('80001','catalog reader',zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),?,?)`, routingTestNow, routingTestNow)
	userID, err := user.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	for index := range modelCount {
		name := fmt.Sprintf("model-%03d", index)
		model := exec(`INSERT INTO charity_models(provider,model,full_name,enabled,pricing_mode,created_at,updated_at)
VALUES('benchmark',?,?,1,'per_request',?,?)`, name, "[公益]benchmark/"+name, routingTestNow, routingTestNow)
		modelID, err := model.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO charity_model_access(model_id) VALUES(?)`, modelID)
	}
	if historyPerModel > 0 {
		exec(`WITH RECURSIVE samples(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM samples WHERE n<?)
INSERT INTO logical_requests(id,user_id,route_kind,model_snapshot,state,attempt_limit,caller_result_class,caller_status,accounting_state,settlement_destination,ledger_rows_remaining,created_at,terminal_at)
SELECT 'req_'||printf('%021d',n)||'A',?,'charity_chat_completions','synthetic model','terminal',1,'success',200,'none','user',zeroblob(16),
CASE WHEN (n-1)%?<100 THEN ?-1 ELSE ?-86401 END,
CASE WHEN (n-1)%?<100 THEN ?-1 ELSE ?-86401 END FROM samples`,
			modelCount*historyPerModel, userID, historyPerModel, routingTestNow, routingTestNow, historyPerModel, routingTestNow, routingTestNow)
		exec(`INSERT INTO request_logs(logical_request_id,user_id,route_kind,started_at,completed_at,caller_result_class,caller_status,status_code)
SELECT id,user_id,route_kind,created_at,terminal_at,caller_result_class,caller_status,caller_status FROM logical_requests ORDER BY id`)
		exec(`INSERT INTO charity_request_outcomes(request_log_id,model_id,completed_at,dispatched,result)
SELECT id,1+(id-1)/?,completed_at,CASE WHEN (id-1)%10=0 THEN 0 ELSE 1 END,
CASE (id-1)%3 WHEN 0 THEN 'success' WHEN 1 THEN 'failure' ELSE 'cancelled' END FROM request_logs`, historyPerModel)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return &Service{db: database, readDB: store.ReadDB(), now: func() time.Time { return time.Unix(routingTestNow, 0) }}, userID
}
