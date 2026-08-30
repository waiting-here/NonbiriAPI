package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultDiscordOAuthScopes = "identify guilds.members.read"
	defaultDiscordAPIBase     = "https://discord.com/api"
	defaultDiscordAuthorize   = "https://discord.com/oauth2/authorize"
	maxDiscordResponseBytes   = 64 * 1024
	maxDiscordTokenBytes      = 4096
	maxDiscordRoleIDs         = 512
	discordCDNBase            = "https://cdn.discordapp.com"
)

type HTTPDiscordProvider struct {
	client                                                                    *http.Client
	clientID, clientSecret, scopes, apiBase, authorizeEndpoint, tokenEndpoint string
}
type HTTPDiscordProviderConfig struct {
	ClientID, ClientSecret, Scopes, APIBaseURL, AuthorizeEndpoint, TokenEndpoint string
	HTTPClient                                                                   *http.Client
}

func NewHTTPDiscordProvider(c HTTPDiscordProviderConfig) (*HTTPDiscordProvider, error) {
	if !validateBoundedText(c.ClientID, 512, false) || !validateBoundedText(c.ClientSecret, 4096, false) {
		return nil, ErrProviderUnavailable
	}
	if strings.TrimSpace(c.Scopes) == "" {
		c.Scopes = DefaultDiscordOAuthScopes
	}
	if !validateBoundedText(c.Scopes, 256, false) {
		return nil, ErrProviderUnavailable
	}
	api := strings.TrimRight(strings.TrimSpace(c.APIBaseURL), "/")
	if api == "" {
		api = defaultDiscordAPIBase
	}
	authEP := strings.TrimSpace(c.AuthorizeEndpoint)
	if authEP == "" {
		authEP = defaultDiscordAuthorize
	}
	tokenEP := strings.TrimSpace(c.TokenEndpoint)
	if tokenEP == "" {
		tokenEP = api + "/oauth2/token"
	}
	for _, ep := range []string{api, authEP, tokenEP} {
		if validateProviderEndpoint(ep) != nil {
			return nil, ErrProviderUnavailable
		}
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	copyClient := *client
	if copyClient.Timeout <= 0 || copyClient.Timeout > 30*time.Second {
		copyClient.Timeout = 15 * time.Second
	}
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &HTTPDiscordProvider{client: &copyClient, clientID: c.ClientID, clientSecret: c.ClientSecret, scopes: c.Scopes, apiBase: api, authorizeEndpoint: authEP, tokenEndpoint: tokenEP}, nil
}

func validateProviderEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || !validateBoundedText(raw, 4096, false) || !strings.EqualFold(u.Scheme, "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return ErrProviderUnavailable
	}
	return nil
}

func (p *HTTPDiscordProvider) AuthorizationURL(_ context.Context, r DiscordAuthorizeRequest) (string, error) {
	if p == nil || !validateOAuthStateText(r.State) || !validateBoundedText(r.RedirectURI, 2048, false) || !validateIntent(r.Intent) {
		return "", ErrProviderUnavailable
	}
	v := url.Values{}
	v.Set("client_id", p.clientID)
	v.Set("redirect_uri", r.RedirectURI)
	v.Set("response_type", "code")
	v.Set("scope", p.scopes)
	v.Set("state", r.State)
	return p.authorizeEndpoint + "?" + v.Encode(), nil
}

