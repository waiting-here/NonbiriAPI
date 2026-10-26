package openai

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCharityTextRuneCountIncludesToolPromptText(t *testing.T) {
	longText := strings.Repeat("完整故事与上下文🐱", 100)
	encoded, _ := json.Marshal(longText)
	for _, tc := range []struct {
		name, fields string
		want         int
	}{
		{"tool_description", `"messages":[{"role":"user","content":"continue please"}],"tools":[{"type":"function","function":{"name":"reply","description":` + string(encoded) + `}}]`, 15 + utf8.RuneCountInString(longText)},
		{"nested_schema", `"messages":[{"role":"user","content":"continue please"}],"tools":[{"type":"function","function":{"name":"reply","parameters":{"$defs":{"story":{"description":` + string(encoded) + `}}}}}]`, 15 + 5 + 5 + 11 + utf8.RuneCountInString(longText)},
		{"tool_arguments_without_content", `"messages":[{"role":"user","content":"continue please"},{"role":"assistant","tool_calls":[{"id":"ignored","type":"function","function":{"name":"reply","arguments":` + string(encoded) + `}}]}]`, 15 + utf8.RuneCountInString(longText)},
		{"legacy_functions", `"messages":[{"role":"user","content":"continue please"},{"role":"assistant","content":null,"function_call":{"name":"reply","arguments":"甲🐱"}}],"functions":[{"name":"reply","description":"乙"}]`, 18},
		{"tool_result_once", `"messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"reply","arguments":"{\"text\":\"甲\"}"}}]},{"role":"tool","tool_call_id":"call_1","content":"乙🐱"}]`, 14},
		{"unicode_escapes", `"messages":[{"role":"user","content":"\u7532"}],"tools":[{"type":"function","function":{"name":"reply","parameters":{"description":"\ud83d\udc31"}}}]`, 13},
		{"media_and_metadata_ignored", `"messages":[{"role":"user","name":"ignored long name","content":[{"type":"text","text":"甲🐱"},{"type":"image_url","image_url":{"url":"data:image/png;base64,very-long-data"}},{"type":"input_audio","input_audio":{"data":"very-long-data","format":"wav"}}]}],"metadata":{"description":` + string(encoded) + `},"user":` + string(encoded), 2},
		{"tool_presence_is_not_exemption", `"messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"arbitrarily_long_id","type":"function","function":{"name":"arbitrarily_long_name","arguments":"{}"}}]}],"tools":[{"type":"function","function":{"name":"arbitrarily_long_name","parameters":{}}}]`, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, err := DecodeChatRequest(strings.NewReader(`{"model":"provider/model",`+tc.fields+`}`), MaxRequestBodyBytes)
			if err != nil {
				t.Fatal(err)
			}
			defer request.Clear()
			before, err := request.LogicalBody()
			if err != nil {
				t.Fatal(err)
			}
			got, err := request.CharityTextRuneCount()
			if err != nil || got != tc.want {
				t.Fatalf("count=%d want=%d err=%v", got, tc.want, err)
			}
			after, err := request.LogicalBody()
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("counting changed request body", err)
			}
		})
	}
}

func TestCharitySchemaTextIgnoresEncodingAndFormattingSize(t *testing.T) {
	for _, raw := range []string{
		`{"description":"甲🐱","enum":["a",true,null,1e999]}`,
		"{\n  \"description\" : \"\\u7532\\ud83d\\udc31\", \"enum\": [\"a\", true, null, 1e999]\n}",
	} {
		if got := charitySchemaTextRunes([]byte(raw)); got != 18 {
			t.Fatalf("count=%d want=18", got)
		}
	}
}
