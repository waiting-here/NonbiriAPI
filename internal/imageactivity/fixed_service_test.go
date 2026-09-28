package imageactivity

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
)

type fixedServiceMock struct {
	server                 *httptest.Server
	mode                   atomic.Int32
	posts, polls, catalogs atomic.Int64
	mu                     sync.Mutex
	catalog                []json.RawMessage
	bodies                 []map[string]any
	pollPaths              []string
}

func newFixedServiceMock(t *testing.T, image []byte) *fixedServiceMock {
	t.Helper()
	mock := &fixedServiceMock{catalog: []json.RawMessage{fixedMetadata(`{"customSizeMapping":{"compact":{"1:1":{"width":512,"height":512},"16:9":{"width":768,"height":432}}},"resolution":{"default":"compact"},"aspectRatio":{"default":"1:1"},"promptCharacterLimit":128,"steps":{"max":8,"default":4},"cfgScale":{"scale":[1,1.5,2],"default":1.5},"quality":true}`, `{"negative_prompts":true,"steps":true}`)}}
	items := []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(image)}}
	mock.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if request.Header.Get("Authorization") != "Bearer fixture-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/v1/models":
			mock.catalogs.Add(1)
			mock.mu.Lock()
			catalog := append([]json.RawMessage(nil), mock.catalog...)
			mock.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"data": catalog})
		case request.Method == http.MethodPost && request.URL.Path == "/v1/images/generations":
			mock.posts.Add(1)
			var body map[string]any
			if json.NewDecoder(request.Body).Decode(&body) != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mock.mu.Lock()
			mock.bodies = append(mock.bodies, body)
			mock.mu.Unlock()
			if body["async"] != true || body["response_format"] != "b64_json" || body["model"] != "atelier-test" || body["resolution"] != "compact" || body["aspect_ratio"] == nil || body["size"] == nil || body["cfg_scale"] == nil || body["guidance"] != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			switch mock.mode.Load() {
			case 1, 2:
				_ = json.NewEncoder(w).Encode(map[string]any{"async": true, "task_id": "opaque/../job", "status": "queued"})
			case 3:
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "error", "error": map[string]string{"message": "private synthetic upstream detail"}})
			case 4:
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "unexpected"})
			default:
				_ = json.NewEncoder(w).Encode(map[string]any{"data": items})
			}
		case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/v1/images/jobs/"):
			mock.polls.Add(1)
			mock.mu.Lock()
			mock.pollPaths = append(mock.pollPaths, request.URL.EscapedPath())
			mock.mu.Unlock()
			switch mock.mode.Load() {
			case 1:
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "queued"})
			case 2:
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "running"})
			case 3:
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "error", "error": map[string]string{"message": "private synthetic upstream detail"}})
			case 4:
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "unexpected"})
			default:
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "done", "data": items})
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(mock.server.Close)
	return mock
}

func fixedAdminRequest(t *testing.T, fixture *fixture, routes muxRoutes, method, path string, input any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, adminPrefix+path, bytes.NewReader(body)).WithContext(fixture.ctx(fixture.admin))
	request.Header.Set("Content-Type", "application/json")
	if method != http.MethodGet {
		request.Header.Set("Idempotency-Key", fixture.key())
	}
	output := httptest.NewRecorder()
	routes.mux.ServeHTTP(output, request)
	return output
}

