package imageactivity

import (
	"encoding/json"
	"strings"
	"testing"
)

func receiptTestAdapter() Adapter {
	return Adapter{
		Discovery: DiscoveryAdapter{Method: "GET", Path: "/catalog", ItemsPointer: "/items", IDPointer: "/name"},
		Submit: SubmitAdapter{Method: "POST", Path: "/render",
			Mapping: Mapping{ModelPointer: "/engine", Parameters: map[ParameterKey]string{Prompt: "/text"}},
			Receipt: &ReceiptAdapter{IndicatorPointer: "/queued", IndicatorValue: json.RawMessage("true")}},
		Poll: &PollAdapter{Method: "GET", Path: "/work/{task_id}"},
		Response: ResponseAdapter{TaskIDPointer: ptr("/ticket"), StatePointer: ptr("/phase"),
			WorkingStates: []string{"rendering"}, SuccessStates: []string{"complete"}, FailureStates: []string{"rejected"},
			ImagesPointer: "/pictures", Base64Pointer: ptr("/bytes")},
	}
}

func TestSubmissionReceiptSeparatesPhaseAndRejectsContradictions(t *testing.T) {
	a := receiptTestAdapter()
	if err := ValidateAdapter(a); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, body, state string
	}{
		{"receipt", `{"queued":true,"ticket":"opaque/../ticket"}`, "running"},
		{"working receipt", `{"queued":true,"ticket":"one","phase":"rendering","pictures":[]}`, "running"},
		{"direct images", `{"pictures":[{"bytes":"candidate"}]}`, "succeeded"},
		{"negative receipt direct", `{"queued":false,"pictures":[{"bytes":"candidate"}]}`, "succeeded"},
		{"explicit successful direct", `{"phase":"complete","pictures":[{"bytes":"candidate"}]}`, "succeeded"},
		{"explicit failure", `{"phase":"rejected"}`, "failed"},
		{"negative receipt failure", `{"queued":false,"phase":"rejected","pictures":[]}`, "failed"},
		{"missing id", `{"queued":true}`, ""},
		{"empty id", `{"queued":true,"ticket":""}`, ""},
		{"numeric id", `{"queued":true,"ticket":7}`, ""},
		{"control id", `{"queued":true,"ticket":"bad\u0000value"}`, ""},
		{"unmarked id", `{"ticket":"one"}`, ""},
		{"id and direct images", `{"ticket":"one","pictures":[{}]}`, ""},
		{"typed marker", `{"queued":"true","pictures":[{}]}`, ""},
		{"null marker", `{"queued":null,"pictures":[{}]}`, ""},
		{"array marker", `{"queued":[],"pictures":[{}]}`, ""},
		{"receipt with images", `{"queued":true,"ticket":"one","pictures":[{}]}`, ""},
		{"receipt with malformed image array", `{"queued":true,"ticket":"one","pictures":{}}`, ""},
		{"receipt with null image array", `{"queued":true,"ticket":"one","pictures":null}`, ""},
		{"receipt with success state", `{"queued":true,"ticket":"one","phase":"complete"}`, ""},
		{"receipt with failure state", `{"queued":true,"ticket":"one","phase":"rejected"}`, ""},
		{"unmarked working images", `{"phase":"rendering","pictures":[{}]}`, ""},
		{"failure images", `{"phase":"rejected","pictures":[{}]}`, ""},
		{"unknown state images", `{"phase":"unrecognized","pictures":[{}]}`, ""},
		{"null state images", `{"phase":null,"pictures":[{}]}`, ""},
		{"empty direct", `{"pictures":[]}`, ""},
		{"missing images", `{}`, ""},
		{"invalid images", `{"pictures":{}}`, ""},
		{"truncated images", `{"pictures":[{}]`, ""},
		{"trailing document", `{"pictures":[{}]}{}`, ""},
		{"duplicate marker", `{"queued":false,"queued":true,"ticket":"one"}`, ""},
		{"invalid unrelated string", `{"pictures":[{}],"other":"\x"}`, ""},
		{"excessive images", `{"pictures":[` + strings.Repeat("{},", 16) + "{}]}", ""},
		{"oversized id", `{"queued":true,"ticket":"` + strings.Repeat("x", 2049) + `"}`, ""},
		{"deep unrelated structure", `{"pictures":[{}],"other":` + strings.Repeat("[", 33) + "0" + strings.Repeat("]", 33) + "}", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, err := submissionState([]byte(tc.body), a)
			if tc.state == "" {
				if err == nil {
					t.Fatalf("ambiguous or invalid response accepted as %q", state)
				}
			} else if err != nil || state != tc.state {
				t.Fatalf("state=%q err=%v want=%q", state, err, tc.state)
			}
		})
	}
	for _, body := range []string{`{"queued":true,"ticket":"one"}`, `{"phase":"unrecognized"}`} {
		if _, err := responseState([]byte(body), a.Response); err == nil {
			t.Fatal("submission grammar weakened the poll decoder")
		}
	}
	a.Submit.Receipt = nil
	if state, err := submissionState([]byte(`{"phase":"rendering"}`), a); err != nil || state != "running" {
		t.Fatalf("existing submission grammar changed: %q %v", state, err)
	}
	if _, err := submissionState([]byte(`{"pictures":[{}]}`), a); err == nil {
		t.Fatal("unconfigured receipt mode changed existing semantics")
	}
}

