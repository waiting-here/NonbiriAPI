package charityrouting

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connector"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/credits"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
	"github.com/waiting-here/NonbiriAPI/internal/forward"
)

// DefaultForwardTimeout mirrors the personal forwarding exit: one aggregate
// wall-clock budget shared by route resolution, every candidate attempt, and
// any retry path. It bounds how long a reservation can stay in flight, which
// in turn keeps the recovery sweep's staleness cutoff safely above any live
// request.
const DefaultForwardTimeout = forward.DefaultForwardTimeout

// stallRecoveryCutoff is the minimum age a reserved/dispatched row must reach
// before the PERIODIC recovery sweep may converge it. Startup recovery uses no
// cutoff (no request can be in flight in a fresh process); the periodic sweep
// uses this constant so a live request — bounded by DefaultForwardTimeout plus
// scheduling slack — can never be converged underneath its caller.
const stallRecoveryCutoff = 6 * time.Hour

// Sentinels mapped by the handler to the stable error envelope. They are
// defined in the forward package so the handler can match them without an
// import cycle; None carries request or secret material.
var (
	// ErrModelNotFound reports that the [公益] model does not exist or is
	// disabled (the two are indistinguishable at the routing exit so
	// availability state is not disclosed).
	ErrModelNotFound = forward.ErrCharityModelNotFound
	// ErrUnboundModel reports that the model has no usable donated candidate
	// right now (donations may return later).
	ErrUnboundModel = forward.ErrCharityUnboundModel
	// ErrKeysExhausted reports that every donated key refused admission
	// (per-key concurrency / RPM / usage cap). It maps to 429 rate_limited.
	ErrKeysExhausted = forward.ErrCharityKeysExhausted
	// ErrCharityDisabled reports the site-wide charity switch is off.
	ErrCharityDisabled = forward.ErrCharityDisabled
	// ErrCharitySuspended reports the caller's charity eligibility is
	// currently suspended.
	ErrCharitySuspended = forward.ErrCharitySuspended
	// ErrInternal is the fail-closed envelope for unexpected failures.
	ErrInternal = errors.New("charityrouting: internal failure")
)

// Runner is the single-attempt charity dispatch boundary implemented by
// forward.SecureRunner. It performs exactly one upstream attempt through the
// shared egress stack and owns no retry or accounting logic.
type Runner interface {
	RunCharity(ctx context.Context, writer http.ResponseWriter, input forward.CharityAttemptInput) connectorcontract.AttemptResult
}

// Repository is the persistence surface of the routing rail. It is satisfied
// by *db.Store; tests may inject fakes for every method.
type Repository interface {
	ResolveCharityRoute(ctx context.Context, fullName string, now int64, limit int) (db.CharityRoute, error)
	CreateCharityReservation(ctx context.Context, in db.ReserveCharityInput) (db.CharityReservation, db.CharityPricingSnapshot, error)
	SwapCharityReservationKey(ctx context.Context, reservationID int64, newKey db.CharityCandidate, newKeyReserve int64, now int64) error
	DispatchCharityReservation(ctx context.Context, reservationID, now int64) (bool, error)
	UndispatchCharityReservation(ctx context.Context, reservationID, now int64) (bool, error)
	CommitCharityReservation(ctx context.Context, reservationID int64, plan db.CommitPlan, now int64) (db.CharityReservation, error)
	ReleaseCharityReservation(ctx context.Context, reservationID, now int64) (bool, error)
	GetCharityReservationByAttempt(ctx context.Context, attemptID string) (db.CharityReservation, error)
	RecordCharityOutcome(ctx context.Context, modelID int64, success bool, now int64) error
	RecordRequest(ctx context.Context, input db.RequestLogInput) error
}

// ServiceConfig wires the repository and runner. The shared safety-identifier
// factory is owned by SecureRunner. Now defaults to time.Now; ForwardTimeout defaults to
// DefaultForwardTimeout.
type ServiceConfig struct {
	Store          Repository
	Runner         Runner
	Logger         *slog.Logger
	Now            func() time.Time
	ForwardTimeout time.Duration
	// PreflightHook runs after request decoding but before route reservation or
	// any upstream dispatch. It is optional so the charity rail remains usable
	// in focused tests and in deployments without the anti-abuse policy.
	PreflightHook func(context.Context, int64, *openai.ChatRequest) error
	Registry      *connector.Registry
}

