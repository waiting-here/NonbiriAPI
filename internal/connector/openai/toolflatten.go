package openai

import (
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/connector/tooltext"
)

var jsonString = tooltext.JsonString
var flattenCompletion = tooltext.FlattenCompletion
var flattenToolCalls = tooltext.FlattenToolCalls
var flattenToolCallsWithBudget = tooltext.FlattenToolCallsWithBudget
var flattenedContent = tooltext.FlattenedContent
var escapeToolAttr = tooltext.EscapeToolAttr
var unescapeToolAttr = tooltext.UnescapeToolAttr
var validToolName = tooltext.ValidToolName
var validToolID = tooltext.ValidToolID
var toolNames = tooltext.ToolNames
var reverseMessages = tooltext.ReverseMessages
var markStreamContentEmitted = tooltext.MarkStreamContentEmitted
var streamToolCount = tooltext.StreamToolCount
var streamArgumentBytes = tooltext.StreamArgumentBytes
var clearStreamStates = tooltext.ClearStreamStates
var accumulateStreamChunk = tooltext.AccumulateStreamChunk
var streamCompletionBody = tooltext.StreamCompletionBody
var streamCompletionBodyAfter = tooltext.StreamCompletionBodyAfter
var completionToStreamFrames = tooltext.CompletionToStreamFrames
var completionToStreamChunk = tooltext.CompletionToStreamChunk
var parseFlattenedContent = tooltext.ParseFlattenedContent
var parseFlattenedContentWithBudget = tooltext.ParseFlattenedContentWithBudget
var parseToolOpen = tooltext.ParseToolOpen
var parseToolResultOpen = tooltext.ParseToolResultOpen

type flattenedCall = tooltext.FlattenedCall
type reverseCall = tooltext.ReverseCall
type streamToolDelta = tooltext.StreamToolDelta
type streamChoiceState = tooltext.StreamChoiceState

const maxFlattenCalls = tooltext.MaxFlattenCalls
const maxFlattenArguments = tooltext.MaxFlattenArguments
const maxFlattenAggregate = tooltext.MaxFlattenAggregate

var errToolFlatten = tooltext.ErrToolFlatten
var errFlattenStreamRejected = tooltext.ErrFlattenStreamRejected

// ReverseFlatten returns a single immutable transformed request. Any
// malformed/partial/non-matching block causes the original request to be
// cloned unchanged, allowing normal OpenAI/Anthropic validation to decide its
// fate without inventing tool calls or results.
func (r *ChatRequest) ReverseFlatten() (*ChatRequest, error) {
	clone := r.CloneForAttempt()
	if clone == nil {
		return nil, ErrInvalidRequest
	}
	toolsRaw, ok := r.RawField("tools")
	if !ok {
		clear(toolsRaw)
		return clone, nil
	}
	defer clear(toolsRaw)
	allowed := toolNames(toolsRaw)
	if allowed == nil {
		return clone, nil
	}
	messagesIndex := -1
	for i, field := range clone.fields {
		if field.name == "messages" {
			messagesIndex = i
			break
		}
	}
	if messagesIndex < 0 {
		return clone, nil
	}
	var messages []json.RawMessage
	if json.Unmarshal(clone.fields[messagesIndex].value, &messages) != nil {
		return clone, nil
	}
	transformed, changed := reverseMessages(messages, allowed)
	if !changed {
		return clone, nil
	}
	clone.fields[messagesIndex].value, _ = json.Marshal(transformed)
	clone.requirements = projectCapabilities(clone.fields, clone.Stream)
	return clone, nil
}
