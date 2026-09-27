package clientguard

import (
	"strings"
	"testing"
)

func TestReadableReasonCombinesNamesWithoutExposingRawEvidence(t *testing.T) {
	cases := []struct {
		name, previous, want string
		refs                 []ruleRef
	}{
		{name: "single", refs: []ruleRef{{ID: "private-rule-id", Name: "Tavo"}}, want: "识别到违规第三方客户端特征：Tavo"},
		{name: "normalized duplicate names", refs: []ruleRef{{Name: " Tavo\t\nClient "}, {Name: "Tavo Client"}, {Name: "Other"}}, want: "识别到违规第三方客户端特征：Other、Tavo Client"},
		{name: "blank custom name", refs: []ruleRef{{Name: " \n\t "}}, want: "识别到违规第三方客户端特征：未命名规则"},
		{name: "same existing reason", refs: []ruleRef{{Name: "Tavo"}}, previous: "识别到违规第三方客户端特征：Tavo", want: "识别到违规第三方客户端特征：Tavo"},
		{name: "full prior evidence", refs: []ruleRef{{Name: "Tavo"}}, previous: strings.Repeat("原", 1024), want: strings.Repeat("原", 1024)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := boundedReason(tc.refs, tc.previous)
			if err != nil || got != tc.want {
				t.Fatalf("reason %q, want %q: %v", got, tc.want, err)
			}
		})
	}
}
