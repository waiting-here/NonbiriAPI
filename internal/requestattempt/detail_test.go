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

func TestProtocolSuggestionsUseTheRecordedFailure(t *testing.T) {
	for _, item := range []struct{ reason, want string }{
		{"field cannot be represented by this connector", "upstream bindings"},
		{"Responses fields cannot be represented by the selected Chat protocol", "upstream bindings"},
		{"rerank field is not supported by the selected protocol", "upstream bindings"},
		{"Responses input cannot be represented as complete Chat messages", "native Responses"},
		{"tool flattening is unavailable for Responses conversion", "Turn off tool flattening"},
		{"expected the integer 1", "field type or value"},
	} {
		current := NewDetail("n", item.reason)
		if current == nil || !strings.Contains(current.Suggestion, item.want) {
			t.Fatalf("%s: %+v", item.reason, current)
		}
	}
}

func TestHistoricalUnknownReasonRebuildsSuggestion(t *testing.T) {
	detail := DecodeDetail(`{"field":"request","reason":"request validation failed without a recorded field","suggestion":"Check the request format and selected model capabilities. If it persists, share the request ID with support."}`)
	if detail == nil || detail.Suggestion != "The specific cause was not recorded. Share the request ID with support if the problem persists." {
		t.Fatal("historical suggestion was not rebuilt", detail)
	}
}

func TestIndexedRejectionFieldsRoundTripOnlyCanonicalProtocolPaths(t *testing.T) {
	const reason = "invalid Google continuation metadata"
	if field := TopLevelField("messages[0].role", 2); field != "request.fields[2]" {
		t.Fatal("caller-controlled top-level key imitated a nested diagnostic", field)
	}
	for _, field := range []string{"messages[0]", "messages[4095].role", "messages[0].content[0].image_url.detail", "messages[87].tool_calls[127].function.arguments"} {
		d := DecodeDetail(EncodeDetail(NewDetail(field, reason)))
		if d == nil || d.Field != field || !(RouteObservation{Field: field, Reason: "protocol_rejected"}).valid() {
			t.Fatal(field)
		}
	}
	for _, field := range []string{"messages[-1]", "messages[00]", "messages[+2]", "messages[4096]", "messages[0].PRIVATE", "messages[0].tool_calls[128].id", "messages[0].content[33554432].text", "messages[0].content[1].image_url.PRIVATE", "messages[0].tool_calls[0].function.arguments.PRIVATE", "messages[0].role" + string(rune(10))} {
		d := DecodeDetail(EncodeDetail(NewDetail(field, reason)))
		if d == nil || d.Field != "request" || (RouteObservation{Field: field, Reason: "protocol_rejected"}).valid() {
			t.Fatal(field)
		}
	}
}
