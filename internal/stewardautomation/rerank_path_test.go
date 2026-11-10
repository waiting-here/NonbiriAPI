package stewardautomation

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

func TestAutomationCreateFreezesRerankPathWithoutManagementOverride(t *testing.T) {
	f := newPersonalFixture(t)
	f.exec(t, "UPDATE users SET level=6 WHERE id=?", f.userID)
	f.exec(t, "UPDATE site_config SET value='1' WHERE key IN ('donation_accept_enabled','charity_enabled')")
	ctx := authz.WithStewardCaller(t.Context(), authz.StewardCaller{UserID: f.userID, Generation: 1})
	body := `{"discord_public_thanks":false,"description":"Rerank path","review_note":"Approved","endpoint":{"connector_type":"cohere-rerank","base_url":"https://rerank.example.test/v1","native_services":["rerank"],"rerank_path":"/compatible-api/v1/reranks"},"keys":[{"secret":"fixture-rerank-credential","native_services":["rerank"],"concurrency_overflow":"queue","rpm_overflow":"queue","concurrency_queue_limit":12,"rpm_queue_limit":13}]}`
	response := f.request(ctx, http.MethodPost, DonationsPath, strings.Repeat("r", 22), body)
	if response.Code != http.StatusCreated {
		t.Fatal(response.Code, response.Body.String())
	}
	var result createdDonation
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	var concurrencyOverflow, rpmOverflow string
	var concurrencyCap, rpmCap int64
	if err := f.store.DB().QueryRow(`SELECT concurrency_overflow,rpm_overflow,concurrency_queue_limit,rpm_queue_limit FROM endpoint_key_limits WHERE endpoint_key_id=?`, result.Keys[0].EndpointKeyID).Scan(&concurrencyOverflow, &rpmOverflow, &concurrencyCap, &rpmCap); err != nil {
		t.Fatal(err)
	}
	if concurrencyOverflow != "queue" || rpmOverflow != "queue" || concurrencyCap != 12 || rpmCap != 13 {
		t.Fatal("automation lost queue configuration")
	}
	var endpointPath, snapshot string
	var override *string
	if err := f.store.DB().QueryRow("SELECT rerank_path FROM endpoints WHERE id=?", result.EndpointID).Scan(&endpointPath); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DB().QueryRow("SELECT r.rerank_path,u.rerank_path_override FROM donation_uses u JOIN donation_use_revisions r ON r.id=u.current_approved_revision_id WHERE u.donation_key_id=?", result.Keys[0].DonationKeyID).Scan(&snapshot, &override); err != nil {
		t.Fatal(err)
	}
	if endpointPath != "/compatible-api/v1/reranks" || snapshot != endpointPath || override != nil {
		t.Fatal(endpointPath, snapshot, override)
	}
	path := StewardPrefix + "donations/" + result.DonationID + "/uses/"
	var useID int64
	if err := f.store.DB().QueryRow("SELECT id FROM donation_uses WHERE donation_key_id=?", result.Keys[0].DonationKeyID).Scan(&useID); err != nil {
		t.Fatal(err)
	}
	response = f.request(ctx, http.MethodPatch, path+strconv.FormatInt(useID, 10), strings.Repeat("p", 22), `{"expected_revision":"2","rerank_path_override":"/managed"}`)
	if response.Code != http.StatusNotFound {
		t.Fatal("automation route expanded", response.Code, response.Body.String())
	}
}
