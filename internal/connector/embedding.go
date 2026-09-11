package connector

import (
	"bytes"
	"context"
	"encoding/json"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

func (c *openAIConnector) attemptEmbedding(ctx context.Context, input AttemptInput) connectorcontract.AttemptResult {
	defer input.Credential.Clear()
	driver, ok := c.driver.(OpenAIEmbeddingDriver)
	if !ok {
		return connectorcontract.AttemptResult{Failure: connectorcontract.FailureInternal, Diagnostic: "connector operation unsupported"}
	}
	plaintext, ciphertext, ok := input.Credential.Take()
	if !ok {
		return connectorcontract.AttemptResult{Failure: connectorcontract.FailureInternal, Diagnostic: "forwarding attempt unavailable"}
	}
	defer clear(plaintext)
	defer clear(ciphertext)
	// Only identity applies to embeddings. Nonstandard caller store remains
	// visible to the existing observer as an unmodified caller parameter.
	policy := connectorcontract.AttemptPolicy{SafetyIdentifier: input.Policy.SafetyIdentifier}
	observation := Observation{Kind: ObservationAttemptStarted, Connector: c.Type(), TraceID: input.TraceID,
		AttemptIndex: input.AttemptIndex, SafetyIdentifierApplied: policy.SafetyIdentifier != ""}
	if input.Observer != nil {
		raw, present := input.Embedding.RawField("store")
		observation.CallerStorePresent = present
		observation.CallerStoreValueKnown = bytes.Equal(raw, []byte("true")) || bytes.Equal(raw, []byte("false"))
		if observation.CallerStoreValueKnown {
			observation.CallerStoreValueKnown = json.Unmarshal(raw, &observation.CallerStoreValue) == nil
		}
		clear(raw)
		input.Observer.TryObserve(observation)
	}
	target := openai.NewTarget(input.Target.BaseURL(), input.Target.UpstreamModel(), openai.NewCredential(plaintext, ciphertext))
	result := driver.AttemptEmbedding(ctx, input.Sink, target, input.Embedding, policy)
	if input.Observer != nil {
		observation.Kind, observation.Success, observation.Committed = ObservationAttemptFinished, result.Success, result.Committed
		observation.Failure, observation.Usage, observation.Diagnostic = result.Failure, result.Usage, result.Diagnostic
		input.Observer.TryObserve(observation)
	}
	return result
}
