package forward

import (
	"context"
	"errors"
	"net/http"
	"reflect"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/endpoint"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

// TargetRepository is the final ownership/invalidation check performed after
// selection and immediately before decrypting a credential.
type TargetRepository interface {
	GetForwardTarget(ctx context.Context, userID int64, fullName string, bindingID int64) (db.ForwardTarget, error)
}

// CharityTargetRepository is the charity-path counterpart: the final
// candidate-predicate revalidation (donation approved/enabled/unexpired, key
// enabled, live claim, live endpoint/key, fetched cache row) performed inside
// one SELECT immediately before dispatch. The returned projection carries the
// DONOR identity because the sealed envelope is bound to the donor's ownership
// scope — never to the consumer's.
type CharityTargetRepository interface {
	GetCharityForwardTarget(ctx context.Context, bindingID int64, fullName string, now int64) (db.CharityForwardTarget, error)
}

// Adapter is one protocol implementation admitted by the endpoint registry.
// Attempt must perform exactly one egress attempt and must not route or retry.
type Adapter = connector.OpenAIDriver

// AttemptInput is the non-sensitive input handed to a single attempt runner.
type AttemptInput struct {
	UserID                int64
	FullName              string
	BindingID             int64
	ExpectedConnectorType connectorcontract.Type
	Request               *openai.ChatRequest
	TraceID               string
	AttemptIndex          int
}

const maxForwardCiphertextBytes = 128 << 10

// AttemptRunner is the explicit boundary a later routing/retry layer wraps.
// The current service invokes it once. A runner may write response bytes only
// through the supplied writer and reports the exact commit boundary in its result.
type AttemptRunner interface {
	Run(context.Context, http.ResponseWriter, AttemptInput) connectorcontract.AttemptResult
}

// SecureRunnerConfig wires the ownership projection, Vault, authoritative
// connector registry, and protocol adapters. CharityTargets is the optional
// charity-path projection; when absent, RunCharity fails closed.
type SecureRunnerConfig struct {
	Repository                TargetRepository
	CharityTargets            CharityTargetRepository
	Secrets                   secret.ContextOpener
	Registry                  *endpoint.Registry
	Adapters                  []Adapter
	Backend                   backend.Backend
	Observer                  *connector.SafeObserver
	SafetyIdentifiers         *SafetyIdentifierFactory
	AnthropicDefaultMaxTokens connectorcontract.AnthropicDefaultMaxTokensProvider
}

// SecureRunner revalidates a selected binding, decrypts its credential for
// only the duration needed to construct the outbound request, and delegates to
// the exact registered adapter. It owns no retry policy.
type SecureRunner struct {
	repository        TargetRepository
	charityTargets    CharityTargetRepository
	secrets           secret.ContextOpener
	registry          *endpoint.Registry
	connectors        map[connectorcontract.Type]connector.Connector
	observer          *connector.SafeObserver
	safetyIdentifiers *SafetyIdentifierFactory
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
	if config.SafetyIdentifiers == nil {
		return nil, errors.New("forward: safety identifier factory is required")
	}
	adapters := make(map[connectorcontract.Type]Adapter, len(config.Adapters))
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
	connectors := make(map[connectorcontract.Type]connector.Connector)
	for _, connectorType := range config.Registry.Types() {
		dependencies := connector.Dependencies{Backend: config.Backend, AnthropicDefaultMaxTokens: config.AnthropicDefaultMaxTokens}
		if connectorType == connectorcontract.TypeOpenAICompatible {
			dependencies.OpenAI = adapters[connectorType]
		}
		instance, err := config.Registry.NewConnector(connectorType, dependencies)
		if err != nil {
			return nil, errors.New("forward: connector could not be constructed")
		}
		connectors[connectorType] = instance
	}
	if len(connectors) == 0 {
		return nil, errors.New("forward: at least one connector is required")
	}
	return &SecureRunner{
		repository:        config.Repository,
		charityTargets:    config.CharityTargets,
		secrets:           config.Secrets,
		registry:          config.Registry,
		connectors:        connectors,
		observer:          config.Observer,
		safetyIdentifiers: config.SafetyIdentifiers,
	}, nil
}

func (r *SecureRunner) Run(ctx context.Context, writer http.ResponseWriter, input AttemptInput) connectorcontract.AttemptResult {
	if r == nil || r.repository == nil || r.registry == nil || nilCodec(r.secrets) || ctx == nil || writer == nil || input.UserID <= 0 || input.FullName == "" || input.BindingID <= 0 || input.Request == nil {
		return internalAttemptFailure("forwarding attempt unavailable")
	}
	expectedConnectorType, err := r.registry.MustValidate(endpoint.ConnectorType(input.ExpectedConnectorType))
	if err != nil {
		return internalAttemptFailure("forwarding connector is not registered")
	}
	if ctx.Err() != nil {
		return connectorcontract.AttemptResult{Failure: connectorcontract.FailureCanceled, Diagnostic: "request canceled"}
	}

	target, err := r.repository.GetForwardTarget(ctx, input.UserID, input.FullName, input.BindingID)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return selectedTargetUnavailableFailure()
		}
		if errors.Is(err, db.ErrEndpointCredentialUnavailable) {
			return internalAttemptFailure("credential unavailable")
		}
		return internalAttemptFailure("forwarding target lookup failed")
	}
	return r.dispatch(ctx, writer, dispatchInput{
		ownerUserID:    input.UserID,
		preDecrypted:   target,
		expectedType:   expectedConnectorType,
		request:        input.Request,
		consumerUserID: input.UserID,
		traceID:        input.TraceID,
		attemptIndex:   input.AttemptIndex,
	})
}

