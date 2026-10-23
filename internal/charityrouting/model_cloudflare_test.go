package charityrouting

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestModelCloudflareDefaultPermissionsAndReplay(t *testing.T) {
	e := newRoutingTestEnv(t)
	worker, err := config.ParseCloudflareWorker("https://worker.example/private", "charity-projection-private-token-0123456")
	if err != nil {
		t.Fatal(err)
	}
	stack, err := egress.NewStack(egress.StackOptions{CloudflareWorker: worker})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.CloseIdleConnections()
	e.service.baseURLs = stack
	e.seedUser(t, true, nil)
	level := int64(6)
	steward := e.seedUser(t, false, &level)
	model := e.createModel(t, 'a')
	id, _ := parsePositiveID(model.ID)
	if model.CloudflareProxyEnabled || model.CloudflareProxyMode != "worker" {
		t.Fatal("charity default enabled")
	}
	enabled := true
	input := ModelPatch{CloudflareProxyEnabled: &enabled, ExpectedRevision: "1"}
	mutation := routingMutation(t, 'b', http.MethodPatch, routeStewardModel, []int64{id}, input)
	for index := 0; index < 2; index++ {
		got, err := e.service.PatchSteward(t.Context(), steward, id, mutation, input)
		if err != nil || !got.Value.CloudflareProxyEnabled || got.Value.Revision != "2" || got.Replayed != (index == 1) {
			t.Fatal(got, err)
		}
	}
	if _, err := e.store.DB().Exec(`UPDATE charity_models SET is_mainstream=1 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	page, err := e.service.AutomationModels(t.Context(), steward, "", pagination.Default())
	if err != nil || len(page.Data) != 1 || page.Data[0].CloudflareProxyMode != "worker" || !page.Data[0].CloudflareProxyEnabled {
		t.Fatal(page, err)
	}
	if _, err := e.store.DB().Exec(`UPDATE users SET level=5 WHERE id=?`, steward); err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.PatchSteward(t.Context(), steward, id, mutation, input); !errors.Is(err, ErrForbidden) {
		t.Fatal("revoked replay", err)
	}
	got, err := e.service.GetSteward(t.Context(), steward, id)
	if err != nil || !got.CloudflareProxyEnabled || got.CloudflareProxyMode != "worker" {
		t.Fatal(got, err)
	}
	name := "renamed"
	omitted := ModelPatch{Model: &name, ExpectedRevision: "2"}
	changed, err := e.service.PatchAdmin(t.Context(), id, routingMutation(t, 'c', http.MethodPatch, routeAdminModel, []int64{id}, omitted), omitted)
	if err != nil || !changed.Value.CloudflareProxyEnabled {
		t.Fatal(changed, err)
	}
	disabled := false
	input = ModelPatch{CloudflareProxyEnabled: &disabled, ExpectedRevision: "3"}
	changed, err = e.service.PatchAdmin(t.Context(), id, routingMutation(t, 'd', http.MethodPatch, routeAdminModel, []int64{id}, input), input)
	if err != nil || changed.Value.CloudflareProxyEnabled {
		t.Fatal(changed, err)
	}
	for _, raw := range []string{`{"cloudflare_proxy_enabled":null}`, `{"cloudflare_proxy_enabled":0}`} {
		var create modelCreateWire
		var patch modelPatchWire
		if json.Unmarshal([]byte(raw), &create) == nil || json.Unmarshal([]byte(raw), &patch) == nil {
			t.Fatal("invalid bool accepted", raw)
		}
	}
}

func TestModelCloudflareCharityReplayRetainsOriginalBoolean(t *testing.T) {
	s := &Service{}
	for _, tc := range []struct {
		fields  string
		enabled bool
	}{{"", false}, {`,"cloudflare_proxy_enabled":false,"cloudflare_proxy_mode":"worker"`, false}, {`,"cloudflare_proxy_enabled":true,"cloudflare_proxy_mode":"legacy"`, true}} {
		raw := []byte(`{"id":"7","revision":"11","effective_discount":{"source":"none"}` + tc.fields + `}`)
		decision := idempotency.Decision{HTTPStatus: 200, ResponseBody: raw}
		got, err := s.replayModel(decision)
		if err != nil || got.Value.CloudflareProxyEnabled != tc.enabled || got.Value.CloudflareProxyMode != "direct" || got.Value.Revision != "11" || string(decision.ResponseBody) != string(raw) {
			t.Fatal(got, err)
		}
	}
}
