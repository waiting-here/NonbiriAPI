package headerfacts

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func decodeFact(t *testing.T, fact Fact, redact bool) View {
	t.Helper()
	at, status := fact.CapturedAt(), fact.Status()
	view, err := Decode(fact.State(), &at, &status, fact.Compressed(), redact)
	if err != nil {
		t.Fatal(err)
	}
	return view
}
func TestCapturePreservesCompleteValuesAndRedactsBeforeEncoding(t *testing.T) {
	headers := http.Header{"aUtHoRiZaTiOn": {"credential-one", "credential-two"}, "Set-Cookie": {"session=a", "session=b"}, "X-Goog-Api-Key": {"private"}, "X-Csrf-Token": {"csrf"}, "X-Trace": {"", strings.Repeat("long-path/?query=x", 1500)}, "X-Binary": {string([]byte{0xff, 0x80})}, "X-RateLimit-Remaining-Tokens": {"123"}}
	fact := Capture(headers, "example.invalid", 0, time.Unix(1700000000, 0))
	view := decodeFact(t, fact, false)
	values := map[string][]string{}
	for _, entry := range view.Headers {
		values[entry.Name] = append(values[entry.Name], entry.Value)
	}
	if fmt.Sprint(values["aUtHoRiZaTiOn"]) != "[[redacted] [redacted]]" || values["X-Goog-Api-Key"][0] != "[redacted]" || values["X-Csrf-Token"][0] != "[redacted]" || values["X-Trace"][0] != "" || values["X-Trace"][1] != headers["X-Trace"][1] || values["Host"][0] != "example.invalid" || values["X-RateLimit-Remaining-Tokens"][0] != "123" {
		t.Fatal(values)
	}
	for _, entry := range view.Headers {
		if entry.Name == "X-Binary" {
			raw, err := base64.StdEncoding.DecodeString(entry.Value)
			if err != nil || entry.ValueEncoding != "base64" || !bytes.Equal(raw, []byte{0xff, 0x80}) {
				t.Fatal(entry, err)
			}
		}
	}
	for _, format := range []string{"%v", "%+v", "%#v"} {
		if strings.Contains(fmt.Sprintf(format, fact), "credential") {
			t.Fatal("fact formatting leaked")
		}
	}
	encoded, _ := json.Marshal(fact)
	if strings.Contains(string(encoded), "credential") {
		t.Fatal("JSON leaked")
	}
	// Decoding is independent of subsequent source mutations.
	headers["X-Trace"][1] = "changed"
	if decodeFact(t, fact, false).Headers == nil {
		t.Fatal("missing snapshot")
	}
}
func TestHeaderIPv4OnNamesAndNonUTF8Values(t *testing.T) {
	fact := Capture(http.Header{"X-192.0.2.5": {"prefix 198.51.100.7 suffix", string([]byte{0xff}) + "203.0.113.8"}}, "", 200, time.Now())
	view := decodeFact(t, fact, true)
	if view.Headers[0].Name != "X-[redacted]" || view.Headers[0].Value != "prefix [redacted] suffix" {
		t.Fatal(view)
	}
	raw, _ := base64.StdEncoding.DecodeString(view.Headers[1].Value)
	if string(raw) != string([]byte{0xff})+"[redacted]" {
		t.Fatal(view)
	}
	if strings.Contains(fmt.Sprint(decodeFact(t, fact, false).Headers), "X-[redacted]") {
		t.Fatal("stored fact changed")
	}
}
func TestEmptyOverflowAndAttemptIsolation(t *testing.T) {
	if fact := Capture(nil, "", 0, time.Now()); fact.State() != "saved" || len(decodeFact(t, fact, false).Headers) != 0 {
		t.Fatal("empty capture missing")
	}
	if fact := Capture(http.Header{"X-Large": {strings.Repeat("x", MaxHeaderBytes)}}, "", 0, time.Now()); fact.State() != "too_large" || len(fact.Compressed()) != 0 {
		t.Fatal("oversized header partially saved")
	}
	parent, cancel := context.WithCancel(context.Background())
	first := WithResponse(parent, "request", 1)
	second := WithResponse(parent, "request", 2)
	CaptureResponse(first, &http.Response{StatusCode: 200, Header: http.Header{"X-Attempt": {"one"}}})
	CaptureResponse(second, &http.Response{StatusCode: 503, Header: http.Header{"X-Attempt": {"two"}}})
	cancel()
	one := Response(PrepareResponse(context.WithoutCancel(first), "request", 1), "request", 1)
	two := Response(PrepareResponse(second, "request", 2), "request", 2)
	oneView, oneErr := DecodeCollection(one.State(), one.Compressed(), false)
	twoView, twoErr := DecodeCollection(two.State(), two.Compressed(), false)
	if oneErr != nil || twoErr != nil || *oneView.Responses[0].HTTPStatus != 200 || *twoView.Responses[0].HTTPStatus != 503 || Response(second, "request", 1).State() != "" {
		t.Fatal("attempts crossed")
	}
	r := httptest.NewRequest("POST", "http://example.invalid", nil)
	r.TransferEncoding = []string{"chunked"}
	r.Trailer = http.Header{"X-Final": nil}
	view := decodeFact(t, Request(WithRequest(context.Background(), r)), false)
	values := map[string]string{}
	for _, entry := range view.Headers {
		values[entry.Name] = entry.Value
	}
	if values["Transfer-Encoding"] != "chunked" || values["Trailer"] != "X-Final" {
		t.Fatal(view)
	}
}
func BenchmarkHeaderCapture(b *testing.B) {
	headers := http.Header{"Authorization": {"Bearer private"}, "User-Agent": {"ordinary-client/1.0"}, "Accept": {"application/json"}, "X-Request-Id": {"synthetic-request"}, "X-Trace": {"first", "second"}}
	b.ReportAllocs()
	for b.Loop() {
		Capture(headers, "example.invalid", 200, time.Unix(1700000000, 0))
	}
}

