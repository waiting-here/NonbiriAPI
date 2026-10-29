// Package codexauth implements the fixed Codex device-code OAuth protocol.
package codexauth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

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
	return e.Status == http.StatusUnauthorized || e.Code == "invalid_grant" || e.Code == "refresh_token_expired" || e.Code == "refresh_token_reused" || e.Code == "refresh_token_invalidated"
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
	stack  *egress.Stack
	issuer string
	now    func() time.Time
}
type deviceCode struct {
	DeviceID, Code string
	Interval       time.Duration
}
type authorization struct {
	Code     string `json:"authorization_code"`
	Verifier string `json:"code_verifier"`
}

func (c *client) request(ctx context.Context, path, contentType string, body []byte, out any) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	defer clear(body)
	client, err := c.stack.NewClient(c.issuer)
	if err != nil {
		return 0, &OAuthError{Code: "connection_unavailable"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.issuer+path, bytes.NewReader(body))
	if err != nil {
		return 0, ErrInvalid
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		return 0, &OAuthError{Code: "connection_unavailable"}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxCredentials+1))
	defer clear(raw)
	if err != nil || len(raw) > maxCredentials {
		return resp.StatusCode, &OAuthError{Code: "invalid_response", Status: resp.StatusCode}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
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
		return resp.StatusCode, &OAuthError{Code: code, Status: resp.StatusCode}
	}
	if json.Unmarshal(raw, out) != nil {
		return resp.StatusCode, &OAuthError{Code: "invalid_response", Status: resp.StatusCode}
	}
	return resp.StatusCode, nil
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
		return deviceCode{}, ErrInvalid
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
			return deviceCode{}, ErrInvalid
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
				return Credentials{}, ErrInvalid
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
func (c *client) refresh(ctx context.Context, old Credentials) (Credentials, error) {
	raw, _ := json.Marshal(map[string]string{"grant_type": "refresh_token", "client_id": ClientID, "refresh_token": old.RefreshToken, "refresh_reason": "proactive_expiry"})
	return c.exchange(ctx, raw, "application/json", old)
}
func (c *client) exchange(ctx context.Context, raw []byte, contentType string, previous Credentials) (Credentials, error) {
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
		return Credentials{}, ErrInvalid
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
		return Credentials{}, ErrInvalid
	}
	// Tokens arrive only from the fixed TLS-authenticated issuer. Their
	// claims supply account routing, not local operator authorization.
	identity, err := claims(next.IDToken)
	if err != nil {
		return Credentials{}, err
	}
	var auth struct {
		Account string `json:"chatgpt_account_id"`
	}
	if json.Unmarshal(identity.Auth, &auth) != nil || !boundedString(auth.Account, 512) || !boundedString(identity.Subject, 512) || len(identity.Email) > 512 {
		return Credentials{}, ErrInvalid
	}
	if previous.AccountID != "" && (previous.AccountID != auth.Account || previous.Subject != identity.Subject) {
		return Credentials{}, ErrInvalid
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
		return Credentials{}, ErrInvalid
	}
	if next.ExpiresAt > c.now().Unix()+86400*30 {
		return Credentials{}, ErrInvalid
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
