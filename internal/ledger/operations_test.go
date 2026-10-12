package ledger

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

func TestOperationRegistryCoversDeclaredKinds(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "types.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	declared := map[Kind]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		spec, ok := node.(*ast.ValueSpec)
		if !ok {
			return true
		}
		typeName, ok := spec.Type.(*ast.Ident)
		if !ok || typeName.Name != "Kind" {
			return true
		}
		for _, value := range spec.Values {
			literal, ok := value.(*ast.BasicLit)
			if !ok {
				t.Fatal("operation identities must be string constants")
			}
			kind, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatal(err)
			}
			declared[Kind(kind)] = true
		}
		return true
	})
	if len(declared) != len(RegisteredKinds()) {
		t.Fatal("operation registry is missing or inventing a kind")
	}
	for _, kind := range RegisteredKinds() {
		if !declared[kind] {
			t.Fatal("unregistered operation constant", kind)
		}
	}
}

func TestRegistryRejectsWrongOperationSourceAndSequence(t *testing.T) {
	meta := Meta{OperationID: mustLedgerID(t, "op_"), CreatedAt: ledgerTestNow}
	for _, test := range []struct {
		kind     Kind
		source   sourceType
		id       string
		sequence uint64
	}{
		{KindWelfareClaim, sourceOperation, mustLedgerID(t, "req_"), 0},
		{KindWelfareClaim, sourceLogicalRequest, mustLedgerID(t, "req_"), 0},
		{KindWelfareClaim, sourceOperation, meta.OperationID, 1},
		{KindRPSRoundCut, sourceRPSSession, mustLedgerID(t, "rps_"), 0},
		{Kind("future_kind"), sourceOperation, meta.OperationID, 0},
	} {
		if _, err := newPlan(meta, test.kind, test.source, test.id, mustU128(t, strconv.FormatUint(test.sequence, 10))); err == nil {
			t.Fatal("invalid operation relationship accepted", test.kind, test.source)
		}
	}
}