// Service orchestrates one logical charity call end-to-end. Exactly one user
// reserve exists per invocation; retries move only the donated-key reserve.
type Service struct {
	store     Repository
	runner    Runner
	logger    *slog.Logger
	now       func() time.Time
	timeout   time.Duration
	preflight func(context.Context, int64, *openai.ChatRequest) error
	limits    *keyLimiter
	registry  *connector.Registry

	mu      sync.Mutex
	nextCtx uint64
	active  map[int64]map[uint64]context.CancelFunc
}

func NewService(config ServiceConfig) (*Service, error) {
	if config.Store == nil {
		return nil, errors.New("charityrouting: store is required")
	}
	if config.Runner == nil {
		return nil, errors.New("charityrouting: runner is required")
	}
	if config.ForwardTimeout < 0 {
		return nil, errors.New("charityrouting: timeout must not be negative")
	}
	if config.ForwardTimeout == 0 {
		config.ForwardTimeout = DefaultForwardTimeout
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.Registry == nil {
		config.Registry = connector.NewDefaultRegistry()
	}
	return &Service{
		store:     config.Store,
		runner:    config.Runner,
		logger:    config.Logger,
		now:       config.Now,
		timeout:   config.ForwardTimeout,
		preflight: config.PreflightHook,
		limits:    newKeyLimiter(),
		registry:  config.Registry,
		active:    make(map[int64]map[uint64]context.CancelFunc),
	}, nil
}

// registerContext tracks one in-flight call per consumer so account deletion
// can cancel it BEFORE the deletion transaction converges the reservations
// (frozen §5.4: cancel first, converge second).
func (s *Service) registerContext(userID int64, cancel context.CancelFunc) (func(), uint64) {
	token := s.nextToken()
	s.mu.Lock()
	if s.active[userID] == nil {
		s.active[userID] = make(map[uint64]context.CancelFunc)
	}
	s.active[userID][token] = cancel
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		if set := s.active[userID]; set != nil {
			delete(set, token)
			if len(set) == 0 {
				delete(s.active, userID)
			}
		}
		s.mu.Unlock()
	}, token
}

func (s *Service) nextToken() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextCtx++
	return s.nextCtx
}

// CancelUserContexts cancels every in-flight charity call of userID. The
// lifecycle delete path calls this before opening its transaction; the calls
// observe cancellation and settle/release their own reservations, and the
// deletion transaction converges whatever remains.
func (s *Service) CancelUserContexts(userID int64) {
	if userID <= 0 {
		return
	}
	s.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.active[userID]))
	for _, cancel := range s.active[userID] {
		cancels = append(cancels, cancel)
	}
	delete(s.active, userID)
	s.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

// ForgetDonationKeys closes the given donation keys in the per-key admission
// limiter so no further charity call can take a slot against them, while
// preserving the slot accounting of any in-flight call (release still
// decrements and reclaims the entry when it goes idle). It is the lifecycle
// hook for donation disable / delete / donor account deletion; the
// time-aware sweep reclaims a closed-but-not-yet-idle entry once its last slot
// releases and its RPM window expires. Forgetting unknown keys is a no-op.
func (s *Service) ForgetDonationKeys(keyIDs ...int64) {
	if s == nil {
		return
	}
	s.limits.ForgetDonationKeys(keyIDs...)
}

// RestoreDonationKeys re-opens successfully re-enabled keys without
// discarding their existing admission state.
func (s *Service) RestoreDonationKeys(keyIDs ...int64) {
	if s == nil {
		return
	}
	s.limits.RestoreDonationKeys(keyIDs...)
}

// Close cancels every tracked context (shutdown hygiene).
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	all := make(map[int64][]context.CancelFunc, len(s.active))
	for uid, set := range s.active {
		for _, cancel := range set {
			all[uid] = append(all[uid], cancel)
		}
	}
	s.active = make(map[int64]map[uint64]context.CancelFunc)
	s.mu.Unlock()
	for _, cancels := range all {
		for _, cancel := range cancels {
			cancel()
		}
	}
	return nil
}

