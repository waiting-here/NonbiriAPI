package requestattempt

import (
	"strings"
	"testing"
)

func TestRejectionDetailsNeverKeepArbitraryFieldsOrReasons(t *testing.T) {
	detail := NewDetail("SECRET_FIELD", "unsupported top-level field")
	if detail == nil || detail.Field != "request" || strings.Contains(EncodeDetail(detail), "SECRET") {
		t.Fatal(detail)
	}
	for _, reason := range []string{"SECRET_INPUT", strings.Repeat("a", 600), "expected a boolean" + string(rune(10)) + "SECRET"} {
		if NewDetail("model", reason) != nil || DecodeDetail(`{"field":"model","reason":"`+reason+`"}`) != nil {
			t.Fatal("arbitrary reason accepted")
		}
	}
	if DecodeDetail("legacy arbitrary raw error") != nil {
		t.Fatal("guessed legacy cause")
	}
}

func TestPreviouslyRecordedContentLengthRemainsReadable(t *testing.T) {
	d := DecodeDetail("content has 2 characters; minimum is 8")
	if d == nil || d.Field != "messages" || d.Reason != "content has 2 characters; minimum is 8" {
		t.Fatal(d)
	}
	for _, unsafe := range []string{"content has 2 characters; minimum is 8 SECRET", "content has -1 characters; minimum is 8", "content has 9 characters; minimum is 8"} {
		if DecodeDetail(unsafe) != nil {
			t.Fatal("unsafe count", unsafe)
		}
	}
}

func TestHistoricalUnknownReasonRebuildsSuggestion(t *testing.T) {
	detail := DecodeDetail(`{"field":"request","reason":"request validation failed without a recorded field","suggestion":"Check the request format and selected model capabilities. If it persists, share the request ID with support."}`)
	if detail == nil || detail.Suggestion != "The specific cause was not recorded. Share the request ID with support if the problem persists." {
		t.Fatal("historical suggestion was not rebuilt", detail)
	}
}
