package requestkind

import contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"

var inferencePaths = map[string]contract.Operation{
	"/v1/systemone":              contract.OperationSystemOne,
	"/v1/responses":              contract.OperationResponses,
	"/v1/responses/compact":      contract.OperationResponsesCompact,
	"/v1/responses/input_tokens": contract.OperationResponsesInputTokens,
	"/v1/completions":            contract.OperationCompletions,
	"/v1/images/edits":           contract.OperationImagesEdits,
	"/v1/images/variations":      contract.OperationImagesVariations,
	"/v1/audio/speech":           contract.OperationAudioSpeech,
	"/v1/audio/transcriptions":   contract.OperationAudioTranscriptions,
	"/v1/audio/translations":     contract.OperationAudioTranslations,
	"/v1/moderations":            contract.OperationModerations,
	"/v1/rerank":                 contract.OperationRerank,
}

func extendedOperation(k Kind, prefix string) contract.Operation {
	for _, operation := range contract.AllOperations()[4:] {
		if string(k) == prefix+string(operation) {
			return operation
		}
	}
	return ""
}
