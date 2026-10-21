package logapi

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func seedLogResourceTargets(f *logFixture) {
	f.mustExec(`INSERT INTO charity_models(id,full_name) VALUES(701,'CHARITY-LOGICAL-MODEL-SENTINEL')`)
	f.mustExec(`INSERT INTO charity_reservations(logical_request_id,charity_model_id) VALUES(?,701)`, f.charityID)
	f.mustExec(`INSERT INTO donations(id) VALUES(801),(802)`)
	// Both keys use the same endpoint key. The immutable endpoint snapshot
	// cannot distinguish which donation supplied an individual attempt.
	f.mustExec(`INSERT INTO donation_keys(id,donation_id,endpoint_key_id) VALUES(9007199254740993,801,302),(9007199254740994,802,302)`)
	f.mustExec(`INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,donation_key_id)
VALUES(?,?,1,'charity',9007199254740993),(?,?,2,'charity',9007199254740994)`,
		logOpaqueID("clm_", 21), f.charityID, logOpaqueID("clm_", 22), f.charityID)
}

func TestCharityInferenceRouteJoinsPreserveResourceOwnership(t *testing.T) {
	f := newLogFixture(t)
	seedLogResourceTargets(f)
	f.mustExec(`INSERT INTO users(id,username) VALUES(?,'caller')`, logUserOne)
	for _, operation := range []string{"chat_completions", "embeddings", "images_generations", "decisions", "responses", "responses_compact", "responses_input_tokens", "completions", "images_edits", "images_variations", "audio_speech", "audio_transcriptions", "audio_translations", "moderations", "rerank", "systemone"} {
		f.mustExec(`UPDATE request_logs SET route_kind=? WHERE logical_request_id=?`, "charity_"+operation, f.charityID)
		var caller, model, donation int64
		if err := f.db.QueryRow(`SELECT u.id,cm.id FROM request_logs l`+callerIdentityJoin+`WHERE l.logical_request_id=?`, f.charityID).Scan(&caller, &model); err != nil || caller != logUserOne || model != 701 {
			t.Fatalf("%s caller/model = %d/%d, %v", operation, caller, model, err)
		}
		if err := f.db.QueryRow(`SELECT d.id FROM request_attempts a`+managementAttemptTargetJoin+`WHERE l.logical_request_id=? AND a.attempt_seq=1`, f.charityID).Scan(&donation); err != nil || donation != 801 {
			t.Fatalf("%s donation = %d, %v", operation, donation, err)
		}
	}
}

func requireLogResourceID(t *testing.T, value *string, want string) {
	t.Helper()
	if want == "" {
		if value != nil {
			t.Fatalf("resource ID = %q, want absent", *value)
		}
		return
	}
	if value == nil || *value != want {
		t.Fatalf("resource ID = %v, want %q", value, want)
	}
}

