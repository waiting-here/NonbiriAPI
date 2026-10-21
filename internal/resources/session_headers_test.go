package resources

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/sessionheader"
)

func TestPersonalSessionHeaderHTTPDefaultsPatchAndExport(t *testing.T) {
	e := newResourceTestEnvironment(t)
	owner, other := e.seedUser(t, "header-owner"), e.seedUser(t, "header-other")
	api := httpAPI{repository: e.repository}
	var created Model
	for i, tc := range []struct {
		field  string
		status int
	}{
		{`,"session_headers":[{"name":"X-Session","idle_seconds":0,"format":"opencode_session"},{"name":"X-Message"}]`, 201},
		{"", 201}, {`,"session_headers":null`, 400}, {`,"session_headers":[{"name":"Authorization"}]`, 400},
		{`,"session_headers":[{"name":"x"},{"name":"X"}]`, 400}, {`,"session_headers":[{"name":"x","format":"unknown"}]`, 400},
	} {
		req := httptest.NewRequest(http.MethodPost, routeModels, strings.NewReader(fmt.Sprintf(`{"provider":"headers","model":"m%d"%s}`, i, tc.field)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", resourceTestKey(byte('a'+i)))
		out := httptest.NewRecorder()
		api.createModel(out, req, UserPrincipal{UserID: owner})
		if out.Code != tc.status {
			t.Fatal(tc, out.Code, out.Body.String())
		}
		if i == 0 {
			if err := json.Unmarshal(out.Body.Bytes(), &created); err != nil {
				t.Fatal(err)
			}
		}
		if i == 1 && !strings.Contains(out.Body.String(), `"session_headers":[]`) {
			t.Fatal(out.Body.String())
		}
	}
	if len(created.SessionHeaders) != 2 || created.SessionHeaders[0].Name != "x-message" || created.SessionHeaders[0].IdleSeconds != 300 || created.SessionHeaders[1].IdleSeconds != 0 {
		t.Fatal(created)
	}
	id := resourceTestID(t, created.ID)
	if _, err := e.repository.GetModel(t.Context(), other, id); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	name := "renamed"
	in := PatchModelInput{Model: &name, ExpectedRevision: 1}
	got, err := e.repository.PatchModel(t.Context(), owner, id, resourceTestMutation(t, resourceTestKey('p'), http.MethodPatch, routeModel, []int64{id}, in), in)
	if err != nil || len(got.Value.SessionHeaders) != 2 {
		t.Fatal(got, err)
	}
	tx, err := e.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	exported, err := e.repository.ExportLifecycleResources(t.Context(), tx, owner, resourceTestNow, 100)
	tx.Rollback()
	if err != nil || len(exported.Models) != 2 || len(exported.Models[0].SessionHeaders) != 2 || exported.Models[0].SessionHeaders[1].IdleSeconds != 0 {
		t.Fatal(exported, err)
	}
	empty := []sessionheader.Config{}
	in = PatchModelInput{SessionHeaders: &empty, ExpectedRevision: 2}
	mutation := resourceTestMutation(t, resourceTestKey('q'), http.MethodPatch, routeModel, []int64{id}, in)
	for i := range 2 {
		got, err = e.repository.PatchModel(t.Context(), owner, id, mutation, in)
		if err != nil || got.Value.SessionHeaders == nil || len(got.Value.SessionHeaders) != 0 || got.Replayed != (i == 1) {
			t.Fatal(got, err)
		}
	}
	for i, field := range []string{`null`, `[{"name":"x","idle_seconds":-1}]`, `[{"name":"x","extra":1}]`} {
		req := httptest.NewRequest(http.MethodPatch, routeModel, strings.NewReader(`{"expected_revision":"3","session_headers":`+field+"}"))
		req.SetPathValue("id", created.ID)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", resourceTestKey(byte('x'+i)))
		out := httptest.NewRecorder()
		api.patchModel(out, req, UserPrincipal{UserID: owner})
		if out.Code != 400 {
			t.Fatal(out.Code, out.Body.String())
		}
	}
}
