package contract

import (
	"errors"
	"slices"
	"testing"
)

func nativeServicesForTest(t *testing.T, services ...NativeService) NativeServiceSet {
	t.Helper()
	set, err := ParseNativeServices(services)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func TestNativeServicesFrozenBitsAndProjection(t *testing.T) {
	want := []NativeService{
		"chat_completions", "responses", "responses_compact", "responses_input_tokens",
		"embeddings", "images_generations", "images_edits", "images_variations",
		"audio_speech", "audio_transcriptions", "audio_translations", "completions",
		"moderations", "decisions", "rerank", "systemone", "anthropic_messages",
		"gemini_generate_content", "gateway_text",
	}
	all := AllNativeServices()
	if !slices.Equal(all, want) {
		t.Fatalf("native service order = %v", all)
	}
	for i, service := range want {
		set := nativeServicesForTest(t, service)
		stored, err := NativeServicesFromMask(1 << i)
		if err != nil || stored != set || set.Mask() != 1<<i || !slices.Equal(set.Services(), []NativeService{service}) {
			t.Fatalf("service %s changed bit %d: set=%v stored=%v err=%v", service, i, set, stored, err)
		}
	}
	set := nativeServicesForTest(t, NativeServiceGatewayText, NativeServiceEmbeddings, NativeServiceResponses)
	wantProjection := []NativeService{NativeServiceResponses, NativeServiceEmbeddings, NativeServiceGatewayText}
	if !slices.Equal(set.Services(), wantProjection) || set.Mask() != 1<<1|1<<4|1<<18 {
		t.Fatalf("unstable set projection: %v", set.Services())
	}
	all[0] = "changed"
	projection := set.Services()
	projection[0] = "changed"
	if AllNativeServices()[0] != NativeServiceChatCompletions || !slices.Equal(set.Services(), wantProjection) {
		t.Fatal("returned arrays alias the service set")
	}
}

func TestNativeServicesValidationAndSetBoundaries(t *testing.T) {
	for _, input := range [][]NativeService{{"unknown"}, {""}, {"Chat_completions"}, {" responses"}, {"responses", "responses"}} {
		if set, err := ParseNativeServices(input); !errors.Is(err, ErrInvalidNativeServices) || !set.Empty() {
			t.Fatalf("invalid services accepted: %v, %v", input, err)
		}
	}
	for _, mask := range []int64{-1, 1 << 19, 1<<35 | 1} {
		if _, err := NativeServicesFromMask(mask); !errors.Is(err, ErrInvalidNativeServices) {
			t.Fatalf("invalid mask %d accepted", mask)
		}
	}
	empty := nativeServicesForTest(t)
	storedEmpty, err := NativeServicesFromMask(0)
	if err != nil || storedEmpty != empty || empty.Services() == nil || len(empty.Services()) != 0 {
		t.Fatal("empty model set was not preserved")
	}
	chat := nativeServicesForTest(t, NativeServiceChatCompletions)
	responses := nativeServicesForTest(t, NativeServiceResponses)
	both := nativeServicesForTest(t, NativeServiceChatCompletions, NativeServiceResponses)
	if chat.Has(NativeServiceResponses) || chat.Has("unknown") || chat.Intersect(responses) != empty || both.Intersect(chat) != chat ||
		!empty.IsSubsetOf(chat) || !chat.IsSubsetOf(both) || both.IsSubsetOf(chat) {
		t.Fatal("native services were widened or subset boundaries changed")
	}
	if err := ValidateNativeServices(TypeOpenAICompatible, empty, ""); !errors.Is(err, ErrInvalidNativeServices) {
		t.Fatal("empty endpoint selection was accepted")
	}
}

func TestNativeServicesLegacyPermissions(t *testing.T) {
	for _, test := range []struct {
		kind Type
		mask int64
	}{
		{TypeOpenAICompatible, 1<<0 | 1<<4 | 1<<5},
		{TypeAnthropicCompatible, 1 << 16},
		{TypeAISDKGatewayV3, 1 << 18},
		{TypeGoogleGemini, 1 << 17},
		{TypeOpenAIResponses, 1 << 1},
		{TypeOpenAIDecisions, 1 << 13},
		{TypeAISDKGatewayV4, 1 << 18},
		{TypeOpenAIChatCompletions, 1 << 0},
		{TypeCohereRerank, 1 << 14},
		{TypeJinaRerank, 1 << 14},
		{TypeVoyageRerank, 1 << 14},
		{TypeSiliconFlowRerank, 1 << 14},
		{TypeTypeSafeSystemOne, 1 << 15},
	} {
		t.Run(string(test.kind), func(t *testing.T) {
			legacy, err := LegacyNativeServices(test.kind)
			supported, supportErr := SupportedNativeServices(test.kind)
			if err != nil || supportErr != nil || legacy.Mask() != test.mask || !legacy.IsSubsetOf(supported) || ValidateNativeServices(test.kind, legacy, "") != nil {
				t.Fatalf("legacy permission widened: legacy=%v supported=%v errors=%v/%v", legacy.Services(), supported.Services(), err, supportErr)
			}
		})
	}
	if _, err := LegacyNativeServices("unknown"); !errors.Is(err, ErrInvalidNativeServices) {
		t.Fatal("unknown legacy connector accepted")
	}
}

func TestNativeServicesProtocolAndRerankValidation(t *testing.T) {
	for _, test := range []struct {
		name     string
		kind     Type
		services []NativeService
		format   RerankFormat
		wantErr  error
	}{
		{"explicit generic", TypeOpenAICompatible, []NativeService{NativeServiceRerank}, RerankFormatGeneric, nil},
		{"rerank format required", TypeOpenAICompatible, []NativeService{NativeServiceRerank}, "", ErrInvalidRerankFormat},
		{"chat subset keeps endpoint format", TypeOpenAICompatible, []NativeService{NativeServiceChatCompletions}, RerankFormatCohere, nil},
		{"unknown format in chat subset", TypeOpenAICompatible, []NativeService{NativeServiceChatCompletions}, "guess", ErrInvalidRerankFormat},
		{"fixed format omitted", TypeCohereRerank, []NativeService{NativeServiceRerank}, "", nil},
		{"fixed format explicit", TypeCohereRerank, []NativeService{NativeServiceRerank}, RerankFormatCohere, nil},
		{"fixed format conflict", TypeCohereRerank, []NativeService{NativeServiceRerank}, RerankFormatJina, ErrInvalidRerankFormat},
		{"gateway native rerank", TypeAISDKGatewayV4, []NativeService{NativeServiceRerank}, "", nil},
		{"gateway format conflict", TypeAISDKGatewayV4, []NativeService{NativeServiceRerank}, RerankFormatGeneric, ErrInvalidRerankFormat},
		{"gateway unused format", TypeAISDKGatewayV3, []NativeService{NativeServiceGatewayText}, RerankFormatCohere, ErrInvalidRerankFormat},
		{"messages format conflict", TypeAnthropicCompatible, []NativeService{NativeServiceAnthropicMessages}, RerankFormatGeneric, ErrInvalidRerankFormat},
		{"chat alias explicitly enables responses", TypeOpenAIChatCompletions, []NativeService{NativeServiceResponses}, "", nil},
		{"responses alias explicitly enables chat", TypeOpenAIResponses, []NativeService{NativeServiceChatCompletions}, "", nil},
		{"decisions alias explicitly enables embeddings", TypeOpenAIDecisions, []NativeService{NativeServiceEmbeddings}, "", nil},
		{"gemini has no embedding wire", TypeGoogleGemini, []NativeService{NativeServiceEmbeddings}, "", ErrInvalidNativeServices},
		{"gateway has no native responses", TypeAISDKGatewayV3, []NativeService{NativeServiceResponses}, "", ErrInvalidNativeServices},
		{"openai has no systemone", TypeOpenAICompatible, []NativeService{NativeServiceSystemOne}, "", ErrInvalidNativeServices},
		{"unknown protocol", "unknown", []NativeService{NativeServiceChatCompletions}, "", ErrInvalidNativeServices},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateNativeServices(test.kind, nativeServicesForTest(t, test.services...), test.format)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("validation error = %v, want %v", err, test.wantErr)
			}
		})
	}
}
