package charityrouting

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore/testseed"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestScopedCatalogQueriesUseSelectedMembers(t *testing.T) {
	e := newRoutingTestEnv(t)
	for name, query := range map[string]string{
		"bindings":    adminBindingsQuery(),
		"manual page": manualCatalogSelection() + ` ORDER BY pc.normalized_model_id LIMIT ? OFFSET ?`,
		"support":     catalogSupportQuery(),
	} {
		t.Run(name, func(t *testing.T) {
			args := []any{1, 100}
			if name == "manual page" {
				args = []any{1, 1, 1, 10, 0}
			} else if name == "support" {
				args = []any{1, "selected"}
			}
			rows, err := e.store.ReadDB().QueryContext(t.Context(), `EXPLAIN QUERY PLAN `+query, args...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var details []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				details = append(details, detail)
				if strings.Contains(detail, "charity_model_catalog") || strings.HasPrefix(detail, "SCAN u ") ||
					strings.HasPrefix(detail, "SCAN ac ") || strings.HasPrefix(detail, "SCAN donation_key_manual_models ") ||
					strings.Contains(detail, "idx_discovery_runs_active") {
					t.Fatalf("unrelated catalog enumeration: %s", detail)
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			plan := strings.Join(details, "\n")
			if !strings.Contains(plan, "idx_catalog_run_model") || !strings.Contains(plan, "donation_key_id=?") {
				t.Fatalf("missing member/run lookup: %s", plan)
			}
			t.Log(plan)
		})
	}
}

func TestScopedCatalogRetainsSourceProjection(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	_, member, physical := e.seedCandidate(t, e.caller, 'a', "both")
	_, _, unrelated := e.seedCandidate(t, e.caller, 'b', "outside")
	e.seedAutomaticCatalog(t, physical, "automatic", "both", "both")
	e.seedAutomaticCatalog(t, unrelated, "outside", "absent")
	if _, err := e.store.DB().Exec(`INSERT INTO donation_key_manual_models(donation_key_id,normalized_model_id,display_name,revision,created_at,updated_at) VALUES(?,'manual','Custom label',1,?,?)`, member, routingTestNow, routingTestNow); err != nil {
		t.Fatal(err)
	}
	model := e.createModel(t, 'c')
	modelID, _ := parsePositiveID(model.ID)
	e.seedBinding(t, modelID, member, "both", 0)
	for _, mutation := range []string{"", `UPDATE donation_uses SET enabled=0,revision=revision+1 WHERE donation_key_id=?`, `UPDATE donation_uses SET current_approved_revision_id=NULL,revision=revision+1 WHERE donation_key_id=?`} {
		tx, err := e.store.DB().BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if mutation != "" {
			if _, err := tx.ExecContext(t.Context(), mutation, member); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
		}
		for _, name := range []string{"automatic", "both", "manual", "absent"} {
			var oldAutomatic, oldManual, automatic, manual bool
			if err := tx.QueryRowContext(t.Context(), `SELECT COALESCE(pc.automatic_supports,0)>0,COALESCE(pc.manual_supports,0)>0 FROM donation_keys dk LEFT JOIN charity_model_catalog pc ON pc.donation_key_id=dk.id AND pc.normalized_model_id=? WHERE dk.id=?`, name, member).Scan(&oldAutomatic, &oldManual); err != nil {
				t.Fatal(err)
			}
			if err := tx.QueryRowContext(t.Context(), `SELECT `+automaticCatalogSupportSQL("?")+`,`+manualCatalogSupportSQL("?")+` FROM donation_keys dk WHERE dk.id=?`, name, name, member).Scan(&automatic, &manual); err != nil {
				t.Fatal(err)
			}
			if automatic != oldAutomatic || manual != oldManual {
				t.Fatalf("%s source mismatch: got %v/%v want %v/%v", name, automatic, manual, oldAutomatic, oldManual)
			}
			var supported bool
			if err := tx.QueryRowContext(t.Context(), catalogSupportQuery(), member, name).Scan(&supported); err != nil || supported != (oldAutomatic || oldManual) {
				t.Fatalf("%s binding support=%v err=%v", name, supported, err)
			}
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := e.store.ReadDB().BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	entries, meta, _, err := readManualCandidatesTx(t.Context(), tx, member, "", pagination.Default())
	if err != nil || meta.TotalItems != "3" || len(entries) != 3 {
		t.Fatalf("member page: entries=%+v pagination=%+v err=%v", entries, meta, err)
	}
	got := make([]string, len(entries))
	for i, entry := range entries {
		got[i] = entry.UpstreamModelID + ":" + entry.Source
	}
	if want := []string{"automatic:automatic", "both:both", "manual:manual"}; !reflect.DeepEqual(got, want) || entries[2].DisplayName != "Custom label" {
		t.Fatalf("source page=%v", entries)
	}
	bindings, err := readAdminBindingsTx(t.Context(), tx, modelID)
	if err != nil || len(bindings.Bindings) != 1 || !reflect.DeepEqual(bindings.Bindings[0].SourceTypes, []string{"automatic", "manual"}) {
		t.Fatalf("binding source=%+v err=%v", bindings, err)
	}
	// A concurrent committed edit must not change the existing read snapshot.
	if _, err := e.store.DB().ExecContext(t.Context(), `INSERT INTO donation_key_manual_models(donation_key_id,normalized_model_id,display_name,revision,created_at,updated_at) VALUES(?,'later','later',1,?,?)`, member, routingTestNow, routingTestNow); err != nil {
		t.Fatal(err)
	}
	_, snapshotMeta, _, err := readManualCandidatesTx(t.Context(), tx, member, "", pagination.Default())
	if err != nil || snapshotMeta.TotalItems != "3" {
		t.Fatalf("snapshot changed: %+v err=%v", snapshotMeta, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	fresh, err := e.store.ReadDB().BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Rollback()
	_, meta, _, err = readManualCandidatesTx(t.Context(), fresh, member, "", pagination.Default())
	if err != nil || meta.TotalItems != "4" {
		t.Fatalf("new snapshot missed committed source: %+v err=%v", meta, err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, _, err := readManualCandidatesTx(canceled, fresh, member, "", pagination.Default()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
}

func TestScopedCatalogIgnoresUnrelatedCatalogSize(t *testing.T) {
	e := newRoutingTestEnv(t)
	_, member, _ := e.seedCandidate(t, e.caller, 'a', "selected")
	_, _, unrelated := e.seedCandidate(t, e.caller, 'b', "outside")
	baseline := `SELECT COALESCE(pc.automatic_supports,0)>0,COALESCE(pc.manual_supports,0)>0 FROM donation_keys dk LEFT JOIN charity_model_catalog pc ON pc.donation_key_id=dk.id AND pc.normalized_model_id='selected' WHERE dk.id=?`
	scoped := `SELECT ` + automaticCatalogSupportSQL("'selected'") + `,` + manualCatalogSupportSQL("'selected'") + ` FROM donation_keys dk WHERE dk.id=?`
	for _, count := range []int{1, 1000} {
		models := make([]testseed.DiscoveryModel, count)
		for i := range models {
			models[i].ID = fmt.Sprintf("unrelated-%04d", i)
		}
		tx, err := e.store.DB().BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := testseed.SeedDiscoveryCatalogTx(t.Context(), tx, testseed.DiscoveryCatalogInput{EndpointKeyID: unrelated, ListFormat: "openai", Models: models, At: routingTestNow}); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		for name, query := range map[string]string{"aggregate": baseline, "scoped": scoped} {
			started := time.Now()
			for range 5 {
				var automatic, manual bool
				if err := e.store.ReadDB().QueryRowContext(t.Context(), query, member).Scan(&automatic, &manual); err != nil || !automatic || !manual {
					t.Fatalf("%s: automatic=%v manual=%v err=%v", name, automatic, manual, err)
				}
			}
			t.Logf("unrelated_entries=%d query=%s runs=5 elapsed=%s", count, name, time.Since(started))
		}
	}
}
