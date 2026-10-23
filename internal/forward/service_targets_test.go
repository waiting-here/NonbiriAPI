package forward

import (
	"errors"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/routing"
)

func serviceTarget(t *testing.T, binding, target int64, groupOrder, targetOrder int, services ...contract.NativeService) RouteCandidate {
	t.Helper()
	set, err := contract.ParseNativeServices(services)
	if err != nil {
		t.Fatal(err)
	}
	return RouteCandidate{BindingID: binding, Order: groupOrder, TargetOrder: targetOrder,
		NativeServices: set, WeightDenominator: 7,
		Authorization: routing.TargetAuthorization{BindingID: binding, TargetID: target, TargetRevision: 1,
			ConfiguredType: contract.TypeOpenAICompatible, CredentialVersionID: target + 100, GrantRevisionID: target + 200}}
}

func TestServiceTargetsPreferNativeWithinOriginalBinding(t *testing.T) {
	converted := serviceTarget(t, 10, 101, 0, 0, contract.NativeServiceResponses)
	native := serviceTarget(t, 10, 102, 0, 1, contract.NativeServiceChatCompletions)
	other := serviceTarget(t, 20, 201, 1, 0, contract.NativeServiceChatCompletions)
	expanded, err := expandServiceTargets([]RouteCandidate{converted, native, other}, contract.OperationChatCompletions)
	if err != nil {
		t.Fatal(err)
	}
	selected := firstCapableBindings(expanded)
	if len(selected) != 2 || selected[0].Authorization.TargetID != 102 || selected[1].BindingID != 20 ||
		!selected[0].NativeMatch || selected[0].WeightDenominator != 7 {
		t.Fatalf("native selection changed logical bindings or weight: %+v", selected)
	}
	// A rejected native target may leave its finite conversion usable. That
	// local choice still contributes only one claim and one weighted entry.
	filtered := make([]RouteCandidate, 0, len(expanded))
	for _, candidate := range expanded {
		if candidate.Authorization.TargetID != 102 {
			filtered = append(filtered, candidate)
		}
	}
	selected = firstCapableBindings(filtered)
	if len(selected) != 2 || selected[0].Authorization.TargetID != 101 || selected[0].NativeMatch ||
		selected[0].ConnectorType != contract.TypeOpenAIResponses || selected[0].Authorization.ConfiguredType != contract.TypeOpenAICompatible {
		t.Fatalf("local conversion did not preserve configured identity: %+v", selected)
	}
}

func TestServiceTargetsDoNotMoveNativeAcrossBindings(t *testing.T) {
	first := serviceTarget(t, 10, 101, 0, 0, contract.NativeServiceResponses)
	second := serviceTarget(t, 20, 201, 1, 0, contract.NativeServiceChatCompletions)
	got, err := expandServiceTargets([]RouteCandidate{second, first}, contract.OperationChatCompletions)
	if err != nil || len(got) != 2 || got[0].BindingID != 10 || got[1].BindingID != 20 {
		t.Fatalf("native preference changed cross-binding order: %v, %+v", err, got)
	}
}

func TestServiceTargetsKeepApprovedServiceVersion(t *testing.T) {
	responseUse := serviceTarget(t, 10, 101, 0, 0, contract.NativeServiceChatCompletions, contract.NativeServiceResponses)
	responseUse.Authorization.NativeService = contract.NativeServiceResponses
	responseUse.Authorization.UseID, responseUse.Authorization.UseRevisionID = 40, 400
	responseUse.Authorization.CredentialVersionID = 1000
	chatUse := responseUse
	chatUse.Authorization.NativeService = contract.NativeServiceChatCompletions
	chatUse.Authorization.UseID, chatUse.Authorization.UseRevisionID = 41, 401
	chatUse.Authorization.CredentialVersionID = 1001
	got, err := expandServiceTargets([]RouteCandidate{responseUse, chatUse}, contract.OperationChatCompletions)
	if err != nil || len(got) != 2 {
		t.Fatalf("expand exact use versions: %v, %+v", err, got)
	}
	if got[0].Authorization.UseID != 41 || got[0].Authorization.CredentialVersionID != 1001 ||
		got[1].Authorization.UseID != 40 || got[1].Authorization.CredentialVersionID != 1000 {
		t.Fatalf("service borrowed another approved credential version: %+v", got)
	}
}

func TestServiceTargetsRejectMissingAuthorization(t *testing.T) {
	candidate := serviceTarget(t, 10, 101, 0, 0, contract.NativeServiceChatCompletions)
	candidate.Authorization.TargetRevision = 0
	if _, err := expandServiceTargets([]RouteCandidate{candidate}, contract.OperationChatCompletions); !errors.Is(err, ErrInternal) {
		t.Fatalf("missing target revision: %v", err)
	}
}
