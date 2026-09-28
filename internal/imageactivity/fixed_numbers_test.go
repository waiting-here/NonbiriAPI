package imageactivity

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestFixedNumbersRejectRoundedValuesAndUnboundedExponents(t *testing.T) {
	capability := compileFixedCapability(fixedMetadata(`{"steps":{"max":8,"default":4},"cfgScale":{"scale":[1,1.5,2]}}`, `{"steps":true}`))
	for _, input := range []SubmitInput{
		{Seed: json.RawMessage("0.0000000000000000000000000000000000001")},
		{Seed: json.RawMessage("999999999.0000000000000000000000001")},
		{Steps: json.RawMessage("4.000000000000000000000000000000000001")},
		{Guidance: json.RawMessage("1.500000000000000000000000000000000001")},
		{Seed: json.RawMessage("1e-999999999")},
		{Seed: json.RawMessage("0e999999999")},
		{Seed: json.RawMessage("1e999999999")},
		{Seed: json.RawMessage(strings.Repeat("0", 300))},
	} {
		if err := validateFixedNumbers(input, capability.rules); !errors.Is(err, ErrInvalid) {
			t.Fatalf("rounded or unbounded numeric input accepted: %+v", input)
		}
	}
	input := SubmitInput{Seed: json.RawMessage("0e-12"), Steps: json.RawMessage("40e-1"), Guidance: json.RawMessage("15e-1")}
	if err := validateFixedNumbers(input, capability.rules); err != nil {
		t.Fatal("exact equivalent decimal representation was rejected")
	}
	for _, settings := range []string{
		`{"steps":{"max":8.00000000000000000000000000000001}}`,
		`{"steps":{"max":8,"default":4.00000000000000000000000000000001}}`,
		`{"cfgScale":{"scale":[1.50000000000000000000000000000001]}}`,
		`{"promptCharacterLimit":0.00000000000000000000000000000001}`,
	} {
		if compiled := compileFixedCapability(fixedMetadata(settings, `{"steps":true}`)); compiled.compiled.Readiness != "pending" {
			t.Fatalf("metadata numeric precision was silently rounded: %s", settings)
		}
	}
}

func TestFixedServiceExactNumbersValidateQuoteSubmitAndCheck(t *testing.T) {
	f, mock, _ := configureFixedService(t)
	model, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []json.RawMessage{json.RawMessage("1.500000000000000000000000000000001"), json.RawMessage("1e-999999999")} {
		input := SubmitInput{ModelID: f.model, ExpectedModelRevision: model.Revision, ExpectedPricingRevision: model.PricingRevision, Prompt: "Synthetic exact numeric request", Guidance: raw}
		if _, err = f.service.Quote(f.ctx(f.user), f.user, input); !errors.Is(err, ErrInvalid) {
			t.Fatal("quote accepted a rounded guidance value")
		}
		if _, err = f.service.Submit(f.ctx(f.user), f.user, f.key(), input); !errors.Is(err, ErrInvalid) {
			t.Fatal("submission accepted a rounded guidance value")
		}
		check, err := f.service.CheckModelSettings(f.ctx(f.admin), f.admin, ModelSettingsCheck{ModelID: f.model, Draft: ModelSettingsInput{ExpectedRevision: model.Revision, Enabled: true, Price: model.Price}, Parameters: input})
		if err != nil || check.Valid || len(check.Issues) != 1 || check.Issues[0].Code != "invalid_parameters" {
			t.Fatalf("check accepted a rounded guidance value: %+v %v", check, err)
		}
	}
	if mock.posts.Load() != 0 {
		t.Fatal("invalid exact numeric values caused an upstream request")
	}
	input := SubmitInput{ModelID: f.model, Prompt: "Synthetic exact numeric request", Seed: json.RawMessage("0e-12"), Steps: json.RawMessage("40e-1"), Guidance: json.RawMessage("15e-1")}
	if _, err = f.service.Quote(f.ctx(f.user), f.user, input); err != nil {
		t.Fatal("valid exact decimals cannot be quoted")
	}
	f.checkLedger(t)
}
