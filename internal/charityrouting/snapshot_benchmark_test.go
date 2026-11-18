package charityrouting

import (
	"fmt"
	"testing"
	"time"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
)

func BenchmarkCallerRoutingSnapshot(b *testing.B) {
	env := newBrowseProjectionFixture(b, 330, 16)
	// The transaction-local expiry owner uses the supplied transaction. Include
	// its real SQL in both builds, also when there is no due work.
	env.service.donationState = &donation.Service{}
	for _, model := range []int64{51, 1} { // One target and 74 targets, respectively.
		for _, hold := range []time.Duration{0, 100 * time.Millisecond} {
			b.Run(fmt.Sprintf("model=%d/writer=%s", model, hold), func(b *testing.B) {
				for range b.N {
					var released chan struct{}
					if hold > 0 {
						b.StopTimer()
						conn, err := env.store.DB().Conn(b.Context())
						if err != nil {
							b.Fatal(err)
						}
						released = make(chan struct{})
						time.AfterFunc(hold, func() { _ = conn.Close(); close(released) })
						b.StartTimer()
					}
					snapshot, err := env.service.SnapshotForCaller(b.Context(), env.caller, model, routingTestNow, []contract.Type{contract.TypeOpenAICompatible}, contract.OperationChatCompletions)
					if err != nil || len(snapshot.Candidates()) == 0 {
						b.Fatalf("no usable route: %v", err)
					}
					if released != nil {
						b.StopTimer()
						<-released
						b.StartTimer()
					}
				}
			})
		}
	}
}
