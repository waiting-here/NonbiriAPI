package gemini

import (
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestGeminiModalityPartitions(t *testing.T) {
	base := `{"promptTokenCount":10,"cachedContentTokenCount":4,"candidatesTokenCount":3,"totalTokenCount":13,"promptTokensDetails":[{"modality":"TEXT","tokenCount":6},{"modality":"IMAGE","tokenCount":4}],"cacheTokensDetails":[{"modality":"TEXT","tokenCount":1},{"modality":"IMAGE","tokenCount":3}],"candidatesTokensDetails":[{"modality":"AUDIO","tokenCount":2},{"modality":"TEXT","tokenCount":1}]}`
	u, ok := parseUsage([]byte(base))
	if !ok || !u.Present || !u.Modalities.InputPresent || !u.Modalities.OutputPresent || u.Modalities.Input[0] != [4]int64{5, 1} || u.Modalities.Input[2] != [4]int64{1, 3} || u.Modalities.Output != [4]int64{1, 0, 2} {
		t.Fatal(u, ok)
	}
	for _, tc := range []struct{ old, new, reason string }{
		{`"IMAGE"`, `"DOCUMENT"`, contract.ModalityUnknown},
		{`"TEXT","tokenCount":6`, `"TEXT","tokenCount":5`, contract.ModalityIncomplete},
		{`"TEXT","tokenCount":6`, `"TEXT","tokenCount":null`, contract.ModalityInvalid},
		{`"IMAGE","tokenCount":4`, `"TEXT","tokenCount":4`, contract.ModalityInvalid},
		{`"TEXT","tokenCount":1},{"modality":"IMAGE","tokenCount":3`, `"TEXT","tokenCount":0},{"modality":"IMAGE","tokenCount":4`, ""},
	} {
		u, ok := parseUsage([]byte(strings.Replace(base, tc.old, tc.new, 1)))
		if !ok || !u.Present || u.Modalities.InputReason != tc.reason || !u.Modalities.OutputPresent {
			t.Fatal(tc, u, ok)
		}
	}
	missing := strings.Replace(base, `,"cacheTokensDetails":[{"modality":"TEXT","tokenCount":1},{"modality":"IMAGE","tokenCount":3}]`, "", 1)
	u, ok = parseUsage([]byte(missing))
	if !ok || u.Modalities.InputPresent || u.Modalities.InputReason != contract.ModalityCacheUnknown || !u.Modalities.OutputPresent {
		t.Fatal(u, ok)
	}
	thought := strings.Replace(base, `"totalTokenCount":13`, `"totalTokenCount":15,"thoughtsTokenCount":2`, 1)
	u, ok = parseUsage([]byte(thought))
	if !ok || !u.Modalities.InputPresent || u.Modalities.OutputPresent || u.Modalities.OutputReason != contract.ModalityReasoningUnknown || u.OutputTokens != 5 {
		t.Fatal(u, ok)
	}
}

func TestGeminiZeroAndSnapshotClassification(t *testing.T) {
	u, ok := parseUsage([]byte(`{"promptTokenCount":0,"candidatesTokenCount":0,"totalTokenCount":0,"promptTokensDetails":[],"candidatesTokensDetails":[]}`))
	if !ok || !u.Modalities.InputPresent || !u.Modalities.OutputPresent {
		t.Fatal(u, ok)
	}
	d := &decoder{}
	d.observeUsage([]byte(`{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2,"promptTokensDetails":[{"modality":"VIDEO","tokenCount":1}],"candidatesTokensDetails":[{"modality":"TEXT","tokenCount":1}]}`))
	d.observeUsage([]byte(`{"promptTokenCount":1,"candidatesTokenCount":2,"totalTokenCount":3}`))
	if !d.usage.Present || d.usage.Modalities.OutputPresent || d.usage.Modalities.InputPresent || d.usage.OutputTokens != 2 {
		t.Fatal(d.usage)
	}
}
