package requestattempt

import (
	"strings"
	"testing"
)

func TestRouteObservationsReserveSpaceForProtocolAndRecheck(t *testing.T) {
	ctx, _, err := New(t.Context(), 1, "POST", "/v1/chat/completions")
	if err != nil {
		t.Fatal(err)
	}
	ObserveRoute(ctx, 1, "PRIVATE_SERVICE", "unavailable", "", "")
	ObserveRoute(ctx, 1, "responses", "protocol_rejected", "conversion", "PRIVATE_FIELD")
	if RouteSnapshot(ctx) != nil {
		t.Fatal("retained unknown service or field")
	}
	for id := int64(1); id <= 100; id++ {
		ObserveRoute(ctx, id, "chat_completions", "failure_disabled", "", "")
	}
	ObserveRoute(ctx, 101, "responses", "protocol_rejected", "conversion", "request.fields[2]")
	for id := int64(102); id <= 120; id++ {
		ObserveRoute(ctx, id, "responses", "eligible", "conversion", "")
	}
	d := RouteSnapshot(ctx)
	if !d.Valid() || !d.Truncated || len(d.Observations) >= maxRouteObservations || d.Essential().Observations[0].Field != "request.fields[2]" {
		t.Fatal(d)
	}
	ObserveRecheck(ctx, 120, "responses", "capacity", "dispatch")
	d = RouteSnapshot(ctx)
	if !d.Valid() || len(d.Observations) != maxRouteObservations || d.Essential().Observations[0].Stage != "dispatch" {
		t.Fatal(d)
	}
}

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

func TestTopLevelLocationRetainsPositionWithoutArbitraryNames(t *testing.T) {
	for _, position := range []int{1, 1024} {
		field := TopLevelField("PRIVATE_KEY", position)
		detail := NewDetail(field, "unsupported top-level field")
		decoded := DecodeDetail(EncodeDetail(detail))
		if decoded == nil || decoded.Field != field || !strings.Contains(decoded.Suggestion, "numbering starts at 1") || strings.Contains(EncodeDetail(detail), "PRIVATE") {
			t.Fatal(decoded)
		}
	}
	if got := TopLevelField("logit_bias", 3); got != "logit_bias" {
		t.Fatal(got)
	}
	for _, field := range []string{"request.fields[0]", "request.fields[1025]", "request.fields[01]", "request.fields[+1]", "request.fields[1]PRIVATE", "request.fields[PRIVATE]"} {
		if detail := NewDetail(field, "unsupported top-level field"); detail.Field != "request" {
			t.Fatal(detail)
		}
	}
	legacy := DecodeDetail(`{"field":"request","reason":"unsupported top-level field"}`)
	if legacy == nil || !strings.Contains(legacy.Suggestion, "did not record") {
		t.Fatal(legacy)
	}
}

func TestHistoricalUnknownReasonRebuildsSuggestion(t *testing.T) {
	detail := DecodeDetail(`{"field":"request","reason":"request validation failed without a recorded field","suggestion":"Check the request format and selected model capabilities. If it persists, share the request ID with support."}`)
	if detail == nil || detail.Suggestion != "The specific cause was not recorded. Share the request ID with support if the problem persists." {
		t.Fatal("historical suggestion was not rebuilt", detail)
	}
}
