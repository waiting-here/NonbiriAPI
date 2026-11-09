package donationquota

import (
	"database/sql"
	"errors"
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
	if _, err := q.Exec(`UPDATE donation_keys SET input_token_reserve=NULL,output_token_reserve=NULL WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTokenBudget(t.Context(), q, 1); !errors.Is(err, ErrInvariant) {
		t.Fatal("model split limit accepted a missing reservation pair", err)
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
	if !strings.Contains(plan, "idx_donation_model_split_limits") || !strings.Contains(plan, "idx_donation_quota_rules_key_order") {
		t.Fatal("split-limit presence lookup lost its partial indexes", plan)
	}
	t.Log(plan)
}

func TestTokenBudgetChecksActiveSplitRulesAcrossModels(t *testing.T) {
	for _, metric := range []string{"input_tokens", "output_tokens"} {
		t.Run(metric, func(t *testing.T) {
			q := newQuotaDB(t)
			if _, err := q.Exec(`UPDATE donation_keys SET token_reserve=99,input_token_reserve=2,output_token_reserve=3 WHERE id=1`); err != nil {
				t.Fatal(err)
			}
			if err := transaction(t, q, func(tx *sql.Tx) error {
				return ReplaceModel(t.Context(), tx, 1, testNow, "another-model", []RuleInput{rule(metric, "100")})
			}); err != nil {
				t.Fatal(err)
			}
			budget, err := ReadTokenBudget(t.Context(), q, 1)
			if err != nil || budget.Reservation.Total != 5 || budget.Reservation.Input == nil || budget.Reservation.Output == nil {
				t.Fatal("configured reservation pair changed", budget, err)
			}
			if _, err := q.Exec(`UPDATE donation_keys SET input_token_reserve=NULL,output_token_reserve=NULL WHERE id=1`); err == nil {
				t.Fatal("another model's active split rule allowed its reservation pair to be removed")
			}
			if err := transaction(t, q, func(tx *sql.Tx) error {
				return ReplaceModel(t.Context(), tx, 1, testNow+1, "another-model", nil)
			}); err != nil {
				t.Fatal(err)
			}
			var retained int
			if err := q.QueryRow(`SELECT count(*) FROM donation_quota_rules WHERE donation_key_id=1 AND current_epoch IS NULL`).Scan(&retained); err != nil || retained != 1 {
				t.Fatal("retired rule fixture disappeared", retained, err)
			}
			if _, err := q.Exec(`UPDATE donation_keys SET input_token_reserve=NULL,output_token_reserve=NULL WHERE id=1`); err != nil {
				t.Fatal(err)
			}
			budget, err = ReadTokenBudget(t.Context(), q, 1)
			if err != nil || budget.Reservation.Total != 99 || budget.Reservation.Input != nil || budget.Reservation.Output != nil {
				t.Fatal("retired rule still required a split reservation", budget, err)
			}
		})
	}
}
