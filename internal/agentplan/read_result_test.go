package agentplan

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReadBudgetRejectsWholeResultAtItemAndByteBoundaries(t *testing.T) {
	var budget ReadBudget
	for range ReadMaxItems {
		if err := budget.Add(json.RawMessage(`null`)); err != nil {
			t.Fatal(err)
		}
	}
	if err := budget.Add(json.RawMessage(`null`)); !IsReadLimit(err) {
		t.Fatal("item boundary was not enforced", err)
	}
	if raw, err := MarshalRead(strings.Repeat("x", ReadMaxBytes-2)); err != nil || len(raw) != ReadMaxBytes {
		t.Fatal("exact byte boundary rejected", len(raw), err)
	}
	if raw, err := MarshalRead(strings.Repeat("x", ReadMaxBytes-1)); !IsReadLimit(err) || raw != nil {
		t.Fatal("oversized result was returned", len(raw), err)
	}
	budget = ReadBudget{}
	if err := budget.Add(strings.Repeat("界", ReadMaxBytes/3)); !IsReadLimit(err) {
		t.Fatal("UTF-8 bytes were counted as characters", err)
	}
}

func TestReadBudgetCountsEncodedJSONWithoutBase64Expansion(t *testing.T) {
	raw, err := json.Marshal(strings.Repeat("x", 800<<10))
	if err != nil {
		t.Fatal(err)
	}
	var budget ReadBudget
	if err = budget.AddEncoded(raw); err != nil {
		t.Fatal("valid encoded JSON was charged as base64", err)
	}
	result, err := MarshalRead(map[string]any{"data": []json.RawMessage{raw}, "total": "1"})
	if err != nil || len(result) < 800<<10 || len(result) > ReadMaxBytes {
		t.Fatal("in-budget JSON result rejected", len(result), err)
	}
}
