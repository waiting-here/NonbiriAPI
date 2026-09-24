package forward

import (
	"io"
	"log/slog"

	"github.com/waiting-here/NonbiriAPI/internal/connector"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

// validatedRequest owns exactly one protocol snapshot. Metadata and capability
// checks cannot cause a chat policy to read or transform embedding input.
type validatedRequest struct {
	operation         contract.Operation
	chat              *openai.ChatRequest
	embedding         *openai.EmbeddingRequest
	Model             string
	Stream            bool
	policyModelID     int64
	policyDecisionNow int64
}

func (*validatedRequest) String() string       { return "[redacted forward request]" }
func (*validatedRequest) GoString() string     { return "[redacted forward request]" }
func (*validatedRequest) LogValue() slog.Value { return slog.StringValue("[redacted forward request]") }

func chatRequest(request *openai.ChatRequest) *validatedRequest {
	if request == nil {
		return nil
	}
	return &validatedRequest{operation: contract.OperationChatCompletions, chat: request, Model: request.Model, Stream: request.Stream}
}

func embeddingRequest(request *openai.EmbeddingRequest) *validatedRequest {
	if request == nil {
		return nil
	}
	return &validatedRequest{operation: contract.OperationEmbeddings, embedding: request, Model: request.Model}
}

func decodeRequest(body io.Reader, operation contract.Operation) (*validatedRequest, error) {
	switch operation {
	case contract.OperationChatCompletions:
		request, err := openai.DecodeChatRequest(body, openai.MaxRequestBodyBytes)
		return chatRequest(request), err
	case contract.OperationEmbeddings:
		request, err := openai.DecodeEmbeddingRequest(body, openai.MaxRequestBodyBytes)
		return embeddingRequest(request), err
	default:
		return nil, openai.ErrInvalidRequest
	}
}

func (r *validatedRequest) valid() bool {
	if r == nil {
		return false
	}
	switch r.operation {
	case contract.OperationChatCompletions:
		return r.chat != nil && r.embedding == nil && r.Model == r.chat.Model && r.Stream == r.chat.Stream
	case contract.OperationEmbeddings:
		return r.chat == nil && r.embedding != nil && r.Model == r.embedding.Model && !r.Stream
	default:
		return false
	}
}

func (r *validatedRequest) Clear() {
	if r == nil {
		return
	}
	r.chat.Clear()
	r.embedding.Clear()
	*r = validatedRequest{}
}

func (r *validatedRequest) CloneForAttempt() *validatedRequest {
	if !r.valid() {
		return nil
	}
	var result *validatedRequest
	if r.chat != nil {
		result = chatRequest(r.chat.CloneForAttempt())
	} else {
		result = embeddingRequest(r.embedding.CloneForAttempt())
	}
	result.policyModelID, result.policyDecisionNow = r.policyModelID, r.policyDecisionNow
	return result
}

func (r *validatedRequest) excludeFields(fields []string) error {
	if !r.valid() {
		return openai.ErrInvalidRequest
	}
	if r.chat != nil {
		return r.chat.ExcludeFields(fields)
	}
	return r.embedding.ExcludeFields(fields)
}

func (r *validatedRequest) supports(registry *connector.Registry, kind contract.Type) bool {
	return r.valid() && registry.SupportsOperationRequest(kind, r.operation, r.chat, r.embedding)
}