// CharityAttemptInput is the non-sensitive input of one charity-path attempt.
// ConsumerUserID is used only to generate the origin-scoped safety_identifier;
// it is never used for decryption or ownership. Credential context and
// ownership checks remain entirely donor-scoped.
type CharityAttemptInput struct {
	BindingID             int64
	FullName              string
	ExpectedConnectorType connectorcontract.Type
	Now                   int64 // authoritative unix time for the expiry predicate
	ConsumerUserID        int64 // consumer identity used only for safety_identifier
	Request               *openai.ChatRequest
	TraceID               string
	AttemptIndex          int
}

// RunCharity revalidates one charity binding through the full candidate
// predicate and dispatches exactly one upstream attempt, mirroring Run's
// secret-handling discipline. It owns no retry policy and no accounting.
func (r *SecureRunner) RunCharity(ctx context.Context, writer http.ResponseWriter, input CharityAttemptInput) connectorcontract.AttemptResult {
	if r == nil || r.charityTargets == nil || r.registry == nil || nilCodec(r.secrets) || ctx == nil || writer == nil || input.BindingID <= 0 || input.Request == nil || input.Now <= 0 {
		return internalAttemptFailure("forwarding attempt unavailable")
	}
	expectedConnectorType, err := r.registry.MustValidate(endpoint.ConnectorType(input.ExpectedConnectorType))
	if err != nil {
		return internalAttemptFailure("forwarding connector is not registered")
	}
	if ctx.Err() != nil {
		return connectorcontract.AttemptResult{Failure: connectorcontract.FailureCanceled, Diagnostic: "request canceled"}
	}

	target, err := r.charityTargets.GetCharityForwardTarget(ctx, input.BindingID, input.FullName, input.Now)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return selectedTargetUnavailableFailure()
		}
		if errors.Is(err, db.ErrEndpointCredentialUnavailable) {
			return internalAttemptFailure("credential unavailable")
		}
		return internalAttemptFailure("charity target lookup failed")
	}
	return r.dispatch(ctx, writer, dispatchInput{
		ownerUserID:    target.DonorUserID,
		preDecrypted:   target.ForwardTarget,
		expectedType:   expectedConnectorType,
		request:        input.Request,
		consumerUserID: input.ConsumerUserID,
		traceID:        input.TraceID,
		attemptIndex:   input.AttemptIndex,
	})
}