func (p *HTTPDiscordProvider) Exchange(ctx context.Context, code, redirectURI string) (DiscordLogin, error) {
	if p == nil || p.client == nil || ctx == nil || !validateOAuthCode(code) || !validateBoundedText(redirectURI, 2048, false) {
		return DiscordLogin{}, ErrProviderUnauthorized
	}
	v := url.Values{}
	v.Set("client_id", p.clientID)
	v.Set("client_secret", p.clientSecret)
	v.Set("grant_type", "authorization_code")
	v.Set("code", code)
	v.Set("redirect_uri", redirectURI)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.tokenEndpoint, strings.NewReader(v.Encode()))
	if err != nil {
		return DiscordLogin{}, ErrProviderUnavailable
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.client.Do(req)
	if err != nil || resp == nil {
		return DiscordLogin{}, ErrProviderUnavailable
	}
	var token struct {
		AccessToken string `json:"access_token"`
	}
	decodeErr := decodeDiscordJSON(resp, &token)
	if decodeErr != nil {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusBadRequest {
			return DiscordLogin{}, ErrProviderUnauthorized
		}
		return DiscordLogin{}, ErrProviderUnavailable
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusBadRequest {
		return DiscordLogin{}, ErrProviderUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return DiscordLogin{}, ErrProviderUnavailable
	}
	if !validateBoundedText(token.AccessToken, maxDiscordTokenBytes, false) {
		return DiscordLogin{}, ErrProviderUnauthorized
	}
	identity, err := p.fetchIdentity(ctx, token.AccessToken)
	if err != nil {
		return DiscordLogin{}, err
	}
	access := token.AccessToken
	return DiscordLogin{Identity: identity, GuildMember: func(c context.Context, g string) (GuildMember, error) { return p.fetchGuildMember(c, access, g) }}, nil
}

func (p *HTTPDiscordProvider) fetchIdentity(ctx context.Context, token string) (DiscordIdentity, error) {
	var out struct {
		ID, Username string
		GlobalName   string `json:"global_name"`
		Avatar       string `json:"avatar"`
	}
	status, err := p.getJSON(ctx, "/users/@me", token, &out)
	if err != nil {
		return DiscordIdentity{}, ErrProviderUnavailable
	}
	if status == 401 || status == 403 {
		return DiscordIdentity{}, ErrProviderUnauthorized
	}
	if status != 200 {
		return DiscordIdentity{}, ErrProviderUnavailable
	}
	if !validateBoundedText(out.ID, 128, false) {
		return DiscordIdentity{}, ErrProviderUnauthorized
	}
	name := out.GlobalName
	if name == "" {
		name = out.Username
	}
	if !validateBoundedText(name, maxUsernameBytes, false) || !validateBoundedText(out.Avatar, 1024, true) {
		return DiscordIdentity{}, ErrInvalidIdentity
	}
	return DiscordIdentity{ID: out.ID, Username: name, GlobalName: out.GlobalName, Avatar: out.Avatar}, nil
}

func (p *HTTPDiscordProvider) fetchGuildMember(ctx context.Context, token, guild string) (GuildMember, error) {
	if !validateBoundedText(guild, 128, false) {
		return GuildMember{}, ErrGuildRoleMismatch
	}
	var out struct {
		Roles  []string `json:"roles"`
		Nick   string   `json:"nick"`
		Avatar string   `json:"avatar"`
	}
	status, err := p.getJSON(ctx, "/users/@me/guilds/"+url.PathEscape(guild)+"/member", token, &out)
	if err != nil {
		return GuildMember{}, ErrProviderUnavailable
	}
	if status == 401 || status == 403 || status == 404 {
		return GuildMember{}, nil
	}
	if status != 200 {
		return GuildMember{}, ErrProviderUnavailable
	}
	if len(out.Roles) > maxDiscordRoleIDs || !validateBoundedText(out.Nick, maxUsernameBytes, true) || !validateBoundedText(out.Avatar, 1024, true) {
		return GuildMember{}, ErrInvalidIdentity
	}
	for _, r := range out.Roles {
		if !validateBoundedText(r, 128, false) {
			return GuildMember{}, ErrInvalidIdentity
		}
	}
	return GuildMember{Nick: out.Nick, Avatar: out.Avatar, Roles: out.Roles}, nil
}

func (p *HTTPDiscordProvider) getJSON(ctx context.Context, path, token string, dst any) (int, error) {
	if p == nil || ctx == nil || !validateBoundedText(path, 512, false) || !validateBoundedText(token, maxDiscordTokenBytes, false) {
		return 0, ErrProviderUnauthorized
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.apiBase+path, nil)
	if err != nil {
		return 0, ErrProviderUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+token)
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
		return resp.StatusCode, err
	}
	defer clear(data)
	if len(data) > 0 && json.Unmarshal(data, dst) != nil {
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
		return err
	}
	defer clear(data)
	if len(data) == 0 {
		return fmt.Errorf("empty provider response")
	}
	if json.Unmarshal(data, dst) != nil {
		return ErrProviderUnavailable
	}
	return nil
}
func readDiscordBody(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, ErrProviderUnavailable
	}
	b, err := io.ReadAll(io.LimitReader(r, maxDiscordResponseBytes+1))
	if err != nil || len(b) > maxDiscordResponseBytes {
		clear(b)
		return nil, ErrProviderUnavailable
	}
	return b, nil
}

func discordAvatarURL(id, hash string) *string {
	if id == "" || hash == "" {
		return nil
	}
	ext := ".png"
	if strings.HasPrefix(hash, "a_") {
		ext = ".gif"
	}
	v := discordCDNBase + "/avatars/" + id + "/" + hash + ext + "?size=64"
	return &v
}
func discordGuildAvatarURL(guild, id, hash string) *string {
	if guild == "" || id == "" || hash == "" {
		return nil
	}
	ext := ".png"
	if strings.HasPrefix(hash, "a_") {
		ext = ".gif"
	}
	v := discordCDNBase + "/guilds/" + guild + "/users/" + id + "/avatars/" + hash + ext + "?size=64"
	return &v
}
