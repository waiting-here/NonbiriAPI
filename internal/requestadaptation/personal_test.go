package requestadaptation

import (
	"encoding/json"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"strings"
	"testing"
)

func TestPersonalAdaptationExclusionsAndSafeLifecycle(t *testing.T) {
	store, database := testStore(t)
	if _, err := database.Exec("INSERT INTO models(id,user_id) VALUES(10,1),(20,2)"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{10, 20} {
		patch, err := ParsePatch([]byte(`{"expected_revision":"0","excluded_request_fields":["temperature"],"fixed_headers":{"mode":"replace","values":{"X-Custom":{"action":"replace","value":"private-secret"}}}}`))
		if err != nil {
			t.Fatal(err)
		}
		doc, err := ApplyPatch(Empty(ScopePersonalModel), patch, ScopePersonalModel)
		if err != nil {
			t.Fatal(err)
		}
		tx, _ := database.BeginTx(t.Context(), nil)
		_, err = store.SaveTx(t.Context(), tx, Ref{Scope: ScopePersonalModel, ID: id}, 0, doc, id/10, 100)
		doc.Clear()
		if err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	tx, _ := database.BeginTx(t.Context(), nil)
	defer tx.Rollback()
	exported, err := store.ExportRequestAdaptations(t.Context(), tx, lifecycle.ExportRequest{UserID: 1, Limit: 1})
	if err != nil || len(exported) != 1 {
		t.Fatal(exported, err)
	}
	item := exported[0]
	raw, _ := json.Marshal(item)
	if item.ModelID != "10" || item.EndpointID != "" || item.Modes["body_defaults"] != "inherit" || len(item.ExcludedRequestFields) != 1 || strings.Contains(string(raw), "private-secret") {
		t.Fatalf("unsafe projection: %s", raw)
	}
	if _, err = store.PrepareDelete(t.Context(), tx, lifecycle.DeleteRequest{UserID: 1, Source: lifecycle.DeleteSelf}); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err = tx.QueryRow("SELECT count(*) FROM request_adaptation_audits WHERE scope='personal_model'").Scan(&remaining); err != nil || remaining != 1 {
		t.Fatal(remaining, err)
	}
	tx.Rollback()
	if err = database.QueryRow("SELECT count(*) FROM request_adaptation_audits WHERE scope='personal_model'").Scan(&remaining); err != nil || remaining != 2 {
		t.Fatal("rollback", remaining, err)
	}
}

func TestPersonalAdaptationRejectsProtectedAndConflictingExclusions(t *testing.T) {
	for _, raw := range []string{
		`{"expected_revision":"0","excluded_request_fields":["model"]}`,
		`{"expected_revision":"0","excluded_request_fields":null}`,
		`{"expected_revision":"0","excluded_request_fields":["temperature"],"body_forced":{"mode":"replace","values":{"/temperature":{"action":"replace","value":0.5}}}}`,
	} {
		p, err := ParsePatch([]byte(raw))
		if err == nil {
			_, err = ApplyPatch(Empty(ScopePersonalModel), p, ScopePersonalModel)
		}
		if err == nil {
			t.Fatalf("accepted invalid policy: %s", raw)
		}
	}
	p, err := ParsePatch([]byte(`{"expected_revision":"0","excluded_request_fields":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ApplyPatch(Empty(ScopeEndpoint), p, ScopeEndpoint); err == nil {
		t.Fatal("endpoint accepted model-only exclusions")
	}
}
