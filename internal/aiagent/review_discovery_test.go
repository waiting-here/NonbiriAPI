package aiagent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type discoveryReviewOperations struct{ *fakeOperations }

func (o discoveryReviewOperations) Tools(context.Context, Actor, bool) ([]Tool, error) {
	return []Tool{{Name: "refresh_models", Parameters: json.RawMessage(`{"type":"object","properties":{}}`)}, {Name: "propose_management_operation", ChatOnly: true, Parameters: json.RawMessage(`{"type":"object","properties":{}}`)}}, nil
}
func (o discoveryReviewOperations) Intelligence(context.Context, int64) (json.RawMessage, string, error) {
	raw, _ := json.Marshal(map[string]string{"catalog": o.signature})
	return raw, o.signature, nil
}
func (o discoveryReviewOperations) Read(context.Context, Actor, string, json.RawMessage) (json.RawMessage, error) {
	o.signature = "refreshed-v2"
	return json.RawMessage(`{"status_code":200}`), nil
}
func TestAutomaticDiscoveryRefreshesFactsBeforeNextDecision(t *testing.T) {
	t.Run("current", func(t *testing.T) { testDiscoveryReview(t, false) })
	t.Run("changed_after_model_read", func(t *testing.T) { testDiscoveryReview(t, true) })
}
func testDiscoveryReview(t *testing.T, lateChange bool) {
	f := newFixture(t)
	f.s.config.Operations = discoveryReviewOperations{f.ops}
	settings := defaultSettings()
	settings.Mode = "auto"
	if _, err := f.s.PutSettings(t.Context(), f.actor, SettingsUpdate{Settings: settings}); err != nil {
		t.Fatal(err)
	}
	row, err := f.db.Exec(`INSERT INTO ai_agent_jobs(kind,actor_id,actor_role,actor_label,donation_id,revision_signature,mode,state,created_at,updated_at,intelligence) VALUES('auto_review',1,'admin','reviewer',10,'v1','auto','pending',0,0,'{"catalog":"v1"}')`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := row.LastInsertId()
	outcome, _ := json.Marshal(ReviewResult{Decision: "reject", InternalReason: "Verified current catalog", UserNote: "Please add details", Bindings: []ReviewBinding{}})
	message, _ := json.Marshal(map[string]string{"role": "assistant", "content": string(outcome)})
	f.driver.replies = []string{`{"role":"assistant","tool_calls":[{"id":"refresh","type":"function","function":{"name":"refresh_models","arguments":"{}"}}]}`, string(message)}
	if lateChange {
		f.driver.afterWait = func(id int64) {
			if id == 2 {
				f.ops.signature = "changed-v3"
			}
		}
	}
	if err = f.s.run(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if len(f.driver.inputs) != 2 || !strings.Contains(string(f.driver.inputs[1].Messages), "refreshed-v2") {
		t.Fatal("model did not see refreshed evidence", f.driver.inputs)
	}
	if strings.Contains(string(f.driver.inputs[0].Tools), "propose_management_operation") {
		t.Fatal("automatic write tool exposed")
	}
	want := 1
	if lateChange {
		want = 0
	}
	if count(t, f.db, "test_mutations") != want {
		t.Fatal("refreshed signature was rejected or repeated")
	}
	var signature string
	if err = f.db.QueryRow("SELECT revision_signature FROM ai_agent_jobs WHERE id=?", id).Scan(&signature); err != nil || signature != "refreshed-v2" {
		t.Fatal(signature, err)
	}
}