// Preflight is the narrow policy hook invoked by the charity forward rail
// after decoding and route resolution, but before Forward creates a reservation.
func (s *Service) Preflight(ctx context.Context, userID int64, request *openai.ChatRequest) error {
	if s == nil || ctx == nil || userID <= 0 || request == nil {
		return ErrInternal
	}
	if s.preflight == nil {
		return nil
	}
	return s.preflight(ctx, userID, request)
}

// ListCallerModels returns the [公益]-namespace /v1/models projection. The
// authoritative charity_enabled switch is read inside the store projection, so
// a disabled site returns an empty list (never an error) and the shared
// /v1/models response shape stays stable.
func (s *Service) ListCallerModels(ctx context.Context) ([]db.CallerModel, error) {
	if s == nil || ctx == nil {
		return nil, ErrInternal
	}
	list, ok := s.store.(charityModelLister)
	if !ok {
		return nil, ErrInternal
	}
	return list.ListCallerCharityModels(ctx, s.now().Unix(), db.MaxCharityRouteCandidates)
}

// charityModelLister is the optional listing surface of the store.
type charityModelLister interface {
	ListCallerCharityModels(ctx context.Context, now int64, limit int) ([]db.CallerModel, error)
}

// Forward orchestrates one logical charity call end-to-end over the frozen
// reservation state machine (implementation contract §5). The consumer is
// debited exactly once per invocation; retries across donated keys swap ONLY
// the key reserve atomically. The per-key admission (concurrency / RPM /
// usage cap) gates every candidate AFTER selection and BEFORE dispatch; a
// refused key is retried against the next candidate and never feeds any ban
// statistic. The first legal response-body byte is the reserved→dispatched
// linearization point; only a protocol-terminating success writes the ring
// buffer. The caller retains the runner's committed result; a pre-dispatch
// control-flow failure is surfaced as a sentinel error.
func (s *Service) Forward(ctx context.Context, writer http.ResponseWriter, userID int64, request *openai.ChatRequest) (connectorcontract.AttemptResult, error) {
	if s == nil || ctx == nil || writer == nil || userID <= 0 || request == nil {
		return connectorcontract.AttemptResult{}, ErrInternal
	}
	// Namespace isolation: only [公益]-prefixed models are addressable here.
	// A personal model name can never enter this rail (the handler dispatches
	// by prefix), and this guard fails closed if it ever does.
	if !strings.HasPrefix(request.Model, db.CharityPrefix) {
		return connectorcontract.AttemptResult{}, ErrModelNotFound
	}

	parentCtx := ctx
	callCtx, cancel := context.WithTimeout(parentCtx, s.timeout)
	defer cancel()
	releaseCtx, _ := s.registerContext(userID, cancel)
	defer releaseCtx()
	// Accounting (reservation create/swap/dispatch CAS/commit/release/log)
	// runs on a fresh context so it completes even after the client
	// disconnects: a dispatched reservation must still settle, and a canceled
	// pre-dispatch must still release. Only the upstream dispatch and route
	// resolution observe the caller's deadline/cancellation.
	acctCtx := context.Background()

	nowUnix := s.now().Unix()
	route, err := s.store.ResolveCharityRoute(callCtx, request.Model, nowUnix, db.MaxCharityRouteCandidates)
	if err != nil {
		if callCtx.Err() != nil {
			return endedContextResult(parentCtx, callCtx), nil
		}
		switch {
		case errors.Is(err, db.ErrNotFound):
			return connectorcontract.AttemptResult{}, ErrModelNotFound
		default:
			return connectorcontract.AttemptResult{}, ErrInternal
		}
	}
	if len(route.Candidates) == 0 {
		return connectorcontract.AttemptResult{}, ErrUnboundModel
	}
	capable, allOpenAI, err := filterCharityCandidates(s.registry, request, route.Candidates)
	if err != nil {
		return connectorcontract.AttemptResult{}, ErrInternal
	}
	if len(capable) == 0 {
		if allOpenAI {
			return connectorcontract.AttemptResult{}, openai.ErrInvalidRequest
		}
		return connectorcontract.AttemptResult{}, forward.ErrUnsupportedCapabilities
	}
	route.Candidates = capable
	// The anti-abuse hook runs only after the [公益] model and at least one
	// candidate have resolved, but before any user/key reservation or upstream
	// dispatch. Unknown models and empty candidate chains therefore never
	// create a charity violation.
	if s.preflight != nil {
		if err := s.preflight(callCtx, userID, request); err != nil {
			return connectorcontract.AttemptResult{}, err
		}
	}

	attemptID, err := newAttemptID()
	if err != nil {
		return connectorcontract.AttemptResult{}, ErrInternal
	}

	var (
		reservationID int64
		snapshot      db.CharityPricingSnapshot
		userReserved  int64
		keyReserved   int64
		lastResult    connectorcontract.AttemptResult
		admittedAny   bool
	)
	for i := range route.Candidates {
		candidate := route.Candidates[i]
		if callCtx.Err() != nil {
			// Caller canceled mid-loop. A reservation not yet dispatched is
			// released (refund); a dispatched one is left for settlement by
			// the connector's sink-failure path, which observes the canceled
			// context and reports a non-success result we then settle.
			break
		}

		// Per-key admission (concurrency + RPM). These limits protect shared
		// donated credentials only; they never feed any ban statistic.
		if !s.limits.tryAdmit(candidate.DonationKeyID, candidate.MaxConcurrency, candidate.RPMLimit, s.now()) {
			lastResult = admissionExhaustedResult()
			continue
		}
		// Cheap in-memory usage-cap headroom probe; the reservation
		// transaction re-checks authoritatively. A false probe only skips a
		// doomed candidate early.
		probeReserve := provisionalKeyReserve(route.Model)
		if !s.limits.capHeadroom(candidate.CreditsUsed, candidate.CreditsReserved, candidate.CreditsUsageCap, probeReserve) {
			s.limits.release(candidate.DonationKeyID, s.now())
			lastResult = admissionExhaustedResult()
			continue
		}

		// Acquire the DB reservation: create on the first admitted candidate,
		// swap the key atomically on every retry. The user is debited exactly
		// once (create path); swaps touch only the key reserve.
		if reservationID == 0 {
			res, snap, cerr := s.store.CreateCharityReservation(acctCtx, db.ReserveCharityInput{
				UserID:        userID,
				FullName:      request.Model,
				BindingID:     candidate.BindingID,
				DonationKeyID: candidate.DonationKeyID,
				AttemptID:     attemptID,
				BaseURL:       candidate.BaseURL,
				Now:           nowUnix,
			})
			if cerr != nil {
				s.limits.release(candidate.DonationKeyID, s.now())
				switch {
				case errors.Is(cerr, db.ErrInsufficientCredits):
					return connectorcontract.AttemptResult{}, cerr
				case errors.Is(cerr, db.ErrCharityDisabled):
					return connectorcontract.AttemptResult{}, ErrCharityDisabled
				case errors.Is(cerr, db.ErrCharitySuspended):
					return connectorcontract.AttemptResult{}, ErrCharitySuspended
				case errors.Is(cerr, db.ErrCharityTokenReserveUnconfigured):
					return connectorcontract.AttemptResult{}, ErrUnboundModel
				case errors.Is(cerr, db.ErrDonationKeyCapReached), errors.Is(cerr, db.ErrNotFound):
					lastResult = admissionExhaustedResult()
					continue
				default:
					return connectorcontract.AttemptResult{}, ErrInternal
				}
			}
			reservationID = res.ID
			snapshot = snap
			userReserved = res.UserReserved
			keyReserved = res.KeyReserved
		} else {
			newKeyReserve := snapshotKeyReserve(snapshot)
			serr := s.store.SwapCharityReservationKey(acctCtx, reservationID, candidate, newKeyReserve, nowUnix)
			if serr != nil {
				s.limits.release(candidate.DonationKeyID, s.now())
				switch {
				case errors.Is(serr, db.ErrDonationKeyCapReached), errors.Is(serr, db.ErrNotFound):
					lastResult = admissionExhaustedResult()
					continue
				default:
					return connectorcontract.AttemptResult{}, ErrInternal
				}
			}
			keyReserved = newKeyReserve
		}
		admittedAny = true

		// Dispatch exactly one upstream attempt through the shared egress
		// boundary. The dispatchWriter performs the reserved→dispatched CAS
		// synchronously inside the FIRST body byte, before delegating, so a
		// crash after this point always finds a dispatched row that recovery
		// converges conservatively — never a released one after bytes flowed.
		resID := reservationID
		dw := newDispatchWriter(writer,
			func() bool {
				applied, derr := s.store.DispatchCharityReservation(acctCtx, resID, s.now().Unix())
				return derr == nil && applied
			},
			func() {
				if _, derr := s.store.UndispatchCharityReservation(acctCtx, resID, s.now().Unix()); derr != nil &&
					!errors.Is(derr, db.ErrNotFound) && callCtx.Err() == nil {
					s.logger.Error("charity undispatch failed", "reservation", resID, "error", derr)
				}
			})
		started := s.now()
		result := s.runner.RunCharity(callCtx, dw, forward.CharityAttemptInput{
			BindingID:             candidate.BindingID,
			FullName:              route.Model.FullName,
			ExpectedConnectorType: connectorcontract.Type(candidate.ConnectorType),
			Now:                   nowUnix,
			ConsumerUserID:        userID,
			Request:               request,
			TraceID:               attemptID,
			AttemptIndex:          i,
		})
		if callCtx.Err() != nil && !result.Committed {
			result = endedContextResult(parentCtx, callCtx)
		}
		result.Diagnostic = boundDiag(result.Diagnostic)
		finished := s.now()
		if finished.Before(started) {
			finished = started
		}

		if result.Committed {
			// Bytes crossed the dispatch boundary: settle under the frozen
			// formula. The commit transaction is idempotent (a repeated
			// callback or recovery is a no-op returning the stored result).
			plan, perr := computeCommitPlan(snapshot, userReserved, keyReserved, result)
			if perr != nil {
				// A malformed usage already degrades to unknown; any other
				// failure is a checked-arithmetic overflow: settle under
				// unknown semantics so the consumer pays the discounted
				// reserve and the key consumes the undiscounted reserve.
				plan = unknownCommitPlan(userReserved, keyReserved)
			}
			if _, cerr := s.store.CommitCharityReservation(acctCtx, reservationID, plan, finished.Unix()); cerr != nil &&
				!errors.Is(cerr, db.ErrNotFound) && callCtx.Err() == nil {
				s.logger.Error("charity commit failed", "reservation", reservationID, "error", cerr)
			}
			// The ring buffer records ONLY protocol-terminating success.
			if rerr := s.store.RecordCharityOutcome(acctCtx, route.Model.ID, result.Success, finished.Unix()); rerr != nil &&
				!errors.Is(rerr, db.ErrNotFound) && callCtx.Err() == nil {
				s.logger.Error("charity outcome record failed", "model", route.Model.ID, "error", rerr)
			}
			s.recordLog(acctCtx, userID, request.Model, attemptID, reservationID, candidate, result, plan, started, finished)
			s.limits.release(candidate.DonationKeyID, s.now())
			return result, nil
		}

		// Pre-dispatch failure: release this key's admission slot and retry
		// the next donated key. The user reserve stays put; the next
		// iteration swaps the key reserve atomically. A poisoned dispatch
		// writer (the CAS lost to a concurrent terminal move) is also a
		// pre-dispatch failure: no byte reached the client.
		s.limits.release(candidate.DonationKeyID, s.now())
		lastResult = result
	}

	// Every candidate was tried. A reservation that never dispatched is
	// released (refund the user, release the key reserve); the frozen release
	// is idempotent so a concurrent recovery release is a no-op.
	if reservationID != 0 {
		if _, rerr := s.store.ReleaseCharityReservation(acctCtx, reservationID, s.now().Unix()); rerr != nil &&
			!errors.Is(rerr, db.ErrNotFound) && !errors.Is(rerr, credits.ErrIllegalTransition) && callCtx.Err() == nil {
			s.logger.Error("charity release failed", "reservation", reservationID, "error", rerr)
		}
	}
	if callCtx.Err() != nil {
		return endedContextResult(parentCtx, callCtx), nil
	}
	if admittedAny {
		// At least one candidate was admitted and dispatched-against but
		// every attempt failed before any byte (upstream pre-dispatch
		// failure, sink failure, or cancellation). Surface the last result.
		return lastResult, nil
	}
	// No candidate was ever admitted: every donated key refused on its
	// per-key limits. The frozen exit is 429 rate_limited.
	return connectorcontract.AttemptResult{}, ErrKeysExhausted
}

