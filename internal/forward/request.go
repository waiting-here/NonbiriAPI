package forward

import (
	"io"
	"log/slog"

	"github.com/waiting-here/NonbiriAPI/internal/connector"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/rolepolicy"
	"github.com/waiting-here/NonbiriAPI/internal/transportpolicy"
)

// validatedRequest owns exactly one protocol snapshot. Metadata and capability
// checks cannot cause a chat policy to read or transform embedding input.
type validatedRequest struct {
	operation         contract.Operation
	chat              *openai.ChatRequest
	embedding         *openai.EmbeddingRequest
	image             *openai.ImageRequest
	Model             string
	Stream            bool
	roleSnapshot      *rolepolicy.Policy
	transportRule     transportpolicy.Rule
	policyRevision    int64
	policyModelID     int64
	policyDecisionNow int64
	excluded          []string
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

func imageRequest(request *openai.ImageRequest) *validatedRequest {
	if request == nil {
		return nil
	}
	return &validatedRequest{operation: contract.OperationImagesGenerations, image: request, Model: request.Model, Stream: request.Stream}
}

func decodeRequest(body io.Reader, operation contract.Operation, limit int64) (*validatedRequest, error) {
	switch operation {
	case contract.OperationChatCompletions:
		request, err := openai.DecodeChatRequest(body, limit)
		return chatRequest(request), err
	case contract.OperationEmbeddings:
		request, err := openai.DecodeEmbeddingRequest(body, limit)
		return embeddingRequest(request), err
	case contract.OperationImagesGenerations:
		request, err := openai.DecodeImageRequest(body, limit)
		return imageRequest(request), err
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
		return r.chat != nil && r.embedding == nil && r.image == nil && r.Model == r.chat.Model && r.Stream == r.chat.Stream
	case contract.OperationEmbeddings:
		return r.chat == nil && r.embedding != nil && r.image == nil && r.Model == r.embedding.Model && !r.Stream
	case contract.OperationImagesGenerations:
		return r.chat == nil && r.embedding == nil && r.image != nil && r.Model == r.image.Model && r.Stream == r.image.Stream
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
	r.image.Clear()
	*r = validatedRequest{}
}

func (r *validatedRequest) CloneForAttempt() *validatedRequest {
	if !r.valid() {
		return nil
	}
	var result *validatedRequest
	if r.chat != nil {
		result = chatRequest(r.chat.CloneForAttempt())
	} else if r.image != nil {
		result = imageRequest(r.image.CloneForAttempt())
	} else {
		result = embeddingRequest(r.embedding.CloneForAttempt())
	}
	result.policyModelID, result.policyDecisionNow = r.policyModelID, r.policyDecisionNow
	if r.roleSnapshot != nil {
		policy := r.roleSnapshot.Clone()
		result.roleSnapshot = &policy
	}
	result.policyRevision = r.policyRevision
	result.transportRule = r.transportRule
	result.excluded = append([]string(nil), r.excluded...)
	return result
}

func (r *validatedRequest) excludeFields(fields []string) error {
	if !r.valid() {
		return openai.ErrInvalidRequest
	}
	var err error
	if r.chat != nil {
		err = r.chat.ExcludeFields(fields)
	} else if r.image != nil {
		err = r.image.ExcludeFields(fields)
	} else {
		err = r.embedding.ExcludeFields(fields)
	}
	if err == nil {
		r.excluded = append([]string(nil), fields...)
	}
	return err
}

func (r *validatedRequest) supports(registry *connector.Registry, kind contract.Type) bool {
	return r.valid() && (r.chat == nil || r.chat.SupportsRolePassthrough(string(kind))) && registry.SupportsOperationRequest(kind, r.operation, r.chat, r.embedding, r.image)
}

func (r *validatedRequest) bodyLimit() int64 {
	if r.chat != nil {
		return r.chat.RequestBodyLimit()
	}
	if r.image != nil {
		return r.image.RequestBodyLimit()
	}
	return r.embedding.RequestBodyLimit()
}

func (r *validatedRequest) checkTarget(registry *connector.Registry, target contract.Target, policy contract.AttemptPolicy) error {
	if !r.valid() {
		return openai.ErrInvalidRequest
	}
	if r.chat != nil && !r.chat.SupportsRolePassthrough(string(target.Type())) {
		return &contract.RequestRejection{Stage: "role preflight", Field: "messages", Reason: "role cannot be passed through to this connector"}
	}
	return registry.CheckTargetRequest(target, r.operation, r.chat, r.embedding, policy, r.image)
}
