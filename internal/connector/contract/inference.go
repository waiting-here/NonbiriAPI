package contract

const (
	TypeOpenAIChatCompletions Type = "openai-chat-completions"
	TypeTypeSafeSystemOne     Type = "typesafe-systemone"
	TypeCohereRerank          Type = "cohere-rerank"
	TypeJinaRerank            Type = "jina-rerank"
	TypeSiliconFlowRerank     Type = "siliconflow-rerank"
	TypeVoyageRerank          Type = "voyage-rerank"
)

const (
	OperationResponses            Operation = "responses"
	OperationResponsesCompact     Operation = "responses_compact"
	OperationResponsesInputTokens Operation = "responses_input_tokens"
	OperationCompletions          Operation = "completions"
	OperationImagesEdits          Operation = "images_edits"
	OperationImagesVariations     Operation = "images_variations"
	OperationAudioSpeech          Operation = "audio_speech"
	OperationAudioTranscriptions  Operation = "audio_transcriptions"
	OperationAudioTranslations    Operation = "audio_translations"
	OperationModerations          Operation = "moderations"
	OperationRerank               Operation = "rerank"
	OperationSystemOne            Operation = "systemone"
)

// AllOperations preserves the persisted bit positions of logical model types.
func AllOperations() []Operation {
	return []Operation{OperationChatCompletions, OperationEmbeddings, OperationImagesGenerations, OperationDecisions,
		OperationResponses, OperationResponsesCompact, OperationResponsesInputTokens, OperationCompletions,
		OperationImagesEdits, OperationImagesVariations, OperationAudioSpeech, OperationAudioTranscriptions,
		OperationAudioTranslations, OperationModerations, OperationRerank, OperationSystemOne}
}

const (
	CapabilityResponses Capability = 1 << (17 + iota)
	CapabilityResponsesCompact
	CapabilityResponsesInputTokens
	CapabilityCompletions
	CapabilityImagesEdits
	CapabilityImagesVariations
	CapabilityAudioSpeech
	CapabilityAudioTranscriptions
	CapabilityAudioTranslations
	CapabilityModerations
	CapabilityRerank
	CapabilitySystemOne
)

const inferenceCapabilities = CapabilityResponses | CapabilityResponsesCompact | CapabilityResponsesInputTokens | CapabilityCompletions |
	CapabilityImagesEdits | CapabilityImagesVariations | CapabilityAudioSpeech | CapabilityAudioTranscriptions |
	CapabilityAudioTranslations | CapabilityModerations | CapabilityRerank | CapabilitySystemOne

func OperationCapability(operation Operation) Capability {
	switch operation {
	case OperationChatCompletions:
		return CapabilityText
	case OperationEmbeddings:
		return CapabilityEmbeddings
	case OperationImagesGenerations:
		return CapabilityImagesGenerations
	case OperationDecisions:
		return CapabilityDecisions
	}
	for i, current := range AllOperations()[4:] {
		if current == operation {
			return CapabilityResponses << i
		}
	}
	return 0
}

func IsHTTPInference(operation Operation) bool {
	for _, current := range AllOperations()[4:] {
		if current == operation {
			return true
		}
	}
	return false
}
