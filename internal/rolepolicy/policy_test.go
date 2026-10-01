package rolepolicy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPolicyStrictBoundariesAndCanonicalRules(t *testing.T) {
	valid := []string{
		`{"default_action":"native","rules":{}}`,
		`{"rules":{"critic":"user","developer":"system"},"default_action":"reject"}`,
	}
	for _, raw := range valid {
		p, err := Decode(raw)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := p.Canonical()
		if err != nil {
			t.Fatal(err)
		}
		round, err := Decode(encoded)
		if err != nil || round.DefaultAction != p.DefaultAction {
			t.Fatal("canonical round trip")
		}
	}
	invalid := []string{
		"null", `{}`, `{"default_action":"native","rules":null}`,
		`{"default_action":"native","rules":{},"extra":true}`,
		`{"default_action":"native","default_action":"user","rules":{}}`,
		`{"default_action":"native","rules":{"critic":"user","critic":"system"}}`,
		`{"default_action":"unknown","rules":{}}`,
		`{"default_action":"native","rules":{"system":"reject"}}`,
		`{"default_action":"native","rules":{"function":"reject"}}`,
		`{"default_action":"native","rules":{" critic":"reject"}}`,
		`{"default_action":"native","rules":{"critic\u0085":"reject"}}`,
		`{"default_action":"native","rules":{"critic":null}}`,
	}
	for _, raw := range invalid {
		if _, err := Decode(raw); err == nil {
			t.Fatalf("accepted invalid policy: %q", raw)
		}
	}
	p := Default()
	p.Rules[strings.Repeat("界", 64)] = "assistant"
	if _, err := p.Canonical(); err != nil {
		t.Fatal("exact Unicode name boundary")
	}
	p.Rules[strings.Repeat("界", 65)] = "assistant"
	if _, err := p.Canonical(); err == nil {
		t.Fatal("oversized name accepted")
	}
	p = Default()
	for i := 0; i < 33; i++ {
		p.Rules[string(rune('A'+i))] = "user"
	}
	if _, err := p.Canonical(); err == nil {
		t.Fatal("too many rules")
	}
	p = Default()
	p.Rules["critic"] = "user"
	clone := p.Clone()
	clone.Rules["critic"] = "system"
	if p.Action("critic") != "user" {
		t.Fatal("mutable policy alias")
	}
	encoded, _ := json.Marshal(Default())
	if string(encoded) != `{"default_action":"native","rules":{}}` {
		t.Fatal("default changed")
	}
}
