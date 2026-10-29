package logapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
)

func TestPrehandlerPhaseFiltersAndManagementExport(t *testing.T) {
	f := newLogFixture(t)
	f.mustExec(`UPDATE request_logs SET rejection_stage='flow',rejection_reason='user_rpm',request_method='POST',request_path='/v1/chat/completions',caller_result_class='failed',caller_status=429,caller_error_code='rate_limited',attempt_count=0,uncached_input_tokens=0,cache_write_input_tokens=0,cache_read_input_tokens=0,output_tokens=0,usage_unknown=0 WHERE logical_request_id=?`, f.charityID)
	ctx := context.Background()
	owner, err := f.repo.ListUser(ctx, logUserOne, ListFilter{Phase: "pre_handler"})
	if err != nil || len(owner.Data) != 1 {
		t.Fatal(owner, err)
	}
	row, ok := owner.Data[0].(UserCharityLogRow)
	if !ok || row.Phase != "pre_handler" || *row.RejectionReason != "user_rpm" || *row.RequestMethod != "POST" {
		t.Fatal("missing refusal fields", owner)
	}
	admin, err := f.repo.ListAdmin(ctx, ListFilter{Phase: "pre_handler"})
	if err != nil || len(admin.Data) != 1 || admin.Data[0].ID != f.charityID {
		t.Fatal(admin, err)
	}
	steward, err := f.repo.ListSteward(ctx, 999, ListFilter{Phase: "pre_handler"}, allowLogStewardRead{})
	if err != nil || len(steward.Data) != 1 || steward.Data[0].Phase != "pre_handler" {
		t.Fatal(steward, err)
	}
	ordinary, err := f.repo.ListUser(ctx, logUserOne, ListFilter{Phase: "handler"})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range ordinary.Data {
		switch v := r.(type) {
		case UserSelfLogRow:
			if v.Phase != "handler" {
				t.Fatal(v)
			}
		case UserCharityLogRow:
			if v.Phase != "handler" {
				t.Fatal(v)
			}
		}
	}
	exported, err := f.repo.ExportAdmin(ctx, ListFilter{Phase: "pre_handler"})
	if err != nil || len(exported) != 1 {
		t.Fatal(exported, err)
	}
	body, err := MarshalAdminCSV(exported)
	if err != nil || !strings.Contains(string(body), "pre_handler,flow,user_rpm,POST,/v1/chat/completions") {
		t.Fatal(string(body), err)
	}
	for _, query := range []string{"phase=", "phase=unknown", "phase=handler&phase=pre_handler"} {
		if _, err := parseListFilter(query, "user", false); err == nil {
			t.Fatal("accepted invalid phase", query)
		}
	}
}

