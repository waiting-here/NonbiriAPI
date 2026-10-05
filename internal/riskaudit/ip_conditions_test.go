package riskaudit

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func ipRule() Rule {
	return Rule{Name: "Example relay", Status: "suspected", Enabled: true, Conditions: []Condition{
		{Field: "effective_ip", Operator: "ip_in", Values: []string{"192.0.2.1", "192.0.2.2", "2001:db8::1"}},
		{Field: "user_agent", Operator: "prefix", Value: "Example/"},
	}}
}

func TestIPListMatchesAnyTrustedAddressWithOtherConditions(t *testing.T) {
	rule := ipRule()
	for _, ip := range []string{"192.0.2.1", "192.0.2.2", "::ffff:192.0.2.2", "2001:0db8:0:0::1"} {
		for _, quality := range []string{"direct_peer", "trusted_forwarded"} {
			matches := MatchRules(Source{EffectiveIP: ip, IPQuality: quality, UserAgent: "Example/1"}, []Rule{rule})
			if len(matches) != 1 || !slices.Equal(matches[0].Fields, []string{"effective_ip", "user_agent"}) {
				t.Fatalf("address %s (%s): %+v", ip, quality, matches)
			}
		}
	}
	for _, source := range []Source{
		{EffectiveIP: "192.0.2.3", IPQuality: "direct_peer", UserAgent: "Example/1"},
		{EffectiveIP: "192.0.2.1", IPQuality: "direct_peer", UserAgent: "Other/1"},
		{EffectiveIP: "192.0.2.1", IPQuality: "peer_fallback", UserAgent: "Example/1"},
		{EffectiveIP: "", IPQuality: "direct_peer", UserAgent: "Example/1"},
		{EffectiveIP: "192.0.2.1:443", IPQuality: "direct_peer", UserAgent: "Example/1"},
		{EffectiveIP: "192.0.2.1", IPQuality: "direct_peer", UserAgent: "Example/1", Quality: map[string]FieldQuality{"effective_ip": {Invalid: true}}},
		{EffectiveIP: "192.0.2.1", IPQuality: "direct_peer", UserAgent: "Example/1", Quality: map[string]FieldQuality{"effective_ip": {Truncated: true}}},
	} {
		if got := MatchRules(source, []Rule{rule}); len(got) != 0 {
			t.Fatalf("unexpected match for %+v: %+v", source, got)
		}
	}
}

func TestIPListNormalizationAndInputLimits(t *testing.T) {
	rule := ipRule()
	rule.Conditions[0].Values = []string{" 192.0.2.1 ", "::ffff:192.0.2.1", "2001:0DB8:0:0::1", "2001:db8::1"}
	normalized, err := normalizeConditions(rule.Conditions)
	if err != nil || !slices.Equal(normalized[0].Values, []string{"192.0.2.1", "2001:db8::1"}) {
		t.Fatalf("normalization: %+v %v", normalized, err)
	}
	if rule.Conditions[0].Values[0] != " 192.0.2.1 " {
		t.Fatal("normalization changed the caller's slice")
	}
	for _, values := range [][]string{
		nil, {}, {""}, {"relay.example"}, {"https://192.0.2.1"}, {"192.0.2.0/24"}, {"192.0.2.1:443"}, {"fe80::1%eth0"}, {"192.0.2.1", "invalid"},
	} {
		rule.Conditions[0].Values = values
		if _, err := normalizeConditions(rule.Conditions); err == nil {
			t.Fatalf("invalid addresses accepted: %q", values)
		}
	}
	addresses := make([]string, MaxConditionIPs)
	for i := range addresses {
		addresses[i] = fmt.Sprintf("192.0.2.%d", i+1)
	}
	rule.Conditions[0].Values = addresses
	if _, err := normalizeConditions(rule.Conditions); err != nil || validateRule(rule) != nil {
		t.Fatal("valid address limit rejected", err)
	}
	rule.Conditions[0].Values = append(addresses, "192.0.2.65")
	if _, err := normalizeConditions(rule.Conditions); err == nil {
		t.Fatal("address limit exceeded")
	}
	for _, condition := range []Condition{
		{Field: "user_agent", Operator: "ip_in", Values: []string{"192.0.2.1"}},
		{Field: "effective_ip", Operator: "ip_in", Value: "192.0.2.2", Values: []string{"192.0.2.1"}},
		{Field: "effective_ip", Operator: "ip_in", CaseSensitive: true, Values: []string{"192.0.2.1"}},
		{Field: "effective_ip", Operator: "equals", Value: "192.0.2.2", Values: []string{"192.0.2.1"}},
	} {
		rule.Conditions = []Condition{condition}
		if validateRule(rule) == nil {
			t.Fatalf("ambiguous condition accepted: %+v", condition)
		}
	}
}

