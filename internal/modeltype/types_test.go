package modeltype

import (
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"testing"
)

func TestChatResponseCategoryAndPersistedBits(t *testing.T) {
	for _, input := range []Set{{contract.OperationChatCompletions}, {contract.OperationResponses}, {contract.OperationChatCompletions, contract.OperationResponses}, Default()} {
		mask, err := input.Mask()
		if err != nil || mask != 17 || !input.Supports(contract.OperationChatCompletions) || !input.Supports(contract.OperationResponses) || input.Supports(contract.OperationEmbeddings) {
			t.Fatalf("input=%v mask=%d err=%v", input, mask, err)
		}
	}
	for _, mask := range []int64{1, 16, 17} {
		var s Set
		if err := s.Scan(mask); err != nil || len(s) != 2 || !s.Supports(contract.OperationChatCompletions) || !s.Supports(contract.OperationResponses) {
			t.Fatalf("mask=%d set=%v", mask, s)
		}
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
