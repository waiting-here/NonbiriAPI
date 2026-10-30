// Package codexauth implements the fixed Codex device-code OAuth protocol.
package codexauth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
)

const (
	Issuer          = "https://auth.openai.com"
	Resource        = "https://chatgpt.com/backend-api/codex"
	ClientID        = "app_EMoamEEZ73f0CkXaXp7hrann"
	VerificationURL = Issuer + "/codex/device"
	maxCredentials  = 256 << 10
	loginLifetime   = 15 * time.Minute
)

var (
	ErrInvalid      = errors.New("codex: invalid input")
	ErrConflict     = errors.New("codex: connection changed")
	ErrDisconnected = errors.New("codex: sign in required")
	ErrClosed       = errors.New("codex: manager closed")
)

type OAuthError struct {
	Code   string
	Status int
}

func (e *OAuthError) Error() string { return "codex: " + e.Code }
func (e *OAuthError) Terminal() bool {
	return e.Code == "invalid_grant" || e.Code == "refresh_token_expired" || e.Code == "refresh_token_reused" || e.Code == "refresh_token_invalidated"
}

type Credentials struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	AccountID    string `json:"account_id"`
	Subject      string `json:"subject"`
	Label        string `json:"label"`
	ExpiresAt    int64  `json:"expires_at"`
}

type client struct {
	stack    *egress.Stack
	db       *sql.DB
	usageURL string
	issuer   string
	now      func() time.Time
}
type deviceCode struct {
	DeviceID, Code string
	Interval       time.Duration
}
type authorization struct {
	Code     string `json:"authorization_code"`
	Verifier string `json:"code_verifier"`
}

