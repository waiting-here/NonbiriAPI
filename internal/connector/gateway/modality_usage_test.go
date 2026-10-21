package gateway

import "testing"

func TestGatewayOutputModalityRequiresCompleteTextCount(t *testing.T) {
	for _, tc := range []struct {
		output string
		want   bool
	}{
		{`{"total":5,"text":5,"reasoning":0}`, true},
		{`{"total":5,"text":5}`, true},
		{`{"total":0,"text":0}`, true},
		{`{"total":5,"text":2,"reasoning":3}`, false},
		{`{"total":5,"reasoning":5}`, false},
		{`{"total":5}`, false},
		{`{"total":5,"text":5,"audio":1}`, false},
	} {
		u, err := parseUsage([]byte(`{"inputTokens":{"total":3,"noCache":3,"cacheRead":0,"cacheWrite":0},"outputTokens":` + tc.output + `}`))
		if err != nil || !u.Present || u.Modalities.InputPresent || u.Modalities.OutputPresent != tc.want {
			t.Fatal(tc, u, err)
		}
		if tc.want && u.Modalities.Output[0] != u.OutputTokens {
			t.Fatal(tc, u)
		}
	}
}