func filterCharityCandidates(registry *connector.Registry, request *openai.ChatRequest, candidates []db.CharityCandidate) ([]db.CharityCandidate, bool, error) {
	if registry == nil || request == nil {
		return nil, false, ErrInternal
	}
	capable := make([]db.CharityCandidate, 0, len(candidates))
	allOpenAI := true
	capabilityByType := make(map[connectorcontract.Type]bool)
	for _, candidate := range candidates {
		connectorType, err := registry.MustValidate(connectorcontract.Type(candidate.ConnectorType))
		if err != nil {
			return nil, false, err
		}
		allOpenAI = allOpenAI && connectorType == connectorcontract.TypeOpenAICompatible
		supported, evaluated := capabilityByType[connectorType]
		if !evaluated {
			supported = registry.SupportsRequest(connectorType, request)
			capabilityByType[connectorType] = supported
		}
		if supported {
			capable = append(capable, candidate)
		}
	}
	return capable, allOpenAI, nil
}

// recordLog persists the charity request-log row with the reservation
// correlation and the three charge columns. The consumer's own log projection
// suppresses donor resources (base URL / key) for charity rows.
func (s *Service) recordLog(ctx context.Context, userID int64, model, attemptID string, reservationID int64, candidate db.CharityCandidate, result connectorcontract.AttemptResult, plan db.CommitPlan, started, finished time.Time) {
	stableCode, clientStatus := classifyResult(result)
	if result.ClientStatus == 0 {
		result.ClientStatus = clientStatus
	}
	input := db.RequestLogInput{
		AttemptID:             attemptID,
		UserID:                userID,
		Model:                 model,
		EndpointKeyID:         candidate.EndpointKeyID,
		UpstreamModelID:       candidate.UpstreamModelID,
		EndpointBaseURL:       candidate.BaseURL,
		StatusCode:            result.ClientStatus,
		DurationMs:            finished.Sub(started).Milliseconds(),
		StartedAt:             started,
		CompletedAt:           finished,
		UncachedInputTokens:   plan.UncachedInputTokens,
		CacheWriteInputTokens: plan.CacheWriteInputTokens,
		CacheReadInputTokens:  plan.CacheReadInputTokens,
		OutputTokens:          plan.OutputTokens,
		UsageUnknown:          plan.UsageUnknown,
		ErrorCode:             stableCode,
		ErrorDiag:             result.Diagnostic,
		RouteKind:             "charity",
		CharityReservationID:  reservationID,
		OriginalChargeMilli:   plan.OriginalCharge,
		UserChargeMilli:       plan.UserCharge,
		DonorRewardMilli:      plan.DonorReward,
	}
	// A protocol-terminating successful charity call is one successful API
	// request: it feeds the same product-activity rollup as the personal exit
	// (frozen §F). Committed-but-failed and unknown rows never fabricate one.
	if result.Success {
		input.Activity = &db.ActivityDelta{
			APIRequests:           1,
			UncachedInputTokens:   plan.UncachedInputTokens,
			CacheWriteInputTokens: plan.CacheWriteInputTokens,
			CacheReadInputTokens:  plan.CacheReadInputTokens,
			OutputTokens:          plan.OutputTokens,
		}
	}
	if err := s.store.RecordRequest(ctx, input); err != nil && ctx.Err() == nil {
		s.logger.Error("charity request log failed", "reservation", reservationID, "error", err)
	}
}