func (c *client) request(ctx context.Context, path, contentType string, body []byte, out any) (status int, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ctx = diagnostic.WithRecorder(ctx)
	operation := "token_exchange"
	switch path {
	case "/api/accounts/deviceauth/usercode":
		operation = "device_code"
	case "/api/accounts/deviceauth/token":
		operation = "device_poll"
	case "/oauth/token":
		if bytes.Contains(body, []byte("refresh_token")) {
			operation = "token_refresh"
		}
	}
	private := [][]byte{body}
	var fields map[string]string
	if contentType == "application/json" {
		_ = json.Unmarshal(body, &fields)
	} else {
		values, _ := url.ParseQuery(string(body))
		fields = map[string]string{}
		for key, value := range values {
			if len(value) > 0 {
				fields[key] = value[0]
			}
		}
	}
	for _, value := range fields {
		private = append(private, []byte(value))
	}
	defer func() {
		defer clear(body)
		for _, value := range private {
			defer clear(value)
		}
		if resultErr == nil {
			return
		}
		var oauth *OAuthError
		if errors.As(resultErr, &oauth) && (oauth.Code == "authorization_pending" || oauth.Code == "slow_down" || operation == "device_poll" && (status == 403 || status == 404) && oauth.Code == "authorization_failed") {
			return
		}
		failure := diagnostic.OperationFailure(ctx, operation, "Codex", resultErr)
		diagnostic.DropReflectedCorrelation(failure.Details, private...)
		diagnostic.SaveOperation(ctx, c.db, failure)
		resultErr = failure
	}()
	client, err := c.stack.NewClient(c.issuer)
	if err != nil {
		diagnostic.RecordKnownError(ctx, "policy", "configuration", "endpoint_invalid", "The authentication endpoint could not pass outbound policy", err)
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.issuer+path, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		return 0, err
	}
	status = resp.StatusCode
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxCredentials+1))
	defer clear(raw)
	if err != nil {
		diagnostic.RecordError(ctx, "read_response", err)
		return status, err
	}
	if len(raw) > maxCredentials {
		err = errors.New("authentication response too large")
		diagnostic.RecordLimit(ctx, "response_bytes", maxCredentials, err)
		return status, err
	}
	if status < 200 || status >= 300 {
		var object struct {
			Error json.RawMessage `json:"error"`
		}
		var code string
		_ = json.Unmarshal(raw, &object)
		if json.Unmarshal(object.Error, &code) != nil {
			var detail struct {
				Code string `json:"code"`
			}
			_ = json.Unmarshal(object.Error, &detail)
			code = detail.Code
		}
		switch code {
		case "invalid_grant", "invalid_client", "access_denied", "expired_token", "authorization_pending", "slow_down", "refresh_token_expired", "refresh_token_reused", "refresh_token_invalidated":
		default:
			code = "authorization_failed"
		}
		oauth := &OAuthError{Code: code, Status: status}
		category, kind, message := "upstream", "upstream_status", "Authentication service returned an unsuccessful HTTP status"
		if status == 401 {
			kind, message = "upstream_authentication", "Authentication service rejected this credential"
		}
		if status == 429 {
			kind, message = "upstream_rate_limited", "Authentication service limited this operation; retry later"
		}
		if oauth.Terminal() {
			category, kind, message = "connection", "credential_expired", "The refresh credential can no longer be used; reconnect Codex"
		}
		diagnostic.RecordKnownError(ctx, "upstream_response", category, kind, message, oauth)
		return status, oauth
	}
	if err = json.Unmarshal(raw, out); err != nil {
		diagnostic.RecordError(ctx, "decode_response", err)
		return status, err
	}
	return status, nil
}
func (c *client) begin(ctx context.Context) (deviceCode, error) {
	raw, _ := json.Marshal(map[string]string{"client_id": ClientID})
	var value struct {
		DeviceID   string          `json:"device_auth_id"`
		Code       string          `json:"user_code"`
		LegacyCode string          `json:"usercode"`
		Interval   json.RawMessage `json:"interval"`
	}
	status, err := c.request(ctx, "/api/accounts/deviceauth/usercode", "application/json", raw, &value)
	if err != nil {
		if status == http.StatusNotFound {
			return deviceCode{}, &OAuthError{Code: "device_login_unavailable", Status: status}
		}
		return deviceCode{}, err
	}
	if value.Code == "" {
		value.Code = value.LegacyCode
	}
	if !boundedString(value.DeviceID, 4096) || !boundedString(value.Code, 128) {
		return deviceCode{}, c.invalidResponse(ctx, "device_code")
	}
	seconds := int64(5)
	if len(value.Interval) > 0 {
		var s string
		if json.Unmarshal(value.Interval, &s) == nil {
			seconds, err = strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		} else {
			err = json.Unmarshal(value.Interval, &seconds)
		}
		if err != nil || seconds < 1 || seconds > 900 {
			return deviceCode{}, c.invalidResponse(ctx, "device_code")
		}
	}
	return deviceCode{value.DeviceID, value.Code, time.Duration(seconds) * time.Second}, nil
}
func (c *client) poll(ctx context.Context, device deviceCode) (Credentials, error) {
	for {
		raw, _ := json.Marshal(map[string]string{"device_auth_id": device.DeviceID, "user_code": device.Code})
		var value authorization
		status, err := c.request(ctx, "/api/accounts/deviceauth/token", "application/json", raw, &value)
		if err == nil {
			if !boundedString(value.Code, 8192) || !boundedString(value.Verifier, 1024) {
				return Credentials{}, c.invalidResponse(ctx, "device_poll")
			}
			form := url.Values{"grant_type": {"authorization_code"}, "client_id": {ClientID}, "code": {value.Code}, "code_verifier": {value.Verifier}, "redirect_uri": {c.issuer + "/deviceauth/callback"}}
			return c.exchange(ctx, []byte(form.Encode()), "application/x-www-form-urlencoded", Credentials{})
		}
		var oauth *OAuthError
		if !errors.As(err, &oauth) {
			return Credentials{}, err
		}
		if oauth.Code == "access_denied" || oauth.Code == "expired_token" {
			return Credentials{}, err
		}
		if status != 403 && status != 404 && oauth.Code != "authorization_pending" && oauth.Code != "slow_down" {
			return Credentials{}, err
		}
		wait := device.Interval
		if oauth.Code == "slow_down" {
			device.Interval = min(device.Interval+5*time.Second, loginLifetime)
			wait = device.Interval
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Credentials{}, ctx.Err()
		case <-timer.C:
		}
	}
}
func (c *client) refresh(ctx context.Context, old Credentials, recovering bool) (Credentials, error) {
	reason := "proactive_expiry"
	if recovering {
		reason = "unauthorized_recovery"
	}
	raw, _ := json.Marshal(map[string]string{"grant_type": "refresh_token", "client_id": ClientID, "refresh_token": old.RefreshToken, "refresh_reason": reason})
	return c.exchange(ctx, raw, "application/json", old)
}
func (c *client) exchange(ctx context.Context, raw []byte, contentType string, previous Credentials) (Credentials, error) {
	operation := "token_exchange"
	if previous.AccessToken != "" {
		operation = "token_refresh"
	}
	var value struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		ID      string `json:"id_token"`
		Expires int64  `json:"expires_in"`
	}
	_, err := c.request(ctx, "/oauth/token", contentType, raw, &value)
	if err != nil {
		return Credentials{}, err
	}
	if !boundedString(value.Access, 65536) {
		return Credentials{}, c.invalidResponse(ctx, operation)
	}
	next := previous
	next.AccessToken = value.Access
	if value.Refresh != "" {
		next.RefreshToken = value.Refresh
	}
	if value.ID != "" {
		next.IDToken = value.ID
	}
	if !boundedString(next.AccessToken, 65536) || !boundedString(next.RefreshToken, 65536) || !boundedString(next.IDToken, 65536) {
		return Credentials{}, c.invalidResponse(ctx, operation)
	}
	// Tokens arrive only from the fixed TLS-authenticated issuer. Their
	// claims supply account routing, not local operator authorization.
	identity, err := claims(next.IDToken)
	if err != nil {
		return Credentials{}, c.invalidResponse(ctx, operation)
	}
	var auth struct {
		Account string `json:"chatgpt_account_id"`
	}
	if json.Unmarshal(identity.Auth, &auth) != nil || !boundedString(auth.Account, 512) || !boundedString(identity.Subject, 512) || len(identity.Email) > 512 {
		return Credentials{}, c.invalidResponse(ctx, operation)
	}
	if previous.AccountID != "" && (previous.AccountID != auth.Account || previous.Subject != identity.Subject) {
		return Credentials{}, c.invalidResponse(ctx, operation)
	}
	next.AccountID = auth.Account
	next.Subject = identity.Subject
	next.Label = identity.Email
	access, err := claims(next.AccessToken)
	if err == nil && access.Expires > c.now().Unix() {
		next.ExpiresAt = access.Expires
	} else if value.Expires > 0 && value.Expires <= 86400 {
		next.ExpiresAt = c.now().Unix() + value.Expires
	} else {
		return Credentials{}, c.invalidResponse(ctx, operation)
	}
	if next.ExpiresAt > c.now().Unix()+86400*30 {
		return Credentials{}, c.invalidResponse(ctx, operation)
	}
	return next, nil
}