func TestReceiptMarkersAreTypedAndConfigurationIsBounded(t *testing.T) {
	a := receiptTestAdapter()
	a.Submit.Receipt.IndicatorValue = json.RawMessage(`"accepted"`)
	if state, err := submissionState([]byte(`{"queued":"accepted","ticket":"one"}`), a); err != nil || state != "running" {
		t.Fatalf("string indicator %q %v", state, err)
	}
	if state, err := submissionState([]byte(`{"queued":"direct","pictures":[{}]}`), a); err != nil || state != "succeeded" {
		t.Fatalf("negative string indicator %q %v", state, err)
	}
	if _, err := submissionState([]byte(`{"queued":true,"ticket":"one"}`), a); err == nil {
		t.Fatal("boolean marker matched configured string")
	}
	a.Submit.Receipt.IndicatorValue = json.RawMessage("false")
	if state, err := submissionState([]byte(`{"queued":false,"ticket":"one"}`), a); err != nil || state != "running" {
		t.Fatalf("false receipt marker %q %v", state, err)
	}
	for _, raw := range []string{"", "null", "1", "[]", "{}", `""`, `"bad\nvalue"`, `"` + strings.Repeat("a", 129) + `"`, "true false"} {
		t.Run("invalid scalar "+raw[:min(len(raw), 20)], func(t *testing.T) {
			invalid := receiptTestAdapter()
			invalid.Submit.Receipt.IndicatorValue = json.RawMessage(raw)
			if err := ValidateAdapter(invalid); err == nil {
				t.Fatal("invalid indicator configuration accepted")
			}
		})
	}
	a = receiptTestAdapter()
	a.Submit.Receipt.IndicatorValue, _ = json.Marshal(strings.Repeat("a", 128))
	if err := ValidateAdapter(a); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func(*Adapter)
	}{
		{"missing poll", func(a *Adapter) { a.Poll = nil; a.Response.TaskIDPointer = nil }},
		{"missing task pointer", func(a *Adapter) { a.Response.TaskIDPointer = nil }},
		{"missing state pointer", func(a *Adapter) { a.Response.StatePointer = nil }},
		{"root marker", func(a *Adapter) { a.Submit.Receipt.IndicatorPointer = "" }},
		{"same task", func(a *Adapter) { a.Submit.Receipt.IndicatorPointer = "/ticket" }},
		{"inside task", func(a *Adapter) { a.Submit.Receipt.IndicatorPointer = "/ticket/nested" }},
		{"marker contains task", func(a *Adapter) {
			a.Submit.Receipt.IndicatorPointer = "/ticket"
			a.Response.TaskIDPointer = ptr("/ticket/value")
		}},
		{"same state", func(a *Adapter) { a.Submit.Receipt.IndicatorPointer = "/phase" }},
		{"same images", func(a *Adapter) { a.Submit.Receipt.IndicatorPointer = "/pictures" }},
		{"inside images", func(a *Adapter) { a.Submit.Receipt.IndicatorPointer = "/pictures/0/flag" }},
		{"state overlaps images", func(a *Adapter) { a.Response.StatePointer = ptr("/pictures/state") }},
		{"escaped overlap", func(a *Adapter) {
			a.Submit.Receipt.IndicatorPointer = "/result~1value"
			a.Response.TaskIDPointer = ptr("/result~1value/id")
		}},
		{"oversized marker path", func(a *Adapter) { a.Submit.Receipt.IndicatorPointer = "/" + strings.Repeat("a", 257) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invalid := receiptTestAdapter()
			tc.edit(&invalid)
			if err := ValidateAdapter(invalid); err == nil {
				t.Fatal("ambiguous or incomplete receipt configuration accepted")
			}
		})
	}
	a = receiptTestAdapter()
	a.Submit.Receipt.IndicatorPointer = "/receipt/queued"
	a.Response.TaskIDPointer = ptr("/receipt/id")
	if state, err := submissionState([]byte(`{"receipt":{"queued":true,"id":"one"}}`), a); err != nil || state != "running" {
		t.Fatalf("disjoint nested receipt %q %v", state, err)
	}
}
