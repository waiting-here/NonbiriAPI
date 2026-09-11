// Package requestkind defines the closed request identities shared by routing,
// accounting, logs and Debug. Unknown identities never imply a personal call.
package requestkind

import contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"

type Kind string

const (
	OpenAIChat        Kind = "openai_chat_completions"
	CharityChat       Kind = "charity_chat_completions"
	OpenAIEmbeddings  Kind = "openai_embeddings"
	CharityEmbeddings Kind = "charity_embeddings"
	Discovery         Kind = "model_discovery"
)

func (k Kind) IsSelf() bool      { return k == OpenAIChat || k == OpenAIEmbeddings }
func (k Kind) IsCharity() bool   { return k == CharityChat || k == CharityEmbeddings }
func (k Kind) IsModelCall() bool { return k.IsSelf() || k.IsCharity() }
func (k Kind) Valid() bool       { return k.IsModelCall() || k == Discovery }
func (k Kind) Operation() contract.Operation {
	switch k {
	case OpenAIChat, CharityChat:
		return contract.OperationChatCompletions
	case OpenAIEmbeddings, CharityEmbeddings:
		return contract.OperationEmbeddings
	default:
		return ""
	}
}

func ForOperation(operation contract.Operation, charity bool) Kind {
	switch operation {
	case contract.OperationChatCompletions:
		if charity {
			return CharityChat
		}
		return OpenAIChat
	case contract.OperationEmbeddings:
		if charity {
			return CharityEmbeddings
		}
		return OpenAIEmbeddings
	default:
		return ""
	}
}

func OperationForPath(path string) contract.Operation {
	switch path {
	case "/v1/chat/completions":
		return contract.OperationChatCompletions
	case "/v1/embeddings":
		return contract.OperationEmbeddings
	default:
		return ""
	}
}
