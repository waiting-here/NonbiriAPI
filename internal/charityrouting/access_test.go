package charityrouting

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

func TestEveryLevelSubsetControlsPreflightUsingCurrentEffectiveLevel(t *testing.T) {
	env := newRoutingTestEnv(t)
	env.seedUser(t, true, nil)
	env.seedUserBalance(t, env.caller, "100000")
	model := env.createModel(t, 'a')
	id, _ := parsePositiveID(model.ID)
	request, err := openai.DecodeChatRequest(strings.NewReader("{\"model\":\"[公益]provider/model\",\"messages\":[{\"role\":\"user\",\"content\":\"A sufficiently long message for admission.\"}]}"), openai.MaxRequestBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer request.Clear()
	for mask := 0; mask < 32; mask++ {
		if _, err := env.store.DB().Exec("UPDATE charity_model_access SET allowed_level_mask=? WHERE model_id=?", mask, id); err != nil {
			t.Fatal(err)
		}
		for level := 1; level <= 5; level++ {
			if _, err := env.store.DB().Exec("UPDATE users SET level=? WHERE id=?", level, env.caller); err != nil {
				t.Fatal(err)
			}
			_, err := env.service.Preflight(context.Background(), env.caller, model.FullName, request, routingTestNow)
			if mask&(1<<(level-1)) == 0 {
				if !errors.Is(err, ErrForbidden) {
					t.Fatalf("mask=%d level=%d got %v", mask, level, err)
				}
			} else if err != nil {
				t.Fatalf("mask=%d level=%d got %v", mask, level, err)
			}
		}
	}
	if _, err := env.store.DB().Exec("UPDATE charity_model_access SET allowed_level_mask=4 WHERE model_id=?", id); err != nil {
		t.Fatal(err)
	}
	for _, level := range []int{1, 3, 4} {
		if _, err := env.store.DB().Exec("UPDATE users SET level=NULL,auto_level=? WHERE id=?", level, env.caller); err != nil {
			t.Fatal(err)
		}
		_, err := env.service.Preflight(context.Background(), env.caller, model.FullName, request, routingTestNow)
		if (level == 3 && err != nil) || (level != 3 && !errors.Is(err, ErrForbidden)) {
			t.Fatalf("auto level %d: %v", level, err)
		}
	}
	if env.state.dueCalls.Load() != 0 {
		t.Fatal("preflight inspected candidates")
	}
}

func TestModelAccessHTTPDefaultsCanonicalPatchAndLimits(t *testing.T) {
	env := newRoutingTestEnv(t)
	env.seedUser(t, true, nil)
	api := &httpAPI{service: env.service}
	createBody := func(name string) map[string]any {
		return map[string]any{
			"provider": "provider", "model": name, "enabled": true,
			"pricing":            map[string]any{"mode": "per_request", "user_price": "0", "donor_reward": "0"},
			"discount":           map[string]any{"enabled": false, "percent": 100, "start_at": nil, "end_at": nil},
			"flatten_tool_calls": false,
		}
	}
	seq := 0
	send := func(method string, body any, id string) *httptest.ResponseRecorder {
		t.Helper()
		seq++
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(method, routeAdminModels, bytes.NewReader(raw))
		request.Header.Set("Idempotency-Key", fmt.Sprintf("%022d", seq))
		request.Header.Set("Content-Type", "application/json")
		result := httptest.NewRecorder()
		if method == http.MethodPatch {
			request.SetPathValue("id", id)
			api.patchAdminModel(result, request)
		} else {
			api.createAdminModel(result, request)
		}
		return result
	}
	created := send(http.MethodPost, createBody("all"), "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create %d %s", created.Code, created.Body.String())
	}
	var model AdminCharityModel
	if err := json.Unmarshal(created.Body.Bytes(), &model); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(model.AllowedLevels, []int{1, 2, 3, 4, 5}) || model.PublicDescription != "" {
		t.Fatalf("defaults %+v", model)
	}
	update := send(http.MethodPatch, map[string]any{"expected_revision": "1", "allowed_levels": []int{5, 2}, "public_description": "<b>literal</b>\r\n😀\tend"}, model.ID)
	if update.Code != 200 {
		t.Fatalf("patch %d %s", update.Code, update.Body.String())
	}
	if err := json.Unmarshal(update.Body.Bytes(), &model); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(model.AllowedLevels, []int{2, 5}) || model.PublicDescription != "<b>literal</b>\n😀\tend" || model.Revision != "2" {
		t.Fatalf("patch value %+v", model)
	}
	omitted := send(http.MethodPatch, map[string]any{"expected_revision": "2", "enabled": false}, model.ID)
	if omitted.Code != 200 {
		t.Fatalf("omitted %d %s", omitted.Code, omitted.Body.String())
	}
	if err := json.Unmarshal(omitted.Body.Bytes(), &model); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(model.AllowedLevels, []int{2, 5}) || model.PublicDescription != "<b>literal</b>\n😀\tend" {
		t.Fatal("omitted access fields were overwritten")
	}
	empty := send(http.MethodPatch, map[string]any{"expected_revision": "3", "allowed_levels": []int{}, "public_description": ""}, model.ID)
	if empty.Code != 200 {
		t.Fatalf("clear %d %s", empty.Code, empty.Body.String())
	}
	if err := json.Unmarshal(empty.Body.Bytes(), &model); err != nil {
		t.Fatal(err)
	}
	if model.AllowedLevels == nil || len(model.AllowedLevels) != 0 || model.PublicDescription != "" {
		t.Fatalf("clear %+v", model)
	}
	for name, value := range map[string]any{"null": nil, "duplicate": []int{1, 1}, "zero": []int{0}, "six": []int{6}, "fraction": []float64{1.5}, "text": "1"} {
		body := createBody("invalid-" + name)
		body["allowed_levels"] = value
		if result := send(http.MethodPost, body, ""); result.Code != 400 {
			t.Fatalf("%s %d %s", name, result.Code, result.Body.String())
		}
		if result := send(http.MethodPatch, map[string]any{"expected_revision": "4", "allowed_levels": value}, model.ID); result.Code != 400 {
			t.Fatalf("patch %s %d %s", name, result.Code, result.Body.String())
		}
	}
	for _, value := range []any{nil, strings.Repeat("😀", 1025), "a\rb", "a\u0085b", "a\u0000b"} {
		body := createBody("invalid-description")
		body["public_description"] = value
		if result := send(http.MethodPost, body, ""); result.Code != 400 {
			t.Fatalf("description %d %s", result.Code, result.Body.String())
		}
		if result := send(http.MethodPatch, map[string]any{"expected_revision": "4", "public_description": value}, model.ID); result.Code != 400 {
			t.Fatalf("patch description %d %s", result.Code, result.Body.String())
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodPatch} {
		request := httptest.NewRequest(method, routeAdminModels, strings.NewReader(strings.Repeat(" ", 16<<10)+"{}"))
		request.Header.Set("Idempotency-Key", strings.Repeat("Z", 22))
		request.SetPathValue("id", model.ID)
		result := httptest.NewRecorder()
		if method == http.MethodPost {
			api.createAdminModel(result, request)
		} else {
			api.patchAdminModel(result, request)
		}
		if result.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("body limit %s %d %s", method, result.Code, result.Body.String())
		}
	}
}

