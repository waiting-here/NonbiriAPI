// Package logapi projects persisted logical request facts into three distinct
// role-safe read models. It never reads credentials, ciphertext, request or
// response bodies, raw upstream errors, Discord identity, or donor material.
package logapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

var (
	ErrInvalid     = errors.New("logapi: invalid request")
	ErrNotFound    = errors.New("logapi: not found")
	ErrForbidden   = errors.New("logapi: forbidden")
	ErrConflict    = errors.New("logapi: state conflict")
	ErrCapacity    = errors.New("logapi: resource limit exceeded")
	ErrUnavailable = errors.New("logapi: unavailable")
	ErrInvariant   = errors.New("logapi: persisted invariant is invalid")
)

type RouteKind string

const (
	RouteOpenAIChat  RouteKind = "openai_chat_completions"
	RouteCharityChat RouteKind = "charity_chat_completions"
	RouteDiscovery   RouteKind = "model_discovery"
)

type ResultClass string

const (
	ResultSuccess   ResultClass = "success"
	ResultFailed    ResultClass = "failed"
	ResultCancelled ResultClass = "cancelled"
)

type ResultKind string

const (
	ResultResponse  ResultKind = "response"
	ResultSynthetic ResultKind = "synthetic"
)

type LogUsage struct {
	UncachedInputTokens   string `json:"uncached_input_tokens"`
	CacheWriteInputTokens string `json:"cache_write_input_tokens"`
	CacheReadInputTokens  string `json:"cache_read_input_tokens"`
	OutputTokens          string `json:"output_tokens"`
	TotalTokens           string `json:"total_tokens"`
	UsageUnknown          bool   `json:"usage_unknown"`
	Charge                string `json:"charge"`
}

type Page[T any] struct {
	Data       []T     `json:"data"`
	NextCursor *string `json:"next_cursor"`
}

// UserLogRow is a closed implementation interface. Only UserSelfLogRow and
// UserCharityLogRow implement it; callers cannot construct a charity row with
// an attempt_count field or a self row without one.
type UserLogRow interface {
	userLogRow()
}

type UserSelfLogRow struct {
	ID                string       `json:"id"`
	RouteKind         RouteKind    `json:"route_kind"`
	CallerResultClass *ResultClass `json:"caller_result_class"`
	CallerStatus      *int         `json:"caller_status"`
	CallerErrorCode   *string      `json:"caller_error_code"`
	StartedAt         int64        `json:"started_at"`
	CompletedAt       *int64       `json:"completed_at"`
	Usage             LogUsage     `json:"usage"`
	Model             string       `json:"model"`
	AttemptCount      string       `json:"attempt_count"`
}

func (UserSelfLogRow) userLogRow() {}

type UserCharityLogRow struct {
	ID                string       `json:"id"`
	RouteKind         RouteKind    `json:"route_kind"`
	CallerResultClass *ResultClass `json:"caller_result_class"`
	CallerStatus      *int         `json:"caller_status"`
	CallerErrorCode   *string      `json:"caller_error_code"`
	StartedAt         int64        `json:"started_at"`
	CompletedAt       *int64       `json:"completed_at"`
	Usage             LogUsage     `json:"usage"`
	Model             string       `json:"model"`
}

func (UserCharityLogRow) userLogRow() {}

type UserSelfLogAttempt struct {
	AttemptSeq      string     `json:"attempt_seq"`
	ResultKind      ResultKind `json:"result_kind"`
	EndpointKeyID   *string    `json:"endpoint_key_id"`
	EndpointBaseURL string     `json:"endpoint_base_url"`
	EndpointNote    string     `json:"endpoint_note"`
	KeyNote         string     `json:"key_note"`
	ConnectorType   string     `json:"connector_type"`
	UpstreamModelID string     `json:"upstream_model_id"`
	StatusCode      *int       `json:"status_code"`
	UpstreamCode    *string    `json:"upstream_code"`
	Diag            *string    `json:"diag"`
	Usage           LogUsage   `json:"usage"`
	StartedAt       int64      `json:"started_at"`
	CompletedAt     int64      `json:"completed_at"`
}

type UserSelfLogDetail struct {
	Request  UserSelfLogRow           `json:"request"`
	Attempts Page[UserSelfLogAttempt] `json:"attempts"`
}