func TestLegalLargeTabsAndCollectionLimits(t *testing.T) {
	fact := Capture(http.Header{"X-Tabs": {strings.Repeat("	", MaxHeaderBytes-64)}}, "", 200, time.Now())
	if fact.State() != "saved" || len(decodeFact(t, fact, false).Headers[0].Value) != MaxHeaderBytes-64 {
		t.Fatal("legal header expanded past limit")
	}
	ctx := WithResponse(context.Background(), "request", 1)
	for i := 0; i < MaxResponses; i++ {
		CaptureResponse(ctx, &http.Response{StatusCode: 200, Header: http.Header{"X-Page": {fmt.Sprint(i)}}})
	}
	prepared := Response(PrepareResponse(ctx, "request", 1), "request", 1)
	view, err := DecodeCollection(prepared.State(), prepared.Compressed(), false)
	if err != nil || len(view.Responses) != MaxResponses || view.Responses[19].ResponseIndex != 20 || view.Responses[19].Headers[0].Value != "19" {
		t.Fatal(view, err)
	}
	CaptureResponse(ctx, &http.Response{StatusCode: 200, Header: http.Header{}})
	prepared = Response(PrepareResponse(ctx, "request", 1), "request", 1)
	if prepared.State() != "too_large" || len(prepared.Compressed()) != 0 {
		t.Fatal("over-count capture looked complete")
	}
	ctx = WithResponse(context.Background(), "request", 2)
	for i := 0; i < 9; i++ {
		CaptureResponse(ctx, &http.Response{StatusCode: 200, Header: http.Header{"X-Large": {strings.Repeat("x", MaxHeaderBytes-64)}}})
	}
	if Response(PrepareResponse(ctx, "request", 2), "request", 2).State() != "too_large" {
		t.Fatal("decoded collection exceeded bound")
	}
}

func TestHeaderDecodeRejectsCorruptionAndInflation(t *testing.T) {
	var inflated bytes.Buffer
	writer := gzip.NewWriter(&inflated)
	_, _ = writer.Write(bytes.Repeat([]byte{'x'}, MaxEncodedBytes+1))
	_ = writer.Close()
	if _, err := Decode("saved", nil, nil, inflated.Bytes(), false); err == nil {
		t.Fatal("inflated stored data exceeded read bound")
	}
	if _, err := DecodeCollection("saved", []byte("HC1\x01\xff"), false); err == nil {
		t.Fatal("truncated collection accepted")
	}
	fact := Capture(http.Header{"X-Test": {"complete"}}, "", 200, time.Now())
	data := fact.Compressed()
	data[len(data)-1] ^= 0xff
	if _, err := Decode("saved", nil, nil, data, false); err == nil {
		t.Fatal("corrupt compressed fact accepted")
	}
}

func TestSwitchingProtocolResponseRemainsInspectable(t *testing.T) {
	ctx := WithResponse(t.Context(), "request", 1)
	CaptureResponse(ctx, &http.Response{StatusCode: 101, Header: http.Header{"Upgrade": {"unexpected"}}})
	fact := Response(PrepareResponse(ctx, "request", 1), "request", 1)
	view, err := DecodeCollection(fact.State(), fact.Compressed(), false)
	if err != nil || len(view.Responses) != 1 || *view.Responses[0].HTTPStatus != 101 || view.Responses[0].Headers[0].Value != "unexpected" {
		t.Fatalf("upgrade response: %+v %v", view, err)
	}
}
