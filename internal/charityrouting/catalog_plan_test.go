package charityrouting

import (
	"strings"
	"testing"
)

func TestCatalogAvailabilityStartsWithModelBindings(t *testing.T) {
	env := newRoutingTestEnv(t)
	for _, callerScoped := range []bool{false, true} {
		name := "catalog"
		if callerScoped {
			name = "caller"
		}
		t.Run(name, func(t *testing.T) {
			query := `EXPLAIN QUERY PLAN SELECT cm.id,` + availableCandidateCountSQL("", callerScoped) + `
FROM charity_models cm CROSS JOIN (SELECT ? AS decision_now,1 AS charity_enabled,? AS caller_id,1 AS caller_level,1 AS token_reserve) cx`
			rows, err := env.store.ReadDB().QueryContext(t.Context(), query, routingTestNow, env.caller)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			binding, key := -1, -1
			var plan []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(detail, "SEARCH b ") && strings.Contains(detail, "charity_model_id=?") {
					binding = len(plan)
				}
				if key < 0 && (strings.HasPrefix(detail, "SEARCH dk ") || strings.HasPrefix(detail, "SCAN dk")) {
					key = len(plan)
				}
				plan = append(plan, detail)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if binding < 0 || key < 0 || binding >= key {
				t.Fatalf("availability visits unrelated donation keys before model bindings: %v", plan)
			}
		})
	}
}
