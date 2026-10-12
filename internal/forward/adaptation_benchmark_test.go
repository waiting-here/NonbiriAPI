package forward

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/requestadaptation"
)

func BenchmarkFreezeAdaptations(b *testing.B) {
	for _, size := range []int{1 << 10, 1 << 20, 10 << 20} {
		for _, count := range []int{1, 10, 100} {
			b.Run(fmt.Sprintf("bytes=%d/candidates=%d", size, count), func(b *testing.B) {
				body := `{"model":"provider/model","messages":[{"role":"user","content":"` + strings.Repeat("x", size) + `"}]}`
				request, err := decodeRequest(strings.NewReader(body), contract.OperationChatCompletions, int64(len(body)+1))
				if err != nil {
					b.Fatal(err)
				}
				defer request.Clear()
				service := &Service{adaptations: adaptationReaderFunc(func(refs []requestadaptation.Ref) map[requestadaptation.Ref]requestadaptation.Snapshot {
					out := make(map[requestadaptation.Ref]requestadaptation.Snapshot, len(refs))
					for _, ref := range refs {
						out[ref] = requestadaptation.Snapshot{Document: requestadaptation.Empty(ref.Scope), Revision: "0"}
					}
					return out
				})}
				plan := executionPlan{candidates: make([]RouteCandidate, count)}
				for i := range plan.candidates {
					plan.candidates[i] = RouteCandidate{EndpointID: int64(i + 1), ConnectorType: contract.TypeOpenAICompatible}
				}
				b.ReportAllocs()
				b.ResetTimer()
				var elapsed []int64
				for b.Loop() {
					started := benchmarkNow()
					if err := service.freezeAdaptations(context.Background(), request, &plan, nil); err != nil {
						b.Fatal(err)
					}
					plan.clearPrepared()
					elapsed = append(elapsed, benchmarkNow()-started)
				}
				slices.Sort(elapsed)
				b.ReportMetric(float64(elapsed[(len(elapsed)-1)/2]), "p50-ns/op")
				b.ReportMetric(float64(elapsed[(len(elapsed)*95+99)/100-1]), "p95-ns/op")
			})
		}
	}
}