func requireManagementResourceTargets(t *testing.T, f *logFixture, model, firstDonation, firstKey string) {
	t.Helper()
	ctx := context.Background()
	admin, err := f.repo.GetAdmin(ctx, f.charityID, AttemptFilter{})
	if err != nil {
		t.Fatal(err)
	}
	steward, err := f.repo.GetSteward(ctx, 999, f.charityID, AttemptFilter{}, allowLogStewardRead{})
	if err != nil {
		t.Fatal(err)
	}
	requireManagementLogEqual(t, admin, steward)
	requireLogResourceID(t, admin.Request.CharityModelID, model)
	if len(admin.Attempts.Data) != 2 {
		t.Fatalf("attempt count = %d", len(admin.Attempts.Data))
	}
	requireLogResourceID(t, admin.Attempts.Data[0].DonationID, firstDonation)
	requireLogResourceID(t, admin.Attempts.Data[0].DonationKeyID, firstKey)
	requireLogResourceID(t, admin.Attempts.Data[1].DonationID, "802")
	requireLogResourceID(t, admin.Attempts.Data[1].DonationKeyID, "9007199254740994")
	adminList, err := f.repo.ListAdmin(ctx, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	stewardList, err := f.repo.ListSteward(ctx, 999, ListFilter{}, allowLogStewardRead{})
	if err != nil {
		t.Fatal(err)
	}
	requireManagementLogEqual(t, adminList, stewardList)
	for _, row := range adminList.Data {
		want := ""
		if row.ID == f.charityID {
			want = model
		}
		requireLogResourceID(t, row.CharityModelID, want)
	}
}

func TestManagementLogTargetsUsePersistedIDsAcrossRenameAndRetries(t *testing.T) {
	for _, route := range []RouteKind{RouteCharityChat, RouteCharityEmbeddings, RouteCharityImages, RouteCharityDecisions} {
		t.Run(string(route), func(t *testing.T) {
			f := newNumberedLogFixture(t)
			seedLogResourceTargets(f)
			f.mustExec(`UPDATE request_logs SET route_kind=? WHERE logical_request_id=?`, route, f.charityID)
			f.mustExec(`UPDATE charity_models SET full_name='Renamed live model' WHERE id=701`)
			f.mustExec(`INSERT INTO charity_models(id,full_name) VALUES(702,'CHARITY-LOGICAL-MODEL-SENTINEL')`)
			requireManagementResourceTargets(t, f, "701", "801", "9007199254740993")

			ctx := context.Background()
			modelFilter := "CHARITY-LOGICAL-MODEL-SENTINEL"
			filter := ListFilter{CharityModel: &modelFilter, Page: &pagination.Request{Page: 99, Size: 10}}
			adminPage, err := f.repo.ListAdmin(ctx, filter)
			if err != nil || len(adminPage.Data) != 1 || adminPage.Data[0].ID != f.charityID {
				t.Fatalf("numbered request page = %+v, %v", adminPage, err)
			}
			requireLogPagination(t, adminPage.Pagination, 1, 10, 1)
			requireLogResourceID(t, adminPage.Data[0].CharityModelID, "701")
			stewardPage, err := f.repo.ListSteward(ctx, 999, filter, allowLogStewardRead{})
			if err != nil {
				t.Fatal(err)
			}
			requireManagementLogEqual(t, adminPage, stewardPage)
			pagedDetail, err := f.repo.GetSteward(ctx, 999, f.charityID, AttemptFilter{Page: &pagination.Request{Page: 99, Size: 10}}, allowLogStewardRead{})
			if err != nil || len(pagedDetail.Attempts.Data) != 2 {
				t.Fatalf("numbered attempt page = %+v, %v", pagedDetail, err)
			}
			requireLogPagination(t, pagedDetail.AttemptPagination, 1, 10, 2)
			requireLogResourceID(t, pagedDetail.Attempts.Data[0].DonationKeyID, "9007199254740993")
			requireLogResourceID(t, pagedDetail.Attempts.Data[1].DonationKeyID, "9007199254740994")

			first, err := f.repo.GetAdmin(ctx, f.charityID, AttemptFilter{Limit: 1})
			if err != nil || first.Attempts.NextCursor == nil || len(first.Attempts.Data) != 1 {
				t.Fatalf("first attempt page = %+v, %v", first, err)
			}
			next, err := f.repo.GetAdmin(ctx, f.charityID, AttemptFilter{Limit: 1, Cursor: *first.Attempts.NextCursor})
			if err != nil || len(next.Attempts.Data) != 1 {
				t.Fatalf("second attempt page = %+v, %v", next, err)
			}
			requireLogResourceID(t, next.Attempts.Data[0].DonationKeyID, "9007199254740994")

			// The ordinary caller cannot obtain these management identifiers.
			user, err := f.repo.GetUser(ctx, logUserOne, f.charityID, AttemptFilter{})
			if err != nil {
				t.Fatal(err)
			}
			userList, err := f.repo.ListUser(ctx, logUserOne, ListFilter{})
			if err != nil {
				t.Fatal(err)
			}
			requireNoJSONKeys(t, user, "charity_model_id", "donation_id", "donation_key_id")
			requireNoJSONKeys(t, userList, "charity_model_id", "donation_id", "donation_key_id")
		})
	}
}

func TestManagementLogTargetsDisappearWithExactRelations(t *testing.T) {
	for _, test := range []struct {
		name          string
		statement     string
		model         string
		firstDonation string
		firstKey      string
	}{
		{"missing reservation", `DELETE FROM charity_reservations`, "", "801", "9007199254740993"},
		{"missing model", `DELETE FROM charity_models WHERE id=701`, "", "801", "9007199254740993"},
		{"cleared model relation", `UPDATE charity_reservations SET charity_model_id=NULL`, "", "801", "9007199254740993"},
		{"missing claim", `DELETE FROM dispatch_claims WHERE attempt_seq=1`, "701", "", ""},
		{"missing key", `DELETE FROM donation_keys WHERE donation_id=801`, "701", "", ""},
		{"missing donation", `DELETE FROM donations WHERE id=801`, "701", "", ""},
		{"cleared key relation", `UPDATE dispatch_claims SET donation_key_id=NULL WHERE attempt_seq=1`, "701", "", ""},
		{"non charity claim", `UPDATE dispatch_claims SET purpose='self' WHERE attempt_seq=1`, "701", "", ""},
		{"different request claim", `UPDATE dispatch_claims SET logical_request_id='unrelated' WHERE attempt_seq=1`, "701", "", ""},
		{"different attempt claim", `UPDATE dispatch_claims SET attempt_seq=3 WHERE attempt_seq=1`, "701", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newLogFixture(t)
			seedLogResourceTargets(f)
			f.mustExec(test.statement)
			// A later object with the same model name or endpoint key must not
			// recreate a relation that was removed from the historical request.
			f.mustExec(`INSERT INTO charity_models(id,full_name) VALUES(702,'replacement')`)
			f.mustExec(`INSERT INTO donation_keys(id,donation_id,endpoint_key_id) VALUES(903,802,302)`)
			requireManagementResourceTargets(t, f, test.model, test.firstDonation, test.firstKey)
		})
	}
}

