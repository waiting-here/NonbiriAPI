package requestattempt

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

type outer struct{ http.ResponseWriter }

func (w outer) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func TestRefusalPersistsBeforeResponseAndStorageFailureFailsClosed(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "committed", true: "rollback"}[fail], func(t *testing.T) {
			ctx, id, err := New(context.Background(), 7, "POST", "/v1/embeddings")
			if err != nil {
				t.Fatal(err)
			}
			Stage(ctx, "flow", "global_rpm")
			Model(ctx, "safe/model")
			w := httptest.NewRecorder()
			calls := 0
			wrapped := outer{Wrap(w, ctx, func(_ context.Context, user int64, f Fact) error {
				calls++
				if w.Body.Len() != 0 || user != 7 || f.ID != id || f.Path != "/v1/embeddings" || f.Stage != "flow" || f.Reason != "global_rpm" || f.Model != "safe/model" {
					t.Fatal("incorrect or late fact", f)
				}
				if fail {
					return errors.New("storage failure")
				}
				return nil
			})}
			httperr.WriteError(wrapped, httperr.New(httperr.CodeRateLimited, "rate limit exceeded"))
			want := 429
			if fail {
				want = 503
			}
			if calls != 1 || w.Code != want || fail && !strings.Contains(w.Body.String(), "service_unavailable") {
				t.Fatal(w.Code, w.Body.String(), calls)
			}
		})
	}
}

func TestClaimedAndDebugInterceptedDoNotRecordAgain(t *testing.T) {
	for _, handled := range []bool{true, false} {
		ctx, _, err := New(context.Background(), 7, "POST", "/v1/chat/completions")
		if err != nil {
			t.Fatal(err)
		}
		code := httperr.CodeDebugDryRunIntercepted
		if handled {
			Handled(ctx)
			code = httperr.CodeUpstream
		}
		w := Wrap(httptest.NewRecorder(), ctx, func(context.Context, int64, Fact) error { t.Fatal("counted twice"); return nil })
		httperr.WriteError(w, httperr.New(code, "test"))
	}
}
