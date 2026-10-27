// Package chatgptauth implements this application's Sign in with ChatGPT flow.
package chatgptauth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/egress"
)

const Resource = "https://api.openai.com/v1"
const Scopes = "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct"
const maxCredentialBytes = 128 << 10

var (
	ErrInvalid      = errors.New("chatgpt: invalid authorization material")
	ErrDisconnected = errors.New("chatgpt: sign in required")
	ErrConflict     = errors.New("chatgpt: connection changed")
)

type OAuthError struct {
	Code   string
	Status int
}

func (e *OAuthError) Error() string { return "chatgpt: " + e.Code }
func (e *OAuthError) Terminal() bool {
	switch e.Code {
	case "invalid_grant", "invalid_refresh_token", "token_expired", "refresh_token_expired", "refresh_token_invalidated", "refresh_token_reused":
		return true
	}
	return false
}

type Credentials struct {
	ClientID     string `json:"client_id"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	Scope        string `json:"scope"`
	ExpiresAt    int64  `json:"expires_at"`
	Nonce        string `json:"nonce"`
	Subject      string `json:"subject"`
	Label        string `json:"label"`
}
type Client struct {
	stack  *egress.Stack
	issuer string
	now    func() time.Time
}

func NewClient(stack *egress.Stack) *Client {
	return &Client{stack: stack, issuer: "https://auth.openai.com", now: time.Now}
}
func randomValue(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func hasScopes(scope string) bool {
	set := map[string]bool{}
	for _, v := range strings.Fields(scope) {
		set[v] = true
	}
	return set["openid"] && set["offline_access"] && set["resource.invoke"] && set["chatgpt.tokens.use.direct"]
}
func validCredentials(c Credentials) bool {
	return c.ClientID != "" && c.ClientID != "dynamic_agent_client" && len(c.ClientID) <= 256 && len(c.AccessToken) > 0 && len(c.AccessToken) <= 65536 && !strings.ContainsAny(c.AccessToken, "\r\n") && len(c.RefreshToken) > 0 && len(c.RefreshToken) <= 65536 && len(c.IDToken) > 0 && len(c.IDToken) <= 65536 && hasScopes(c.Scope) && c.ExpiresAt > 0 && len(c.Nonce) >= 20
}
func (c *Client) request(ctx context.Context, method, target string, form url.Values, out any) error {
	if c == nil || c.stack == nil {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client, err := c.stack.NewClient(c.issuer)
	if err != nil {
		return err
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return ErrInvalid
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		return &OAuthError{Code: "connection_unavailable"}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if err != nil || len(raw) > 1<<20 {
		return &OAuthError{Code: "invalid_response", Status: resp.StatusCode}
	}
	defer clear(raw)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &failure)
		code := "authorization_failed"
		switch failure.Error {
		case "invalid_grant", "invalid_client", "invalid_refresh_token", "token_expired", "refresh_token_expired", "refresh_token_invalidated", "refresh_token_reused", "access_denied":
			code = failure.Error
		}
		return &OAuthError{Code: code, Status: resp.StatusCode}
	}
	if out != nil && json.Unmarshal(raw, out) != nil {
		return &OAuthError{Code: "invalid_response", Status: resp.StatusCode}
	}
	return nil
}

type metadata struct {
	Issuer     string `json:"issuer"`
	JWKS       string `json:"jwks_uri"`
	Revocation string `json:"revocation_endpoint"`
}

func (c *Client) metadata(ctx context.Context) (metadata, error) {
	var m metadata
	if err := c.request(ctx, "GET", c.issuer+"/.well-known/openid-configuration", nil, &m); err != nil {
		return m, err
	}
	if m.Issuer != c.issuer {
		return m, ErrInvalid
	}
	for _, v := range []string{m.JWKS, m.Revocation} {
		u, e := url.Parse(v)
		if e != nil || u.Scheme+"://"+u.Host != c.issuer || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return m, ErrInvalid
		}
	}
	return m, nil
}

type identity struct {
	Subject         string          `json:"sub"`
	Issuer          string          `json:"iss"`
	Audience        json.RawMessage `json:"aud"`
	AuthorizedParty string          `json:"azp"`
	Expires         int64           `json:"exp"`
	Issued          int64           `json:"iat"`
	Nonce           string          `json:"nonce"`
	Email           string          `json:"email"`
}

func (c *Client) Validate(ctx context.Context, token, clientID, nonce string) (string, string, error) {
	if len(token) > 65536 {
		return "", "", ErrInvalid
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", "", ErrInvalid
	}
	decode := base64.RawURLEncoding.DecodeString
	h, err := decode(parts[0])
	if err != nil {
		return "", "", ErrInvalid
	}
	var header struct {
		Alg string `json:"alg"`
		KID string `json:"kid"`
	}
	if json.Unmarshal(h, &header) != nil || header.KID == "" || header.Alg != "RS256" && header.Alg != "ES256" {
		return "", "", ErrInvalid
	}
	p, err := decode(parts[1])
	if err != nil {
		return "", "", ErrInvalid
	}
	defer clear(p)
	var claims identity
	if json.Unmarshal(p, &claims) != nil {
		return "", "", ErrInvalid
	}
	var aud string
	var audiences []string
	if json.Unmarshal(claims.Audience, &aud) == nil {
		audiences = []string{aud}
	} else if json.Unmarshal(claims.Audience, &audiences) != nil {
		return "", "", ErrInvalid
	}
	matches := false
	for _, a := range audiences {
		if a == clientID {
			matches = true
		}
	}
	if !matches || clientID == "" || clientID == "dynamic_agent_client" || claims.Issuer != c.issuer || claims.Subject == "" || len(claims.Subject) > 512 || claims.Nonce != nonce || nonce == "" || claims.Expires <= c.now().Unix() || claims.Issued <= 0 || claims.Issued > c.now().Unix()+60 {
		return "", "", ErrInvalid
	}
	if (len(audiences) > 1 || claims.AuthorizedParty != "") && claims.AuthorizedParty != clientID {
		return "", "", ErrInvalid
	}
	m, err := c.metadata(ctx)
	if err != nil {
		return "", "", err
	}
	var keys struct {
		Keys []struct {
			KTY string `json:"kty"`
			KID string `json:"kid"`
			Alg string `json:"alg"`
			Use string `json:"use"`
			N   string `json:"n"`
			E   string `json:"e"`
			CRV string `json:"crv"`
			X   string `json:"x"`
			Y   string `json:"y"`
		} `json:"keys"`
	}
	if err = c.request(ctx, "GET", m.JWKS, nil, &keys); err != nil {
		return "", "", err
	}
	if len(keys.Keys) > 100 {
		return "", "", ErrInvalid
	}
	sig, err := decode(parts[2])
	if err != nil {
		return "", "", ErrInvalid
	}
	hash := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	valid := false
	for _, key := range keys.Keys {
		if key.KID != header.KID || key.Alg != "" && key.Alg != header.Alg || key.Use != "" && key.Use != "sig" {
			continue
		}
		if header.Alg == "RS256" && key.KTY == "RSA" {
			n, en := decode(key.N)
			e, ee := decode(key.E)
			if en != nil || ee != nil || len(n) < 256 || len(n) > 1024 || len(e) < 1 || len(e) > 4 {
				continue
			}
			exp := 0
			for _, v := range e {
				exp = exp<<8 + int(v)
			}
			if exp < 3 || exp%2 == 0 {
				continue
			}
			valid = rsa.VerifyPKCS1v15(&rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exp}, crypto.SHA256, hash[:], sig) == nil
		}
		if header.Alg == "ES256" && key.KTY == "EC" && key.CRV == "P-256" && len(sig) == 64 {
			x, ex := decode(key.X)
			y, ey := decode(key.Y)
			if ex != nil || ey != nil || len(x) != 32 || len(y) != 32 {
				continue
			}
			pk := ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
			if pk.Curve.IsOnCurve(pk.X, pk.Y) {
				valid = ecdsa.Verify(&pk, hash[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:]))
			}
		}
		if valid {
			break
		}
	}
	if !valid {
		return "", "", ErrInvalid
	}
	label := claims.Email
	if len(label) > 512 {
		label = ""
	}
	return claims.Subject, label, nil
}
func (c *Client) exchange(ctx context.Context, form url.Values, previous Credentials) (Credentials, error) {
	var response struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		ID      string `json:"id_token"`
		Type    string `json:"token_type"`
		Scope   string `json:"scope"`
		Expires int64  `json:"expires_in"`
	}
	if err := c.request(ctx, "POST", c.issuer+"/api/accounts/oauth/token", form, &response); err != nil {
		return Credentials{}, err
	}
	if !strings.EqualFold(response.Type, "bearer") || response.Expires < 1 || response.Expires > 86400 {
		return Credentials{}, ErrInvalid
	}
	out := previous
	out.AccessToken = response.Access
	out.RefreshToken = response.Refresh
	out.ExpiresAt = c.now().Unix() + response.Expires
	if response.ID != "" {
		out.IDToken = response.ID
	}
	if response.Scope != "" {
		out.Scope = response.Scope
	}
	if !validCredentials(out) {
		return Credentials{}, ErrInvalid
	}
	return out, nil
}
func (c *Client) Refresh(ctx context.Context, old Credentials) (Credentials, error) {
	return c.exchange(ctx, url.Values{"grant_type": {"refresh_token"}, "client_id": {old.ClientID}, "refresh_token": {old.RefreshToken}, "resource": {Resource}}, old)
}
func (c *Client) Revoke(ctx context.Context, old Credentials) error {
	m, err := c.metadata(ctx)
	if err != nil {
		return err
	}
	return c.request(ctx, "POST", m.Revocation, url.Values{"token": {old.RefreshToken}, "token_type_hint": {"refresh_token"}, "client_id": {old.ClientID}}, nil)
}