func TestManagementLogTargetsRequireCharityRoute(t *testing.T) {
	f := newLogFixture(t)
	seedLogResourceTargets(f)
	f.mustExec(`UPDATE request_logs SET route_kind=? WHERE logical_request_id=?`, RouteOpenAIChat, f.charityID)
	ctx := context.Background()
	admin, err := f.repo.GetAdmin(ctx, f.charityID, AttemptFilter{})
	if err != nil {
		t.Fatal(err)
	}
	steward, err := f.repo.GetSteward(ctx, 999, f.charityID, AttemptFilter{}, allowLogStewardRead{})
	if err != nil {
		t.Fatal(err)
	}
	requireManagementLogEqual(t, admin, steward)
	requireLogResourceID(t, admin.Request.CharityModelID, "")
	for _, attempt := range admin.Attempts.Data {
		requireLogResourceID(t, attempt.DonationID, "")
		requireLogResourceID(t, attempt.DonationKeyID, "")
	}
	user, err := f.repo.GetUser(ctx, logUserOne, f.charityID, AttemptFilter{})
	if err != nil {
		t.Fatal(err)
	}
	requireNoJSONKeys(t, user, "charity_model_id", "donation_id", "donation_key_id")
}

func TestManagementExportIncludesLiveModelTarget(t *testing.T) {
	f := newLogFixture(t)
	seedLogResourceTargets(f)
	ctx := context.Background()
	admin, err := f.repo.ExportAdmin(ctx, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	steward, err := f.repo.ExportSteward(ctx, 999, ListFilter{}, allowLogStewardRead{})
	if err != nil {
		t.Fatal(err)
	}
	requireManagementLogEqual(t, admin, steward)
	jsonBody, err := MarshalAdminJSON(admin)
	if err != nil {
		t.Fatal(err)
	}
	var decoded AdminLogExport
	if err := json.Unmarshal(jsonBody, &decoded); err != nil {
		t.Fatal(err)
	}
	requireLogResourceID(t, decoded.Data[1].CharityModelID, "701")
	csvBody, err := MarshalAdminCSV(steward)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(csvBody))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	column := -1
	for index, name := range rows[0] {
		if name == "charity_model_id" {
			column = index
		}
	}
	if column < 0 || rows[0][column] != "charity_model_id" || rows[2][column] != "701" || rows[1][column] != "" {
		t.Fatalf("CSV model targets = %v", rows)
	}
}
