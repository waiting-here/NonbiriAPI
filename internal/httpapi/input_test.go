package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

func TestFieldPresenceAndNull(t *testing.T) {
	type payload struct {
		Required RequestField[bool]   `json:"required"`
		Nullable NullableField[int64] `json:"nullable"`
		Object   RequestField[struct {
			Name string `json:"name"`
		}] `json:"object"`
	}
	for _, test := range []struct {
		raw                                 string
		set, nullableSet, hasValue, invalid bool
	}{
		{raw: "{}"},
		{raw: "{\"required\":false}", set: true},
		{raw: "{\"required\":null}", invalid: true},
		{raw: "{\"nullable\":null}", nullableSet: true},
		{raw: "{\"nullable\":0}", nullableSet: true, hasValue: true},
		{raw: "{\"object\":{\"unexpected\":1}}", invalid: true},
	} {
		t.Run(test.raw, func(t *testing.T) {
			var got payload
			err := DecodeJSON([]byte(test.raw), &got)
			if (err != nil) != test.invalid {
				t.Fatalf("decode = %v", err)
			}
			if err == nil && (got.Required.Set != test.set || got.Nullable.Set != test.nullableSet || (got.Nullable.Value != nil) != test.hasValue) {
				t.Fatalf("presence = %+v", got)
			}
		})
	}
	var nullable NullableField[int64]
	if json.Unmarshal([]byte("1"), &nullable) != nil || json.Unmarshal([]byte("null"), &nullable) != nil || !nullable.Set || nullable.Value != nil {
		t.Fatal("null did not clear prior value")
	}
	for _, raw := range []string{"1 2", "true false", "{}{}"} {
		var field RequestField[any]
		if field.UnmarshalJSON([]byte(raw)) == nil {
			t.Fatalf("accepted trailing value: %s", raw)
		}
	}
}

type brokenBody struct{}

func (brokenBody) Read([]byte) (int, error) { return 0, errors.New("private body read failure") }
func (brokenBody) Close() error             { return nil }

func TestReadJSONPoliciesAndErrors(t *testing.T) {
	tests := []struct {
		name, raw, contentType string
		required               bool
		limit                  int64
		want                   error
	}{
		{name: "optional media type", raw: "{\"value\":\"ok\"}", limit: 128},
		{name: "required media type", raw: "{\"value\":\"ok\"}", required: true, limit: 128, want: ErrInvalid},
		{name: "json with parameters", raw: "{\"value\":\"ok\"}", contentType: "APPLICATION/JSON; charset=utf-8", required: true, limit: 128},
		{name: "duplicate decoded names", raw: "{\"value\":\"ok\",\"\\u0076alue\":\"bad\"}", limit: 128, want: ErrInvalid},
		{name: "unknown field", raw: "{\"unknown\":true}", limit: 128, want: ErrInvalid},
		{name: "trailing json", raw: "{\"value\":\"ok\"}{}", limit: 128, want: ErrInvalid},
		{name: "unpaired surrogate", raw: "{\"value\":\"\\ud800\"}", limit: 128, want: ErrInvalid},
		{name: "invalid utf8", raw: "{\"value\":\"" + string([]byte{255}) + "\"}", limit: 128, want: ErrInvalid},
		{name: "too large", raw: "{\"value\":\"ok\"}", limit: 8, want: ErrTooLarge},
		{name: "empty", limit: 128, want: ErrInvalid},
		{name: "exact byte bound", raw: "{\"value\":\"ok\"}", limit: 14},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.raw))
			r.Header.Set("Content-Type", test.contentType)
			var got struct {
				Value string `json:"value"`
			}
			options := BodyOptions{MaxBytes: test.limit, Validate: strictjson.ValidateObject}
			if test.required {
				options.ContentType = JSONContentType
			}
			body, err := ReadJSON(httptest.NewRecorder(), r, &got, options)
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v, want=%v", err, test.want)
			}
			if err != nil && body != nil {
				t.Fatal("returned failed input")
			}
			if err == nil && string(body) != test.raw {
				t.Fatal("changed accepted raw body")
			}
		})
	}
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Body = brokenBody{}
	var dst any
	if _, err := ReadJSON(httptest.NewRecorder(), r, &dst, BodyOptions{MaxBytes: 64, Validate: strictjson.ValidateObject}); err != ErrInvalid {
		t.Fatalf("read failure=%v", err)
	}
}

func TestQueryAndEmptyBodyPolicies(t *testing.T) {
	for _, test := range []struct {
		raw                          string
		force, reject, rawOnly, want bool
	}{
		{want: true}, {force: true, want: true}, {force: true, reject: true},
		{raw: "&&", want: true}, {raw: "&&", rawOnly: true}, {raw: "a="},
		{raw: "a=1&a=2"}, {raw: "%zz"}, {raw: "a=1;b=2"},
	} {
		r := &http.Request{URL: &url.URL{RawQuery: test.raw, ForceQuery: test.force}}
		if got := EmptyQuery(r, test.reject, test.rawOnly); got != test.want {
			t.Fatalf("%+v: empty=%v", test, got)
		}
	}
	if ExactQuery(url.Values{"a": {"1", "2"}}, "a") || ExactQuery(url.Values{"b": {"1"}}, "a") || !ExactQuery(url.Values{"a": {""}}, "a") {
		t.Fatal("query allowlist drift")
	}
	for _, raw := range []string{"", " ", "x"} {
		r := httptest.NewRequest(http.MethodGet, "/", strings.NewReader(raw))
		if NoBody(r) != (raw == "") {
			t.Fatalf("no-body policy: %q", raw)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Body = io.NopCloser(brokenBody{})
	if NoBody(r) {
		t.Fatal("accepted failing body")
	}
}

func TestFieldBudgetPoliciesRemainDistinct(t *testing.T) {
	raw := []byte("{\"items\":[" + strings.Repeat("{\"value\":0},", 299) + "{\"value\":0}]}")
	var target map[string]json.RawMessage
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(raw)))
	if _, err := ReadJSON(httptest.NewRecorder(), r, &target, BodyOptions{MaxBytes: 8192, Validate: strictjson.ValidateObjectPerObject}); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(raw)))
	if _, err := ReadJSON(httptest.NewRecorder(), r, &target, BodyOptions{MaxBytes: 8192, Validate: strictjson.ValidateObject}); err != ErrInvalid {
		t.Fatalf("aggregate budget bypass: %v", err)
	}
}

func TestExactNumberDecodeRemainsOptIn(t *testing.T) {
	var exact map[string]any
	if err := DecodeJSONWithNumbers([]byte("{\"value\":9007199254740993}"), &exact); err != nil {
		t.Fatal(err)
	}
	if got, ok := exact["value"].(json.Number); !ok || got.String() != "9007199254740993" {
		t.Fatalf("lost exact number: %+v", exact)
	}
	var ordinary map[string]any
	if err := DecodeJSON([]byte("{\"value\":1}"), &ordinary); err != nil {
		t.Fatal(err)
	}
	if _, ok := ordinary["value"].(float64); !ok {
		t.Fatal("default numeric decode changed")
	}
}
