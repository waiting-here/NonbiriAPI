package modeltype

import (
	"slices"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestIndependentChatResponseTypesAndPersistedBits(t *testing.T) {
	for _, tc := range []struct {
		name            string
		input           Set
		mask            int64
		chat, responses bool
	}{
		{"chat", Set{contract.OperationChatCompletions}, 1, true, false},
		{"responses", Set{contract.OperationResponses}, 16, false, true},
		{"both", Set{contract.OperationChatCompletions, contract.OperationResponses}, 17, true, true},
		{"default", Default(), 17, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mask, err := tc.input.Mask()
			if err != nil || mask != tc.mask || tc.input.Supports(contract.OperationChatCompletions) != tc.chat || tc.input.Supports(contract.OperationResponses) != tc.responses || tc.input.Supports(contract.OperationEmbeddings) {
				t.Fatalf("input=%v mask=%d err=%v", tc.input, mask, err)
			}
			var stored Set
			if err := stored.Scan(tc.mask); err != nil || !slices.Equal(stored, tc.input) {
				t.Fatalf("stored mask=%d set=%v err=%v", tc.mask, stored, err)
			}
		})
	}
	if _, err := (Set{contract.OperationChatCompletions, contract.OperationChatCompletions}).Mask(); err == nil {
		t.Fatal("identical duplicates accepted")
	}
	if mask, err := (Set{contract.OperationSystemOne}).Mask(); err != nil || mask != 32768 {
		t.Fatalf("System One bit changed: %d %v", mask, err)
	}
	all := Set(contract.AllOperations())
	mask, err := all.Mask()
	if err != nil || mask != 65535 {
		t.Fatalf("all=%d %v", mask, err)
	}
}
