package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// DefaultDiscordOAuthScopes is an operational default only. Deployments can
	// override it through startup configuration while the public scope policy
	// remains outside the users schema.
	DefaultDiscordOAuthScopes = "identify guilds.members.read"
	defaultDiscordAPIBase     = "https://discord.com/api"
	defaultDiscordAuthorize   = "https://discord.com/oauth2/authorize"
	maxDiscordResponseBytes   = 64 * 1024
	maxDiscordTokenBytes      = 4096
	maxDiscordRoleIDs         = 512
)

// HTTPDiscordProvider is a bounded Discord OAuth implementation behind the
// narrow DiscordProvider interface. Access tokens live only in the callback
// stack and transient membership closure.
type HTTPDiscordProvider struct {
	client            *http.Client
	clientID          string
	clientSecret      string
	scopes            string
	apiBase           string
	authorizeEndpoint string
	tokenEndpoint     string
}

// HTTPDiscordProviderConfig contains fixed provider endpoints and startup
// credentials. BaseURL is useful for a local fake server in tests; production
// should use Discord's HTTPS endpoints.
type HTTPDiscordProviderConfig struct {
	ClientID          string
	ClientSecret      string
	Scopes            string
	APIBaseURL        string
	AuthorizeEndpoint string
	TokenEndpoint     string
	HTTPClient        *http.Client
}