// provisionalKeyReserve returns the key-side reserve for the cheap in-memory
// cap-headroom probe. Per-request uses the model's fixed price; per-token
// cannot know the global token reserve without a config read, so the probe
// passes (amount 0) and the reservation transaction re-checks authoritatively.
func provisionalKeyReserve(m db.CharityModel) int64 {
	if m.PricingMode == db.CharityPricingPerRequest {
		return m.RequestUserPrice
	}
	return 0
}

// snapshotKeyReserve returns the key-side reserve captured in the persisted
// snapshot, used when swapping to a new candidate (the retry reserves from the
// user exactly once, so only the key reserve moves).
func snapshotKeyReserve(snapshot db.CharityPricingSnapshot) int64 {
	switch snapshot.PricingMode {
	case db.CharityPricingPerRequest:
		return snapshot.RequestUserPrice
	case db.CharityPricingPerToken:
		return snapshot.TokenReserveMilli
	default:
		return 0
	}
}

// unknownCommitPlan is the frozen §5.4 fallback settlement used when the
// connector's usage cannot be settled: the user keeps paying the discounted
// reserve, the key consumes the undiscounted reserve, reward 0.
func unknownCommitPlan(userReserved, keyReserved int64) db.CommitPlan {
	return db.CommitPlan{
		OriginalCharge: keyReserved,
		UserCharge:     userReserved,
		UsageUnknown:   true,
	}
}

