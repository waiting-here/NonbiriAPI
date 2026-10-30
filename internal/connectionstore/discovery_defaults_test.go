package connectionstore_test

import (
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestDefaultDiscoveryCatalogCreationAndLookupAgree(t *testing.T) {
	cases := []struct {
		kind   contract.Type
		format string
	}{
		{contract.TypeOpenAICompatible, "openai"},
		{contract.TypeOpenAIResponses, "openai"},
		{contract.TypeOpenAIChatCompletions, "openai"},
		{contract.TypeOpenAIDecisions, "openai"},
		{contract.TypeTypeSafeSystemOne, "openai"},
		{contract.TypeAnthropicCompatible, "anthropic"},
		{contract.TypeGoogleGemini, "gemini"},
		{contract.TypeAISDKGatewayV3, "ai-sdk-gateway-v3"},
		{contract.TypeAISDKGatewayV4, "ai-sdk-gateway-v4"},
		{contract.TypeCohereRerank, ""},
		{contract.TypeJinaRerank, ""},
		{contract.TypeSiliconFlowRerank, ""},
		{contract.TypeVoyageRerank, ""},
		{"unknown-protocol", ""},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			if got := connectionstore.DefaultDiscoveryFormat(tc.kind); got != tc.format {
				t.Fatalf("default = %q, want %q", got, tc.format)
			}
			_, tx := connectionFixture(t)
			seedApprovedUse(t, tx, false)
			use, err := connectionstore.LoadDiscoveryUseTx(t.Context(), tx, 1, 0)
			if err != nil {
				t.Fatal(err)
			}
			// Catalog lookup consumes the protocol from the calling authorization.
			use.Grant.ConfiguredType = tc.kind
			revision, err := connectionstore.EnsureDiscoverySourceTx(t.Context(), tx, use, 2)
			if tc.format == "" {
				if !errors.Is(err, connectionstore.ErrUnavailable) || revision != 0 {
					t.Fatalf("unsupported default silently substituted: %d %v", revision, err)
				}
				return
			}
			if err != nil || revision == 0 {
				t.Fatalf("initial catalog: %d %v", revision, err)
			}
			var format string
			if err := tx.QueryRow("SELECT list_format FROM discovery_source_revisions WHERE id=?", revision).Scan(&format); err != nil || format != tc.format {
				t.Fatalf("created format: %q %v", format, err)
			}
			// No self association or donation pin exists: exercise the fallback
			// used by donated catalogs, review and management projections.
			query := "SELECT " + connectionstore.DiscoverySourceRevisionSQL + " FROM (SELECT NULL discovery_source_revision_id) ur, (SELECT ? endpoint_key_id) dk, (SELECT ? owner_user_id,? credential_id,? credential_version_id,? canonical_base_url,? configured_type) a"
			g := use.Grant
			var found int64
			if err := tx.QueryRow(query, g.EndpointKeyID, g.OwnerUserID, g.CredentialID, g.CredentialVersionID, g.CanonicalBaseURL, tc.kind).Scan(&found); err != nil || found != revision {
				t.Fatalf("catalog fallback: %d, want %d: %v", found, revision, err)
			}
			use.DiscoverySourceRevisionID = found
			reused, err := connectionstore.EnsureDiscoverySourceTx(t.Context(), tx, use, 3)
			if err != nil || reused != revision {
				t.Fatalf("existing catalog was replaced: %d %v", reused, err)
			}
			var count int
			if err := tx.QueryRow("SELECT count(*) FROM discovery_sources").Scan(&count); err != nil || count != 1 {
				t.Fatalf("catalog duplicated: %d %v", count, err)
			}
		})
	}
}
