package aiagent

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

func writeDriverFailure(w http.ResponseWriter, err error) bool {
	if errors.Is(err, authz.ErrUnauthorized) || errors.Is(err, authz.ErrForbidden) || errors.Is(err, authz.ErrPermissionDenied) {
		return false
	}
	var failure *diagnostic.OperationError
	if !errors.As(err, &failure) {
		return false
	}
	message := "模型来源请求未完成，请查看错误详情后重试。"
	switch failure.Details.Code {
	case "upstream_authentication":
		message = "上游拒绝了当前凭据，请查看认证详情后重试。"
	case "upstream_account_restricted":
		message = "上游报告账号或工作区限制，请查看详情。"
	case "upstream_rate_limited":
		message = "上游暂时限制了请求，请按等待时间重试。"
	case "connection_changed":
		message = "连接已更换，请刷新后重试。"
	case "connection_disconnected", "credential_expired":
		message = "Codex连接已失效，请重新连接。"
	case "credential_unreadable":
		message = "无法读取已保存的凭据，请检查服务器凭据配置。"
	case "response_limit":
		message = "上游响应超过读取上限，请查看详情。"
	case "invalid_json", "invalid_response", "json_type_mismatch":
		message = "上游响应格式无法识别，请查看详情后重试。"
	}
	if failure.Details.Category == "timeout" {
		message = "上游请求超时，请重试。"
	}
	code, source := httperr.CodeUpstream, httperr.SourceUpstream
	switch failure.SiteStatus {
	case 400:
		code, source = httperr.CodeInvalidRequest, httperr.SourcePlatform
	case 409:
		code, source = httperr.CodeConflict, httperr.SourcePlatform
	case 500:
		code, source = httperr.CodeInternal, httperr.SourcePlatform
	case 503:
		code = httperr.CodeServiceUnavailable
	}
	if wait := failure.Details.RetryAfterSeconds; wait != nil {
		w.Header().Set("Retry-After", strconv.FormatInt(*wait, 10))
	}
	// This route is administrator-only. Generic public error envelopes do not
	// gain access to account diagnostics or acquire new session semantics.
	httperr.WriteJSON(w, failure.SiteStatus, map[string]any{"error": map[string]any{
		"code": code, "source": source, "message": message, "request_id": failure.ID,
		"operation_diagnostic": failure,
	}})
	return true
}