// RecoverAll converges stalled reservations older than the periodic cutoff.
// It runs before every maintenance sweep; startup callers pass startup=true to
// converge everything (no request can be in flight during process start).
func (s *Service) RecoverAll(ctx context.Context, startup bool) {
	now := s.now().Unix()
	before := now - int64(stallRecoveryCutoff/time.Second)
	if startup {
		before = now + 1 // no live requests exist yet: converge everything
	}
	staller, ok := s.store.(interface {
		ListStalledCharityReservations(context.Context, int64, int) ([]db.StaleCharityReservation, error)
	})
	if !ok {
		return
	}
	for {
		if ctx.Err() != nil {
			return
		}
		stalled, err := staller.ListStalledCharityReservations(ctx, before, 200)
		if err != nil {
			s.logger.Error("charity recovery listing failed", "error", err)
			return
		}
		if len(stalled) == 0 {
			return
		}
		progressed := false
		for _, row := range stalled {
			applied, rerr := recoverTyped(s.store, ctx, row.ID, now)
			if rerr != nil && !errors.Is(rerr, db.ErrNotFound) {
				s.logger.Error("charity recovery failed", "reservation", row.ID, "error", rerr)
				continue
			}
			progressed = progressed || applied
		}
		if !progressed || len(stalled) < 200 {
			return
		}
	}
}

