package fetch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/endpoint"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

// Defaults for the bounded refresh machinery. The pool (workers + queue +
// per-user pending/running bound) bounds global and per-user fetch
// concurrency and dispatches round-robin across users; the per-user limiter
// is the shared admission budget consumed by both automatic hook fetches and
// manual refreshes.
const (
	DefaultWorkers                 = 4
	DefaultQueueSize               = 32
	DefaultPerUserCap              = 8
	DefaultRefreshPerUserPerMinute = 10
	maxWorkers                     = 64
	maxQueueSize                   = 1024
	maxPerUserCap                  = 1024
	// maxIssueDiagBytes bounds the upstream-derived fragment that may appear
	// in a user issue message after the shared diagnostic boundary.
	maxIssueDiagBytes = 512
	// maxErrorBodyDiagBytes bounds how much of an upstream error response
	// body is read for a diagnostic.
	maxErrorBodyDiagBytes = 4 << 10
)

// FetcherConfig wires a Fetcher. Store, Stack, Secrets, and Registry are
// mandatory; Workers/QueueSize/PerUserCap/RefreshPerUserPerMinute default to
// the package constants when <= 0.
type FetcherConfig struct {
	Store    *db.Store
	Stack    *egress.Stack
	Secrets  secret.Codec
	Registry *endpoint.Registry
	Now      func() int64

	Workers                 int
	QueueSize               int
	PerUserCap              int
	RefreshPerUserPerMinute int
}

// Fetcher discovers upstream models per (Endpoint, Key) combo. It implements
// endpoint.FetchHook (see FetchModels) so the endpoint rail can trigger one
// automatic fetch after a save/edit/key-add, and it drives the manual refresh
// route. All outbound work goes through the shared egress Stack; all secret
// reads go through the ownership-scoped ciphertext accessor + secret.Codec.
type Fetcher struct {
	store    *db.Store
	stack    *egress.Stack
	secrets  secret.Codec
	registry *endpoint.Registry
	now      func() int64

	pool    *pool
	refresh *ratelimit.RPM
}

// NewFetcher validates the collaborators and starts the bounded worker pool.
// The returned Fetcher must be Closed on process shutdown so queued work is
// dropped and in-flight fetches observe cancellation.
func NewFetcher(cfg FetcherConfig) (*Fetcher, error) {
	if cfg.Store == nil {
		return nil, errors.New("fetch: store is required")
	}
	if cfg.Stack == nil {
		return nil, errors.New("fetch: egress stack is required")
	}
	if nilCodec(cfg.Secrets) {
		return nil, errors.New("fetch: secret codec is required")
	}
	if cfg.Registry == nil {
		return nil, errors.New("fetch: connector registry is required")
	}
	if cfg.Now == nil {
		cfg.Now = func() int64 { return time.Now().Unix() }
	}
	workers := cfg.Workers
	if workers <= 0 {
		workers = DefaultWorkers
	}
	if workers > maxWorkers {
		workers = maxWorkers
	}
	queueSize := cfg.QueueSize
	if queueSize <= 0 {
		queueSize = DefaultQueueSize
	}
	if queueSize > maxQueueSize {
		queueSize = maxQueueSize
	}
	perUserCap := cfg.PerUserCap
	if perUserCap <= 0 {
		perUserCap = DefaultPerUserCap
	}
	if perUserCap > maxPerUserCap {
		perUserCap = maxPerUserCap
	}
	perUser := cfg.RefreshPerUserPerMinute
	if perUser <= 0 {
		perUser = DefaultRefreshPerUserPerMinute
	}
	if perUser > ratelimit.DefaultRPMConfig().MaxEvents {
		perUser = ratelimit.DefaultRPMConfig().MaxEvents
	}

	f := &Fetcher{
		store:    cfg.Store,
		stack:    cfg.Stack,
		secrets:  cfg.Secrets,
		registry: cfg.Registry,
		now:      cfg.Now,
	}
	f.pool = newPool(context.Background(), workers, queueSize, perUserCap, f.runJob)
	rpm, err := ratelimit.NewRPM(ratelimit.RPMConfig{
		Window:       time.Minute,
		GlobalLimit:  4 * perUser,
		PerUserLimit: perUser,
		MaxUserKeys:  ratelimit.DefaultRPMConfig().MaxUserKeys,
		MaxEvents:    ratelimit.DefaultRPMConfig().MaxEvents,
		MaxKeyBytes:  ratelimit.DefaultRPMConfig().MaxKeyBytes,
	})
	if err != nil {
		f.pool.Close()
		return nil, fmt.Errorf("fetch: refresh limiter: %w", err)
	}
	f.refresh = rpm
	return f, nil
}

// Close shuts the fetcher down: no new fetches are accepted, queued jobs are
// dropped, in-flight fetches observe cancellation, and workers are joined.
// Close is idempotent.
func (f *Fetcher) Close() error {
	if f == nil || f.pool == nil {
		return nil
	}
	f.pool.Close()
	if f.refresh != nil {
		return f.refresh.Close()
	}
	return nil
}