func configureFixedService(t *testing.T) (*fixture, *fixedServiceMock, muxRoutes) {
	t.Helper()
	f := newFixture(t)
	f.configure(t)
	mock := newFixedServiceMock(t, f.upstream.png)
	stack, err := egress.NewStack(egress.StackOptions{AllowedOrigins: []string{f.upstream.server.URL, mock.server.URL}, Resolver: publicResolver{}})
	if err != nil {
		t.Fatal(err)
	}
	if err = stack.AddSelfOrigins(context.Background(), &config.Config{SiteBaseURL: "https://site.example.invalid", UserHost: "site.example.invalid", AdminHost: "admin.example.invalid", ListenAddr: "127.0.0.1:1"}); err != nil {
		t.Fatal(err)
	}
	f.service.config.Egress = stack
	routes := muxRoutes{mux: http.NewServeMux(), user: f.user, admin: f.admin}
	if err = RegisterRoutes(routes, routes, f.service); err != nil {
		t.Fatal(err)
	}
	output := fixedAdminRequest(t, f, routes, http.MethodPut, "/upstream", ConnectionInput{ExpectedRevision: "2", BaseURL: mock.server.URL + "/v1", Secret: SecretInput{Mode: "keep"}})
	if output.Code != http.StatusOK {
		t.Fatalf("minimal connection rejected: %d %s", output.Code, output.Body.String())
	}
	output = fixedAdminRequest(t, f, routes, http.MethodPost, "/models/refresh", struct{}{})
	var accepted RefreshResult
	if output.Code != http.StatusOK || json.Unmarshal(output.Body.Bytes(), &accepted) != nil {
		t.Fatalf("refresh rejected: %d %s", output.Code, output.Body.String())
	}
	f.wait(t, func() bool {
		refresh, err := f.service.GetRefresh(f.ctx(f.admin), f.admin, accepted.Operation.ID)
		return err == nil && (refresh.State == "succeeded" || refresh.State == "failed")
	})
	refresh, err := f.service.GetRefresh(f.ctx(f.admin), f.admin, accepted.Operation.ID)
	if err != nil || refresh.State != "succeeded" {
		t.Fatalf("fixed catalog refresh %+v: %v", refresh, err)
	}
	catalog, err := f.service.ListCatalog(f.ctx(f.admin), f.admin, CatalogQuery{})
	if err != nil || len(catalog.Data) != 1 || catalog.Data[0].CapabilityReadiness != "ready" || catalog.Data[0].CapabilityIssues == nil {
		t.Fatalf("automatic catalog not ready: %+v %v", catalog, err)
	}
	if catalog.Data[0].Configured || catalog.Data[0].CatalogType != "image" || len(catalog.Data[0].Parameters) == 0 {
		t.Fatalf("fresh automatic model lost its discovery state: %+v", catalog.Data[0])
	}
	for _, rule := range catalog.Data[0].Parameters {
		capability := acceptedParameterCapability(t, catalog.Data[0], rule.Key)
		if capability.Source != "discovered" || capability.Overridden || capability.Conflict || (capability.Support == CapabilitySupported) != rule.Supported {
			t.Fatalf("fresh automatic parameter lost its origin: %+v", capability)
		}
	}
	f.model = catalog.Data[0].ID
	output = fixedAdminRequest(t, f, routes, http.MethodPut, "/models/"+f.model, ModelSettingsInput{ExpectedRevision: "0", Enabled: true, Price: Price{"2", "1"}})
	if output.Code != http.StatusOK {
		t.Fatalf("minimal model settings rejected: %d %s", output.Code, output.Body.String())
	}
	return f, mock, routes
}

func fixedSubmit(t *testing.T, f *fixture, n int) Task {
	t.Helper()
	model, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.service.Submit(f.ctx(f.user), f.user, f.key(), SubmitInput{ModelID: f.model, ExpectedModelRevision: model.Revision, ExpectedPricingRevision: model.PricingRevision, Prompt: "Synthetic prompt 😀", N: &n})
	if err != nil {
		t.Fatal(err)
	}
	return result.Value.Task
}