// recoverTyped routes one stalled row to its frozen terminal target.
func recoverTyped(store Repository, ctx context.Context, id, now int64) (bool, error) {
	recoverer, ok := store.(interface {
		RecoverCharityReservation(context.Context, int64, int64) (bool, error)
	})
	if !ok {
		return false, errors.New("charityrouting: store cannot recover reservations")
	}
	return recoverer.RecoverCharityReservation(ctx, id, now)
}

// newAttemptID mints the random opaque correlation id of one logical call. It
// is never derived from client input.
func newAttemptID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	id := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:])
	clear(raw[:])
	return id, nil
}

// classifyResult maps an attempt result onto the stable log error code and the
// client status the personal exit already uses.
func classifyResult(result connectorcontract.AttemptResult) (string, int) {
	if result.Success {
		return "", http.StatusOK
	}
	switch result.Failure {
	case connectorcontract.FailureUpstream:
		return "upstream", http.StatusBadGateway
	case connectorcontract.FailureCanceled, connectorcontract.FailureSink:
		return "", 499
	default:
		return "internal", http.StatusInternalServerError
	}
}

// admissionExhaustedResult is the placeholder result carried while every key
// refuses admission; if no key is ever admitted it is discarded in favor of
// the ErrKeysExhausted sentinel.
func admissionExhaustedResult() connectorcontract.AttemptResult {
	return connectorcontract.AttemptResult{
		Failure:      connectorcontract.FailureUpstream,
		ClientStatus: http.StatusTooManyRequests,
		Diagnostic:   "donation key admission refused",
	}
}

// endedContextResult distinguishes caller cancellation from the service-owned
// aggregate deadline. Before commit, the latter is a stable upstream failure
// so the handler emits 502 instead of an empty response; after commit the
// caller keeps the runner's committed result.
func endedContextResult(parent, bounded context.Context) connectorcontract.AttemptResult {
	if parent != nil && parent.Err() == nil && bounded != nil && errors.Is(bounded.Err(), context.DeadlineExceeded) {
		return connectorcontract.AttemptResult{
			Failure:      connectorcontract.FailureUpstream,
			ClientStatus: http.StatusBadGateway,
			Diagnostic:   "charity request timed out",
		}
	}
	return connectorcontract.AttemptResult{Failure: connectorcontract.FailureCanceled, Diagnostic: "request canceled"}
}

// boundDiag keeps diagnostics within the shared sink policy.
func boundDiag(s string) string { return diagnostic.BoundTo(s, 512) }
