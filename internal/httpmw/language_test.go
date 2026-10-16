package httpmw

import (
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"net/http"
	"strings"
	"testing"
)

func TestRequestLanguageReachesBoundaryAndHandlerErrors(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httperr.WriteError(w, httperr.New(httperr.CodeConflict, "request conflicts with current state"))
	})
	for _, host := range []string{"unknown.example", "example.com"} {
		request := makeRequest(http.MethodGet, "http://wire.invalid/api/value", host, "198.51.100.1:4400")
		request.Header.Set("Accept-Language", "zh-CN,en;q=0.8")
		response := runBoundary(t, testConfig(false), next, request)
		if response.Header().Get("Content-Language") != "zh" || strings.Contains(response.Body.String(), "request conflicts") {
			t.Fatalf("%s: %d %s", host, response.Code, response.Body)
		}
		if host == "example.com" && !strings.Contains(response.Body.String(), "当前状态") {
			t.Fatal(response.Body.String())
		}
	}
}
