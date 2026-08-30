package resourcebridge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sort"
	"strings"
	"testing"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func TestRuntimeSurfaceDoesNotExposeDeletionSecretLookupClaimOrPosting(t *testing.T) {
	runtimeType := reflect.TypeOf((*Runtime)(nil))
	deletionType := reflect.TypeOf((*resources.EndpointKeyDeletionHook)(nil)).Elem()
	if runtimeType.Implements(deletionType) {
		t.Fatal("Runtime must not implement EndpointKeyDeletionHook")
	}
	methods := make([]string, 0, runtimeType.NumMethod())
	for index := 0; index < runtimeType.NumMethod(); index++ {
		methods = append(methods, runtimeType.Method(index).Name)
	}
	sort.Strings(methods)
	want := []string{"Close", "Discover", "GoString", "LogValue", "MarkEndpointSecretOrphaned", "String", "WriteEndpointSecret"}
	if !reflect.DeepEqual(methods, want) {
		t.Fatalf("exported Runtime methods = %v, want %v", methods, want)
	}
	for _, forbidden := range []string{"Decrypt", "Lookup", "OpenSecret", "Post", "Claim", "PrepareEndpointKeyDeletion"} {
		if _, exists := runtimeType.MethodByName(forbidden); exists {
			t.Fatalf("forbidden exported method %q exists", forbidden)
		}
	}
}

func TestRuntimeFormattingAndDependencyErrorsAreRedacted(t *testing.T) {
	fixture := newBridgeFixture(t)
	markers := []string{
		"plaintext-hostile-marker",
		"nbsec:v2:hostile-ciphertext-marker",
		"https://hostile-base.example.test/v1",
		"hostile/upstream-model",
		"00112233445566778899aabbccddeeff",
		"ffeeddccbbaa99887766554433221100ffeeddccbbaa99887766554433221100",
	}
	formatted := []string{
		fmt.Sprint(fixture.runtime),
		fmt.Sprintf("%+v", fixture.runtime),
		fmt.Sprintf("%#v", fixture.runtime),
		fixture.runtime.LogValue().String(),
		fmt.Sprint(slog.AnyValue(fixture.runtime)),
		fmt.Sprint(Config{Store: fixture.store, Vault: fixture.vault, Claims: fixture.claims, Backend: fixture.backend}),
		fmt.Sprintf("%#v", Config{Store: fixture.store, Vault: fixture.vault, Claims: fixture.claims, Backend: fixture.backend}),
	}
	for _, output := range formatted {
		if !strings.Contains(output, "redacted") {
			t.Fatalf("runtime formatting is not explicitly redacted: %q", output)
		}
		assertNoMarkers(t, output, markers)
	}

	fixture.backend.setOpenError(errors.New(strings.Join(markers, " | ")))
	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	plaintext := []byte(markers[0])
	_, err = fixture.runtime.WriteEndpointSecret(context.Background(), tx, resources.SecretWriteInput{
		CanonicalBaseURL: markers[2], ConnectorType: "openai-compatible",
		Plaintext: plaintext, CreatedAt: bridgeTestNow,
	})
	_ = tx.Rollback()
	if !errors.Is(err, ErrInvalidInput) || err.Error() != ErrInvalidInput.Error() || !allZero(plaintext) {
		t.Fatalf("backend failure = (%v, cleared=%v)", err, allZero(plaintext))
	}
	assertNoMarkers(t, err.Error(), markers)
}