// dispatchInput carries one revalidated projection plus protocol inputs into
// the shared decrypt-and-dispatch flow.
type dispatchInput struct {
	ownerUserID    int64
	consumerUserID int64
	preDecrypted   db.ForwardTarget
	expectedType   connectorcontract.Type
	request        *openai.ChatRequest
	traceID        string
	attemptIndex   int
}

func (r *SecureRunner) dispatch(ctx context.Context, writer http.ResponseWriter, in dispatchInput) connectorcontract.AttemptResult {
	target := in.preDecrypted
	if connectorcontract.Type(target.ConnectorType) != in.expectedType {
		target.DiscardEncryptedSecret()
		return selectedTargetUnavailableFailure()
	}
	connectorType := in.expectedType
	protocolConnector := r.connectors[connectorType]
	if protocolConnector == nil {
		target.DiscardEncryptedSecret()
		return internalAttemptFailure("forwarding connector is unavailable")
	}

	_, canonicalOrigin, err := egress.CanonicalEndpointTarget(target.BaseURL)
	if err != nil {
		target.DiscardEncryptedSecret()
		return internalAttemptFailure("credential unavailable")
	}
	if in.consumerUserID <= 0 || r.safetyIdentifiers == nil {
		target.DiscardEncryptedSecret()
		return internalAttemptFailure("safety identifier unavailable")
	}
	safetyIdentifier, err := r.safetyIdentifiers.Generate(in.consumerUserID, canonicalOrigin)
	if err != nil {
		target.DiscardEncryptedSecret()
		return internalAttemptFailure("safety identifier unavailable")
	}
	credentialContext, err := secret.NewEndpointKeyContext(
		in.ownerUserID, target.EndpointID, target.EndpointKeyID, canonicalOrigin,
	)
	canonicalOrigin = ""
	if err != nil {
		target.DiscardEncryptedSecret()
		return internalAttemptFailure("credential unavailable")
	}
	ciphertext := target.TakeEncryptedSecret()
	if len(ciphertext) == 0 || len(ciphertext) > maxForwardCiphertextBytes {
		ciphertext = ""
		return internalAttemptFailure("credential unavailable")
	}
	ciphertextBytes := []byte(ciphertext)
	plaintext, err := r.secrets.OpenForContext(ciphertext, credentialContext)
	ciphertext = ""
	if err != nil {
		clear(ciphertextBytes)
		return internalAttemptFailure("credential unavailable")
	}
	defer clear(plaintext)
	defer clear(ciphertextBytes)
	attemptRequest := in.request.CloneForAttempt()
	if attemptRequest == nil {
		return internalAttemptFailure("forwarding request snapshot failed")
	}
	defer attemptRequest.Clear()

	result := protocolConnector.Attempt(ctx, connector.AttemptInput{
		Target:       connectorcontract.NewTarget(connectorType, target.BaseURL, target.UpstreamModelID),
		Credential:   connectorcontract.NewShortLivedSecret(plaintext, ciphertextBytes),
		Ingress:      attemptRequest,
		Policy:       connectorcontract.AttemptPolicy{SafetyIdentifier: safetyIdentifier},
		Sink:         writer,
		Observer:     r.observer,
		TraceID:      in.traceID,
		AttemptIndex: in.attemptIndex,
	})
	// The frozen log contract requires the dispatch-time canonical base-URL
	// snapshot on every committed request. The base URL is the owner-visible
	// endpoint value, never credential material; it travels as bounded
	// metadata alongside the other attempt fields.
	result.EndpointBaseURL = target.BaseURL
	return result
}

func internalAttemptFailure(diagnostic string) connectorcontract.AttemptResult {
	return connectorcontract.AttemptResult{Failure: connectorcontract.FailureInternal, Diagnostic: diagnostic}
}

func selectedTargetUnavailableFailure() connectorcontract.AttemptResult {
	return connectorcontract.AttemptResult{Failure: connectorcontract.FailureUpstream, Diagnostic: "selected upstream target is no longer available"}
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

func nilCodec(codec secret.ContextOpener) bool {
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
