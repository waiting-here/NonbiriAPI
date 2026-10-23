package charityrouting

import (
	"context"
	"errors"
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

func TestInferencePreflightRetainsAdmissionWithoutChatTextPolicy(t *testing.T) {
	e := newRoutingTestEnv(t)
	admin := e.seedUser(t, true, nil)
	caller := e.seedUser(t, false, nil)
	e.seedUserBalance(t, caller, "100000")
	e.createModel(t, 'I')
	if _, err := e.store.DB().Exec(`UPDATE site_config SET value='1000' WHERE key='charity_min_chars'`); err != nil {
		t.Fatal(err)
	}
	request, err := openai.DecodeInferenceRequest(strings.NewReader(`{"model":"[公益]provider/model","input":"x","max_output_tokens":10}`), contract.OperationResponses, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer request.Clear()
	if _, err := e.service.PreflightInference(context.Background(), caller, request.Model, request, routingTestNow); err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.PreflightInference(context.Background(), admin, request.Model, request, routingTestNow); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("admin bypass", err)
	}
	if _, err := e.service.PreflightInference(context.Background(), caller, request.Model, nil, routingTestNow); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("nil request", err)
	}
	if e.state.dueCalls.Load() != 0 {
		t.Fatal("preflight inspected physical candidates")
	}
}