func TestSafeRejectionDetailAllRolesAndExports(t *testing.T) {
	f := newNumberedLogFixture(t)
	f.mustExec(`UPDATE request_logs SET rejection_stage='preflight',rejection_reason='invalid_request',request_method='POST',request_path='/v1/chat/completions',caller_result_class='failed',caller_status=400,caller_error_code='invalid_request',attempt_count=0,uncached_input_tokens=0,cache_write_input_tokens=0,cache_read_input_tokens=0,output_tokens=0,usage_unknown=0 WHERE logical_request_id=?`, f.charityID)
	want := requestattempt.NewDetail("stream", "expected a boolean or null")
	for _, diag := range []string{requestattempt.EncodeDetail(want), "", `raw error PRIVATE_INPUT`, `{"field":"PRIVATE_FIELD","reason":"PRIVATE_INPUT"}`} {
		f.mustExec(`UPDATE request_logs SET error_diag=? WHERE logical_request_id=?`, diag, f.charityID)
		ctx := context.Background()
		admin, err := f.repo.ListAdmin(ctx, ListFilter{Phase: "pre_handler"})
		if err != nil || len(admin.Data) != 1 {
			t.Fatal(admin, err)
		}
		expected := diag == requestattempt.EncodeDetail(want)
		got := admin.Data[0].RejectionDetail
		if expected && (got == nil || *got != *want) || !expected && got != nil {
			t.Fatal("invalid projection", got)
		}
		detail, err := f.repo.GetAdmin(ctx, f.charityID, AttemptFilter{})
		if err != nil {
			t.Fatal(err)
		}
		steward, err := f.repo.GetSteward(ctx, 999, f.charityID, AttemptFilter{}, allowLogStewardRead{})
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range []*requestattempt.RejectionDetail{detail.Request.RejectionDetail, steward.Request.RejectionDetail} {
			if expected && (d == nil || *d != *want) || !expected && d != nil {
				t.Fatal("read guessed a cause", d)
			}
		}
		exported, err := f.repo.ExportSteward(ctx, 999, ListFilter{Phase: "pre_handler"}, allowLogStewardRead{})
		if err != nil {
			t.Fatal(err)
		}
		csv, err := MarshalAdminCSV(exported)
		if err != nil {
			t.Fatal(err)
		}
		if expected && !strings.Contains(string(csv), "stream,expected a boolean or null") || strings.Contains(string(csv), "PRIVATE_") {
			t.Fatal(string(csv))
		}
		user, err := f.repo.GetUser(ctx, logUserOne, f.charityID, AttemptFilter{})
		if err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(user)
		if strings.Contains(string(body), "rejection_detail") != expected || strings.Contains(string(body), "PRIVATE_") {
			t.Fatal("unsafe or missing owner diagnostic", string(body))
		}
		ownerPage, err := f.repo.ListUser(ctx, logUserOne, ListFilter{Phase: "pre_handler"})
		if err != nil {
			t.Fatal(err)
		}
		ownerRows, err := f.repo.ExportUser(ctx, logUserOne, ListFilter{Phase: "pre_handler"})
		if err != nil {
			t.Fatal(err)
		}
		ownerJSON, err := MarshalUserJSON(ownerRows)
		if err != nil {
			t.Fatal(err)
		}
		ownerCSV, err := MarshalUserCSV(ownerRows)
		if err != nil {
			t.Fatal(err)
		}
		pageJSON, _ := json.Marshal(ownerPage)
		for _, projection := range []string{string(pageJSON), string(ownerJSON), string(ownerCSV)} {
			if strings.Contains(projection, want.Reason) != expected || strings.Contains(projection, "PRIVATE_") {
				t.Fatal("unsafe or missing owner export", projection)
			}
		}
		if _, err := f.repo.GetUser(ctx, logUserTwo, f.charityID, AttemptFilter{}); !errors.Is(err, ErrNotFound) {
			t.Fatal("cross-account diagnostic", err)
		}
	}
}

