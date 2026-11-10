package egress

import (
	"net/http"
	"testing"
)

func TestRerankPathKeepsCloudflareOriginAndPrefix(t *testing.T) {
	calls := 0
	client, _, _ := routedClient(t, func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(200) }, nil)
	target := "https://provider.example/compatible-api/v1/reranks"
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Api-Key", routeKey)
	response, err := client.Do(request)
	if response != nil {
		response.Body.Close()
	}
	if err == nil || calls != 0 {
		t.Fatalf("path escaped configured prefix: calls=%d err=%v", calls, err)
	}
	target = "https://provider.example/v1/reranks"
	request, err = http.NewRequestWithContext(t.Context(), http.MethodPost, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Api-Key", routeKey)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if calls != 1 {
		t.Fatal("matching path did not use configured proxy")
	}
}
