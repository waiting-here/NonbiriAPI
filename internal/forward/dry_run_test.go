package forward

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

// dryRunSpyRepository deliberately implements both repository seams.  A dry
// validation is allowed to use the logical row only; a candidate query is a
// physical selection boundary and must remain untouched.
type dryRunSpyRepository struct {
	logicalCalls   atomic.Int32
	candidateCalls atomic.Int32
	logical        db.LogicalForwardRoute
}

func (r *dryRunSpyRepository) ListCallerModels(context.Context, int64, int) ([]db.CallerModel, error) {
	return nil, nil
}

func (r *dryRunSpyRepository) ResolveForwardRoute(context.Context, int64, string, int) (db.ForwardRoute, error) {
	r.candidateCalls.Add(1)
	return db.ForwardRoute{}, nil
}

func (r *dryRunSpyRepository) ResolveLogicalForwardRoute(context.Context, int64, string) (db.LogicalForwardRoute, error) {
	r.logicalCalls.Add(1)
	return r.logical, nil
}

type dryRunSpySelector struct{ calls *atomic.Int32 }

func (s dryRunSpySelector) Select(context.Context, Selection) ([]int64, error) {
	s.calls.Add(1)
	return nil, nil
}

type dryRunSpyRunner struct{ calls *atomic.Int32 }

func (r dryRunSpyRunner) Run(context.Context, http.ResponseWriter, AttemptInput) connectorcontract.AttemptResult {
	r.calls.Add(1)
	return connectorcontract.AttemptResult{}
}

func TestValidateDryRunUsesOnlyLogicalRouteAndHasNoPhysicalSideEffects(t *testing.T) {
	const userID int64 = 77
	repository := &dryRunSpyRepository{logical: db.LogicalForwardRoute{
		ModelID: 11, UserID: userID, FullName: "personal/model", RouteStrategy: "ordered",
	}}
	var selectorCalls, runnerCalls, attemptHookCalls, usageHookCalls, failoverHookCalls atomic.Int32
	service, err := NewService(ServiceConfig{
		Repository: repository,
		Selector:   dryRunSpySelector{calls: &selectorCalls},
		Runner:     dryRunSpyRunner{calls: &runnerCalls},
		Hooks: Hooks{
			Attempt:  func(AttemptRecord) { attemptHookCalls.Add(1) },
			Usage:    func(UsageRecord) { usageHookCalls.Add(1) },
			Failover: func(FailoverRecord) { failoverHookCalls.Add(1) },
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })

	request, err := openai.DecodeChatRequest(strings.NewReader(`{"model":"personal/model","messages":[]}`), openai.MaxRequestBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer request.Clear()
	validated, err := service.ValidateDryRun(context.Background(), userID, request)
	if err != nil {
		t.Fatal(err)
	}
	if validated.ModelID != 11 || validated.FullName != "personal/model" || validated.RouteStrategy != "ordered" {
		t.Fatalf("dry projection=%+v", validated)
	}
	if got := repository.logicalCalls.Load(); got != 1 {
		t.Fatalf("logical route calls=%d, want 1", got)
	}
	if got := repository.candidateCalls.Load(); got != 0 {
		t.Fatalf("candidate route calls=%d, want 0", got)
	}
	for name, got := range map[string]int32{
		"selector": selectorCalls.Load(), "runner": runnerCalls.Load(),
		"attempt hook": attemptHookCalls.Load(), "usage hook": usageHookCalls.Load(),
		"failover hook": failoverHookCalls.Load(),
	} {
		if got != 0 {
			t.Fatalf("%s calls=%d, want 0", name, got)
		}
	}
}
