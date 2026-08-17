package forward

import (
	"context"
	"errors"
	"net/http"
	"reflect"

	"nonbiriapi/internal/connector/openai"
	"nonbiriapi/internal/db"
	"nonbiriapi/internal/endpoint"
	"nonbiriapi/internal/secret"
)

// TargetRepository is the final ownership/invalidation check performed after
// selection and immediately before decrypting a credential.
type TargetRepository interface {
	GetForwardTarget(ctx context.Context, userID int64, fullName string, bindingID int64) (db.ForwardTarget, error)
}

// Adapter is one protocol implementation admitted by the endpoint registry.
// Attempt must perform exactly one egress attempt and must not route or retry.
type Adapter interface {
	ConnectorType() endpoint.ConnectorType
	Attempt(context.Context, http.ResponseWriter, openai.Target, *openai.ChatRequest, string) openai.AttemptResult
}

// AttemptInput is the non-sensitive input handed to a single attempt runner.
type AttemptInput struct {
	UserID           int64
	FullName         string
	BindingID        int64
	Request          *openai.ChatRequest
	SafetyIdentifier string
}

const maxForwardCiphertextBytes = 128 << 10

// AttemptRunner is the explicit boundary a later routing/retry layer wraps.
// The current service invokes it once. A runner may write response bytes only
// through the supplied writer and reports the exact commit boundary in its result.
type AttemptRunner interface {
	Run(context.Context, http.ResponseWriter, AttemptInput) openai.AttemptResult
}

// SecureRunnerConfig wires the ownership projection, Vault, authoritative
// connector registry, and protocol adapters.
type SecureRunnerConfig struct {
	Repository TargetRepository
	Secrets    secret.Codec
	Registry   *endpoint.Registry
	Adapters   []Adapter
}

// SecureRunner revalidates a selected binding, decrypts its credential for
// only the duration needed to construct the outbound request, and delegates to
// the exact registered adapter. It owns no retry policy.
type SecureRunner struct {
	repository TargetRepository
	secrets    secret.Codec
	registry   *endpoint.Registry
	adapters   map[endpoint.ConnectorType]Adapter
}

func NewSecureRunner(config SecureRunnerConfig) (*SecureRunner, error) {
	if config.Repository == nil {
		return nil, errors.New("forward: target repository is required")
	}
	if nilCodec(config.Secrets) {
		return nil, errors.New("forward: secret codec is required")
	}
	if config.Registry == nil {
		return nil, errors.New("forward: connector registry is required")
	}
	adapters := make(map[endpoint.ConnectorType]Adapter, len(config.Adapters))
	for _, adapter := range config.Adapters {
		if nilAdapter(adapter) {
			return nil, errors.New("forward: connector adapter is required")
		}
		connectorType, err := config.Registry.MustValidate(adapter.ConnectorType())
		if err != nil {
			return nil, errors.New("forward: adapter connector is not registered")
		}
		if _, duplicate := adapters[connectorType]; duplicate {
			return nil, errors.New("forward: duplicate connector adapter")
		}
		adapters[connectorType] = adapter
	}
	if len(adapters) == 0 {
		return nil, errors.New("forward: at least one connector adapter is required")
	}
	return &SecureRunner{
		repository: config.Repository,
		secrets:    config.Secrets,
		registry:   config.Registry,
		adapters:   adapters,
	}, nil
}

func (r *SecureRunner) Run(ctx context.Context, writer http.ResponseWriter, input AttemptInput) openai.AttemptResult {
	if r == nil || r.repository == nil || r.registry == nil || nilCodec(r.secrets) || ctx == nil || writer == nil || input.UserID <= 0 || input.FullName == "" || input.BindingID <= 0 || input.Request == nil {
		return internalAttemptFailure("forwarding attempt unavailable")
	}
	if ctx.Err() != nil {
		return openai.AttemptResult{Failure: openai.FailureCanceled, Diagnostic: "request canceled"}
	}

	target, err := r.repository.GetForwardTarget(ctx, input.UserID, input.FullName, input.BindingID)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return openai.AttemptResult{Failure: openai.FailureUpstream, Diagnostic: "selected upstream target is no longer available"}
		}
		return internalAttemptFailure("forwarding target lookup failed")
	}
	connectorType, err := r.registry.MustValidate(endpoint.ConnectorType(target.ConnectorType))
	if err != nil {
		target.DiscardEncryptedSecret()
		return internalAttemptFailure("forwarding connector is not registered")
	}
	adapter := r.adapters[connectorType]
	if adapter == nil {
		target.DiscardEncryptedSecret()
		return internalAttemptFailure("forwarding connector is unavailable")
	}

	ciphertext := target.TakeEncryptedSecret()
	if len(ciphertext) == 0 || len(ciphertext) > maxForwardCiphertextBytes {
		ciphertext = ""
		return internalAttemptFailure("forwarding credential is unavailable")
	}
	ciphertextBytes := []byte(ciphertext)
	plaintext, err := r.secrets.Open(ciphertext)
	ciphertext = ""
	if err != nil {
		clear(ciphertextBytes)
		return internalAttemptFailure("forwarding credential is unavailable")
	}
	defer clear(plaintext)
	defer clear(ciphertextBytes)

	result := adapter.Attempt(ctx, writer, openai.NewTarget(
		target.BaseURL,
		target.UpstreamModelID,
		openai.NewCredential(plaintext, ciphertextBytes),
	), input.Request, input.SafetyIdentifier)
	// Adapter clears both slices before dialing; these clears are a backstop
	// for a rejected adapter call and make the ownership transfer explicit.
	clear(plaintext)
	clear(ciphertextBytes)
	return result
}

func internalAttemptFailure(diagnostic string) openai.AttemptResult {
	return openai.AttemptResult{Failure: openai.FailureInternal, Diagnostic: diagnostic}
}

func nilAdapter(adapter Adapter) bool {
	if adapter == nil {
		return true
	}
	value := reflect.ValueOf(adapter)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func nilCodec(codec secret.Codec) bool {
	if codec == nil {
		return true
	}
	value := reflect.ValueOf(codec)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
