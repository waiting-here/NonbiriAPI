package charityrouting

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore/testseed"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func BenchmarkBrowseModelProjection(b *testing.B) {
	for _, models := range []int{100, 327} {
		for _, keys := range []int{1, 16} {
			b.Run(fmt.Sprintf("models=%d/keys=%d", models, keys), func(b *testing.B) {
				env := newBrowseProjectionFixture(b, models, keys)
				for _, narrow := range []bool{false, true} {
					name := "all"
					if narrow {
						name = "narrow"
					}
					b.Run("capability/"+name, func(b *testing.B) {
						if _, err := env.store.DB().Exec(`UPDATE charity_model_access SET allowed_level_mask=CASE WHEN ?=0 OR model_id%10=1 THEN 63 ELSE 0 END`, narrow); err != nil {
							b.Fatal(err)
						}
						want := 0
						for id := 1; id <= models; id++ {
							if id%4 != 0 && (!narrow || id%10 == 1) {
								want++
							}
						}
						read := func() {
							got, err := env.service.Capability(b.Context(), env.caller, routingTestNow)
							if err != nil || len(got.Models) != want || got.State != "available" {
								b.Fatalf("models=%d want=%d state=%s err=%v", len(got.Models), want, got.State, err)
							}
						}
						read()
						b.ReportAllocs()
						b.ResetTimer()
						for b.Loop() {
							read()
						}
					})
				}
				for _, filtered := range []bool{false, true} {
					name := "unfiltered"
					filter := CatalogFilter{}
					if filtered {
						name = "available"
						yes := true
						filter.CurrentlyAvailable = &yes
					}
					b.Run("catalog/"+name, func(b *testing.B) {
						read := func() {
							got, err := env.service.Catalog(b.Context(), env.caller, filter, pagination.Request{Page: 1, Size: 50})
							if err != nil || len(got.Models) != 50 {
								b.Fatalf("catalog count=%d err=%v", len(got.Models), err)
							}
							for _, model := range got.Models {
								if filtered && !model.CurrentlyAvailable {
									b.Fatal("filtered unavailable model")
								}
							}
						}
						read()
						b.ReportAllocs()
						b.ResetTimer()
						for b.Loop() {
							read()
						}
					})
				}
			})
		}
	}
}

// Most models have one source; several have many bindings. The same source
// identities may be reused across models, and recent outcomes are interleaved.
func newBrowseProjectionFixture(t testing.TB, modelCount, keyCount int) *routingTestEnv {
	t.Helper()
	env := newRoutingTestEnv(t)
	owner := env.seedUser(t, false, nil)
	keys := make([]int64, keyCount)
	for i := range keys {
		_, keys[i], _ = env.seedCandidateWithConnector(t, owner, fmt.Sprintf("browse-%d", i), "upstream", contract.TypeOpenAICompatible)
	}
	tx, err := env.store.DB().BeginTx(context.Background(), nil)
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
	for id := 1; id <= modelCount; id++ {
		name := fmt.Sprintf("model-%03d", id)
		exec(`INSERT INTO charity_models(id,provider,model,full_name,enabled,pricing_mode,created_at,updated_at)
VALUES(?,'example',?,?,1,'per_request',?,?)`, id, name, "[公益]example/"+name, routingTestNow, routingTestNow)
		exec(`INSERT INTO charity_model_access(model_id) VALUES(?)`, id)
		bindings := 1
		if id <= 6 {
			bindings = 74
		} else if id <= 50 {
			bindings = 10
		}
		if id%4 == 0 {
			bindings = 0
		}
		for ordinal := 0; ordinal < bindings; ordinal++ {
			if _, err := testseed.InsertCharityBindingTx(context.Background(), tx, testseed.BindingInput{ModelID: int64(id), KeyID: keys[(id+ordinal)%keyCount], UpstreamModelID: "upstream", Order: ordinal, At: routingTestNow}); err != nil {
				t.Fatal(err)
			}
		}
	}
	const outcomesPerModel = 300
	exec(`WITH RECURSIVE samples(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM samples WHERE n<?)
INSERT INTO logical_requests(id,user_id,route_kind,model_snapshot,state,attempt_limit,caller_result_class,caller_status,accounting_state,settlement_destination,ledger_rows_remaining,created_at,terminal_at)
SELECT 'req_'||printf('%021d',n)||'A',?,'charity_chat_completions','synthetic model','terminal',1,'success',200,'none','user',zeroblob(16),?-1,?-1 FROM samples`, modelCount*outcomesPerModel, env.caller, routingTestNow, routingTestNow)
	exec(`INSERT INTO request_logs(logical_request_id,user_id,route_kind,started_at,completed_at,caller_result_class,caller_status,status_code)
SELECT id,user_id,route_kind,created_at,terminal_at,caller_result_class,caller_status,caller_status FROM logical_requests ORDER BY id`)
	exec(`INSERT INTO charity_request_outcomes(request_log_id,model_id,completed_at,dispatched,result)
SELECT id,1+(id-1)%?,completed_at,1,CASE (id-1)/?%3 WHEN 0 THEN 'success' WHEN 1 THEN 'failure' ELSE 'cancelled' END FROM request_logs`, modelCount, modelCount)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return env
}
