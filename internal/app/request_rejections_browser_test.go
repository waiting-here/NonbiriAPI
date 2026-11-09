package app

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connector"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
)

// Reuse the pure connector preflight and production zero-cost rejection
// recorder; the original option name and content never enter the log seed.
func seedRequestRejectionsBrowser(t *testing.T, f *imageBrowserFixture) map[string]any {
	t.Helper()
	const field = "PRIVATE_REJECTION_OPTION"
	const value = "PRIVATE_REJECTION_VALUE"
	const input = "PRIVATE_REJECTION_INPUT"
	body := `{"model":"[公益]synthetic/rejection","messages":[{"role":"user","content":"` + input + `"}],"` + field + `":"` + value + `"}`
	request, err := openai.DecodeChatRequest(strings.NewReader(body), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer request.Clear()
	err = connector.NewDefaultRegistry().CheckTargetRequest(contract.NewTarget(contract.TypeAnthropicCompatible, "https://synthetic.invalid", "model"), contract.OperationChatCompletions, request, nil, contract.AttemptPolicy{})
	var rejected *contract.RequestRejection
	if !errors.As(err, &rejected) {
		t.Fatal("expected synthetic unsupported field rejection", err)
	}
	owner, err := strconv.ParseInt(f.users[0].ID, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	record := func(detail *requestattempt.RejectionDetail) string {
		ctx, id, err := requestattempt.New(t.Context(), owner, "POST", "/v1/chat/completions")
		if err != nil {
			t.Fatal(err)
		}
		requestattempt.Classify(ctx, "charity")
		fact := requestattempt.Fact{ID: id, Method: "POST", Path: "/v1/chat/completions", Stage: "preflight", Reason: "invalid_request", Model: "[公益]synthetic/rejection", Status: 400, Code: "invalid_request", Detail: detail}
		if err = f.app.Load().claims.RecordRejection(ctx, owner, fact); err != nil {
			t.Fatal(err)
		}
		return id
	}
	return map[string]any{
		"precise_id":      record(requestattempt.NewDetail(rejected.Field, rejected.Reason)),
		"historical_id":   record(requestattempt.NewDetail("request", "unsupported top-level field")),
		"private_markers": []string{field, value, input},
	}
}
