package donation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

func TestRecurringHTTPExplainsPastFirstResetAndAllowsCorrection(t *testing.T) {
	for _, role := range []reviewerRole{reviewerAdmin, reviewerSteward} {
		for _, language := range []string{"en", "zh"} {
			t.Run(string(role)+"/"+language, func(t *testing.T) {
				e := newDonationTestEnv(t)
				e.clock.Store(time.Date(2026, 11, 1, 3, 41, 9, 0, time.UTC).Unix())
				owner := e.seedUser(t, "recurring-date-owner", nil, false)
				level := int64(6)
				actor := e.seedUser(t, "recurring-date-steward", &level, false)
				e.seedUser(t, "", nil, true)
				_, key := e.seedEndpointKey(t, owner, 'r')
				d := e.createDonation(t, owner, key)
				mux := http.NewServeMux()
				registrar := recurringHTTPRegistrar{mux: mux, actor: actor}
				prefix := "/admin/api"
				if role == reviewerAdmin {
					if err := RegisterAdminRoutes(registrar, e.service); err != nil {
						t.Fatal(err)
					}
				} else {
					prefix = "/api/steward"
					if err := RegisterStewardRoutes(registrar, e.service); err != nil {
						t.Fatal(err)
					}
				}
				path := prefix + "/donations/" + d.ID + "/keys/" + d.Keys[0].ID + "/recurring-limits"
				request := func(body, seed string) *httptest.ResponseRecorder {
					r := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
					r.Header.Set("Idempotency-Key", strings.Repeat(seed, 22))
					w := httptest.NewRecorder()
					mux.ServeHTTP(httperr.WithLanguage(w, language), r)
					return w
				}
				body := `{"expected_revision":"1","rules":[{"id":null,"mode":"reset","interval":"month","alignment":"exact_time","anchor_local":"2026-10-30T23:59:59","time_zone":"America/Indianapolis","week_starts_on":null,"metric":"credits","limit":"240000"}]}`
				w := request(body, "A")
				var failure httperr.Envelope
				if w.Code != http.StatusBadRequest || json.Unmarshal(w.Body.Bytes(), &failure) != nil {
					t.Fatalf("past date response: %d %s", w.Code, w.Body.String())
				}
				want := FirstResetNotFutureMessage
				if language == "zh" {
					want = "新建循环规则或改用指定刷新时间时，首次刷新必须在未来。请按规则时区选择更晚的日期与时间。"
				}
				if failure.Error.Code != httperr.CodeInvalidRequest || failure.Error.Message != "[NonbiriAPI] "+want {
					t.Fatalf("missing actionable reason: %+v", failure.Error)
				}
				read := func() RecurringLimits {
					v, err := e.service.RecurringAdmin(context.Background(), parseTestID(t, d.ID), parseTestID(t, d.Keys[0].ID))
					if err != nil {
						t.Fatal(err)
					}
					return v
				}
				if v := read(); v.DonationRevision != "1" || len(v.Rules) != 0 {
					t.Fatalf("rejected request changed rules or revision: %+v", v)
				}
				w = request(strings.Replace(body, "2026-10-30T23:59:59", "2026-11-30T23:59:59", 1), "B")
				if w.Code != http.StatusOK {
					t.Fatalf("corrected date response: %d %s", w.Code, w.Body.String())
				}
				v := read()
				if v.DonationRevision != "2" || len(v.Rules) != 1 || *v.Rules[0].AnchorLocal != "2026-11-30T23:59:59" || v.Rules[0].Limit != "240000" {
					t.Fatalf("corrected rule was not retained: %+v", v)
				}
			})
		}
	}
}