func TestDiscoveryErrorsAndPanicNeverFormatSensitiveValues(t *testing.T) {
	fixture := newBridgeFixture(t)
	ownerID := fixture.seedUser(t, "hostile-discovery")
	key := fixture.seedEndpointKey(t, ownerID, "hostile-discovery", connectorcontract.TypeOpenAICompatible, "hostile-discovery-secret")
	markers := []string{key.baseURL, "hostile-discovery-secret", "hostile/model", "raw-provider-body", "raw-transport-error"}
	result, err := fixture.runtime.Discover(context.Background(), fixture.discoveryInput(t, key, discovererFunc(
		func(context.Context, connectorcontract.DiscoveryInput) connectorcontract.DiscoveryResult {
			panic(strings.Join(markers, " | "))
		},
	)))
	if err != nil || result.FailureClass != resources.DiscoveryFailureProtocol || result.SafeDiagnostic != diagnosticConnectorPanic {
		t.Fatalf("panic discovery = (%+v, %v)", result, err)
	}
	assertNoMarkers(t, fmt.Sprintf("%s %v", result.SafeDiagnostic, err), markers)

	stale := fixture.discoveryInput(t, key, discovererFunc(
		func(context.Context, connectorcontract.DiscoveryInput) connectorcontract.DiscoveryResult {
			return connectorcontract.DiscoveryResult{}
		},
	))
	stale.EndpointID++
	result, err = fixture.runtime.Discover(context.Background(), stale)
	if !errors.Is(err, ErrUnavailable) || err.Error() != ErrUnavailable.Error() || result.FailureClass != resources.DiscoveryFailureProtocol {
		t.Fatalf("stale discovery = (%+v, %v)", result, err)
	}
	assertNoMarkers(t, fmt.Sprintf("%s %v", result.SafeDiagnostic, err), markers)
}

func TestDiscoveryInputValidationCreatesNoClaim(t *testing.T) {
	fixture := newBridgeFixture(t)
	ownerID := fixture.seedUser(t, "invalid-input")
	key := fixture.seedEndpointKey(t, ownerID, "invalid-input", connectorcontract.TypeOpenAICompatible, "invalid-input-secret")
	validDiscoverer := discovererFunc(func(context.Context, connectorcontract.DiscoveryInput) connectorcontract.DiscoveryResult {
		return connectorcontract.DiscoveryResult{}
	})
	var nilDiscoverer *nilDiscovererType
	tests := []struct {
		name   string
		mutate func(*resources.DiscoveryClaimInput)
	}{
		{name: "operation", mutate: func(input *resources.DiscoveryClaimInput) { input.OperationID = "op_invalid" }},
		{name: "owner", mutate: func(input *resources.DiscoveryClaimInput) { input.OwnerUserID = 0 }},
		{name: "endpoint", mutate: func(input *resources.DiscoveryClaimInput) { input.EndpointID = 0 }},
		{name: "key", mutate: func(input *resources.DiscoveryClaimInput) { input.EndpointKeyID = 0 }},
		{name: "connector", mutate: func(input *resources.DiscoveryClaimInput) { input.ConnectorType = connectorcontract.Type("future") }},
		{name: "base", mutate: func(input *resources.DiscoveryClaimInput) { input.CanonicalBaseURL = "https://invalid.example/\n" }},
		{name: "discoverer", mutate: func(input *resources.DiscoveryClaimInput) { input.Discoverer = nil }},
		{name: "typed nil discoverer", mutate: func(input *resources.DiscoveryClaimInput) { input.Discoverer = nilDiscoverer }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := fixture.discoveryInput(t, key, validDiscoverer)
			test.mutate(&input)
			result, err := fixture.runtime.Discover(context.Background(), input)
			if !errors.Is(err, ErrInvalidInput) || result.Succeeded || len(result.Models) != 0 ||
				result.FailureClass != "" || result.SafeDiagnostic != "" {
				t.Fatalf("Discover invalid input = (%+v, %v)", result, err)
			}
		})
	}
	var count int
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM logical_requests WHERE route_kind='model_discovery'`).Scan(&count); err != nil {
		t.Fatalf("count discovery claims: %v", err)
	}
	if count != 0 {
		t.Fatalf("invalid inputs created %d discovery requests", count)
	}
}

func assertNoMarkers(t testing.TB, output string, markers []string) {
	t.Helper()
	for _, marker := range markers {
		if marker != "" && strings.Contains(output, marker) {
			t.Fatalf("output exposed marker %q in %q", marker, output)
		}
	}
}

type nilDiscovererType struct{}

func (*nilDiscovererType) Discover(context.Context, connectorcontract.DiscoveryInput) connectorcontract.DiscoveryResult {
	return connectorcontract.DiscoveryResult{}
}