func TestFixedServiceMinimalHTTPConfigurationAndSynchronousGeneration(t *testing.T) {
	f, mock, routes := configureFixedService(t)
	model, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil {
		t.Fatal(err)
	}
	input := ModelSettingsCheck{ModelID: f.model, Draft: ModelSettingsInput{ExpectedRevision: model.Revision, Enabled: true, Price: model.Price}, Parameters: SubmitInput{Prompt: "Synthetic preview"}}
	output := fixedAdminRequest(t, f, routes, http.MethodPost, "/models/check", input)
	var check CheckResult
	if output.Code != http.StatusOK || json.Unmarshal(output.Body.Bytes(), &check) != nil || !check.Valid || mock.posts.Load() != 0 {
		t.Fatalf("local preview generated a request or failed: %d %s", output.Code, output.Body.String())
	}
	quote, err := f.service.Quote(f.ctx(f.user), f.user, SubmitInput{ModelID: f.model, Prompt: "Synthetic quote"})
	if err != nil || quote.Total != (Price{"2", "1"}) || mock.posts.Load() != 0 {
		t.Fatalf("quote %+v: %v", quote, err)
	}
	task := fixedSubmit(t, f, 2)
	f.wait(t, func() bool {
		result, err := f.service.GetTask(f.ctx(f.user), f.user, task.ID)
		return err == nil && result.Status == "succeeded"
	})
	result, err := f.service.GetTask(f.ctx(f.user), f.user, task.ID)
	if err != nil || result.Charge != (Price{"4", "2"}) || result.ActualImages != 1 || !result.ResultAvailable || mock.posts.Load() != 1 || mock.polls.Load() != 0 {
		t.Fatalf("synchronous result %+v: %v", result, err)
	}
	mock.mu.Lock()
	body := mock.bodies[0]
	mock.mu.Unlock()
	if body["resolution"] != "compact" || body["aspect_ratio"] != "1:1" || body["size"] != "512x512" || body["cfg_scale"] != 1.5 || body["steps"] != float64(4) || body["quality"] != "low" || body["seed"] != nil {
		t.Fatalf("lost automatic parameters: %+v", body)
	}
	if _, err := f.service.GetTask(f.ctx(f.other), f.other, task.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("task ownership boundary changed")
	}
	f.checkLedger(t)
}

func TestFixedServiceQueuedRunningAndDonePolling(t *testing.T) {
	f, mock, _ := configureFixedService(t)
	mock.mode.Store(1)
	task := fixedSubmit(t, f, 1)
	f.wait(t, func() bool {
		row, err := f.service.readTask(context.Background(), task.ID)
		return err == nil && row.state == "running" && row.upstreamID != ""
	})
	f.now.Add(5)
	f.wait(t, func() bool {
		row, err := f.service.readTask(context.Background(), task.ID)
		return err == nil && row.pollCount >= 1
	})
	row, err := f.service.readTask(context.Background(), task.ID)
	if err != nil || row.state != "running" || row.finance != "reserved" {
		t.Fatal("queued status incorrectly ended the task")
	}
	mock.mode.Store(2)
	f.now.Add(10)
	f.wait(t, func() bool {
		row, err := f.service.readTask(context.Background(), task.ID)
		return err == nil && row.pollCount >= 2
	})
	mock.mode.Store(0)
	f.now.Add(20)
	f.wait(t, func() bool {
		result, err := f.service.GetTask(f.ctx(f.user), f.user, task.ID)
		return err == nil && result.Status == "succeeded"
	})
	if mock.posts.Load() != 1 {
		t.Fatal("polling repeated generation")
	}
	mock.mu.Lock()
	paths := append([]string(nil), mock.pollPaths...)
	mock.mu.Unlock()
	for _, path := range paths {
		if !strings.HasPrefix(path, "/v1/images/jobs/%6F%70%61%71%75%65%2F%2E%2E%2F%6A%6F%62") {
			t.Fatalf("unescaped upstream task ID: %s", path)
		}
	}
	f.checkLedger(t)
}