type CallerSafeResult struct {
	Class ResultClass `json:"class"`
}

type UserCharityLogDetail struct {
	Request          UserCharityLogRow `json:"request"`
	CallerSafeResult CallerSafeResult  `json:"caller_safe_result"`
}

type UserLogDetail interface {
	userLogDetail()
}

func (UserSelfLogDetail) userLogDetail()    {}
func (UserCharityLogDetail) userLogDetail() {}

type AdminLogRow struct {
	ID                string       `json:"id"`
	RouteKind         RouteKind    `json:"route_kind"`
	CallerResultClass *ResultClass `json:"caller_result_class"`
	CallerStatus      *int         `json:"caller_status"`
	CallerErrorCode   *string      `json:"caller_error_code"`
	StartedAt         int64        `json:"started_at"`
	CompletedAt       *int64       `json:"completed_at"`
	Usage             LogUsage     `json:"usage"`
	UserID            *string      `json:"user_id"`
	AttemptCount      string       `json:"attempt_count"`
}

type AdminLogAttempt struct {
	AttemptSeq      string     `json:"attempt_seq"`
	ResultKind      ResultKind `json:"result_kind"`
	EndpointKeyID   *string    `json:"endpoint_key_id"`
	EndpointBaseURL string     `json:"endpoint_base_url"`
	ConnectorType   string     `json:"connector_type"`
	UpstreamModelID string     `json:"upstream_model_id"`
	StatusCode      *int       `json:"status_code"`
	UpstreamCode    *string    `json:"upstream_code"`
	Diag            *string    `json:"diag"`
	Usage           LogUsage   `json:"usage"`
	StartedAt       int64      `json:"started_at"`
	CompletedAt     int64      `json:"completed_at"`
}

type AdminLogDetail struct {
	Request  AdminLogRow           `json:"request"`
	Attempts Page[AdminLogAttempt] `json:"attempts"`
}

// Steward types repeat every allowed field. They intentionally do not embed,
// alias, or convert through Admin DTOs, so future Admin additions cannot cross
// the L5 boundary by construction.
type StewardLogRow struct {
	ID                string       `json:"id"`
	RouteKind         RouteKind    `json:"route_kind"`
	CallerResultClass *ResultClass `json:"caller_result_class"`
	CallerStatus      *int         `json:"caller_status"`
	CallerErrorCode   *string      `json:"caller_error_code"`
	StartedAt         int64        `json:"started_at"`
	CompletedAt       *int64       `json:"completed_at"`
	Usage             LogUsage     `json:"usage"`
	AttemptCount      string       `json:"attempt_count"`
}

type StewardLogAttempt struct {
	AttemptSeq      string     `json:"attempt_seq"`
	ResultKind      ResultKind `json:"result_kind"`
	EndpointKeyID   *string    `json:"endpoint_key_id"`
	EndpointBaseURL string     `json:"endpoint_base_url"`
	ConnectorType   string     `json:"connector_type"`
	UpstreamModelID string     `json:"upstream_model_id"`
	StatusCode      *int       `json:"status_code"`
	UpstreamCode    *string    `json:"upstream_code"`
	Diag            *string    `json:"diag"`
	Usage           LogUsage   `json:"usage"`
	StartedAt       int64      `json:"started_at"`
	CompletedAt     int64      `json:"completed_at"`
}

type StewardLogDetail struct {
	Request  StewardLogRow           `json:"request"`
	Attempts Page[StewardLogAttempt] `json:"attempts"`
}

type ListFilter struct {
	UserID          *int64
	EndpointBaseURL *string
	UpstreamModel   *string
	Model           *string
	ErrorCode       *string
	Status          *int
	From            *int64
	To              *int64
	Cursor          string
	Limit           int
}

type AttemptFilter struct {
	Cursor string
	Limit  int
}

type UserPrincipal = resources.UserPrincipal
type AuthorizedUserHandler = resources.AuthorizedUserHandler
type UserRouteRegistrar = resources.UserRouteRegistrar

type AdminRouteRegistrar interface {
	RegisterAdminRoute(method, pattern string, handler http.Handler) error
}

type StewardAuthorizer interface {
	AuthorizeStewardRead(context.Context, *sql.Tx, int64) error
}
