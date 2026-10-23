package forward

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/lifecyclegate"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/returnmarker"
)

type returnTestKeys struct{}

func (returnTestKeys) DeriveGenerationTwoSubkey([]byte) ([]byte, error) {
	return bytes.Repeat([]byte{5}, 32), nil
}

func TestReturnedMarkerDoesNotRefuseAuthenticatedRequest(t *testing.T) {
	marker, err := returnmarker.New(returnTestKeys{})
	if err != nil {
		t.Fatal(err)
	}
	for _, valid := range []bool{true, false} {
		t.Run(map[bool]string{true: "verified", false: "ordinary"}[valid], func(t *testing.T) {
			gate, err := lifecyclegate.New(lifecyclegate.Config{MaxUsers: 4})
			if err != nil {
				t.Fatal(err)
			}
			defer gate.Close()
			resolver := &fakeCallerKeyResolver{identity: resources.CallerIdentity{UserID: 9, Generation: 3}}
			middleware, _ := NewCallerKeyMiddleware(resolver, gate)
			observed := false
			middleware.SetReturnObserver(func(request *http.Request, userID int64, _ string) {
				if userID != 9 || resolver.calls != 2 {
					t.Fatal("observed before final identity check")
				}
				_, observed = marker.Verify(request.Header)
			})
			request := httptest.NewRequest("POST", "https://site.example/v1/chat/completions", nil)
			request.Header.Set("Authorization", "Bearer nbk_test")
			if valid {
				marker.Stamp(request, "https://provider.example/v1")
			}
			response := httptest.NewRecorder()
			middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })).ServeHTTP(response, request)
			if response.Code != http.StatusNoContent || observed != valid || resolver.calls != 2 {
				t.Fatal("marker changed processing or added identity queries")
			}
		})
	}
}
