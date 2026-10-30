package codexauth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
)

const UsageURL = "https://chatgpt.com/backend-api/wham/usage"
const quotaTTL = 30 * time.Second
const quotaBudget = 15 * time.Second
const quotaLimit = 1 << 20

type QuotaResult struct {
	Source       string                     `json:"source"`
	Revision     int64                      `json:"revision,string"`
	Availability string                     `json:"availability"`
	FetchedAt    int64                      `json:"fetched_at"`
	AttemptedAt  int64                      `json:"attempted_at"`
	Stale        bool                       `json:"stale"`
	Data         *QuotaData                 `json:"data"`
	Error        *diagnostic.OperationError `json:"error,omitempty"`
}
type quotaFlight struct {
	revision int64
	epoch    uint64
	done     chan struct{}
	cancel   context.CancelFunc
	waiters  int
	result   QuotaResult
	err      error
}

// Quota merges overlapping reads without allowing a canceled operation or
// disconnected account to publish a late result to another connection.
func (m *Manager) Quota(ctx context.Context, force bool) (QuotaResult, error) {
	if err := m.authorize(ctx); err != nil {
		return QuotaResult{}, err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return QuotaResult{}, ErrClosed
	}
	s, err := load(ctx, m.config.ReadDB)
	if err != nil {
		m.mu.Unlock()
		return QuotaResult{}, err
	}
	if s.encrypted == "" {
		m.mu.Unlock()
		return QuotaResult{}, ErrDisconnected
	}
	now := m.config.Now().UnixMilli()
	if cached := m.quotaCache; cached != nil && cached.Revision == s.revision && !force && now-cached.AttemptedAt < quotaTTL.Milliseconds() {
		out := *cached
		m.mu.Unlock()
		return out, nil
	}
	flight := m.quotaFlight
	if flight == nil || flight.revision != s.revision || flight.epoch != m.epoch {
		child, cancel := context.WithTimeout(context.WithoutCancel(ctx), quotaBudget)
		stop := context.AfterFunc(m.connection, cancel)
		flight = &quotaFlight{revision: s.revision, epoch: m.epoch, done: make(chan struct{}), cancel: func() { stop(); cancel() }}
		m.quotaFlight = flight
		m.workers.Add(1)
		go m.runQuota(child, flight)
	}
	flight.waiters++
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		flight.waiters--
		if flight.waiters == 0 {
			select {
			case <-flight.done:
			default:
				flight.cancel()
				if m.quotaFlight == flight {
					m.quotaFlight = nil
				}
			}
		}
	}()
	select {
	case <-ctx.Done():
		return QuotaResult{}, ctx.Err()
	case <-flight.done:
		return flight.result, flight.err
	}
}
func (m *Manager) runQuota(ctx context.Context, flight *quotaFlight) {
	defer m.workers.Done()
	defer flight.cancel()
	result := QuotaResult{Source: "openai_codex", Revision: flight.revision, AttemptedAt: m.config.Now().UnixMilli(), Availability: "unavailable"}
	data, err := m.fetchQuota(ctx, flight.revision)
	if err == nil {
		result.Data = &data
		result.FetchedAt = m.config.Now().UnixMilli()
		result.Availability = "available"
	} else {
		errors.As(err, &result.Error)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if flight.epoch != m.epoch || m.quotaFlight != flight || m.closed {
		flight.err = ErrConflict
	} else if errors.Is(err, ErrConflict) || errors.Is(err, ErrDisconnected) {
		flight.err = err
	} else {
		if err != nil && m.quotaCache != nil && m.quotaCache.Revision == flight.revision {
			result.Data = m.quotaCache.Data
			result.FetchedAt = m.quotaCache.FetchedAt
			result.Stale = result.Data != nil
		}
		if !errors.Is(ctx.Err(), context.Canceled) {
			m.quotaCache = &result
		}
		flight.result = result
		m.quotaFlight = nil
	}
	if m.quotaFlight == flight {
		m.quotaFlight = nil
	}
	close(flight.done)
}
func (m *Manager) fetchQuota(ctx context.Context, revision int64) (data QuotaData, resultErr error) {
	ctx = diagnostic.WithRecorder(ctx)
	var upstreamCode string
	defer func() {
		if resultErr == nil {
			return
		}
		failure := diagnostic.OperationFailure(ctx, "codex_quota", "Codex", resultErr)
		if failure.UpstreamCode == "" {
			failure.UpstreamCode = upstreamCode
		}
		diagnostic.SaveOperation(ctx, m.config.DB, failure)
		resultErr = failure
	}()
	diagnostic.Phase(ctx, "credential")
	key, account, current, err := m.AccessTokenSnapshot(ctx)
	if err != nil {
		RecordCredentialFailure(ctx, err)
		return data, err
	}
	defer func() { clear(key) }()
	if current != revision {
		RecordCredentialFailure(ctx, ErrConflict)
		return data, ErrConflict
	}
	child, cancel, err := m.RequestContext(ctx, revision)
	if err != nil {
		RecordCredentialFailure(ctx, err)
		return data, err
	}
	defer cancel()
	ctx = egress.WithDispatchCheck(child, func(check context.Context) error {
		if err := m.authorize(check); err != nil {
			return err
		}
		err := m.ValidateGeneration(check, revision)
		if err != nil {
			RecordCredentialFailure(check, err)
		}
		return err
	})
	client, err := m.config.Stack.NewClient(m.client.usageURL)
	if err != nil {
		diagnostic.RecordKnownError(ctx, "policy", "configuration", "endpoint_invalid", "Quota endpoint failed outbound policy", err)
		return data, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.client.usageURL, nil)
		if err != nil {
			return data, err
		}
		req.Header.Set("Authorization", "Bearer "+string(key))
		req.Header.Set("ChatGPT-Account-ID", account)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("originator", "nonbiriapi")
		response, err := client.Do(req)
		if err != nil {
			if response != nil && response.Body != nil {
				response.Body.Close()
			}
			return data, err
		}
		if response.StatusCode == 401 && attempt == 0 {
			response.Body.Close()
			next, nextAccount, nextRevision, refreshErr := m.RecoverAccessToken(ctx, revision, key)
			if refreshErr != nil {
				RecordCredentialFailure(ctx, refreshErr)
				return data, refreshErr
			}
			clear(key)
			key = next
			account = nextAccount
			if nextRevision != revision {
				return data, ErrConflict
			}
			ctx = diagnostic.WithRecorder(ctx)
			continue
		}
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, quotaLimit+1))
		response.Body.Close()
		if readErr != nil {
			clear(raw)
			diagnostic.RecordError(ctx, "read_response", readErr)
			return data, readErr
		}
		if len(raw) > quotaLimit {
			clear(raw)
			err = errors.New("quota response too large")
			diagnostic.RecordLimit(ctx, "response_bytes", quotaLimit, err)
			return data, err
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			upstreamCode, err = diagnostic.RecordHTTPFailure(ctx, response.StatusCode, raw, func(value []byte) bool { return diagnostic.ReflectsSecret(value, key) })
			clear(raw)
			return data, err
		}
		if diagnostic.ReflectsSecret(raw, key) {
			clear(raw)
			err = errors.New("credential reflected in quota")
			diagnostic.RecordKnownError(ctx, "decode_response", "protocol", "credential_reflected", "The response contained authentication data and was withheld", err)
			return data, err
		}
		data, err = decodeQuota(raw)
		clear(raw)
		if err != nil {
			diagnostic.RecordError(ctx, "decode_response", err)
			return data, err
		}
		if err = m.ValidateGeneration(ctx, revision); err != nil {
			RecordCredentialFailure(ctx, err)
			return QuotaData{}, err
		}
		return data, nil
	}
	return data, ErrInvalid
}
func RecordCredentialFailure(ctx context.Context, err error) {
	switch {
	case errors.Is(err, ErrConflict):
		diagnostic.RecordKnownError(ctx, "credential", "connection", "connection_changed", "The Codex connection changed during this operation; reload and retry", err)
	case errors.Is(err, ErrDisconnected):
		diagnostic.RecordKnownError(ctx, "credential", "connection", "connection_disconnected", "Codex is disconnected; reconnect to continue", err)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		diagnostic.RecordError(ctx, "credential", err)
	default:
		var operation *diagnostic.OperationError
		if !errors.As(err, &operation) {
			diagnostic.RecordKnownError(ctx, "credential", "platform", "credential_unreadable", "The stored credential could not be opened", err)
		}
	}
}
