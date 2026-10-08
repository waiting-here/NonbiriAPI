package debug

import (
	"context"
	"testing"
)

func TestImageCaptureSupportsBothModesAndStreaming(t *testing.T) {
	for _, mode := range []Mode{ModeDry, ModeLive} {
		for _, route := range []RouteKind{RouteOpenAIImages, RouteCharityImages} {
			t.Run(string(mode)+"/"+string(route), func(t *testing.T) {
				hub, _ := newDebugTestHub(t, newDebugTestClock(1000), &debugTestVerifier{state: IdentityActive})
				mustStartDebug(t, hub, 7, "image-binding")
				if mode == ModeLive {
					if _, err := hub.ChangeMode(7, "1", ModeLive, true); err != nil {
						t.Fatal(err)
					}
				}
				decision, err := hub.DecideAfterAdmission(context.Background(), CaptureInput{
					UserID: 7, RouteKind: route, Model: "provider/image", Stream: true, Charity: route.IsCharity(),
					MediaType: "application/json", Body: []byte(`{"prompt":"owner-visible","stream":true}`), IdentityCertain: true,
				})
				if err != nil || decision.Mode != mode || decision.Trace == nil || decision.DryIntercepted() != (mode == ModeDry) {
					t.Fatalf("decision=%+v err=%v", decision, err)
				}
				hub.mu.Lock()
				trace := cloneTrace(hub.activeByUser[7].traces[decision.Trace.TraceID()].trace)
				hub.mu.Unlock()
				if trace.Request.RouteKind != route || !trace.Request.Stream || !trace.Request.valid() || trace.UpstreamResult != nil {
					t.Fatalf("trace=%+v", trace)
				}
			})
		}
	}
}