func TestFixedServiceFailuresRefundOnceWithoutResubmission(t *testing.T) {
	for _, tc := range []struct {
		name   string
		async  bool
		mode   int32
		status string
	}{
		{"synchronous error", false, 3, "failed"},
		{"asynchronous error", true, 3, "failed"},
		{"unknown receipt", false, 4, "unknown_refunded"},
		{"unknown poll", true, 4, "unknown_refunded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, mock, _ := configureFixedService(t)
			if tc.async {
				mock.mode.Store(1)
			} else {
				mock.mode.Store(tc.mode)
			}
			task := fixedSubmit(t, f, 1)
			if tc.async {
				f.wait(t, func() bool {
					row, err := f.service.readTask(context.Background(), task.ID)
					return err == nil && row.state == "running"
				})
				mock.mode.Store(tc.mode)
				f.now.Add(5)
				f.wait(t, func() bool { return mock.polls.Load() > 0 })
			}
			if tc.mode == 4 {
				f.wait(t, func() bool { return mock.posts.Load() > 0 })
				f.now.Add(61)
			}
			f.wait(t, func() bool {
				result, err := f.service.GetTask(f.ctx(f.user), f.user, task.ID)
				return err == nil && result.Status == tc.status
			})
			result, err := f.service.GetTask(f.ctx(f.user), f.user, task.ID)
			if err != nil || result.Refund != (Price{"2", "1"}) || result.ActualImages != 0 || result.ResultAvailable || mock.posts.Load() != 1 {
				t.Fatalf("refund %+v: %v", result, err)
			}
			if err := f.service.Step(context.Background()); err != nil {
				t.Fatal(err)
			}
			f.checkLedger(t)
		})
	}
}

func TestFixedServiceRecoveryPollsAcceptedJobWithoutGeneratingAgain(t *testing.T) {
	f, mock, _ := configureFixedService(t)
	mock.mode.Store(1)
	task := fixedSubmit(t, f, 1)
	f.wait(t, func() bool {
		row, err := f.service.readTask(context.Background(), task.ID)
		return err == nil && row.state == "running" && row.upstreamID != ""
	})
	old := f.service
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	fresh, err := New(old.config)
	if err != nil {
		t.Fatal(err)
	}
	f.service = fresh
	if _, err = fresh.RecoverBeforeListener(context.Background(), f.now.Load(), 100, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	mock.mode.Store(0)
	f.now.Add(5)
	f.wait(t, func() bool {
		result, err := fresh.GetTask(f.ctx(f.user), f.user, task.ID)
		return err == nil && result.Status == "succeeded"
	})
	if mock.posts.Load() != 1 || mock.polls.Load() == 0 {
		t.Fatal("recovery resubmitted the accepted job")
	}
	f.checkLedger(t)
}

func TestFixedServiceUpgradePreservesIdentityBudgetsPricesAndAcceptedTask(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	old, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := f.service.GetUpstream(f.ctx(f.admin), f.admin)
	if err != nil {
		t.Fatal(err)
	}
	f.upstream.mode.Store(4)
	task := f.submit(t, f.user, 1)
	if err = f.service.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.upstream.started:
	case <-time.After(3 * time.Second):
		t.Fatal("legacy generation did not start")
	}
	if _, err = f.service.PutConnection(f.ctx(f.admin), f.admin, f.key(), ConnectionInput{ExpectedRevision: connection.Revision, BaseURL: connection.BaseURL, Secret: SecretInput{Mode: "keep"}}); err != nil {
		t.Fatal(err)
	}
	updated, err := f.service.GetUpstream(f.ctx(f.admin), f.admin)
	if err != nil || updated.Control.ID != connection.Control.ID || updated.BaseURL != connection.BaseURL || updated.ExecutionTimeoutSeconds != connection.ExecutionTimeoutSeconds || updated.PerUserLimit != connection.PerUserLimit || !updated.SecretSet {
		t.Fatalf("connection identity or budget changed: %+v %v", updated, err)
	}
	unchanged, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil || unchanged.Revision != old.Revision || unchanged.Enabled != old.Enabled || unchanged.Price != old.Price {
		t.Fatal("missing metadata revoked an existing model policy")
	}
	metadata := strings.Replace(string(fixedMetadata(`{}`, `{}`)), `"atelier-test"`, `"studio-test"`, 1)
	f.upstream.mu.Lock()
	f.upstream.catalogBody = []byte(`{"data":[` + metadata + `]}`)
	f.upstream.mu.Unlock()
	accepted, err := f.service.RefreshServiceModels(f.ctx(f.admin), f.admin, f.key())
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, func() bool {
		refresh, err := f.service.GetRefresh(f.ctx(f.admin), f.admin, accepted.Value.Operation.ID)
		return err == nil && refresh.State == "succeeded"
	})
	model, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil || model.ID != old.ID || model.Price != old.Price || model.Enabled != old.Enabled || model.Revision == old.Revision || model.CapabilityReadiness != "ready" || model.SizeCapability == nil {
		t.Fatalf("automatic migration %+v: %v", model, err)
	}
	row, err := f.service.readTask(context.Background(), task.ID)
	if err != nil || row.modelRevision != 1 || row.upstreamRevision != 2 {
		t.Fatalf("accepted task snapshot changed: %+v %v", row, err)
	}
	f.upstream.unblock()
	f.wait(t, func() bool {
		result, err := f.service.GetTask(f.ctx(f.user), f.user, task.ID)
		return err == nil && result.Status == "succeeded"
	})
	accepted, err = f.service.RefreshServiceModels(f.ctx(f.admin), f.admin, f.key())
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, func() bool {
		refresh, err := f.service.GetRefresh(f.ctx(f.admin), f.admin, accepted.Value.Operation.ID)
		return err == nil && refresh.State == "succeeded"
	})
	stable, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil || stable.ID != model.ID || stable.Revision != model.Revision || stable.PricingRevision != model.PricingRevision {
		t.Fatal("identical catalog refresh churned model revisions")
	}
	f.checkLedger(t)
}

