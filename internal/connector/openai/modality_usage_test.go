package openai

import (
	"math"
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestChatModalityUsageIndependentFallback(t *testing.T) {
	base := `{"prompt_tokens":10,"completion_tokens":6,"total_tokens":16,"prompt_tokens_details":{"text_tokens":4,"image_tokens":6,"cached_tokens":0},"completion_tokens_details":{"text_tokens":5,"audio_tokens":1,"reasoning_tokens":3}}`
	u := ParseChatUsage([]byte(base))
	if !u.Present || !u.Modalities.InputPresent || !u.Modalities.OutputPresent || u.Modalities.Input[0] != [4]int64{4, 6} || u.Modalities.Output != [4]int64{5, 0, 1} || u.OutputTokens != 6 {
		t.Fatalf("%+v", u)
	}
	for _, tc := range []struct{ old, new, reason string }{
		{`"cached_tokens":0`, `"cached_tokens":2`, contract.ModalityCacheUnknown},
		{`"image_tokens":6`, `"image_tokens":5`, contract.ModalityIncomplete},
		{`"image_tokens":6`, `"image_tokens":null`, contract.ModalityInvalid},
		{`"image_tokens":6`, `"image_tokens":-1`, contract.ModalityInvalid},
		{`"image_tokens":6`, `"video_tokens":6`, contract.ModalityUnknown},
		{`"image_tokens":6`, `"image_tokens":6,"future_tokens":0`, contract.ModalityUnknown},
	} {
		u := ParseChatUsage([]byte(strings.Replace(base, tc.old, tc.new, 1)))
		if !u.Present || u.Modalities.InputPresent || !u.Modalities.OutputPresent || u.Modalities.InputReason != tc.reason {
			t.Fatal(tc, u)
		}
	}
	// Reasoning is already included in completion_tokens. A decomposition that
	// omits it cannot be repaired by counting visible text or adding it twice.
	u = ParseChatUsage([]byte(strings.Replace(base, `"text_tokens":5`, `"text_tokens":2`, 1)))
	if !u.Present || !u.Modalities.InputPresent || u.Modalities.OutputPresent || u.OutputTokens != 6 {
		t.Fatal(u)
	}
	u = ParseChatUsage([]byte(strings.Replace(base, `"total_tokens":16`, `"total_tokens":1`, 1)))
	if !u.Present || !u.TotalMismatch || !u.Modalities.InputPresent || !u.Modalities.OutputPresent {
		t.Fatal(u)
	}
	for _, raw := range []string{`{"prompt_tokens":null,"completion_tokens":0,"total_tokens":0}`, `{"prompt_tokens":0,"completion_tokens":0,"total_tokens":null}`} {
		if u := ParseChatUsage([]byte(raw)); u.Present {
			t.Fatal("null count accepted", u)
		}
	}
}

func TestImageAndAudioModalityUsage(t *testing.T) {
	image := `{"input_tokens":10,"output_tokens":5,"total_tokens":15,"input_tokens_details":{"text_tokens":2,"image_tokens":8},"output_tokens_details":{"text_tokens":1,"image_tokens":4}}`
	u := ParseImageUsage([]byte(image))
	if !u.Present || !u.Modalities.InputPresent || !u.Modalities.OutputPresent || u.Modalities.Output != [4]int64{1, 4} {
		t.Fatal(u)
	}
	u = ParseImageUsage([]byte(`{"input_tokens":0,"output_tokens":0,"total_tokens":0,"input_tokens_details":{"text_tokens":0,"image_tokens":0}}`))
	if !u.Modalities.InputPresent || u.Modalities.OutputPresent {
		t.Fatal("zero and missing conflated", u)
	}
	u = ParseImageUsage([]byte(strings.Replace(image, `"image_tokens":8`, `"image_tokens":8,"image_tokens":8`, 1)))
	if u.Present {
		t.Fatal("duplicate usage accepted", u)
	}
	u = ParseAudioUsage([]byte(`{"type":"tokens","input_tokens":14,"input_token_details":{"text_tokens":0,"audio_tokens":14},"output_tokens":45,"total_tokens":59}`))
	if !u.Present || !u.Modalities.InputPresent || !u.Modalities.OutputPresent || u.Modalities.Input[0][2] != 14 || u.Modalities.Output[0] != 45 {
		t.Fatal(u)
	}
	if u := ParseAudioUsage([]byte(`{"type":"duration","seconds":12}`)); u.Present {
		t.Fatal(u)
	}
}

func TestRealtimeModalityCacheCrossPartition(t *testing.T) {
	base := `{"input_tokens":10,"output_tokens":4,"total_tokens":14,"input_token_details":{"text_tokens":3,"image_tokens":1,"audio_tokens":6,"cached_tokens":5,"cached_tokens_details":{"text_tokens":1,"image_tokens":0,"audio_tokens":4}},"output_token_details":{"text_tokens":1,"audio_tokens":3}}`
	u := ParseRealtimeUsage([]byte(base))
	if !u.Present || !u.Modalities.InputPresent || !u.Modalities.OutputPresent || u.UncachedInputTokens != 5 || u.CacheReadInputTokens != 5 || u.Modalities.Input[0] != [4]int64{2, 1, 2} || u.Modalities.Input[2] != [4]int64{1, 0, 4} {
		t.Fatal(u)
	}
	u = ParseRealtimeUsage([]byte(strings.Replace(base, `"text_tokens":1,"image_tokens":0,"audio_tokens":4`, `"text_tokens":4,"image_tokens":0,"audio_tokens":1`, 1)))
	if !u.Present || u.Modalities.InputPresent || !u.Modalities.OutputPresent || u.Modalities.InputReason != contract.ModalityMismatch {
		t.Fatal(u)
	}
	u = ParseRealtimeUsage([]byte(strings.Replace(base, `,"cached_tokens_details":{"text_tokens":1,"image_tokens":0,"audio_tokens":4}`, "", 1)))
	if !u.Present || u.Modalities.InputPresent || u.Modalities.InputReason != contract.ModalityCacheUnknown {
		t.Fatal(u)
	}
}

func TestUsageSnapshotsReplaceModalities(t *testing.T) {
	u := cumulativeUsage{}
	first := ParseChatUsage([]byte(`{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3,"prompt_tokens_details":{"text_tokens":2},"completion_tokens_details":{"text_tokens":1}}`))
	last := ParseChatUsage([]byte(`{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5,"prompt_tokens_details":{"text_tokens":2},"completion_tokens_details":{"text_tokens":2,"audio_tokens":1}}`))
	u.observe(first, false)
	if got := u.observe(last, false); got != last {
		t.Fatal(got, last)
	}
	unknown := ParseChatUsage([]byte(`{"prompt_tokens":2,"completion_tokens":4,"total_tokens":6}`))
	if got := u.observe(unknown, false); got.Modalities.OutputPresent || got.OutputTokens != 4 {
		t.Fatal("stale classification retained", got)
	}
}

func TestResponsesIgnoreNonstandardModalityFields(t *testing.T) {
	u := ParseResponseUsage([]byte(`{"input_tokens":7,"output_tokens":5,"total_tokens":12,"input_tokens_details":{"cached_tokens":2,"text_tokens":5,"image_tokens":2},"output_tokens_details":{"reasoning_tokens":3,"text_tokens":5}}`))
	if !u.Present || u.UncachedInputTokens != 5 || u.CacheReadInputTokens != 2 || u.OutputTokens != 5 || u.Modalities.InputPresent || u.Modalities.OutputPresent {
		t.Fatal(u)
	}
}

func TestImageUsageAggregationPreservesCompletePartitions(t *testing.T) {
	image := ParseImageUsage([]byte(`{"input_tokens":10,"output_tokens":5,"total_tokens":15,"input_tokens_details":{"text_tokens":2,"image_tokens":8},"output_tokens_details":{"text_tokens":0,"image_tokens":5}}`))
	first := combineImageUsage(emptyImageUsage(), image)
	if first != image {
		t.Fatal(first, image)
	}
	sum := combineImageUsage(first, image)
	if !sum.Present || sum.UncachedInputTokens != 20 || sum.OutputTokens != 10 || !sum.Modalities.InputPresent || !sum.Modalities.OutputPresent || sum.Modalities.Input[0] != [4]int64{4, 16} || sum.Modalities.Output != [4]int64{0, 10} {
		t.Fatal(sum)
	}
	missing := image
	missing.Modalities.InputPresent = false
	missing.Modalities.InputReason = contract.ModalityIncomplete
	sum = combineImageUsage(first, missing)
	if !sum.Present || sum.Modalities.InputPresent || !sum.Modalities.OutputPresent || sum.Modalities.InputReason != contract.ModalityIncomplete {
		t.Fatal(sum)
	}
	sum = combineImageUsage(sum, image)
	if sum.Modalities.InputPresent || sum.Modalities.InputReason != contract.ModalityIncomplete || sum.OutputTokens != 15 {
		t.Fatal("missing partition restored", sum)
	}
	// An actual zero report lacking details is not the accumulator identity.
	sum = combineImageUsage(Usage{Present: true}, image)
	if sum.Modalities.InputPresent || sum.Modalities.OutputPresent {
		t.Fatal(sum)
	}
	if sum = combineImageUsage(image, Usage{}); sum.Present {
		t.Fatal(sum)
	}
	if sum = combineImageUsage(Usage{Present: true, UncachedInputTokens: math.MaxInt64}, image); sum.Present || !sum.TotalMismatch {
		t.Fatal(sum)
	}
}
