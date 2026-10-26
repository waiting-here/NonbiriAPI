package charityrouting

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestBindingMarkersIncludeCurrentAndFilterOtherTargets(t *testing.T) {
	e, _, did, keys, models := presetFixture(t)
	input := KeyBindingBatch{ExpectedDonationRevision: "1", Targets: []KeyBindingTarget{}}
	for i := range 6 {
		input.Targets = append(input.Targets, KeyBindingTarget{models[0], "0", fmt.Sprint("up-", i), nil})
	}
	for _, model := range models[1:] {
		input.Targets = append(input.Targets, KeyBindingTarget{model, "0", "up-0", nil})
	}
	if _, err := e.service.bindKeyTargets(t.Context(), roleAdmin, 0, did, keys[0], routingMutation(t, 'j', http.MethodPost, routeAdminKeyBindingBatch, []int64{did, keys[0]}, input), input); err != nil {
		t.Fatal(err)
	}
	mid, _ := parsePositiveID(models[0])
	five := int64(5)
	trainee := e.seedUser(t, false, &five)
	presetExec(t, e, `UPDATE charity_models SET is_mainstream=1 WHERE id IN (?,?)`, models[0], models[1])
	for _, role := range []roleKind{roleAdmin, roleSteward} {
		actor := int64(0)
		want := "5"
		preview := 3
		if role == roleSteward {
			actor = trainee
			want = "1"
			preview = 1
		}
		page, err := e.service.candidatesPage(t.Context(), role, actor, mid, CandidateQuery{DonationKeyID: keys[0], IncludeBound: true}, pagination.Default())
		if err != nil || len(page.Data) != 6 || page.Pagination.TotalItems != "6" || page.Data[0].CurrentBindingID == nil || page.Data[0].OtherBindingCount != want || len(page.Data[0].OtherBindings) != preview {
			t.Fatalf("%s markers %+v %v", role, page, err)
		}
		_, unbound, _, err := e.service.bindingSources(t.Context(), role, actor, mid, did, 0, 50, "")
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range unbound {
			if key.DonationKeyID == fmt.Sprint(keys[0]) {
				t.Fatal("default source contains fully bound key")
			}
		}
		_, all, err := e.service.bindingSourcesPage(t.Context(), role, actor, mid, did, "", pagination.Default(), true)
		if err != nil || len(all.Data) != 3 || all.Pagination.TotalItems != "3" {
			t.Fatal("include bound source page", all, err)
		}
	}
	six := int64(6)
	steward := e.seedUser(t, false, &six)
	automatic, err := e.service.AutomationCandidates(t.Context(), steward, mid, "", pagination.Default())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(automatic)
	if strings.Contains(string(body), "other_binding") || strings.Contains(string(body), "current_binding") {
		t.Fatal("automation projection grew", string(body))
	}
	api := &httpAPI{service: e.service}
	for _, query := range []string{"include_bound=1", "include_bound=", "include_bound=true&include_bound=false"} {
		r := httptest.NewRequest(http.MethodGet, "/?"+query, nil)
		r.SetPathValue("id", models[0])
		w := httptest.NewRecorder()
		api.adminCandidates(w, r)
		if w.Code != 400 {
			t.Fatal(query, w.Code)
		}
	}
	r := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/?include_bound=true&donation_key_id=%d&page=1&page_size=10", keys[0]), nil)
	r.SetPathValue("id", models[0])
	w := httptest.NewRecorder()
	api.adminCandidates(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"current_binding_id":"`) {
		t.Fatal(w.Code, w.Body.String())
	}
}
