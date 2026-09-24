package logapi

import (
	"context"
	"strings"
	"testing"
)

func TestManagementCharityModelUsesRequestedSnapshot(t *testing.T) {
	for _, route := range []RouteKind{RouteCharityChat, RouteCharityEmbeddings} {
		t.Run(string(route), func(t *testing.T) {
			f := newLogFixture(t)
			const model = "=Public model <example>"
			f.mustExec(`UPDATE request_logs SET route_kind=?,model=? WHERE logical_request_id=?`, route, model, f.charityID)
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
			if admin.Request.CharityModel == nil || *admin.Request.CharityModel != model {
				t.Fatalf("requested model lost: %+v", admin.Request)
			}
			rows, err := f.repo.ListAdmin(ctx, ListFilter{})
			if err != nil {
				t.Fatal(err)
			}
			matched := false
			for _, row := range rows.Data {
				if row.ID == f.charityID {
					matched = row.CharityModel != nil && *row.CharityModel == model
				} else if row.CharityModel != nil {
					t.Fatalf("private logical model leaked: %+v", row)
				}
			}
			if !matched {
				t.Fatal("list omitted requested model")
			}
			exported, err := f.repo.ExportSteward(ctx, 999, ListFilter{}, allowLogStewardRead{})
			if err != nil {
				t.Fatal(err)
			}
			csv, err := MarshalAdminCSV(exported)
			if err != nil || !strings.Contains(string(csv), "'=Public model <example>") {
				t.Fatalf("unsafe or missing CSV model: %s %v", csv, err)
			}
			f.mustExec(`UPDATE request_logs SET user_id=NULL WHERE logical_request_id=?`, f.charityID)
			detached, err := f.repo.GetAdmin(ctx, f.charityID, AttemptFilter{})
			if err != nil || detached.Request.CharityModel == nil || *detached.Request.CharityModel != model {
				t.Fatalf("historical model lost: %+v %v", detached, err)
			}
		})
	}
}
