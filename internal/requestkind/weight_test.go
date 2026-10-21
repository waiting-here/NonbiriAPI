package requestkind

import (
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestServiceClassUsesOnlyFixedOperation(t *testing.T) {
	want := map[contract.Operation]bool{contract.OperationEmbeddings: true, contract.OperationRerank: true, contract.OperationDecisions: true, contract.Operation("systemone"): true}
	for _, op := range contract.AllOperations() {
		if IsLightOperation(op) != want[op] {
			t.Fatalf("classification %s", op)
		}
	}
	for _, op := range []contract.Operation{"embedding-model", "chat-systemone", "rerank-model", "", "unknown"} {
		if IsLightOperation(op) {
			t.Fatalf("inferred class from name %s", op)
		}
	}
}
