package donationquota

import (
	"strings"
	"testing"
)

func TestModelSplitLimitLookupUsesPartialIndex(t *testing.T) {
	q := newQuotaDB(t)
	if _, err := q.Exec(`WITH RECURSIVE names(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM names WHERE n<24)
INSERT INTO donation_model_quotas(donation_key_id,upstream_model_id,created_at) SELECT 1,'model-'||n,? FROM names`, testNow); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Exec(`UPDATE donation_keys SET input_token_reserve=2,output_token_reserve=3 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Exec(`UPDATE donation_model_quotas SET input_token_limit_mag=nbi_u128(100) WHERE donation_key_id=1 AND upstream_model_id='model-1'`); err != nil {
		t.Fatal(err)
	}
	budget, err := ReadTokenBudget(t.Context(), q, 1)
	if err != nil || budget.Reservation.Total != 5 {
		t.Fatal(budget, err)
	}
	rows, err := q.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+tokenBudgetQuery, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plans []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plans = append(plans, detail)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	plan := strings.Join(plans, "\n")
	if !strings.Contains(plan, "idx_donation_model_split_limits") {
		t.Fatal("model split-limit presence lookup lost its bounded index", plan)
	}
	t.Log(plan)
}