type tokenClaims struct {
	Expires int64           `json:"exp"`
	Subject string          `json:"sub"`
	Email   string          `json:"email"`
	Auth    json.RawMessage `json:"https://api.openai.com/auth"`
}

func claims(token string) (tokenClaims, error) {
	var value tokenClaims
	parts := strings.Split(token, ".")
	if len(parts) != 3 || len(token) > 65536 {
		return value, ErrInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	defer clear(raw)
	if err != nil || json.Unmarshal(raw, &value) != nil {
		return value, ErrInvalid
	}
	return value, nil
}
func boundedString(s string, max int) bool {
	return s != "" && len(s) <= max && !strings.ContainsAny(s, "\x00\r\n")
}

// A successful HTTP exchange can still omit required credential fields.
// Report only the structural failure; OAuth response bytes are never retained.
func (c *client) invalidResponse(ctx context.Context, operation string) error {
	ctx = diagnostic.WithRecorder(ctx)
	diagnostic.RecordResponse(ctx, http.StatusOK, nil)
	diagnostic.RecordKnownError(ctx, "decode_response", "protocol", "invalid_auth_response", "Authentication response omitted required fields or changed account identity", ErrInvalid)
	failure := diagnostic.OperationFailure(ctx, operation, "Codex", ErrInvalid)
	diagnostic.SaveOperation(ctx, c.db, failure)
	return failure
}
