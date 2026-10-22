package donation

import (
	"net/http"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

type appendUseItemWire struct {
	NativeService    requiredField[string] `json:"native_service"`
	ExpectedRevision nullableField[string] `json:"expected_revision"`
}

type appendUsesWire struct {
	ExpectedRevision        requiredField[string]              `json:"expected_revision"`
	DonationKeyID           requiredField[string]              `json:"donation_key_id"`
	CredentialID            requiredField[string]              `json:"credential_id"`
	CredentialVersion       requiredField[string]              `json:"credential_version"`
	GrantRevision           requiredField[string]              `json:"grant_revision"`
	Items                   requiredField[[]appendUseItemWire] `json:"items"`
	DiscoverySourceID       nullableField[string]              `json:"discovery_source_id"`
	DiscoverySourceRevision nullableField[string]              `json:"discovery_source_revision"`
}

func appendNullableID(field nullableField[string]) (*int64, error) {
	if !field.Set {
		return nil, ErrInvalidRequest
	}
	if field.Value == nil {
		return nil, nil
	}
	value, err := parseCanonicalID(*field.Value)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	return &value, nil
}

func (wire appendUsesWire) input() (AppendUsesInput, map[string]any, error) {
	var input AppendUsesInput
	fields := []struct {
		wire  requiredField[string]
		value *int64
	}{
		{wire.ExpectedRevision, &input.ExpectedRevision},
		{wire.DonationKeyID, &input.DonationKeyID},
		{wire.CredentialID, &input.CredentialID},
		{wire.CredentialVersion, &input.CredentialVersion},
		{wire.GrantRevision, &input.GrantRevision},
	}
	for _, field := range fields {
		value, err := requiredID(field.wire)
		if err != nil {
			return input, nil, ErrInvalidRequest
		}
		*field.value = value
	}
	if !wire.Items.Set || len(wire.Items.Value) == 0 || len(wire.Items.Value) > 19 {
		return input, nil, ErrInvalidRequest
	}
	var err error
	input.DiscoverySourceID, err = appendNullableID(wire.DiscoverySourceID)
	if err != nil {
		return input, nil, err
	}
	input.DiscoverySourceRevision, err = appendNullableID(wire.DiscoverySourceRevision)
	if err != nil {
		return input, nil, err
	}
	items := make([]map[string]any, 0, len(wire.Items.Value))
	for _, entry := range wire.Items.Value {
		if !entry.NativeService.Set {
			return input, nil, ErrInvalidRequest
		}
		revision, err := appendNullableID(entry.ExpectedRevision)
		if err != nil {
			return input, nil, err
		}
		input.Items = append(input.Items, AppendUseItem{NativeService: contract.NativeService(entry.NativeService.Value), ExpectedRevision: revision})
		items = append(items, map[string]any{"native_service": entry.NativeService.Value, "expected_revision": entry.ExpectedRevision.Value})
	}
	if _, err := input.services(); err != nil {
		return input, nil, err
	}
	canonical := map[string]any{
		"expected_revision":         wire.ExpectedRevision.Value,
		"donation_key_id":           wire.DonationKeyID.Value,
		"credential_id":             wire.CredentialID.Value,
		"credential_version":        wire.CredentialVersion.Value,
		"grant_revision":            wire.GrantRevision.Value,
		"items":                     items,
		"discovery_source_id":       wire.DiscoverySourceID.Value,
		"discovery_source_revision": wire.DiscoverySourceRevision.Value,
	}
	return input, canonical, nil
}

func (api *httpAPI) appendUses(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	donationID, ok := parsePathID(w, r, "id")
	if !ok || !requireEmptyQuery(w, r) {
		return
	}
	var wire appendUsesWire
	if !decodeStrictObject(w, r, &wire) {
		return
	}
	input, canonical, err := wire.input()
	if err != nil {
		writeDonationError(w, err)
		return
	}
	mutation, ok := mutationFor(w, r, routeOwnerUses, []int64{donationID}, canonical)
	if !ok {
		return
	}
	result, err := api.service.AppendUses(r.Context(), p.UserID, donationID, input, mutation)
	if err != nil {
		writeDonationError(w, err)
		return
	}
	writeMutation(w, result)
}
