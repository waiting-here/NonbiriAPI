package contract

import "errors"

// NativeService names an upstream service independently of caller operations
// and request fidelity capabilities.
type NativeService string

const (
	NativeServiceChatCompletions       NativeService = "chat_completions"
	NativeServiceResponses             NativeService = "responses"
	NativeServiceResponsesCompact      NativeService = "responses_compact"
	NativeServiceResponsesInputTokens  NativeService = "responses_input_tokens"
	NativeServiceEmbeddings            NativeService = "embeddings"
	NativeServiceImagesGenerations     NativeService = "images_generations"
	NativeServiceImagesEdits           NativeService = "images_edits"
	NativeServiceImagesVariations      NativeService = "images_variations"
	NativeServiceAudioSpeech           NativeService = "audio_speech"
	NativeServiceAudioTranscriptions   NativeService = "audio_transcriptions"
	NativeServiceAudioTranslations     NativeService = "audio_translations"
	NativeServiceCompletions           NativeService = "completions"
	NativeServiceModerations           NativeService = "moderations"
	NativeServiceDecisions             NativeService = "decisions"
	NativeServiceRerank                NativeService = "rerank"
	NativeServiceSystemOne             NativeService = "systemone"
	NativeServiceAnthropicMessages     NativeService = "anthropic_messages"
	NativeServiceGeminiGenerateContent NativeService = "gemini_generate_content"
	NativeServiceGatewayText           NativeService = "gateway_text"
)

// This order also fixes persisted bit positions. New services must not reorder
// existing entries, and changes require a corresponding storage contract.
var nativeServiceOrder = [...]NativeService{
	NativeServiceChatCompletions, NativeServiceResponses, NativeServiceResponsesCompact,
	NativeServiceResponsesInputTokens, NativeServiceEmbeddings, NativeServiceImagesGenerations,
	NativeServiceImagesEdits, NativeServiceImagesVariations, NativeServiceAudioSpeech,
	NativeServiceAudioTranscriptions, NativeServiceAudioTranslations, NativeServiceCompletions,
	NativeServiceModerations, NativeServiceDecisions, NativeServiceRerank, NativeServiceSystemOne,
	NativeServiceAnthropicMessages, NativeServiceGeminiGenerateContent, NativeServiceGatewayText,
}

var (
	ErrInvalidNativeServices = errors.New("invalid native services")
	ErrInvalidRerankFormat   = errors.New("invalid rerank format for selected services")
)

// NativeServiceSet can only contain known bits. The empty set represents an
// unresolved model or an empty intersection; endpoint validation rejects it.
type NativeServiceSet struct{ mask uint32 }

func AllNativeServices() []NativeService {
	return append([]NativeService{}, nativeServiceOrder[:]...)
}

func ParseNativeServices(services []NativeService) (NativeServiceSet, error) {
	var result NativeServiceSet
	for _, service := range services {
		bit := nativeServiceBit(service)
		if bit == 0 || result.mask&bit != 0 {
			return NativeServiceSet{}, ErrInvalidNativeServices
		}
		result.mask |= bit
	}
	return result, nil
}

func NativeServicesFromMask(mask int64) (NativeServiceSet, error) {
	if mask < 0 || mask >= 1<<len(nativeServiceOrder) {
		return NativeServiceSet{}, ErrInvalidNativeServices
	}
	return NativeServiceSet{mask: uint32(mask)}, nil
}

func (s NativeServiceSet) Mask() int64 { return int64(s.mask) }
func (s NativeServiceSet) Empty() bool { return s.mask == 0 }
func (s NativeServiceSet) Has(service NativeService) bool {
	return s.mask&nativeServiceBit(service) != 0
}
func (s NativeServiceSet) Intersect(other NativeServiceSet) NativeServiceSet {
	return NativeServiceSet{mask: s.mask & other.mask}
}
func (s NativeServiceSet) IsSubsetOf(other NativeServiceSet) bool {
	return s.mask & ^other.mask == 0
}
func (s NativeServiceSet) Services() []NativeService {
	result := []NativeService{}
	for i, service := range nativeServiceOrder {
		if s.mask&(1<<i) != 0 {
			result = append(result, service)
		}
	}
	return result
}

func nativeServiceBit(service NativeService) uint32 {
	for i, known := range nativeServiceOrder {
		if service == known {
			return 1 << i
		}
	}
	return 0
}

type RerankFormat string

const (
	RerankFormatGeneric     RerankFormat = "generic"
	RerankFormatCohere      RerankFormat = "cohere"
	RerankFormatJina        RerankFormat = "jina"
	RerankFormatVoyage      RerankFormat = "voyage"
	RerankFormatSiliconFlow RerankFormat = "siliconflow"
)