func TestLegacyModelReceiptRetainsOriginalFieldsAndKnownAccessDefaults(t *testing.T) {
	body := []byte("{\"id\":\"7\",\"provider\":\"provider\",\"model\":\"model\",\"full_name\":\"[公益]provider/model\",\"enabled\":true,\"revision\":\"3\"}")
	before := append([]byte(nil), body...)
	result, err := replayModel(idempotency.Decision{Kind: idempotency.Replay, HTTPStatus: 201, ResponseBody: body})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Replayed || result.Status != 201 || !bytes.Equal(body, before) || result.Value.Revision != "3" ||
		!reflect.DeepEqual(result.Value.AllowedLevels, []int{1, 2, 3, 4, 5}) || result.Value.PublicDescription != "" {
		t.Fatalf("legacy %+v", result)
	}
	current := []byte("{\"id\":\"7\",\"allowed_levels\":[],\"public_description\":\"current\",\"revision\":\"4\"}")
	result, err = replayModel(idempotency.Decision{Kind: idempotency.Replay, HTTPStatus: 200, ResponseBody: current})
	if err != nil || !bytes.Equal(result.Body, current) || result.Value.AllowedLevels == nil || len(result.Value.AllowedLevels) != 0 {
		t.Fatalf("empty set replay %+v %v", result, err)
	}
}