func TestHandlerErrorDiagnosticDoesNotBecomeUserRejectionDetail(t *testing.T) {
	f := newLogFixture(t)
	f.mustExec("UPDATE request_logs SET error_diag=? WHERE logical_request_id=?", requestattempt.EncodeDetail(requestattempt.NewDetail("stream", "expected a boolean")), f.charityID)
	detail, err := f.repo.GetUser(context.Background(), logUserOne, f.charityID, AttemptFilter{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(detail)
	if strings.Contains(string(body), "rejection_detail") {
		t.Fatal("handler error reclassified", string(body))
	}
}

func TestUndispatchedResourceDetailKeepsHandlerPhaseAndPrivacy(t *testing.T) {
	f := newLogFixture(t)
	want := requestattempt.NewDetail("model", "no usable model resource is available for this request")
	for _, tc := range []struct {
		code, source string
		attempts     int
		visible      bool
	}{
		{"unbound_model", "platform", 0, true},
		{"rate_limited", "platform", 0, true},
		{"unbound_model", "platform", 1, false},
		{"unbound_model", "upstream", 0, false},
		{"internal_error", "platform", 0, false},
	} {
		f.mustExec(`UPDATE request_logs SET caller_result_class='failed',caller_status=503,caller_error_code=?,error_source=?,attempt_count=?,error_diag=? WHERE logical_request_id=?`, tc.code, tc.source, tc.attempts, requestattempt.EncodeDetail(want), f.charityID)
		admin, err := f.repo.GetAdmin(t.Context(), f.charityID, AttemptFilter{})
		if err != nil {
			t.Fatal(err)
		}
		steward, err := f.repo.GetSteward(t.Context(), 999, f.charityID, AttemptFilter{}, allowLogStewardRead{})
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range []RejectionFields{admin.Request.RejectionFields, steward.Request.RejectionFields} {
			if row.Phase != "handler" || row.RejectionStage != nil || (row.RejectionDetail != nil) != tc.visible {
				t.Fatal("wrong detail boundary or phase", tc, row)
			}
			if tc.visible && *row.RejectionDetail != *want {
				t.Fatal("changed safe detail", row)
			}
		}
		owner, err := f.repo.GetUser(t.Context(), logUserOne, f.charityID, AttemptFilter{})
		if err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(owner)
		if strings.Contains(string(body), want.Reason) != tc.visible || !strings.Contains(string(body), `"phase":"handler"`) {
			t.Fatal("wrong owner detail", string(body))
		}
		if _, err := f.repo.GetUser(t.Context(), logUserTwo, f.charityID, AttemptFilter{}); !errors.Is(err, ErrNotFound) {
			t.Fatal("cross-account diagnostic", err)
		}
	}
}

func TestThinkingContinuationRejectionsKeepOwnerFieldsAndLocalizedReasons(t *testing.T) {
	f := newNumberedLogFixture(t)
	f.mustExec("UPDATE request_logs SET rejection_stage='preflight',rejection_reason='invalid_request',caller_result_class='failed',caller_status=400,caller_error_code='invalid_request',attempt_count=0 WHERE logical_request_id=?", f.charityID)
	for _, tc := range []struct{ field, reason, chinese string }{
		{"extra_content.anthropic", "tool thinking requires the original signed content blocks", "工具调用中的思考内容需要原始签名内容块"},
		{"thinking", "expected a supported thinking configuration", "需要受支持的思考配置"},
		{"thinking.display", "expected summarized, omitted, or updates", "需要 summarized、omitted 或 updates"},
		{"thinking.type", "this model uses adaptive thinking and does not support a manual budget", "此模型使用自适应思考，不支持手动预算"},
		{"tool_choice", "manual thinking requires auto or none tool choice", "手动思考要求 tool_choice 为 auto 或 none"},
		{"thinking.budget_tokens", "manual thinking requires an integer budget of at least 1024", "手动思考预算须为不小于 1024 的整数"},
		{"thinking.budget_tokens", "the thinking budget must be below max_tokens without supported interleaved thinking", "未启用受支持的交错思考时，思考预算必须小于 max_tokens"},
		{"thinking.budget_tokens", "this thinking mode does not accept a manual budget", "此思考模式不接受手动预算"},
		{"thinking.type", "this model requires manual thinking with a budget", "此模型需要带预算的手动思考"},
		{"tool_choice", "this model does not support forced tool use", "此模型不支持强制调用工具"},
		{"thinking.type", "unsupported thinking mode", "不支持此思考模式"},
		{"extra_content.gateway", "expected supported continuation parts and metadata", "需要受支持的续接内容块和元数据"},
		{"extra_content.gateway", "continuation parts must match the assistant message", "续接内容块必须与助手消息一致"},
		{"messages", "provider continuation requires the original permitted model connection", "续接对话需要使用原有且仍获准的模型连接"},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			detail := requestattempt.NewDetail(tc.field, tc.reason)
			if detail == nil || detail.Field != tc.field || detail.Suggestion == "" {
				t.Fatalf("missing fixed detail: %+v", detail)
			}
			f.mustExec("UPDATE request_logs SET error_diag=? WHERE logical_request_id=?", requestattempt.EncodeDetail(detail), f.charityID)
			owner, err := f.repo.GetUser(t.Context(), logUserOne, f.charityID, AttemptFilter{})
			if err != nil {
				t.Fatal(err)
			}
			page, err := f.repo.ListUser(t.Context(), logUserOne, ListFilter{Phase: "pre_handler"})
			if err != nil {
				t.Fatal(err)
			}
			rows, err := f.repo.ExportUser(t.Context(), logUserOne, ListFilter{Phase: "pre_handler"})
			if err != nil {
				t.Fatal(err)
			}
			for _, projection := range []any{owner, page} {
				raw, err := json.Marshal(projection)
				if err != nil || !strings.Contains(string(raw), tc.reason) || !strings.Contains(string(raw), "\"field\":\""+tc.field+"\"") {
					t.Fatalf("missing owner detail: %s %v", raw, err)
				}
			}
			ownerJSON, err := MarshalUserJSON(rows)
			if err != nil {
				t.Fatal(err)
			}
			ownerCSV, err := MarshalUserCSV(rows)
			if err != nil {
				t.Fatal(err)
			}
			for _, exported := range [][]byte{ownerJSON, ownerCSV} {
				if !strings.Contains(string(exported), tc.reason) || !strings.Contains(string(exported), tc.field) {
					t.Fatalf("missing owner export: %s", exported)
				}
			}
			for _, locale := range []string{"en", "zh"} {
				want := tc.reason
				if locale == "zh" {
					want = tc.chinese
				}
				if got := httperr.Localize(httperr.New(httperr.CodeInvalidRequest, tc.reason), locale); got.Message != want {
					t.Fatalf("%s detail: %s", locale, got.Message)
				}
			}
		})
	}
}
