package egress

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestCloudflareStatusProjectsLoadedRoutes(t *testing.T) {
	entries := configuredRoutes(t, "https://Z.example:443/v1/", "https://GATEWAY.example:443/private-account/gateway", "X-Goog-Api-Key", "key")
	entries[0].RetainCredentialHeader = true
	entries = append(entries, configuredRoutes(t, "https://a.example/v1", "https://gateway.example:8443/other-private-path", "Authorization", "")...)
	stack, err := NewStack(StackOptions{CloudflareRoutes: entries})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stack.CloseIdleConnections)
	want := CloudflareStatus{Enabled: true, Routes: []CloudflareRouteStatus{
		{SourceBaseURL: "https://a.example/v1", GatewayOrigin: "https://gateway.example:8443", CredentialHeader: "Authorization"},
		{SourceBaseURL: "https://z.example/v1/", GatewayOrigin: "https://gateway.example", CredentialHeader: "X-Goog-Api-Key", CredentialQueryParameter: "key", RetainCredentialHeader: true},
	}}
	got := stack.CloudflareStatus()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loaded routes = %+v; want %+v", got, want)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{routeToken, "GATEWAY_TOKEN", "token_env", "private-account", "other-private-path"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("status exposed private route configuration")
		}
	}
	got.Routes[0].SourceBaseURL = "https://changed.example"
	entries[0].SourceBaseURL = "https://changed-config.example"
	if !reflect.DeepEqual(stack.CloudflareStatus(), want) {
		t.Fatal("snapshot or original config mutation changed the loaded routes")
	}
}

func TestCloudflareStatusEmptyRoutes(t *testing.T) {
	stack, err := NewStack(StackOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stack.CloseIdleConnections)
	raw, err := json.Marshal(stack.CloudflareStatus())
	if err != nil || string(raw) != `{"enabled":false,"routes":[]}` {
		t.Fatalf("empty status = %s, %v", raw, err)
	}
}

func TestCloudflareVertexRoutesMatchExactProjectAndLocation(t *testing.T) {
	const regional = "https://us-central1-aiplatform.googleapis.com/v1/projects/example-project/locations/us-central1/endpoints/openapi"
	const global = "https://aiplatform.googleapis.com/v1/projects/example-project/locations/global/endpoints/openapi"
	stack := newLoopbackStack(t, nil, func(options *StackOptions) {
		options.CloudflareRoutes = configuredRoutes(t, regional, "https://gateway.example/regional", "Authorization", "")
		options.CloudflareRoutes = append(options.CloudflareRoutes, configuredRoutes(t, global, "https://gateway.example/global", "Authorization", "")...)
	})
	for _, source := range []string{regional, global} {
		client, err := stack.NewClient(source)
		if err != nil || client.route == nil {
			t.Fatalf("configured Vertex base did not match: %v", err)
		}
		for _, different := range []string{
			source + "/",
			source + "/extra",
			strings.Replace(source, "/example-project/", "/other-project/", 1),
			strings.NewReplacer("/locations/us-central1/", "/locations/us-east1/", "/locations/global/", "/locations/us-east1/").Replace(source),
			strings.Replace(source, "/v1/", "/v1beta1/", 1),
			strings.Replace(source, ".googleapis.com/", ".googleapis.com.other.example/", 1),
		} {
			client, err := stack.NewClient(different)
			if err != nil || client.route != nil {
				t.Fatalf("different Vertex base matched the configured route: %s, %v", different, err)
			}
		}
	}
}