// NewHTTPDiscordProvider validates provider endpoints and returns an adapter.
// It never stores a user access/refresh token.
func NewHTTPDiscordProvider(config HTTPDiscordProviderConfig) (*HTTPDiscordProvider, error) {
	if !validateBoundedText(config.ClientID, 512, false) || !validateBoundedText(config.ClientSecret, 4096, false) {
		return nil, ErrProviderUnavailable
	}
	if strings.TrimSpace(config.Scopes) == "" {
		config.Scopes = DefaultDiscordOAuthScopes
	}
	if !validateBoundedText(config.Scopes, 256, false) {
		return nil, ErrProviderUnavailable
	}
	apiBase := strings.TrimRight(strings.TrimSpace(config.APIBaseURL), "/")
	if apiBase == "" {
		apiBase = defaultDiscordAPIBase
	}
	authorizeEndpoint := strings.TrimSpace(config.AuthorizeEndpoint)
	if authorizeEndpoint == "" {
		authorizeEndpoint = defaultDiscordAuthorize
	}
	tokenEndpoint := strings.TrimSpace(config.TokenEndpoint)
	if tokenEndpoint == "" {
		tokenEndpoint = apiBase + "/oauth2/token"
	}
	for _, endpoint := range []string{apiBase, authorizeEndpoint, tokenEndpoint} {
		if err := validateProviderEndpoint(endpoint); err != nil {
			return nil, ErrProviderUnavailable
		}
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	// Never follow a provider redirect with an OAuth bearer token in the
	// request. A caller-supplied client is copied so its transport and timeout
	// remain useful while its redirect policy is fail-closed.
	copyClient := *client
	if copyClient.Timeout <= 0 || copyClient.Timeout > 30*time.Second {
		copyClient.Timeout = 15 * time.Second
	}
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	client = &copyClient
	return &HTTPDiscordProvider{
		client: client, clientID: config.ClientID, clientSecret: config.ClientSecret,
		scopes: config.Scopes, apiBase: apiBase, authorizeEndpoint: authorizeEndpoint,
		tokenEndpoint: tokenEndpoint,
	}, nil
}

func validateProviderEndpoint(raw string) error {
	if !validateBoundedText(raw, 4096, false) {
		return ErrProviderUnavailable
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return ErrProviderUnavailable
	}
	return nil
}

func (p *HTTPDiscordProvider) AuthorizationURL(_ context.Context, request DiscordAuthorizeRequest) (string, error) {
	if p == nil || !validateOAuthStateText(request.State) || !validateBoundedText(request.RedirectURI, 2048, false) || !validateIntent(request.Intent) {
		return "", ErrProviderUnavailable
	}
	values := url.Values{}
	values.Set("client_id", p.clientID)
	values.Set("redirect_uri", request.RedirectURI)
	values.Set("response_type", "code")
	values.Set("scope", p.scopes)
	values.Set("state", request.State)
	return p.authorizeEndpoint + "?" + values.Encode(), nil
}

func (p *HTTPDiscordProvider) Exchange(ctx context.Context, code, redirectURI string) (DiscordLogin, error) {
	if p == nil || p.client == nil || ctx == nil || !validateOAuthCode(code) || !validateBoundedText(redirectURI, 2048, false) {
		return DiscordLogin{}, ErrProviderUnauthorized
	}
	form := url.Values{}
	form.Set("client_id", p.clientID)
	form.Set("client_secret", p.clientSecret)
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return DiscordLogin{}, ErrProviderUnavailable
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.client.Do(req)
	if err != nil || resp == nil {
		return DiscordLogin{}, ErrProviderUnavailable
	}
	var tokenResponse struct {
		AccessToken string `json:"access_token"`
	}
	decodeErr := decodeDiscordJSON(resp, &tokenResponse)
	if decodeErr != nil {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusBadRequest {
			return DiscordLogin{}, ErrProviderUnauthorized
		}
		return DiscordLogin{}, ErrProviderUnavailable
	}
	if resp.StatusCode != http.StatusOK || !validateBoundedText(tokenResponse.AccessToken, maxDiscordTokenBytes, false) {
		return DiscordLogin{}, ErrProviderUnauthorized
	}
	accessToken := tokenResponse.AccessToken
	identity, err := p.fetchIdentity(ctx, accessToken)
	if err != nil {
		if errors.Is(err, ErrProviderUnauthorized) {
			return DiscordLogin{}, err
		}
		return DiscordLogin{}, ErrProviderUnavailable
	}
	return DiscordLogin{
		Identity: identity,
		HasGuildRole: func(memberCtx context.Context, guildID, roleID string) (bool, error) {
			return p.fetchGuildRole(memberCtx, accessToken, guildID, roleID)
		},
	}, nil
}

func (p *HTTPDiscordProvider) fetchIdentity(ctx context.Context, accessToken string) (DiscordIdentity, error) {
	var response struct {
		ID         string `json:"id"`
		Username   string `json:"username"`
		GlobalName string `json:"global_name"`
		Avatar     string `json:"avatar"`
	}
	status, err := p.getJSON(ctx, "/users/@me", accessToken, &response)
	if err != nil {
		return DiscordIdentity{}, ErrProviderUnavailable
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return DiscordIdentity{}, ErrProviderUnauthorized
	}
	if status != http.StatusOK || !validateBoundedText(response.ID, 128, false) {
		return DiscordIdentity{}, ErrProviderUnauthorized
	}
	username := response.GlobalName
	if username == "" {
		username = response.Username
	}
	if !validateBoundedText(username, maxUsernameBytes, false) || !validateBoundedText(response.Avatar, 1024, true) {
		return DiscordIdentity{}, ErrInvalidIdentity
	}
	return DiscordIdentity{ID: response.ID, Username: username, GlobalName: response.GlobalName, Avatar: response.Avatar}, nil
}

func (p *HTTPDiscordProvider) fetchGuildRole(ctx context.Context, accessToken, guildID, roleID string) (bool, error) {
	if !validateBoundedText(guildID, 128, false) || !validateBoundedText(roleID, 128, false) {
		return false, ErrGuildRoleMismatch
	}
	var response struct {
		Roles []string `json:"roles"`
	}
	path := "/users/@me/guilds/" + url.PathEscape(guildID) + "/member"
	status, err := p.getJSON(ctx, path, accessToken, &response)
	if err != nil {
		return false, ErrProviderUnavailable
	}
	if status == http.StatusNotFound || status == http.StatusForbidden || status == http.StatusUnauthorized {
		return false, nil
	}
	if status != http.StatusOK {
		return false, ErrProviderUnavailable
	}
	if len(response.Roles) > maxDiscordRoleIDs {
		return false, ErrInvalidIdentity
	}
	for _, role := range response.Roles {
		if !validateBoundedText(role, 128, false) {
			return false, ErrInvalidIdentity
		}
		if role == roleID {
			return true, nil
		}
	}
	return false, nil
}

func (p *HTTPDiscordProvider) getJSON(ctx context.Context, path, accessToken string, dst any) (int, error) {
	if p == nil || p.client == nil || ctx == nil || !validateBoundedText(path, 512, false) || !validateBoundedText(accessToken, maxDiscordTokenBytes, false) {
		return 0, ErrProviderUnauthorized
	}
	// #nosec G704 -- apiBase is startup-validated HTTPS (and production uses the
	// hard-coded Discord origin); every path is an internal constant plus
	// PathEscape output, never an arbitrary request URL.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.apiBase+path, nil)
	if err != nil {
		return 0, ErrProviderUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	// #nosec G704 -- the validated fixed provider origin above is the only
	// destination and the provider client rejects every redirect.
	resp, err := p.client.Do(req)
	if err != nil || resp == nil {
		return 0, ErrProviderUnavailable
	}
	if resp.Body == nil {
		return resp.StatusCode, nil
	}
	defer resp.Body.Close()
	data, err := readDiscordBody(resp.Body)
	if err != nil {
		return resp.StatusCode, ErrProviderUnavailable
	}
	defer clear(data)
	if len(data) == 0 {
		return resp.StatusCode, nil
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return resp.StatusCode, ErrProviderUnavailable
	}
	return resp.StatusCode, nil
}

func decodeDiscordJSON(resp *http.Response, dst any) error {
	if resp == nil || resp.Body == nil {
		return ErrProviderUnavailable
	}
	defer resp.Body.Close()
	data, err := readDiscordBody(resp.Body)
	if err != nil {
		return ErrProviderUnavailable
	}
	defer clear(data)
	if len(data) == 0 {
		return fmt.Errorf("empty provider response")
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return ErrProviderUnavailable
	}
	return nil
}

func readDiscordBody(body io.Reader) ([]byte, error) {
	if body == nil {
		return nil, ErrProviderUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(body, maxDiscordResponseBytes+1))
	if err != nil {
		return nil, ErrProviderUnavailable
	}
	if len(data) > maxDiscordResponseBytes {
		clear(data)
		return nil, ErrProviderUnavailable
	}
	return data, nil
}
