package resources

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalResourceDTOFixtures(t *testing.T) {
	empty := "empty"
	observed := int64(1_700_000_020)
	entry := CatalogEntry{
		ID: "31", SourceType: "manual", UpstreamModelID: "Vendor/Exact", Provider: "Vendor",
		SourceRevision: "2", PairRevision: "2", CreatedAt: 1_700_000_003, UpdatedAt: 1_700_000_013,
	}
	binding := Binding{
		ID: "51", EndpointKeyID: "21", EndpointBaseURL: "https://example.com/v1",
		ConnectorType: "openai-compatible", EndpointNote: "endpoint note",
		EndpointKeyDisplayHead: "head", EndpointKeyDisplayTail: "tail", EndpointKeyNote: "key note",
		UpstreamModelID: "Vendor/Exact", Ord: 0,
	}
	fixtures := []struct {
		name  string
		value any
	}{
		{"endpoint.json", Endpoint{
			ID: "11", ConnectorType: "openai-compatible", BaseURL: "https://example.com/v1", Origin: EndpointOrigin{Kind: "custom"}, Note: "endpoint note",
			Enabled: true, Revision: "3", KeyCount: "2", CreatedAt: 1_700_000_000, UpdatedAt: 1_700_000_010,
		}},
		{"endpoint_key.json", EndpointKey{
			ID: "21", EndpointID: "11", DisplayHead: "head", DisplayTail: "tail", Note: "key note",
			Enabled: true, ForceStoreFalse: false, SuspensionState: "none", Revision: "4",
			CreatedAt: 1_700_000_001, UpdatedAt: 1_700_000_011,
		}},
		{"caller_key_null.json", (*CallerKeyMetadata)(nil)},
		{"caller_key_metadata.json", CallerKeyMetadata{
			Display: "nbk_abcd…wxyz", CreatedAt: 1_700_000_002, UpdatedAt: 1_700_000_012, Generation: "5",
		}},
		{"catalog_unknown.json", CatalogView{
			Evidence:         DiscoveryEvidence{State: "unknown", Revision: "1", SafeClass: "none"},
			AutomaticEntries: []CatalogEntry{}, ManualEntries: []CatalogEntry{},
		}},
		{"catalog_succeeded_empty.json", CatalogView{
			Evidence:         DiscoveryEvidence{State: "succeeded", Revision: "7", Result: &empty, SafeClass: "none", ObservedAt: &observed, Count: pointer("0")},
			AutomaticEntries: []CatalogEntry{}, ManualEntries: []CatalogEntry{},
		}},
		{"manual_create.json", ManualEntriesResponse{Entries: []CatalogEntry{entry}}},
		{"manual_update.json", ManualUpdateResponse{
			Entries: []CatalogEntry{{
				ID: "31", SourceType: "manual", UpstreamModelID: "Vendor/New", Provider: "Vendor",
				SourceRevision: "1", PairRevision: "1", CreatedAt: 1_700_000_003, UpdatedAt: 1_700_000_014,
			}},
			AffectedModels: []AffectedModel{{
				Model: Model{
					ID: "41", Provider: "logical", Model: "primary", FullName: "logical/primary", RouteStrategy: "ordered",
					SilentRetry: true, FlattenToolCalls: false, Revision: "2", BindingRevision: "6", BindingCount: "1",
					CreatedAt: 1_700_000_004, UpdatedAt: 1_700_000_014,
				},
				Bindings: []Binding{{
					ID: "51", EndpointKeyID: "21", EndpointBaseURL: "https://example.com/v1", ConnectorType: "openai-compatible",
					EndpointNote: "endpoint note", EndpointKeyDisplayHead: "head", EndpointKeyDisplayTail: "tail", EndpointKeyNote: "key note",
					UpstreamModelID: "Vendor/New", Ord: 0,
				}},
			}},
		}},
		{"binding_candidates.json", Page[BindingCandidate]{Data: []BindingCandidate{{
			EndpointKeyID: "21", EndpointBaseURL: "https://example.com/v1", ConnectorType: "openai-compatible",
			EndpointNote: "endpoint note", EndpointKeyDisplayHead: "head", EndpointKeyDisplayTail: "tail", EndpointKeyNote: "key note",
			UpstreamModelID: "Vendor/Exact", SourceTypes: []string{"automatic", "manual"},
		}}}},
		{"bindings.json", BindingsResponse{Bindings: []Binding{binding}, BindingRevision: "6"}},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			got, err := json.Marshal(fixture.value)
			if err != nil {
				t.Fatalf("json.Marshal: %v", err)
			}
			want, err := os.ReadFile(filepath.Join("testdata", fixture.name))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			if !bytes.Equal(got, bytes.TrimSpace(want)) {
				t.Fatalf("fixture mismatch\n got: %s\nwant: %s", got, bytes.TrimSpace(want))
			}
		})
	}
}