func (f RerankFormat) valid() bool {
	switch f {
	case RerankFormatGeneric, RerankFormatCohere, RerankFormatJina, RerankFormatVoyage, RerankFormatSiliconFlow:
		return true
	default:
		return false
	}
}

type nativeServiceProfile struct {
	supported   []NativeService
	legacy      []NativeService
	fixedRerank RerankFormat
}

// OpenAIServiceFamily includes historical configuration names whose explicit
// service selections use the same OpenAI-compatible drivers.
func (t Type) OpenAIServiceFamily() bool {
	return t == TypeOpenAICompatible || t == TypeOpenAIChatCompletions || t == TypeOpenAIResponses || t == TypeOpenAIDecisions
}

func serviceProfile(kind Type) (nativeServiceProfile, error) {
	var profile nativeServiceProfile
	switch kind {
	case TypeOpenAICompatible, TypeOpenAIChatCompletions, TypeOpenAIResponses, TypeOpenAIDecisions:
		profile.supported = AllNativeServices()[:15]
		switch kind {
		case TypeOpenAICompatible:
			profile.legacy = []NativeService{NativeServiceChatCompletions, NativeServiceEmbeddings, NativeServiceImagesGenerations}
		case TypeOpenAIChatCompletions:
			profile.legacy = []NativeService{NativeServiceChatCompletions}
		case TypeOpenAIResponses:
			profile.legacy = []NativeService{NativeServiceResponses}
		case TypeOpenAIDecisions:
			profile.legacy = []NativeService{NativeServiceDecisions}
		}
	case TypeAnthropicCompatible:
		profile.supported = []NativeService{NativeServiceAnthropicMessages}
	case TypeGoogleGemini:
		profile.supported = []NativeService{NativeServiceGeminiGenerateContent}
	case TypeAISDKGatewayV3, TypeAISDKGatewayV4:
		profile.supported = []NativeService{NativeServiceGatewayText, NativeServiceImagesGenerations, NativeServiceEmbeddings, NativeServiceRerank}
		profile.legacy = []NativeService{NativeServiceGatewayText}
	case TypeCohereRerank:
		profile.supported = []NativeService{NativeServiceRerank}
		profile.fixedRerank = RerankFormatCohere
	case TypeJinaRerank:
		profile.supported = []NativeService{NativeServiceRerank}
		profile.fixedRerank = RerankFormatJina
	case TypeVoyageRerank:
		profile.supported = []NativeService{NativeServiceRerank}
		profile.fixedRerank = RerankFormatVoyage
	case TypeSiliconFlowRerank:
		profile.supported = []NativeService{NativeServiceRerank}
		profile.fixedRerank = RerankFormatSiliconFlow
	case TypeTypeSafeSystemOne:
		profile.supported = []NativeService{NativeServiceSystemOne}
	default:
		return nativeServiceProfile{}, ErrInvalidNativeServices
	}
	if profile.legacy == nil {
		profile.legacy = profile.supported
	}
	return profile, nil
}

func SupportedNativeServices(kind Type) (NativeServiceSet, error) {
	profile, err := serviceProfile(kind)
	if err != nil {
		return NativeServiceSet{}, err
	}
	return ParseNativeServices(profile.supported)
}

// LegacyNativeServices defines omitted-field compatibility, including newly
// created records using the old API. It does not enable newer optional services.
func LegacyNativeServices(kind Type) (NativeServiceSet, error) {
	profile, err := serviceProfile(kind)
	if err != nil {
		return NativeServiceSet{}, err
	}
	return ParseNativeServices(profile.legacy)
}

// ValidateNativeServices checks a complete endpoint selection. Model subsets
// may be empty, but must be checked against the authorized endpoint set.
func ValidateNativeServices(kind Type, services NativeServiceSet, format RerankFormat) error {
	profile, err := serviceProfile(kind)
	if err != nil {
		return err
	}
	supported, err := ParseNativeServices(profile.supported)
	if err != nil || services.Empty() || !services.IsSubsetOf(supported) {
		return ErrInvalidNativeServices
	}
	if kind.OpenAIServiceFamily() {
		// A model or donation subset may omit rerank while retaining the
		// endpoint's selected format. Enabling rerank requires an explicit one.
		if format != "" && !format.valid() || services.Has(NativeServiceRerank) && format == "" {
			return ErrInvalidRerankFormat
		}
		return nil
	}
	if profile.fixedRerank != "" {
		if format != "" && format != profile.fixedRerank {
			return ErrInvalidRerankFormat
		}
		return nil
	}
	if format != "" {
		return ErrInvalidRerankFormat
	}
	return nil
}
