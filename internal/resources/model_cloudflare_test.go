package resources

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestModelCloudflareDefaultsStrictHTTPAndExport(t *testing.T) {
	e := newResourceTestEnvironment(t)
	worker, err := config.ParseCloudflareWorker("https://worker.example/private", "model-projection-private-token-0123456")
	if err != nil {
		t.Fatal(err)
	}
	stack, err := egress.NewStack(egress.StackOptions{CloudflareWorker: worker})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.CloseIdleConnections()
	e.repository.baseURLs = stack
	owner := e.seedUser(t, "cloudflare-owner")
	other := e.seedUser(t, "cloudflare-other")
	api := httpAPI{repository: e.repository}
	var original Model
	for index, tc := range []struct {
		field  string
		want   bool
		status int
	}{
		{"", true, 201},
		{`,"cloudflare_proxy_enabled":false`, false, 201},
		{`,"cloudflare_proxy_enabled":true`, true, 201},
		{`,"cloudflare_proxy_enabled":null`, false, 400},
		{`,"cloudflare_proxy_enabled":1`, false, 400},
	} {
		body := fmt.Sprintf(`{"provider":"proxy","model":"model-%d"%s}`, index, tc.field)
		r := httptest.NewRequest(http.MethodPost, routeModels, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", resourceTestKey(byte('a'+index)))
		w := httptest.NewRecorder()
		api.createModel(w, r, UserPrincipal{UserID: owner})
		if w.Code != tc.status {
			t.Fatal(tc, w.Code, w.Body.String())
		}
		if w.Code == 201 {
			var got Model
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.CloudflareProxyEnabled != tc.want || got.CloudflareProxyMode != "worker" {
				t.Fatal(got, err)
			}
			if index == 0 {
				original = got
			}
		}
	}
	id := resourceTestID(t, original.ID)
	disabled := false
	input := PatchModelInput{CloudflareProxyEnabled: &disabled, ExpectedRevision: 1}
	mutation := resourceTestMutation(t, resourceTestKey('p'), http.MethodPatch, routeModel, []int64{id}, input)
	for index := 0; index < 2; index++ {
		got, err := e.repository.PatchModel(t.Context(), owner, id, mutation, input)
		if err != nil || got.Value.CloudflareProxyEnabled || got.Value.Revision != "2" || got.Replayed != (index == 1) {
			t.Fatal(got, err)
		}
	}
	name := "renamed"
	omitted := PatchModelInput{Model: &name, ExpectedRevision: 2}
	got, err := e.repository.PatchModel(t.Context(), owner, id, resourceTestMutation(t, resourceTestKey('q'), http.MethodPatch, routeModel, []int64{id}, omitted), omitted)
	if err != nil || got.Value.CloudflareProxyEnabled {
		t.Fatal(got, err)
	}
	if _, err := e.repository.GetModel(t.Context(), other, id); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	page, err := e.repository.AutomationModels(t.Context(), owner, "", pagination.Default())
	if err != nil || len(page.Data) != 3 {
		t.Fatal(page, err)
	}
	for _, model := range page.Data {
		if model.CloudflareProxyMode != "worker" {
			t.Fatal("unbound model did not report global mode", model)
		}
	}
	automated, err := e.repository.AutomationModel(t.Context(), owner, id)
	if err != nil || automated.CloudflareProxyEnabled || automated.CloudflareProxyMode != "worker" {
		t.Fatal(automated, err)
	}
	r := httptest.NewRequest(http.MethodPatch, routeModel, strings.NewReader(`{"expected_revision":"3","cloudflare_proxy_enabled":null}`))
	r.SetPathValue("id", original.ID)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", resourceTestKey('r'))
	w := httptest.NewRecorder()
	api.patchModel(w, r, UserPrincipal{UserID: owner})
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	tx, err := e.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	exported, err := e.repository.ExportLifecycleResources(t.Context(), tx, owner, resourceTestNow, 100)
	if err != nil || len(exported.Models) != 3 || exported.Models[0].CloudflareProxyEnabled || exported.Models[1].CloudflareProxyEnabled || !exported.Models[2].CloudflareProxyEnabled {
		t.Fatal(exported, err)
	}
}

func TestModelCloudflareReplayCompatibilityIncludesNestedModels(t *testing.T) {
	r := &Repository{}
	for _, tc := range []struct {
		fields  string
		enabled bool
	}{{"", true}, {`,"cloudflare_proxy_enabled":false,"cloudflare_proxy_mode":"worker"`, false}, {`,"cloudflare_proxy_enabled":true,"cloudflare_proxy_mode":"legacy"`, true}} {
		raw := []byte(`{"id":"7","revision":"11","binding_revision":"3"` + tc.fields + `}`)
		decision := idempotency.Decision{HTTPStatus: 200, ResponseBody: raw}
		got, err := r.replayModelMutation(decision)
		if err != nil || got.Value.CloudflareProxyEnabled != tc.enabled || got.Value.CloudflareProxyMode != "direct" || got.Value.Revision != "11" || string(decision.ResponseBody) != string(raw) {
			t.Fatal(got, err)
		}
		nested := []byte(`{"entries":[],"affected_models":[{"model":` + string(raw) + `,"bindings":[]}]}`)
		decision.ResponseBody = nested
		updated, err := r.replayManualModelUpdate(decision)
		if err != nil || len(updated.Value.AffectedModels) != 1 {
			t.Fatal(updated, err)
		}
		model := updated.Value.AffectedModels[0].Model
		if model.CloudflareProxyEnabled != tc.enabled || model.CloudflareProxyMode != "direct" || model.BindingRevision != "3" || string(decision.ResponseBody) != string(nested) {
			t.Fatal(model)
		}
	}
}
