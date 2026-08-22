package charity

// HTTP-surface tests for the charity model management rail (admin prefix in
// these tests; the steward frame mounts the same subs): string-wire price
// parsing, the '[公益]' prefix rule, token-reserve fail closed over the wire,
// discount interval validation, and the binding candidate predicate.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type charityFixture struct {
	st  *db.Store
	svc *Service
	h   http.Handler
}

func newCharityFixture(t *testing.T) *charityFixture {
	t.Helper()
	key := bytes.Repeat([]byte{0x2c}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatalf("vault: %v", err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "charity-handler.db")
	dbtest.EnsureOwnerOnlyParent(t, path)
	st, err := db.Open(path, vault)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	manager, err := st.CreateUser("discord-manager", "manager", "")
	if err != nil {
		t.Fatalf("CreateUser(manager): %v", err)
	}
	f := &charityFixture{st: st, svc: NewService(st)}
	f.h = NewHandler("/admin/api", f.svc, func(r *http.Request) (int64, error) {
		return manager.ID, nil
	}).Handler()
	return f
}

func (f *charityFixture) do(t *testing.T, method, target string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	out := map[string]any{}
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec, out
}

func (f *charityFixture) setTokenReserve(t *testing.T, value string) {
	t.Helper()
	if value == "" {
		if _, err := f.st.DB().Exec(`DELETE FROM site_config WHERE key='charity_token_reserve_milli'`); err != nil {
			t.Fatalf("clear reserve: %v", err)
		}
		return
	}
	if _, err := f.st.DB().Exec(`INSERT INTO site_config (key, value, updated_at) VALUES ('charity_token_reserve_milli', ?, 0)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, value); err != nil {
		t.Fatalf("set reserve: %v", err)
	}
}

func (f *charityFixture) createDonorDonation(t *testing.T) (uid, donationKeyID int64) {
	t.Helper()
	user, err := f.st.CreateUser("discord-donor", "donor", "")
	if err != nil {
		t.Fatalf("CreateUser(donor): %v", err)
	}
	ep, err := f.st.CreateEndpoint(context.Background(), user.ID, "openai-compatible", "https://api.example.com", "", true, 1)
	if err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	k, err := f.st.CreateEndpointKey(context.Background(), user.ID, ep.ID, []byte("sk-charity"), "h", "t", "", true, 1)
	if err != nil {
		t.Fatalf("CreateEndpointKey: %v", err)
	}
	if err := f.st.ReplaceFetchedModels(context.Background(), user.ID, ep.ID, k.ID,
		[]db.FetchedModel{{UpstreamModelID: "up/m", Provider: "p"}}, 5); err != nil {
		t.Fatalf("ReplaceFetchedModels: %v", err)
	}
	in := db.CreateDonationInput{UserID: user.ID, Description: "donation", Now: 10,
		Existing: &db.ExistingEndpointKeys{EndpointID: ep.ID, KeyIDs: []int64{k.ID}}}
	d, err := f.st.CreateDonation(context.Background(), in)
	if err != nil {
		t.Fatalf("CreateDonation: %v", err)
	}
	if _, err := f.st.ApplyDonationReview(context.Background(), db.ReviewDecision{
		DonationID: d.ID, Role: db.ReviewRoleAdmin, ReviewerID: user.ID, Action: db.ReviewActionApprove, Now: 20,
	}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	keys, err := f.st.ListOwnDonationKeys(context.Background(), user.ID, d.ID)
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	return user.ID, keys[0].ID
}

func TestCharityHTTPCreateUpdateAndDiscountValidation(t *testing.T) {
	f := newCharityFixture(t)

	rec, body := f.do(t, "POST", "/admin/api/charity-models", map[string]any{
		"provider": "donor", "model": "m1", "pricing_mode": "per_request",
		"prices": map[string]any{"request_user_price": "500", "request_donor_reward": "100"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %v", rec.Code, body)
	}
	if body["full_name"] != "[公益]donor/m1" {
		t.Fatalf("full_name = %v", body["full_name"])
	}
	id := int64(body["id"].(float64))

	// Non-canonical price strings are invalid over the wire.
	rec, _ = f.do(t, "PATCH", "/admin/api/charity-models/"+itoa64(id), map[string]any{
		"prices": map[string]any{"request_user_price": "007"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("non-canonical price = %d, want 400", rec.Code)
	}

	// Discount out of range is invalid.
	rec, _ = f.do(t, "PATCH", "/admin/api/charity-models/"+itoa64(id), map[string]any{
		"discount": map[string]any{"percent": 101, "enabled": true},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("discount 101 = %d, want 400", rec.Code)
	}

	// Inverted interval fails a request-local sanity rule decidable from the
	// supplied values alone: invalid_request (400).
	rec, _ = f.do(t, "PATCH", "/admin/api/charity-models/"+itoa64(id), map[string]any{
		"discount": map[string]any{"percent": 50, "enabled": true, "start_at": 2000, "end_at": 1000},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("inverted interval = %d, want 400", rec.Code)
	}

	// A valid discount applies and reads back.
	rec, body = f.do(t, "PATCH", "/admin/api/charity-models/"+itoa64(id), map[string]any{
		"discount": map[string]any{"percent": 50, "enabled": true, "start_at": 1000, "end_at": 2000},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("valid discount = %d %v", rec.Code, body)
	}
	got := body["discount"].(map[string]any)
	if got["percent"] != float64(50) || got["enabled"] != true {
		t.Fatalf("discount readback = %v", got)
	}

	// Unknown upstream provider prefix rules: a personal-model style rename
	// that collides with another routing key conflicts.
	rec, _ = f.do(t, "POST", "/admin/api/charity-models", map[string]any{
		"provider": "donor", "model": "m1", "pricing_mode": "per_request",
		"prices": map[string]any{"request_user_price": "1"},
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate = %d, want 409", rec.Code)
	}
}

func TestCharityHTTPTokenReserveFailClosedAndBindings(t *testing.T) {
	f := newCharityFixture(t)
	f.setTokenReserve(t, "") // unset

	rec, _ := f.do(t, "POST", "/admin/api/charity-models", map[string]any{
		"provider": "donor", "model": "tok", "pricing_mode": "per_token", "enabled": true,
		"prices": map[string]any{
			"uncached_user_price": "1000", "cache_write_user_price": "0",
			"cache_read_user_price": "0", "output_user_price": "2000",
			"uncached_donor_reward": "50", "cache_write_donor_reward": "0",
			"cache_read_donor_reward": "0", "output_donor_reward": "100",
		},
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("token create without reserve = %d, want 409 fail closed", rec.Code)
	}

	_, dkID := f.createDonorDonation(t)

	// Per-request model + valid binding through the full predicate.
	rec, body := f.do(t, "POST", "/admin/api/charity-models", map[string]any{
		"provider": "donor", "model": "bind", "pricing_mode": "per_request", "enabled": true,
		"prices": map[string]any{"request_user_price": "500"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("per-request create = %d %v", rec.Code, body)
	}
	modelID := int64(body["id"].(float64))

	// Binding against an unknown donation key id: not_found.
	rec, _ = f.do(t, "POST", "/admin/api/charity-models/"+itoa64(modelID)+"/bindings",
		map[string]any{"donation_key_id": 999999, "upstream_model_id": "up/m"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown key binding = %d, want 404", rec.Code)
	}

	rec, _ = f.do(t, "POST", "/admin/api/charity-models/"+itoa64(modelID)+"/bindings",
		map[string]any{"donation_key_id": dkID, "upstream_model_id": "up/m"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("valid binding = %d %v", rec.Code, body)
	}

	// The success-rate projection starts empty and updates via the ring buffer.
	if err := f.st.RecordCharityOutcome(context.Background(), modelID, true, 30); err != nil {
		t.Fatalf("RecordCharityOutcome: %v", err)
	}
	rec, body = f.do(t, "GET", "/admin/api/charity-models/"+itoa64(modelID), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get model = %d", rec.Code)
	}
	rate := map[string]any{
		"sample_count": body["success_samples"], "success_count": body["success_count"],
	}
	if rate["sample_count"] != float64(1) || rate["success_count"] != float64(1) {
		t.Fatalf("success rate = %v (body=%v)", rate, body)
	}
}

func itoa64(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
