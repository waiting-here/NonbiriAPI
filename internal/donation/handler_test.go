package donation

// Handler-level tests for the user donation surface and the shared review
// surface: the donation_accept_enabled gate (POST only), ownership isolation
// (cross-user ids are 404, never leaks existence), the reviewer identity
// matrix (admin and level5 accepted; forged/unknown roles refused server-side
// even if a frame misbehaves), per-key limit bounds on the wire, string-wire
// economic fields (never floats), and the no-secret-echo rule.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/endpoint"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type donationFixture struct {
	st   *db.Store
	svc  *Service
	user http.Handler
	rev  http.Handler
}

func newDonationFixture(t *testing.T) *donationFixture {
	t.Helper()
	key := bytes.Repeat([]byte{0x2a}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatalf("vault: %v", err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "donation-handler.db")
	dbtest.EnsureOwnerOnlyParent(t, path)
	st, err := db.Open(path, vault)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	policy, err := egress.NewEgressPolicy(nil)
	if err != nil {
		t.Fatalf("egress policy: %v", err)
	}
	svc := NewService(ServiceDeps{
		Store:      st,
		URLs:       policy,
		Connectors: endpoint.NewRegistry(),
	})
	// The review surface resolves REAL user ids: the environment admin row and
	// a level-5 steward are both rows of users, and every review write stores
	// its actor under a foreign key.
	adminRow, err := st.CreateUser("discord-admin-reviewer", "admin-reviewer", "")
	if err != nil {
		t.Fatalf("CreateUser(admin): %v", err)
	}
	stewardRow, err := st.CreateUser("discord-steward", "steward", "")
	if err != nil {
		t.Fatalf("CreateUser(steward): %v", err)
	}
	f := &donationFixture{
		st:  st,
		svc: svc,
		user: NewHandler(svc, func(r *http.Request) (int64, error) {
			v := r.Header.Get("X-Test-User")
			if v == "" {
				return 0, errors.New("no identity")
			}
			var uid int64
			for _, c := range v {
				uid = uid*10 + int64(c-'0')
			}
			return uid, nil
		}),
	}
	rev := NewReviewHandler("/admin/api", svc, func(r *http.Request) (ReviewerIdentity, error) {
		switch r.Header.Get("X-Test-Role") {
		case "admin":
			return ReviewerIdentity{UserID: adminRow.ID, Role: db.ReviewRoleAdmin}, nil
		case "level5":
			return ReviewerIdentity{UserID: stewardRow.ID, Role: db.ReviewRoleLevel5}, nil
		default:
			return ReviewerIdentity{}, errors.New("not a reviewer")
		}
	})
	f.rev = rev.Handler()
	return f
}

func (f *donationFixture) userID(t *testing.T, name string) int64 {
	t.Helper()
	u, err := f.st.CreateUser("discord-"+name, name, "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return u.ID
}

func (f *donationFixture) do(t *testing.T, h http.Handler, method, target, uid string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	if uid != "" {
		req.Header.Set("X-Test-User", uid)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	out := map[string]any{}
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec, out
}

func enableDonationAccept(t *testing.T, f *donationFixture) {
	t.Helper()
	if _, err := f.st.DB().Exec(`INSERT INTO site_config (key, value, updated_at) VALUES ('donation_accept_enabled','1',0)
		ON CONFLICT(key) DO UPDATE SET value='1'`); err != nil {
		t.Fatalf("enable intake: %v", err)
	}
}

// TestDonationIntakeSwitchGatesOnlySubmission verifies frozen §J.4.5: a closed
// intake switch rejects POST with feature_disabled while every other operation
// (list/get/delete of existing donations) keeps working.
func TestDonationIntakeSwitchGatesOnlySubmission(t *testing.T) {
	f := newDonationFixture(t)
	ctx := context.Background()
	uid := f.userID(t, "gina")

	// Closed by default.
	rec, body := f.do(t, f.user, "POST", "/api/donations", itoa(uid), map[string]any{
		"description": "x", "new_endpoint": map[string]any{
			"base_url": "https://api.example.com",
			"keys":     []map[string]any{{"secret": "sk-secret-value"}},
		}})
	if rec.Code != http.StatusForbidden || body["error"] == nil {
		t.Fatalf("closed intake POST = %d %v", rec.Code, body)
	}

	// Seed one donation directly so non-create verbs have a target.
	in := db.CreateDonationInput{UserID: uid, Description: "seeded", Now: 100,
		New: &db.NewEndpointSpec{ConnectorType: "openai-compatible", BaseURL: "https://api.example.com"}}
	in.Keys = []db.NewKeySpec{{Secret: []byte("sk-seed"), DisplayHead: "h", DisplayTail: "t"}}
	d, err := f.st.CreateDonation(ctx, in)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	rec, _ = f.do(t, f.user, "GET", "/api/donations", itoa(uid), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("closed-intake GET list = %d, want 200", rec.Code)
	}
	rec, _ = f.do(t, f.user, "GET", "/api/donations/"+itoa(d.ID), itoa(uid), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("closed-intake GET = %d, want 200", rec.Code)
	}

	enableDonationAccept(t, f)
	rec, body = f.do(t, f.user, "POST", "/api/donations", itoa(uid), map[string]any{
		"description": "open", "existing_endpoint": map[string]any{"endpoint_id": d.EndpointID, "key_ids": []int64{1}}})
	if rec.Code == http.StatusForbidden {
		t.Fatalf("open intake rejected: %d %v", rec.Code, body)
	}
}

func itoa(v int64) string {
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

// TestDonationOwnershipIsolationAndSecretNonEcho proves cross-user access is
// indistinguishable from missing rows, and that no response ever contains
// secret or note material.
func TestDonationOwnershipIsolationAndSecretNonEcho(t *testing.T) {
	f := newDonationFixture(t)
	alice := f.userID(t, "alice")
	bob := f.userID(t, "bob")
	enableDonationAccept(t, f)

	secretValue := "sk-super-secret-material"
	rec, body := f.do(t, f.user, "POST", "/api/donations", itoa(alice), map[string]any{
		"description": "mine",
		"new_endpoint": map[string]any{
			"base_url": "https://api.example.com",
			"note":     "endpoint-note",
			"keys":     []map[string]any{{"secret": secretValue, "note": "key-note"}},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %v", rec.Code, body)
	}
	if strings.Contains(rec.Body.String(), secretValue) || strings.Contains(rec.Body.String(), "key-note") {
		t.Fatal("response echoes secret material or key note")
	}
	id := int64(body["id"].(float64))

	// Bob cannot see, edit, or delete Alice's donation; every path is 404.
	for _, tc := range []struct {
		method, target string
		body           any
	}{
		{"GET", "/api/donations/" + itoa(id), nil},
		{"PATCH", "/api/donations/" + itoa(id), map[string]any{"description": "hijack"}},
		{"DELETE", "/api/donations/" + itoa(id), nil},
	} {
		rec, _ := f.do(t, f.user, tc.method, tc.target, itoa(bob), tc.body)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s cross-user = %d, want 404", tc.method, rec.Code)
		}
	}
	// The list of Bob contains nothing of Alice's.
	rec, _ = f.do(t, f.user, "GET", "/api/donations", itoa(bob), nil)
	if strings.Contains(rec.Body.String(), "mine") {
		t.Fatal("cross-user donation leaked into list")
	}
	// Alice still sees hers with keys/reviews projections only.
	rec, _ = f.do(t, f.user, "GET", "/api/donations/"+itoa(id), itoa(alice), nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"display_head"`) {
		t.Fatalf("owner read = %d %s", rec.Code, rec.Body.String())
	}
}

// TestDonationKeyLimitBoundsOnWire checks the donor-supplied per-key limits:
// negative or over-ceiling values are invalid_request before anything is
// written.
func TestDonationKeyLimitBoundsOnWire(t *testing.T) {
	f := newDonationFixture(t)
	uid := f.userID(t, "hank")
	enableDonationAccept(t, f)

	base := func(mc, rpm any) map[string]any {
		key := map[string]any{"secret": "sk-x"}
		if mc != nil {
			key["max_concurrency"] = mc
		}
		if rpm != nil {
			key["rpm_limit"] = rpm
		}
		return map[string]any{
			"description": "limits",
			"new_endpoint": map[string]any{
				"base_url": "https://api.example.com",
				"keys":     []map[string]any{key},
			},
		}
	}
	for name, payload := range map[string]map[string]any{
		"negative concurrency": base(-1, nil),
		"over-ceiling rpm":     base(nil, db.MaxDonationKeyRPM+1),
	} {
		rec, _ := f.do(t, f.user, "POST", "/api/donations", itoa(uid), payload)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d, want 400 invalid_request", name, rec.Code)
		}
	}
	// A valid boundary value passes.
	rec, _ := f.do(t, f.user, "POST", "/api/donations", itoa(uid), base(db.MaxDonationKeyConcurrency, db.MaxDonationKeyRPM))
	if rec.Code != http.StatusCreated {
		t.Fatalf("boundary values = %d, want 201", rec.Code)
	}
}

// TestReviewSurfaceIdentityMatrix pins the reviewer authorization: both admin
// and level5 roles reach the shared service; an unauthenticated caller is
// refused; the repository independently refuses a forged role so a miswired
// frame can never grant review power.
func TestReviewSurfaceIdentityMatrix(t *testing.T) {
	f := newDonationFixture(t)
	ctx := context.Background()
	uid := f.userID(t, "ivy")
	enableDonationAccept(t, f)

	rec, created := f.do(t, f.user, "POST", "/api/donations", itoa(uid), map[string]any{
		"description": "review me",
		"new_endpoint": map[string]any{
			"base_url": "https://api.example.com",
			"keys":     []map[string]any{{"secret": "sk-r"}},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup create = %d", rec.Code)
	}
	id := int64(created["id"].(float64))
	target := "/admin/api/donations/" + itoa(id)

	// Unauthenticated caller: refused before any business logic.
	req := httptest.NewRequest("PATCH", target, bytes.NewBufferString(`{"action":"approve"}`))
	recUnauth := httptest.NewRecorder()
	f.rev.ServeHTTP(recUnauth, req)
	if recUnauth.Code != http.StatusForbidden && recUnauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated review = %d", recUnauth.Code)
	}

	// Ordered slice, NOT a map: approve must precede disable because the
	// state machine requires an enabled approval before a disable.
	for _, step := range []struct{ role, action string }{{"admin", "approve"}, {"level5", "disable"}} {
		role, action := step.role, step.action
		body := `{"action":"` + action + `","note":"ok"}`
		req := httptest.NewRequest("PATCH", target, bytes.NewBufferString(body))
		req.Header.Set("X-Test-Role", role)
		rec := httptest.NewRecorder()
		f.rev.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("role %s %s review = %d %s", role, action, rec.Code, rec.Body.String())
		}
		reviews, err := f.st.ListDonationReviews(ctx, uid, id)
		if err != nil {
			t.Fatalf("reviews: %v", err)
		}
		last := reviews[len(reviews)-1]
		wantAction := db.ReviewActionApprove
		if role == "level5" {
			wantAction = db.ReviewActionDisable
		}
		if last.ReviewerRole != role || last.Action != wantAction {
			t.Fatalf("audit entry = %+v, want %s/%s", last, role, wantAction)
		}
	}

	// Repository-side forgery guard: an unknown role never reaches storage.
	_, err := f.svc.Review(ctx, db.ReviewDecision{
		DonationID: id, Role: "superuser", ReviewerID: uid, Action: db.ReviewActionDisable, Now: 500,
	})
	if !errors.Is(err, db.ErrConflict) {
		t.Fatalf("forged role = %v, want ErrConflict", err)
	}

	// Steward delete works through the same service without simulating admin.
	rec2, deleted := f.do(t, f.user, "POST", "/api/donations", itoa(uid), map[string]any{
		"description": "delete me",
		"new_endpoint": map[string]any{
			"base_url": "https://api.example.com",
			"keys":     []map[string]any{{"secret": "sk-del"}},
		},
	})
	if rec2.Code != http.StatusCreated {
		t.Fatalf("second create = %d", rec2.Code)
	}
	id2 := int64(deleted["id"].(float64))
	reqDel := httptest.NewRequest("DELETE", "/admin/api/donations/"+itoa(id2), nil)
	reqDel.Header.Set("X-Test-Role", "level5")
	recDel := httptest.NewRecorder()
	f.rev.ServeHTTP(recDel, reqDel)
	if recDel.Code != http.StatusNoContent {
		t.Fatalf("steward delete = %d %s", recDel.Code, recDel.Body.String())
	}
}

func TestReviewListQueryAllowlistForAdminAndLevel5(t *testing.T) {
	f := newDonationFixture(t)
	doList := func(role, query string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/admin/api/donations"+query, nil)
		req.Header.Set("X-Test-Role", role)
		rec := httptest.NewRecorder()
		f.rev.ServeHTTP(rec, req)
		return rec
	}

	accepted := []string{
		"", // missing status is the one canonical all-status representation
		"?status=pending", "?status=approved", "?status=rejected", "?status=deleted",
		"?page=2&page_size=1", "?page=3&page_size=100&status=pending",
	}
	for _, role := range []string{"admin", "level5"} {
		for _, query := range accepted {
			rec := doList(role, query)
			if rec.Code != http.StatusOK {
				t.Fatalf("role=%s query=%q status=%d body=%s", role, query, rec.Code, rec.Body.String())
			}
		}
	}

	rejected := []string{
		"?status=", "?status=all", "?status=unknown",
		"?status=pending&status=approved", "?page=1&page=2", "?page_size=1&page_size=2",
		"?status=pending;ignored=1",
		"?unknown=1", "?page=0", "?page=01", "?page=-1", "?page_size=0", "?page_size=101",
	}
	for _, role := range []string{"admin", "level5"} {
		for _, query := range rejected {
			rec := doList(role, query)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("role=%s rejected query=%q status=%d body=%s", role, query, rec.Code, rec.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] == nil {
				t.Fatalf("role=%s query=%q missing error envelope: %s", role, query, rec.Body.String())
			}
		}
	}
}

func TestReviewListRejectsOverflowBeforeServiceForAdminAndLevel5(t *testing.T) {
	query := "?page=" + strconv.Itoa(math.MaxInt) + "&page_size=2"
	for _, tc := range []struct {
		name   string
		prefix string
		role   string
	}{
		{name: "admin", prefix: "/admin/api", role: db.ReviewRoleAdmin},
		{name: "level5", prefix: "/api/steward", role: db.ReviewRoleLevel5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolved := 0
			h := NewReviewHandler(tc.prefix, nil, func(*http.Request) (ReviewerIdentity, error) {
				resolved++
				return ReviewerIdentity{UserID: 1, Role: tc.role}, nil
			})
			req := httptest.NewRequest(http.MethodGet, tc.prefix+"/donations"+query, nil)
			rec := httptest.NewRecorder()
			h.Handler().ServeHTTP(rec, req)
			if resolved != 1 {
				t.Fatalf("identity resolutions=%d, want one live resolution", resolved)
			}
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("overflow query status=%d body=%s", rec.Code, rec.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] == nil {
				t.Fatalf("overflow query missing error envelope: %s", rec.Body.String())
			}
			// svc is deliberately nil: reaching Service/ListForReview (and thus
			// the database) would panic this test instead of returning 400.
		})
	}
}
