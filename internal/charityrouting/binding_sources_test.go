package charityrouting

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSharedBindingSourcesPreserveStewardBoundaryAndPagination(t *testing.T) {
	env := newRoutingTestEnv(t)
	env.seedUser(t, true, nil)
	level := int64(5)
	steward := env.seedUser(t, false, &level)
	donor := env.seedUser(t, false, nil)
	model := env.createModel(t, 'a')
	var donationIDs []int64
	for _, suffix := range []byte{'a', 'b', 'c'} {
		_, keyID, _ := env.seedCandidate(t, donor, suffix, fmt.Sprintf("model-%c", suffix))
		var donationID int64
		if err := env.store.DB().QueryRow(`SELECT donation_id FROM donation_keys WHERE id=?`, keyID).Scan(&donationID); err != nil {
			t.Fatal(err)
		}
		donationIDs = append(donationIDs, donationID)
		if _, err := env.store.DB().Exec(`UPDATE donations SET description=? WHERE id=?`, fmt.Sprintf("Shared instructions %c", suffix), donationID); err != nil {
			t.Fatal(err)
		}
		if suffix == 'c' {
			if _, err := env.store.DB().Exec(`UPDATE donation_keys SET expires_at=? WHERE id=?`, routingTestNow, keyID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := env.store.DB().Exec(`UPDATE endpoints SET note='private-endpoint-label'; UPDATE endpoint_keys SET note='private-key-label'`); err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.DB().Exec(`INSERT INTO endpoint_key_limits(endpoint_key_id,max_concurrency,max_rpm) SELECT id,3,45 FROM endpoint_keys`); err != nil {
		t.Fatal(err)
	}
	api := httpAPI{service: env.service}
	request := func(modelID, donationID, cursor string, keys bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/steward/charity-models/"+modelID+"/binding-donations?limit=1"+cursor, nil)
		r.SetPathValue("id", modelID)
		if keys {
			r.SetPathValue("donationId", donationID)
		}
		w := httptest.NewRecorder()
		api.bindingSources(w, r, roleSteward, steward, keys)
		return w
	}
	first := request(model.ID, "", "", false)
	if first.Code != 200 {
		t.Fatalf("first page status=%d body=%s", first.Code, first.Body.String())
	}
	var page Page[BindingDonation]
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != fmt.Sprint(donationIDs[0]) || page.Data[0].Description != "Shared instructions a" || page.NextCursor == nil {
		t.Fatal("shared donation page missing other donor or cursor")
	}
	cursor := "&cursor=" + url.QueryEscape(*page.NextCursor)
	second := request(model.ID, "", cursor, false)
	if second.Code != 200 {
		t.Fatal(second.Code)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != fmt.Sprint(donationIDs[1]) || page.NextCursor != nil {
		t.Fatal("expired donation included or pagination repeated")
	}
	keyPage := request(model.ID, fmt.Sprint(donationIDs[0]), "", true)
	var keyData Page[BindingSourceKey]
	if err := json.Unmarshal(keyPage.Body.Bytes(), &keyData); err != nil {
		t.Fatal(err)
	}
	if keyPage.Code != 200 || len(keyData.Data) != 1 || keyData.Data[0].Note != "safe label" {
		t.Fatal("reviewed key projection missing")
	}
	if keyData.Data[0].Source.MaxConcurrency != 3 || keyData.Data[0].Source.MaxRPM != 45 {
		t.Fatal("steward did not receive current owner limits")
	}
	for _, body := range []string{first.Body.String(), keyPage.Body.String()} {
		for _, forbidden := range []string{"private-endpoint-label", "private-key-label", `"owner"`, `"discord_id"`, `"endpoint_key_id"`, `"encrypted_secret"`, `"reviewer"`} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("shared projection exposed %s", forbidden)
			}
		}
	}
	if got := request(model.ID, fmt.Sprint(donationIDs[0]), cursor, true); got.Code != 400 {
		t.Fatalf("cross-scope cursor accepted: %d", got.Code)
	}
	if got := request("999", "", cursor, false); got.Code != 400 {
		t.Fatalf("cross-model cursor accepted: %d", got.Code)
	}
	env.auth.denySteward.Store(true)
	if got := request(model.ID, "", "", false); got.Code != 403 {
		t.Fatalf("lost authority accepted: %d", got.Code)
	}
}
