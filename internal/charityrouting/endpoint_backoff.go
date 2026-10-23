package charityrouting

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/smartbackoff"
)

const (
	routeAdminEndpointBackoffOptions    = "/admin/api/endpoint-backoff-options"
	routeStewardEndpointBackoffOptions  = "/api/steward/endpoint-backoff-options"
	routeAdminEndpointBackoffPolicies   = "/admin/api/endpoint-backoff-policies"
	routeAdminEndpointBackoffPolicy     = "/admin/api/endpoint-backoff-policy"
	routeStewardEndpointBackoffPolicies = "/api/steward/endpoint-backoff-policies"
	routeStewardEndpointBackoffPolicy   = "/api/steward/endpoint-backoff-policy"
)

type EndpointBackoffOptions struct {
	ConnectorTypes []string `json:"connector_types"`
}

func (s *Service) beginEndpointPolicyRead(ctx context.Context, role roleKind, actor int64) (*sql.Tx, error) {
	if s == nil || s.readDB == nil || nilDependency(s.roleAuth) {
		return nil, ErrUnavailable
	}
	tx, err := s.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	if _, err := s.authorizeRoleTx(ctx, tx, role, actor); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func (s *Service) endpointBackoffOptions(ctx context.Context, role roleKind, actor int64) (EndpointBackoffOptions, error) {
	out := EndpointBackoffOptions{ConnectorTypes: []string{}}
	if ctx == nil {
		return out, ErrInvalidRequest
	}
	if !s.endpointPolicyReady() {
		return out, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.beginEndpointPolicyRead(ctx, role, actor)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	for _, kind := range s.connectors.Types() {
		out.ConnectorTypes = append(out.ConnectorTypes, string(kind))
	}
	return out, tx.Commit()
}

type EndpointBackoffPolicy struct {
	ConnectorType string              `json:"connector_type"`
	BaseURL       string              `json:"base_url"`
	Revision      string              `json:"revision"`
	Rules         []smartbackoff.Rule `json:"rules"`
}
type EndpointBackoffPolicySummary struct {
	ConnectorType string `json:"connector_type"`
	BaseURL       string `json:"base_url"`
	Revision      string `json:"revision"`
	RuleCount     string `json:"rule_count"`
	KeyCount      string `json:"key_count"`
}
type EndpointBackoffPolicyInput struct {
	ConnectorType    string              `json:"connector_type"`
	BaseURL          string              `json:"base_url"`
	ExpectedRevision string              `json:"expected_revision"`
	Rules            []smartbackoff.Rule `json:"rules"`
}

func (s *Service) endpointPolicyReady() bool {
	return s != nil && s.connectors != nil && !nilDependency(s.baseURLs)
}

func (s *Service) normalizePolicyEndpoint(kind, base string) (string, error) {
	if !s.endpointPolicyReady() {
		return "", ErrUnavailable
	}
	if _, ok := s.connectors.Descriptor(connectorcontract.Type(kind)); !ok {
		return "", ErrInvalidRequest
	}
	canonical, err := s.baseURLs.ValidateBaseURL(base)
	if err != nil || canonical == "" || len(canonical) > 4096 {
		return "", ErrInvalidRequest
	}
	return canonical, nil
}

func readEndpointBackoffPolicy(ctx context.Context, tx *sql.Tx, kind, base string) (EndpointBackoffPolicy, error) {
	out := EndpointBackoffPolicy{ConnectorType: kind, BaseURL: base, Revision: "0", Rules: []smartbackoff.Rule{}}
	var revision int64
	var encoded string
	err := tx.QueryRowContext(ctx, `SELECT revision,rules_json FROM endpoint_backoff_policies WHERE connector_type=? AND base_url=?`, kind, base).Scan(&revision, &encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if json.Unmarshal([]byte(encoded), &out.Rules) != nil {
		return out, ErrInvariant
	}
	out.Rules, err = smartbackoff.NormalizeRules(out.Rules)
	if err != nil {
		return out, ErrInvariant
	}
	out.Revision = strconv.FormatInt(revision, 10)
	return out, nil
}

func (s *Service) endpointBackoffPolicy(ctx context.Context, role roleKind, actor int64, kind, base string) (EndpointBackoffPolicy, error) {
	var empty EndpointBackoffPolicy
	if ctx == nil {
		return empty, ErrInvalidRequest
	}
	canonical, err := s.normalizePolicyEndpoint(kind, base)
	if err != nil {
		return empty, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.beginEndpointPolicyRead(ctx, role, actor)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	out, err := readEndpointBackoffPolicy(ctx, tx, kind, canonical)
	if err != nil {
		return empty, err
	}
	return out, tx.Commit()
}

const endpointBackoffPoliciesSelect = `WITH donated AS MATERIALIZED (
 SELECT connector_type,canonical_base_url AS base_url,COUNT(*) AS key_count FROM donation_keys GROUP BY connector_type,canonical_base_url
), endpoints AS (
 SELECT connector_type,base_url,key_count FROM donated
 UNION ALL SELECT p.connector_type,p.base_url,0 FROM endpoint_backoff_policies p
 WHERE NOT EXISTS(SELECT 1 FROM donated d WHERE d.connector_type=p.connector_type AND d.base_url=p.base_url)
)
 SELECT e.connector_type,e.base_url,COALESCE(p.revision,0),COALESCE(json_array_length(p.rules_json),0),e.key_count
 FROM endpoints e
 LEFT JOIN endpoint_backoff_policies p ON p.connector_type=e.connector_type AND p.base_url=e.base_url WHERE instr(lower(e.base_url),lower(?))>0`

func (s *Service) endpointBackoffPolicies(ctx context.Context, role roleKind, actor int64, q string, page pagination.Request) (Page[EndpointBackoffPolicySummary], error) {
	out := Page[EndpointBackoffPolicySummary]{Data: []EndpointBackoffPolicySummary{}}
	if !s.endpointPolicyReady() {
		return out, ErrUnavailable
	}
	if ctx == nil || !page.Valid() || !utf8.ValidString(q) || utf8.RuneCountInString(q) > 128 {
		return out, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.beginEndpointPolicyRead(ctx, role, actor)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	meta, offset, err := pageWindow(ctx, tx, endpointBackoffPoliciesSelect, []any{q}, page)
	if err != nil {
		return out, err
	}
	out.Pagination = &meta
	rows, err := tx.QueryContext(ctx, endpointBackoffPoliciesSelect+` ORDER BY e.base_url,e.connector_type LIMIT ? OFFSET ?`, q, page.Size, offset)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var item EndpointBackoffPolicySummary
		var revision, rules, keys int64
		if err := rows.Scan(&item.ConnectorType, &item.BaseURL, &revision, &rules, &keys); err != nil {
			return out, err
		}
		item.Revision, item.RuleCount, item.KeyCount = strconv.FormatInt(revision, 10), strconv.FormatInt(rules, 10), strconv.FormatInt(keys, 10)
		out.Data = append(out.Data, item)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func (s *Service) saveEndpointBackoffPolicy(ctx context.Context, role roleKind, actor int64, mutation resources.ControlMutation, input EndpointBackoffPolicyInput) (resources.MutationResult[EndpointBackoffPolicy], error) {
	empty := resources.MutationResult[EndpointBackoffPolicy]{}
	route := routeAdminEndpointBackoffPolicy
	if role == roleSteward {
		route = routeStewardEndpointBackoffPolicy
	}
	expected, err := parseNonnegativeRevision(input.ExpectedRevision)
	if ctx == nil || err != nil || !validMutation(mutation, http.MethodPut, route) {
		return empty, ErrInvalidRequest
	}
	canonical, err := s.normalizePolicyEndpoint(input.ConnectorType, input.BaseURL)
	if err != nil {
		return empty, err
	}
	rules, err := smartbackoff.NormalizeRules(input.Rules)
	if err != nil {
		return empty, ErrInvalidRequest
	}
	now, err := s.nowUnix()
	if err != nil {
		return empty, err
	}
	tx, actor, err := s.beginRoleTx(ctx, role, actor)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	decision, err := beginMutation(ctx, tx, role, actor, mutation, now)
	if err != nil {
		return empty, err
	}
	if decision.Kind == idempotency.Replay {
		return replay[EndpointBackoffPolicy](decision)
	}
	current, err := readEndpointBackoffPolicy(ctx, tx, input.ConnectorType, canonical)
	if err != nil {
		return empty, err
	}
	if current.Revision != input.ExpectedRevision || expected == math.MaxInt64 {
		return empty, ErrConflict
	}
	encoded, err := json.Marshal(rules)
	if err != nil {
		return empty, err
	}
	if expected == 0 {
		_, err = tx.ExecContext(ctx, `INSERT INTO endpoint_backoff_policies(connector_type,base_url,revision,rules_json) VALUES(?,?,1,?)`, input.ConnectorType, canonical, string(encoded))
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE endpoint_backoff_policies SET revision=revision+1,rules_json=? WHERE connector_type=? AND base_url=? AND revision=?`, string(encoded), input.ConnectorType, canonical, expected)
	}
	if err != nil {
		return empty, err
	}
	value := EndpointBackoffPolicy{ConnectorType: input.ConnectorType, BaseURL: canonical, Revision: strconv.FormatInt(expected+1, 10), Rules: rules}
	result, err := finishJSON(ctx, tx, decision, http.StatusOK, value)
	if err != nil {
		return empty, err
	}
	if err := tx.Commit(); err != nil {
		return empty, err
	}
	return result, nil
}