func TestFixedServiceUnknownModelCannotBeEnabledOrOverrideCapabilities(t *testing.T) {
	f, mock, routes := configureFixedService(t)
	mock.mu.Lock()
	mock.catalog = append(mock.catalog, json.RawMessage(`{"id":"unknown-image","metadata":{}}`))
	mock.mu.Unlock()
	accepted, err := f.service.RefreshServiceModels(f.ctx(f.admin), f.admin, f.key())
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, func() bool {
		refresh, err := f.service.GetRefresh(f.ctx(f.admin), f.admin, accepted.Value.Operation.ID)
		return err == nil && refresh.State == "succeeded"
	})
	catalog, err := f.service.ListCatalog(f.ctx(f.admin), f.admin, CatalogQuery{Type: "unknown"})
	if err != nil || len(catalog.Data) != 1 || catalog.Data[0].CapabilityReadiness != "pending" || len(catalog.Data[0].CapabilityIssues) == 0 || catalog.Data[0].SizeCapability != nil {
		t.Fatalf("unknown model advertised a capability: %+v %v", catalog, err)
	}
	id := catalog.Data[0].ID
	input := ModelSettingsInput{ExpectedRevision: "0", Enabled: true, Price: Price{"2", "1"}}
	output := fixedAdminRequest(t, f, routes, http.MethodPut, "/models/"+id, input)
	if output.Code != http.StatusBadRequest || !strings.Contains(output.Body.String(), "metadata") {
		t.Fatalf("unknown model can be enabled: %d %s", output.Code, output.Body.String())
	}
	input.Enabled = false
	output = fixedAdminRequest(t, f, routes, http.MethodPut, "/models/"+id, input)
	if output.Code != http.StatusOK {
		t.Fatal("pending model settings could not be saved")
	}
	for _, payload := range []map[string]any{
		{"expected_revision": "1", "enabled": true, "price": Price{"2", "1"}, "parameters": []any{}},
		{"expected_revision": "1", "enabled": true, "price": Price{"2", "1"}, "capability_confirmed": true},
		{"expected_revision": "1", "enabled": true, "price": Price{"2", "1"}, "mapping": map[string]any{}},
	} {
		output = fixedAdminRequest(t, f, routes, http.MethodPut, "/models/"+id, payload)
		if output.Code != http.StatusBadRequest {
			t.Fatal("removed capability configuration was accepted")
		}
	}
	for _, path := range []string{"/upstream/capability-profile", "/models/" + id + "/capabilities/apply"} {
		output = fixedAdminRequest(t, f, routes, http.MethodPost, path, map[string]any{})
		if output.Code != http.StatusNotFound {
			t.Fatalf("removed route remains writable: %s %d", path, output.Code)
		}
	}
	if mock.posts.Load() != 0 {
		t.Fatal("administrative configuration generated an image")
	}
}
