package activities

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestThursdayLiteratureHTTPNormalizationAndBounds(t *testing.T) {
	opensAt := beijingThursday(2027, 5, 6)
	fixture := newActivityFixture(t, opensAt-60)
	service, err := NewService(ServiceConfig{Repository: fixture.repository})
	if err != nil {
		t.Fatal(err)
	}
	users, admins := &activityUserRoutes{}, &activityAdminRoutes{}
	if err := RegisterRoutes(users, admins, service); err != nil {
		t.Fatal(err)
	}
	put := func(literature, revision, key string) *httptest.ResponseRecorder {
		t.Helper()
		payload, err := json.Marshal(map[string]any{
			"expected_revision": revision, "period_key": "2027-05-06", "opens_at": opensAt,
			"literature": literature, "entry": "0.1", "per_user_limit": 2,
			"pumps_bp": map[string]int{"platform": 100, "welfare": 100, "next_pool": 100},
		})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPut, routeAdminThursdayNext, bytes.NewReader(payload))
		request.Header.Set("Idempotency-Key", key)
		response := httptest.NewRecorder()
		admins.handlers[http.MethodPut+" "+routeAdminThursdayNext](response, request, AdminPrincipal{UserID: fixture.adminID})
		return response
	}
	first := put("First line\r\n\tSecond line", "1", "thursday-multiline-replay")
	if first.Code != 200 {
		t.Fatalf("save: %d %s", first.Code, first.Body.String())
	}
	var period Period
	if err := json.Unmarshal(first.Body.Bytes(), &period); err != nil {
		t.Fatal(err)
	}
	if period.Literature != "First line\n\tSecond line" {
		t.Fatalf("stored literature: %q", period.Literature)
	}
	replay := put("First line\n\tSecond line", "1", "thursday-multiline-replay")
	if replay.Code != 200 || !bytes.Equal(first.Body.Bytes(), replay.Body.Bytes()) {
		t.Fatalf("normalized replay: %d %s", replay.Code, replay.Body.String())
	}
	for index, value := range []string{"lone\rreturn", "nul\x00", "vertical\vtab", "del\x7f", "c1\u0085", strings.Repeat("界", 1025)} {
		response := put(value, period.Revision, "thursday-invalid-text-"+strconv.Itoa(index))
		if response.Code != 400 {
			t.Fatalf("invalid text %d: %d %s", index, response.Code, response.Body.String())
		}
	}
	if validReason("two\nlines") || validLiterature(string([]byte{0xff})) {
		t.Fatal("unrelated control or malformed UTF-8 accepted")
	}
	for index, value := range []string{"", strings.Repeat("🙂", 1024)} {
		response := put(value, period.Revision, "thursday-valid-bound-"+strconv.Itoa(index))
		if response.Code != 200 {
			t.Fatalf("valid text: %d %s", response.Code, response.Body.String())
		}
		if err := json.Unmarshal(response.Body.Bytes(), &period); err != nil {
			t.Fatal(err)
		}
		if period.Literature != value {
			t.Fatal("text changed")
		}
	}
}

func TestThursdayPreviewVisibilityDoesNotOpenContributions(t *testing.T) {
	opensAt := beijingThursday(2027, 5, 13)
	fixture := newActivityFixture(t, opensAt-1)
	userID, _ := fixture.seedUser("preview", false)
	fixture.fundUser(userID, 2000)
	created, _, err := fixture.repository.PutThursdayNext(context.Background(), fixture.adminID,
		fixture.control(http.MethodPut, routeAdminThursdayNext, map[string]any{"preview": true}),
		ThursdayNextMutation{ExpectedRevision: fixture.configRevision(), PeriodKey: "2027-05-13", OpensAt: opensAt,
			Literature: "Next week\nNew event", Entry: "0.1", PerUserLimit: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, switches := range [][2]bool{{false, false}, {true, false}, {false, true}, {true, true}} {
		fixture.setActivityConfig(switches[0], false, switches[1], 0, 0)
		view, err := fixture.repository.GetActivities(context.Background(), userID)
		if err != nil {
			t.Fatal(err)
		}
		visible := switches[0] && switches[1]
		if (view.Thursday.Next != nil) != visible || view.Thursday.Current != nil {
			t.Fatalf("switches %v: %+v", switches, view.Thursday)
		}
		if visible {
			next := view.Thursday.Next
			if next.Literature != created.Value.Literature || next.Entry != "0.1" || next.PerUserLimit != 2 || next.ClosesAt != opensAt+86400 {
				t.Fatalf("preview: %+v", next)
			}
		}
	}
	contribution := func(key string) error {
		_, _, err := fixture.repository.ContributeThursday(context.Background(), userID,
			fixture.control(http.MethodPost, routeThursdayContributions, map[string]any{"timing": key}),
			ThursdayContributionInput{PeriodID: created.Value.ID, ExpectedRevision: 1})
		return err
	}
	if err := contribution("early"); err == nil {
		t.Fatal("preview accepted a contribution")
	}
	assertUserBalance(t, fixture.store.DB(), userID, "2000")
	fixture.clock.Store(opensAt)
	view, err := fixture.repository.GetActivities(context.Background(), userID)
	if err != nil || view.Thursday.Current == nil || view.Thursday.Next != nil || view.Thursday.State != ThursdayStateOpen {
		t.Fatalf("open: %+v %v", view.Thursday, err)
	}
	if err := contribution("open"); err != nil {
		t.Fatal(err)
	}
	assertUserBalance(t, fixture.store.DB(), userID, "1900")
}
