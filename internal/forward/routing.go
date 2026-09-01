package forward

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	randv2 "math/rand/v2"
	"time"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

type BackoffConfig struct {
	Base time.Duration
	Max  time.Duration
}

func (config BackoffConfig) normalized() BackoffConfig {
	if config.Base < 0 {
		config.Base = 0
	}
	if config.Max <= 0 || config.Max < config.Base {
		config.Max = config.Base
	}
	return config
}

func (config BackoffConfig) wait(ctx context.Context, attempt int) bool {
	config = config.normalized()
	if config.Base == 0 {
		return ctx != nil && ctx.Err() == nil
	}
	delay := config.Base
	for step := 0; step < attempt && delay < config.Max; step++ {
		if delay > config.Max/2 {
			delay = config.Max
			break
		}
		delay *= 2
	}
	if delay > config.Max {
		delay = config.Max
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func orderCandidates(strategy string, candidates []RouteCandidate) ([]RouteCandidate, error) {
	if len(candidates) < 1 || len(candidates) > MaxRouteAttempts {
		return nil, ErrInternal
	}
	ordered := append([]RouteCandidate(nil), candidates...)
	switch strategy {
	case "ordered":
		return ordered, nil
	case "random":
		var seed [16]byte
		if _, err := crand.Read(seed[:]); err != nil {
			return nil, ErrInternal
		}
		rng := randv2.New(randv2.NewPCG(binary.LittleEndian.Uint64(seed[:8]), binary.LittleEndian.Uint64(seed[8:])))
		clear(seed[:])
		rng.Shuffle(len(ordered), func(left, right int) { ordered[left], ordered[right] = ordered[right], ordered[left] })
		return ordered, nil
	default:
		return nil, ErrInternal
	}
}

func retryable(result connectorcontract.AttemptResult, enabled bool) bool {
	return enabled && !result.Committed && !result.SinkFailed && result.Failure == connectorcontract.FailureUpstream
}

func validAttemptResult(result connectorcontract.AttemptResult) bool {
	if result.UpstreamStatus != 0 && (result.UpstreamStatus < 100 || result.UpstreamStatus > 599) {
		return false
	}
	usage := result.Usage
	if usage.UncachedInputTokens < 0 || usage.CacheWriteInputTokens < 0 || usage.CacheReadInputTokens < 0 || usage.OutputTokens < 0 {
		return false
	}
	if !usage.Present && (usage.UncachedInputTokens != 0 || usage.CacheWriteInputTokens != 0 || usage.CacheReadInputTokens != 0 || usage.OutputTokens != 0) {
		return false
	}
	if result.Success {
		return result.Committed && !result.SinkFailed && result.Failure == connectorcontract.FailureNone && result.UpstreamStatus >= 200 && result.UpstreamStatus <= 399
	}
	if result.Failure == connectorcontract.FailureNone {
		return false
	}
	return true
}

func isTimeoutResult(result connectorcontract.AttemptResult) bool {
	switch result.Diagnostic {
	case "upstream request timed out", "upstream response timed out", "forward request timed out":
		return true
	default:
		return false
	}
}
