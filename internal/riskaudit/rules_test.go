package riskaudit

import "testing"

func TestMatchEvidenceBudgetPreservesTruthAndCount(t *testing.T) {
	rules := make([]Rule, 30)
	for i := range rules {
		rules[i] = Rule{Name: "Example", Enabled: true, Status: "suspected", Conditions: []Condition{{Field: "user_agent", Operator: "prefix", Value: "Example/"}}}
	}
	entry := SourceRequest{Source: Source{UserAgent: "Example/1"}}
	budget := 10
	attachMatches(&entry, rules, &budget)
	if entry.MatchCount != 30 || len(entry.Matches) != 10 || !entry.MatchesTruncated || budget != 0 {
		t.Fatalf("budget %+v/%d", entry, budget)
	}
}

func TestFiniteClientRulesAndSourceQuality(t *testing.T) {
	rules := []Rule{
		{ID: "a", Name: "Mobile example", Status: "suspected", Enabled: true, Revision: 2, Conditions: []Condition{{Field: "user_agent", Operator: "prefix", Value: "ExampleMobile/", CaseSensitive: true}}},
		{ID: "b", Name: "Relay example", Status: "confirmed", Enabled: true, Revision: 3, Conditions: []Condition{{Field: "http_referer", Operator: "equals", Value: "https://relay.example"}, {Field: "openrouter_title", Operator: "equals", Value: "Example Relay"}}},
	}
	for _, ua := range []string{"Go-http-client/1.1", "Dart/3.0", "okhttp/4.0", "CFNetwork/1.0", "node-fetch", ""} {
		if len(MatchRules(Source{UserAgent: ua}, rules)) != 0 {
			t.Fatalf("generic client incorrectly identified: %s", ua)
		}
	}
	if matches := MatchRules(Source{UserAgent: "ExampleMobile/1.2 Android"}, rules); len(matches) != 1 || matches[0].Revision != 2 || matches[0].Quality != "self_reported" {
		t.Fatalf("prefix match %+v", matches)
	}
	if len(MatchRules(Source{HTTPReferer: "https://relay.example"}, rules)) != 0 {
		t.Fatal("AND rule matched missing title")
	}
	if matches := MatchRules(Source{HTTPReferer: "https://relay.example", OpenRouterTitle: "EXAMPLE RELAY"}, rules); len(matches) != 1 || matches[0].RuleID != "b" {
		t.Fatalf("combination %+v", matches)
	}
	source := Source{UserAgent: "ExampleMobile/1", Quality: map[string]FieldQuality{"user_agent": {Truncated: true}}}
	if len(MatchRules(source, rules)) != 0 {
		t.Fatal("truncated field presented as known")
	}
	rules[0].Enabled = false
	if len(MatchRules(Source{UserAgent: "ExampleMobile/1"}, rules)) != 0 {
		t.Fatal("disabled rule matched")
	}
	rules[0].Conditions[0].Operator = "regexp"
	if validateRule(rules[0]) == nil {
		t.Fatal("regular expression admitted")
	}
}
func TestIPNormalizationAndCancellationBuckets(t *testing.T) {
	for raw, want := range map[string]string{"192.0.2.1": "192.0.2.1", "::ffff:192.0.2.1": "192.0.2.1", "2001:0db8:0:0::1": "2001:db8::1"} {
		got, ok := normalizeIP(raw)
		if !ok || got != want {
			t.Fatalf("IP %s became %s", raw, got)
		}
	}
	if _, ok := normalizeIP("fe80::1%eth0"); ok {
		t.Fatal("zone ID admitted")
	}
	for ms, index := range map[int64]int{0: 0, 1999: 0, 2000: 1, 29999: 1, 30000: 2, 119999: 2, 120000: 3, 125000: 3, 129999: 3, 130000: 4} {
		if cancellationIndex(ms) != index {
			t.Fatalf("bucket for %d", ms)
		}
	}
}
