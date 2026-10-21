package flowcontrol

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
)

func TestServiceClassConcurrencySharesPersonalAndCharityWithinEachPool(t *testing.T) {
	limits := UserLimits{ConcurrencyLimit: 1, LightConcurrencyLimit: 1}
	c := newTestController(t, generousRPMConfig(), func(context.Context, int64) (UserLimits, error) { return limits, nil }, nil)
	ctx := context.Background()
	for _, firstLight := range []bool{false, true} {
		operations := []contract.Operation{contract.OperationChatCompletions, contract.OperationEmbeddings}
		if firstLight {
			operations[0], operations[1] = operations[1], operations[0]
		}
		first, _, err := c.AdmitOperation(ctx, 7, operations[0], false)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := c.AdmitOperation(ctx, 7, operations[0], true); !errors.Is(err, ErrConcurrencyLimited) {
			t.Fatalf("same pool: %v", err)
		}
		second, _, err := c.AdmitOperation(ctx, 7, operations[1], true)
		if err != nil {
			t.Fatalf("other pool blocked: %v", err)
		}
		c.ForgetUser(7)
		first.Release()
		if _, _, err := c.AdmitOperation(ctx, 7, operations[0], false); !errors.Is(err, ErrConcurrencyLimited) {
			t.Fatalf("retirement split state: %v", err)
		}
		second.Release()
	}
	limits.LightConcurrencyLimit = 2
	a, _, err := c.AdmitOperation(ctx, 7, contract.OperationEmbeddings, false)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := c.AdmitOperation(ctx, 7, contract.OperationRerank, true)
	if err != nil {
		t.Fatal(err)
	}
	limits.LightConcurrencyLimit = 1
	if _, _, err := c.AdmitOperation(ctx, 7, contract.OperationDecisions, false); !errors.Is(err, ErrConcurrencyLimited) {
		t.Fatalf("lowered light cap: %v", err)
	}
	a.Release()
	b.Release()
}

func TestServiceClassDeniedBeforeClassifierAndGlobalOverrideIndependent(t *testing.T) {
	limits := UserLimits{ConcurrencyLimit: 1, LightConcurrencyLimit: 1, RPMLimit: 1, RPMLimitSet: true, LightRPMLimit: 1, LightRPMLimitSet: true, GlobalRPMLimit: 3, GlobalRPMLimitSet: true}
	var reasons []ratelimit.RPMReason
	c, err := New(Config{RPM: generousRPMConfig(), UserLimits: func(context.Context, int64) (UserLimits, error) { return limits, nil }, OnDenied: func(_ context.Context, _ int64, r ratelimit.RPMReason) error {
		reasons = append(reasons, r)
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	m, err := NewMiddleware(c, func(*http.Request) (int64, error) { return 7, nil })
	if err != nil {
		t.Fatal(err)
	}
	held, _, err := c.AdmitOperation(context.Background(), 7, contract.OperationEmbeddings, false)
	if err != nil {
		t.Fatal(err)
	}
	classified := 0
	handler := m.WrapClassified(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }), func(r *http.Request) (*http.Request, bool, func(), error) { classified++; return r, true, nil, nil })
	body := &countingBody{}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("POST", "/v1/embeddings", body))
	if w.Code != 429 || classified != 0 || body.reads.Load() != 0 {
		t.Fatalf("body protection: status=%d classified=%d reads=%d", w.Code, classified, body.reads.Load())
	}
	held.Release()
	for _, path := range []string{"/v1/embeddings", "/v1/chat/completions"} {
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("POST", path, nil))
		if w.Code != 200 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("POST", "/v1/embeddings", nil))
	if w.Code != 429 || len(reasons) != 1 || reasons[0] != ratelimit.RPMCharityUserLimit {
		t.Fatalf("light denial attribution: %d %v", w.Code, reasons)
	}
	personal, _, err := c.AdmitOperation(context.Background(), 7, contract.OperationEmbeddings, false)
	if err != nil {
		t.Fatal(err)
	}
	personal.Commit()
	_, _, err = c.AdmitOperation(context.Background(), 7, contract.OperationChatCompletions, false)
	if !errors.Is(err, ErrRateLimited) || len(reasons) != 2 || reasons[1] != ratelimit.RPMUserLimit {
		t.Fatalf("combined override: %v %v", err, reasons)
	}
}
