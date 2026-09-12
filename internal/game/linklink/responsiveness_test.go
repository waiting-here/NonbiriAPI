package linklink

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func TestMatchHTTPResponsivenessWithoutAutomaticSearch(t *testing.T) {
	for _, spec := range []string{"6x8", "8x8", "10x10"} {
		t.Run(spec, func(t *testing.T) {
			f := newFixture(t)
			user, binding := f.seedUser("response", testFunding)
			state := f.startCurrent(user, binding, spec, 7700)
			d, _ := resolveSpec(spec)
			ordered := board{definition: d, tiles: make([]byte, d.cells()), removed: make([]byte, (d.cells()+7)/8)}
			for i := range ordered.tiles {
				ordered.tiles[i] = byte(i/4 + 1)
			}
			f.replaceBoard(state.SessionID, ordered, 0)
			api := &httpAPI{service: f.service}
			times := make([]time.Duration, 0, d.totalPairs())
			bytesSent := 0
			calls := f.random.callCount()
			for i := 0; i < d.totalPairs(); i++ {
				first, second := f.firstLegalPair(state.SessionID)
				body, _ := json.Marshal(matchBody{ExpectedRevision: state.Revision, First: first, Second: second, IncludePath: true})
				request := httptest.NewRequest(http.MethodPost, RouteMatches, strings.NewReader(string(body)))
				request.SetPathValue("id", state.SessionID)
				request.Header.Set("Idempotency-Key", f.key(7710+i))
				recorder := httptest.NewRecorder()
				start := time.Now()
				api.match(recorder, request, resources.ContinuationUserPrincipal{UserID: user, SessionBinding: binding})
				times = append(times, time.Since(start))
				bytesSent += recorder.Body.Len()
				if recorder.Code != http.StatusOK {
					t.Fatal(recorder.Code, recorder.Body.String())
				}
				if i < d.totalPairs()-1 {
					if err := json.Unmarshal(recorder.Body.Bytes(), &state); err != nil || state.Revision != strconv.Itoa(i+2) {
						t.Fatal(state, err)
					}
				}
			}
			if f.random.callCount() != calls {
				t.Fatal("ordinary matches performed random regeneration")
			}
			sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
			t.Logf("%s real handler + writer + JSON: %d matches p50=%s p95=%s max=%s mean_response_bytes=%d; excludes network latency",
				spec, len(times), times[len(times)/2], times[(len(times)*95-1)/100], times[len(times)-1], bytesSent/len(times))
			if err := f.service.ValidatePersistedState(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
