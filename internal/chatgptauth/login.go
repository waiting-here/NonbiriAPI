package chatgptauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Login starts a short-lived loopback listener. openURL receives only a fresh
// authorization URL, never an access token, refresh token, or ID-token hint.
func (c *Client) Login(ctx context.Context, target Target, localHostID string, openURL func(string) error) (Envelope, error) {
	if target.Version != 1 || target.HostID == "" || !strings.HasPrefix(localHostID, "urn:uuid:") || len(localHostID) != 45 || openURL == nil {
		return Envelope{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return Envelope{}, err
	}
	defer listener.Close()
	redirect := "http://" + listener.Addr().String() + "/auth/callback"
	state, err := randomValue(32)
	if err != nil {
		return Envelope{}, err
	}
	nonce, err := randomValue(32)
	if err != nil {
		return Envelope{}, err
	}
	verifier, err := randomValue(32)
	if err != nil {
		return Envelope{}, err
	}
	challenge := sha256.Sum256([]byte(verifier))
	clientID := target.ClientID
	if clientID == "" {
		clientID = "dynamic_agent_client"
	}
	query := url.Values{"client_id": {clientID}, "ext_agent_host_id": {localHostID}, "response_type": {"code"}, "redirect_uri": {redirect}, "scope": {Scopes}, "resource": {Resource}, "state": {state}, "nonce": {nonce}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}}
	if clientID == "dynamic_agent_client" {
		query.Set("agent_name_hint", "NonbiriAPI")
	}
	type callback struct {
		Code, ClientID string
		Err            error
	}
	done := make(chan callback, 1)
	var once sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/callback", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if r.Host != listener.Addr().String() || len(r.URL.RawQuery) > 16384 || subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("state")), []byte(state)) != 1 {
			http.Error(w, "Invalid authorization response.", 400)
			return
		}
		result := callback{Code: r.URL.Query().Get("code"), ClientID: r.URL.Query().Get("client_id")}
		if r.URL.Query().Get("error") != "" {
			result.Err = &OAuthError{Code: "access_denied"}
		}
		if target.ClientID != "" {
			if result.ClientID != "" && result.ClientID != target.ClientID {
				result.Err = ErrInvalid
			}
			result.ClientID = target.ClientID
		}
		if result.Err == nil && (result.Code == "" || len(result.Code) > 8192 || result.ClientID == "" || result.ClientID == "dynamic_agent_client" || len(result.ClientID) > 256) {
			result.Err = ErrInvalid
		}
		accepted := false
		once.Do(func() { done <- result; accepted = true })
		if !accepted {
			http.Error(w, "Authorization already received.", 409)
			return
		}
		_, _ = w.Write([]byte("Authorization received. Return to the local tool to finish."))
	})
	server := http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, MaxHeaderBytes: 16384}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	defer server.Close()
	if err = openURL(c.issuer + "/api/accounts/authorize?" + query.Encode()); err != nil {
		return Envelope{}, err
	}
	var result callback
	select {
	case <-ctx.Done():
		return Envelope{}, ctx.Err()
	case err = <-served:
		return Envelope{}, err
	case result = <-done:
	}
	if result.Err != nil {
		return Envelope{}, result.Err
	}
	creds, err := c.exchange(ctx, url.Values{"grant_type": {"authorization_code"}, "client_id": {result.ClientID}, "code": {result.Code}, "code_verifier": {verifier}, "redirect_uri": {redirect}, "resource": {Resource}}, Credentials{ClientID: result.ClientID, Nonce: nonce})
	if err != nil {
		return Envelope{}, err
	}
	creds.Subject, creds.Label, err = c.Validate(ctx, creds.IDToken, creds.ClientID, nonce)
	if err != nil {
		return Envelope{}, err
	}
	if target.Subject != "" && target.Subject != creds.Subject {
		return Envelope{}, ErrInvalid
	}
	return Protect(target, creds)
}