func TestIPListSaveAndFrozenScanAcrossRuleEdit(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	actor := Actor{UserID: f.user(6)}
	input := ipRule()
	input.Conditions[0].Values = []string{"192.0.2.1", "::ffff:192.0.2.1", "2001:0DB8::1"}
	rule, err := f.repository.PutRule(ctx, actor, input, true)
	if err != nil || !slices.Equal(rule.Conditions[0].Values, []string{"192.0.2.1", "2001:db8::1"}) || rule.AutoBan != nil {
		t.Fatalf("saved rule: %+v %v", rule, err)
	}
	subject := f.user(1)
	f.source(subject, "self", "192.0.2.1", "direct_peer", "Example/1", "model", "success", 0)
	f.source(subject, "charity", "2001:db8::1", "trusted_forwarded", "Example/1", "model", "success", 0)
	f.source(subject, "charity", "192.0.2.2", "direct_peer", "Example/1", "model", "success", 0)
	f.source(subject, "charity", "192.0.2.1", "peer_fallback", "Example/1", "model", "success", 0)
	scan, err := f.repository.CreateScan(ctx, actor, scanInput("ip_list_frozen_scan"))
	if err != nil {
		t.Fatal(err)
	}
	rule.Conditions[0].Values = []string{"192.0.2.2"}
	if _, err = f.repository.PutRule(ctx, actor, rule, false); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewRepository(f.store.DB(), RepositoryOptions{Now: func() time.Time { return time.Unix(f.now, 0) }, FinalAuth: f.authority})
	if err != nil {
		t.Fatal(err)
	}
	finishScan(t, restarted)
	result, err := restarted.ScanResults(ctx, actor, scan.ID, 1, 20)
	if err != nil || result.TotalItems != "2" || len(result.Items) != 2 {
		t.Fatalf("frozen results: %+v %v", result, err)
	}
	for _, item := range result.Items {
		if len(item.Matches) != 1 || item.Matches[0].RuleID != rule.ID || item.Matches[0].Revision != 1 {
			t.Fatalf("rule snapshot changed: %+v", item)
		}
	}
}

func TestIPListHTTPValidation(t *testing.T) {
	f := newAuditFixture(t)
	actor := Actor{Admin: true, UserID: f.admin}
	for _, test := range []struct {
		values string
		want   int
	}{
		{`["192.0.2.1","::ffff:192.0.2.1","2001:0DB8::1"]`, http.StatusCreated},
		{`["192.0.2.1","relay.example"]`, http.StatusBadRequest},
		{`[]`, http.StatusBadRequest},
	} {
		body := `{"name":"Example relay","status":"suspected","enabled":true,"conditions":[{"field":"effective_ip","operator":"ip_in","values":` + test.values + `}]}`
		request := httptest.NewRequest(http.MethodPost, "/client-rules", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		serve(f.repository, "create_rule", actor, response, request)
		if response.Code != test.want {
			t.Fatalf("values %s: %d %s", test.values, response.Code, response.Body.String())
		}
	}
}
