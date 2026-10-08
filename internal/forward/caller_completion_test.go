package forward

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

type disconnectOnCompletionWriter struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
	fail   bool
}

func (w *disconnectOnCompletionWriter) FlushError() error {
	completed := strings.Contains(w.Body.String(), "data: [DONE]\n\n")
	if completed && w.fail {
		w.cancel()
		return io.ErrClosedPipe
	}
	w.ResponseRecorder.Flush()
	if completed {
		w.cancel()
	}
	return nil
}

func TestCompletedResponseSurvivesCallerDisconnect(t *testing.T) {
	for _, kind := range []contract.Type{contract.TypeOpenAICompatible, contract.TypeAnthropicCompatible, contract.TypeAISDKGatewayV3} {
		for _, failFlush := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/fail_final_flush=%t", kind, failFlush), func(t *testing.T) {
				f := newServiceFixture(t, nil)
				b := &delayedStreamBackend{do: func(*http.Request) (*http.Response, error) {
					return streamResponse(http.StatusOK, successfulStream(kind)), nil
				}}
				useRealStreamConnector(t, f, kind, b, 0, false)
				f.charges.charge = 7
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				w := &disconnectOnCompletionWriter{ResponseRecorder: httptest.NewRecorder(), cancel: cancel, fail: failFlush}
				request := streamTestRequest(t)
				f.service.Chat(ctx, w, 1, request.chat, nil, "application/json", "en")
				if ctx.Err() == nil || !strings.HasSuffix(w.Body.String(), "data: [DONE]\n\n") {
					t.Fatal("the caller did not disconnect at protocol completion")
				}
				if len(f.claims.outcomes) != 1 || !f.claims.outcomes[0].ProtocolSuccess || len(f.claims.requestResults) != 1 {
					t.Fatal("protocol completion was not recorded once")
				}
				terminal := f.claims.requestResults[0]
				if terminal.Disposition != claim.AccountingCommit || terminal.ActualChargeMilli != 7 {
					t.Fatalf("disconnect changed accounting: %+v", terminal)
				}
				caller := terminal.Caller
				if failFlush {
					if caller.Class != claim.ResultCancelled {
						t.Fatalf("failed delivery must remain cancelled: %+v", caller)
					}
				} else if caller.Class != claim.ResultSuccess || caller.Status != http.StatusOK {
					t.Fatalf("completed delivery was overwritten by disconnect: %+v", caller)
				}
			})
		}
	}
}
