package resources

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestBrowseBatchesMatchIndividualQueries(t *testing.T) {
	env := newResourceTestEnvironment(t)
	owner, other := env.seedUser(t, "batch-owner"), env.seedUser(t, "batch-other")
	ep := env.createEndpoint(t, owner, resourceTestKey('a'))
	id := resourceTestID(t, ep.ID)
	key := env.createEndpointKey(t, owner, id, resourceTestKey('b'))
	_ = env.createEndpointKey(t, owner, id, resourceTestKey('c'))
	seedBrowseModels(t, env, owner, other, resourceTestID(t, key.ID), 44)
	for _, enabled := range []int{1, 0} {
		if _, err := env.store.DB().Exec(`UPDATE endpoint_keys SET enabled=? WHERE id=?`, enabled, key.ID); err != nil {
			t.Fatal(err)
		}
		endpoints, err := env.repository.ListEndpointsPage(t.Context(), owner, pagination.Default())
		if err != nil {
			t.Fatal(err)
		}
		keys, err := env.repository.ListEndpointKeysPage(t.Context(), owner, id, pagination.Default())
		if err != nil {
			t.Fatal(err)
		}
		tx, err := env.store.ReadDB().BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, endpoint := range endpoints.Data {
			want, err := baselineEndpointBrowseTx(t.Context(), tx, owner, endpoint)
			if err != nil || !reflect.DeepEqual(want, endpoint.Browse) {
				t.Fatalf("endpoint batch mismatch: got=%+v want=%+v err=%v", endpoint.Browse, want, err)
			}
		}
		for _, item := range keys.Data {
			want, err := baselineEndpointKeyBrowseTx(t.Context(), tx, owner, id, item)
			if err != nil || !reflect.DeepEqual(want, item.Browse) {
				t.Fatalf("key batch mismatch: got=%+v want=%+v err=%v", item.Browse, want, err)
			}
		}
		if enabled == 1 {
			for _, plan := range []struct {
				sql  string
				args []any
			}{
				{keyBrowseFactsSQL, []any{owner, id, fmt.Sprintf("[%s]", key.ID)}},
				{keyBrowseCountsSQL, []any{fmt.Sprintf(`[{"id":"%s","mask":1}]`, key.ID), owner, owner, id}},
				{keyBrowsePreviewsSQL, []any{owner, id, fmt.Sprintf("[%s]", key.ID), owner, owner, owner}},
			} {
				rows, err := tx.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+plan.sql, plan.args...)
				if err != nil {
					t.Fatal(err)
				}
				for rows.Next() {
					var a, b, c int
					var detail string
					if err := rows.Scan(&a, &b, &c, &detail); err != nil {
						t.Fatal(err)
					}
					t.Log(detail)
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				rows.Close()
			}
		}
		tx.Rollback()
	}
}

func TestEndpointBrowseBatchScale(t *testing.T) {
	if os.Getenv("NONBIRI_QUERY_RECHECK_SCALE") != "1" {
		t.Skip("opt-in paired query scale check")
	}
	for _, count := range []int{10000, 100000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			env := newResourceTestEnvironment(t)
			owner := env.seedUser(t, "batch-scale")
			tx, err := env.store.DB().BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			stmt, err := tx.Prepare(`INSERT INTO endpoints(user_id,connector_type,base_url,native_service_mask,capability_origin,created_at,updated_at) VALUES(?,'openai-compatible',?,1,'legacy',?,?)`)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < count; i++ {
				if _, err := stmt.Exec(owner, fmt.Sprintf("https://batch-%d.example/v1", i), resourceTestNow, resourceTestNow); err != nil {
					t.Fatal(err)
				}
			}
			stmt.Close()
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			page, err := env.repository.ListEndpointsPage(t.Context(), owner, pagination.Request{Page: pagination.MaxPage, Size: 100})
			if err != nil {
				t.Fatal(err)
			}
			tx, err = env.store.ReadDB().BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			for run := 0; run < 3; run++ {
				start := time.Now()
				for _, item := range page.Data {
					if _, err := baselineEndpointBrowseTx(t.Context(), tx, owner, item); err != nil {
						t.Fatal(err)
					}
				}
				before := time.Since(start)
				start = time.Now()
				if err := attachEndpointBrowseTx(t.Context(), tx, owner, page.Data); err != nil {
					t.Fatal(err)
				}
				t.Logf("rows=%d page=100 run=%d individual=%v batch=%v statements=100->1", count, run, before, time.Since(start))
			}
			plan, err := tx.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+endpointBrowsePageSQL, owner, "[1,2,3]", owner, owner)
			if err != nil {
				t.Fatal(err)
			}
			var details []string
			for plan.Next() {
				var a, b, c int
				var d string
				if err := plan.Scan(&a, &b, &c, &d); err != nil {
					t.Fatal(err)
				}
				details = append(details, d)
			}
			if err := plan.Err(); err != nil {
				t.Fatal(err)
			}
			plan.Close()
			t.Log(strings.Join(details, "\n"))
		})
	}
}