// Compile-time assertion: Fetcher implements the endpoint rail's FetchHook
// boundary, so the endpoint Service can drive automatic fetches.
var _ endpoint.FetchHook = (*Fetcher)(nil)

// FetchModels implements endpoint.FetchHook. It submits an automatic fetch
// for the (Endpoint, Key) combo and returns once the job is queued (or
// merged into an already-pending fetch); it never blocks on upstream work and
// never fails an already-committed endpoint save. Automatic and manual
// refreshes share the same per-user admission budget, so a burst of edits
// cannot bypass the per-user bound. A full queue or an exhausted budget is
// reported as an error (logged by the endpoint rail, which then drops the
// fetch). The request context only gates submission: a request already
// cancelled at submit time is skipped, while a queued fetch outlives its
// originating request (the pool owns cancellation via Close).
func (f *Fetcher) FetchModels(ctx context.Context, userID, endpointID, keyID int64) error {
	if ctx == nil {
		return errors.New("fetch: context is required")
	}
	return f.admit(ctx, userID, endpointID, keyID)
}

// RefreshManual submits a user-requested refresh and reports whether the
// request passes the shared per-user admission budget and the bounded pool.
// It returns nil when the fetch is queued (or already pending), ErrPoolBusy
// when a bound is reached (rate_limited at the API), ErrPoolClosed during
// shutdown (service_unavailable), and rate-limit errors from the RPM.
func (f *Fetcher) RefreshManual(ctx context.Context, userID, endpointID, keyID int64) error {
	return f.admit(ctx, userID, endpointID, keyID)
}

// admit is the shared per-user admission path for automatic and manual
// refreshes. Both consume the same per-user RPM budget so neither source can
// bypass the other; a duplicate combo is merged by the pool without spawning
// new work. The pool's per-user and global bounds surface as ErrPoolBusy, an
// exhausted budget as ErrRefreshRateLimited, and shutdown as ErrPoolClosed.
func (f *Fetcher) admit(ctx context.Context, userID, endpointID, keyID int64) error {
	if f == nil || f.pool == nil {
		return errors.New("fetch: fetcher is not available")
	}
	if f.pool.closedState() {
		return ErrPoolClosed
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	decision, err := f.refresh.Allow(strconv.FormatInt(userID, 10))
	if err != nil {
		if errors.Is(err, ratelimit.ErrClosed) {
			return ErrPoolClosed
		}
		return fmt.Errorf("fetch: refresh limiter: %w", err)
	}
	if !decision.Allowed {
		return ErrRefreshRateLimited
	}
	return f.pool.Submit(jobKey{userID: userID, endpointID: endpointID, keyID: keyID})
}

// ErrRefreshRateLimited reports that the shared per-user automatic/manual
// refresh frequency limit was reached.
var ErrRefreshRateLimited = errors.New("fetch: refresh rate limit reached")

// runJob executes one queued fetch. All ownership checks are in SQL; a combo
// that vanished or was disabled meanwhile is a silent no-op.
func (f *Fetcher) runJob(ctx context.Context, job jobKey) {
	if err := f.fetchOne(ctx, job.userID, job.endpointID, job.keyID); err != nil {
		// Failures that reached an upstream/secret/protocol stage are recorded
		// inside fetchOne. A repository error here is a server-side fault:
		// log it bounded, never with secret material.
		slog.Warn("fetch job failed", "endpoint_id", job.endpointID, "key_id", job.keyID, "err", diagnostic.Bound(err.Error()))
	}
}

// fetchOne performs one ownership-checked upstream /v1/models fetch and
// applies its result atomically. It returns an error only for server-side
// faults (repository/limiter); protocol and upstream failures are recorded
// via FailFetch (cache cleared, endpoint flagged, user issue written).
func (f *Fetcher) fetchOne(ctx context.Context, userID, endpointID, keyID int64) error {
	state, err := f.store.GetEndpointKeyFetchState(ctx, userID, endpointID, keyID)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return nil // combo deleted before this job ran.
		}
		return fmt.Errorf("read fetch state: %w", err)
	}
	if !state.EndpointEnabled || !state.KeyEnabled {
		return nil // disabled combos never fetch, never flag, never issue.
	}

	// The connector registry is the single authority: only registry-confirmed
	// OpenAI-compatible endpoints are fetched. This is defensive (unknown
	// types are rejected at endpoint creation), but a registry regression must
	// not silently fetch under a different protocol.
	if f.registry == nil || !f.registry.Supported(endpoint.ConnectorOpenAICompatible) ||
		state.ConnectorType != string(endpoint.ConnectorOpenAICompatible) {
		f.recordFailure(ctx, userID, endpointID, keyID, "connector type is not supported for model fetch")
		return nil
	}

	ciphertext, err := f.store.GetEndpointKeyCiphertext(ctx, userID, endpointID, keyID)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("read endpoint key ciphertext: %w", err)
	}
	plaintext, err := f.secrets.Open(ciphertext)
	if err != nil {
		// Never include the envelope or the error detail: it is deliberately
		// opaque so a key rotation or tampering cannot become an oracle.
		f.recordFailure(ctx, userID, endpointID, keyID, "endpoint key could not be decrypted")
		return nil
	}
	defer clear(plaintext)

	models, diag := f.fetchUpstream(ctx, state.BaseURL, plaintext)
	// The bearer material is no longer needed once the egress call returns;
	// clear the slice before parsing diagnostics or writing the cache. The
	// defer remains as a panic/error-path backstop.
	clear(plaintext)
	if diag != "" {
		// A cancelled fetch (pool shutdown / parent cancellation) is not an
		// upstream failure: it must not flag the endpoint or spam the issue
		// center with "context canceled" noise.
		if ctx.Err() == nil {
			f.recordFailure(ctx, userID, endpointID, keyID, diag)
		}
		return nil
	}

	rows := make([]db.FetchedModel, 0, len(models))
	for _, m := range models {
		rows = append(rows, db.FetchedModel{
			EndpointKeyID:   keyID,
			UpstreamModelID: m.ID,
			Provider:        m.Provider,
			Status:          "ok",
		})
	}
	if err := f.store.ReplaceFetchedModels(ctx, userID, endpointID, keyID, rows, f.now()); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return nil // combo deleted mid-fetch; replacement no-oped.
		}
		return fmt.Errorf("replace fetched models: %w", err)
	}
	return nil
}

