package forward

import contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"

func unsupportedModelType() error {
	return &contract.RequestRejection{Stage: "model preflight", Field: "model", Reason: "model does not support this request type"}
}
