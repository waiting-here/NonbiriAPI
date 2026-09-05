package donation

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManagedDonationKeyLimitsAreCurrentAndReadOnly(t *testing.T) {
	e := newDonationTestEnv(t)
	level := int64(5)
	owner := e.seedUser(t, "limit-steward", &level, false)
	_, kid := e.seedEndpointKey(t, owner, 'L')
	value := e.createDonation(t, owner, kid)
	did := parseTestID(t, value.ID)
	if _, err := e.store.DB().Exec(`INSERT INTO endpoint_key_limits(endpoint_key_id,max_concurrency,max_rpm) VALUES(?,4,90)`, kid); err != nil {
		t.Fatal(err)
	}
	admin, err := e.service.GetAdmin(context.Background(), did)
	if err != nil {
		t.Fatal(err)
	}
	steward, err := e.service.GetSteward(context.Background(), owner, did)
	if err != nil {
		t.Fatal(err)
	}
	if admin.Keys[0].MaxConcurrency == nil || *admin.Keys[0].MaxConcurrency != 4 || steward.Keys[0].MaxRPM == nil || *steward.Keys[0].MaxRPM != 90 {
		t.Fatal("management projection lost owner limits")
	}
	api := httpAPI{service: e.service}
	for _, role := range []reviewerRole{reviewerAdmin, reviewerSteward} {
		for _, field := range []string{"max_concurrency", "max_rpm"} {
			r := httptest.NewRequest(http.MethodPatch, "/keys/"+value.Keys[0].ID, strings.NewReader(fmt.Sprintf(`{"expected_revision":"1","%s":0}`, field)))
			r.SetPathValue("id", value.ID)
			r.SetPathValue("keyId", value.Keys[0].ID)
			w := httptest.NewRecorder()
			api.manageKeyRole(w, r, UserPrincipal{UserID: owner}, role)
			if w.Code != 400 {
				t.Fatalf("%s can submit owner %s: %d", role, field, w.Code)
			}
		}
	}
	if _, err := e.store.DB().Exec(`UPDATE endpoint_key_limits SET max_rpm=120 WHERE endpoint_key_id=?`, kid); err != nil {
		t.Fatal(err)
	}
	refreshed, err := e.service.GetSteward(context.Background(), owner, did)
	if err != nil || refreshed.Keys[0].MaxRPM == nil || *refreshed.Keys[0].MaxRPM != 120 {
		t.Fatal("management limits were copied instead of read live")
	}
	e.auth.denySteward.Store(true)
	if _, err := e.service.GetSteward(context.Background(), owner, did); err == nil {
		t.Fatal("revoked role kept read access")
	}
}