// fetchUpstream dials the upstream through the shared egress Stack, enforces
// the protocol, and parses the model list. On any failure it returns a
// non-empty bounded diagnostic (never the URL, key, or full body); on success
// it returns the validated models and an empty diagnostic.
func (f *Fetcher) fetchUpstream(ctx context.Context, baseURL string, keyPlaintext []byte) ([]Model, string) {
	client, err := f.stack.NewClient(baseURL)
	if err != nil {
		return nil, boundedDiag("egress client unavailable", err.Error())
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, joinModelsURL(baseURL), nil)
	if err != nil {
		return nil, boundedDiag("upstream request could not be built", err.Error())
	}
	req.Header.Set("Authorization", "Bearer "+string(keyPlaintext))
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, boundedDiag("upstream request failed", err.Error())
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode > 299 {
		bodyDiag := readErrorBodyDiag(resp.Body)
		// Upstream error bodies are the sanctioned place for bounded upstream
		// text, but never for the credential: some upstreams echo the
		// Authorization value back in error bodies, so a fragment containing
		// the key bytes is withheld entirely.
		if bytes.Contains([]byte(bodyDiag), keyPlaintext) {
			bodyDiag = ""
		}
		return nil, boundedDiag(fmt.Sprintf("upstream returned status %d", resp.StatusCode), bodyDiag)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxModelsBodyBytes+1))
	if err != nil {
		return nil, boundedDiag("upstream response could not be read", err.Error())
	}
	if len(body) > MaxModelsBodyBytes {
		return nil, boundedDiag(errTruncatedBody.Error(), "")
	}
	models, err := parseModels(body)
	if err != nil {
		return nil, boundedDiag("invalid upstream models response", err.Error())
	}
	return models, ""
}

// readErrorBodyDiag reads at most maxErrorBodyDiagBytes of an upstream error
// body so a diagnostic fragment can be surfaced; the fragment is bounded and
// sanitized by the caller.
func readErrorBodyDiag(r io.Reader) string {
	chunk, err := io.ReadAll(io.LimitReader(r, maxErrorBodyDiagBytes))
	if err != nil {
		return ""
	}
	return string(chunk)
}

// boundedDiag folds stable and upstream-derived text through the shared
// diagnostic boundary (maxIssueDiagBytes) so the result is valid UTF-8,
// single-line, and bounded. Empty input yields an empty string.
func boundedDiag(parts ...string) string {
	joined := ""
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if joined == "" {
			joined = p
		} else {
			joined += " " + p
		}
	}
	return diagnostic.BoundTo(joined, maxIssueDiagBytes)
}

// recordFailure writes the failure atomically: combo cache cleared, endpoint
// flagged with timestamp, bounded user issue inserted. It never includes the
// URL, the key, or an unbounded response.
func (f *Fetcher) recordFailure(ctx context.Context, userID, endpointID, keyID int64, diag string) {
	message := "model fetch failed"
	if diag != "" {
		message += ": " + diag
	}
	if err := f.store.FailFetch(ctx, userID, endpointID, keyID,
		"model_fetch_failed", message, strconv.FormatInt(endpointID, 10), f.now()); err != nil {
		slog.Warn("record fetch failure failed", "endpoint_id", endpointID, "key_id", keyID, "err", diagnostic.Bound(err.Error()))
	}
}

// joinModelsURL appends the OpenAI models resource path to a canonical endpoint
// base URL. The base URL carries the provider's full API mount up to and
// including the version segment (for example https://host/v1 or
// https://host/api/v1); only the resource path is appended, so a base already
// ending in /v1 never becomes /v1/v1/models. Callers must supply the version
// segment as part of the endpoint base URL.
func joinModelsURL(baseURL string) string {
	return strings.TrimSuffix(baseURL, "/") + "/models"
}

// nilCodec mirrors the db package's nil-interface check so a typed-nil Codec
// cannot slip past construction.
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
