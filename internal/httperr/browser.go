package httperr

import (
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"html/template"
	"mime"
	"net/http"
	"strconv"
	"strings"
)

// BrowserPageOptions contains trusted site configuration, never request URLs.
type BrowserPageOptions struct {
	Styles  string
	HomeURL string
	Admin   bool
}

// BrowserReason refines presentation without changing an API error or status.
type BrowserReason uint8

const (
	BrowserReasonNone BrowserReason = iota
	BrowserReasonOAuthStart
	BrowserReasonOAuthCallback
	BrowserReasonOAuthState
	BrowserReasonDiscordRejected
	BrowserReasonDiscordUnavailable
	BrowserReasonDiscordGate
	BrowserReasonDiscordIdentity
	BrowserReasonWrongStation
)

//go:embed browser/page.html
var browserHTML string

//go:embed browser/page.css
var browserCSS string

//go:embed browser/page.js
var browserJS string

var (
	browserTemplate   = template.Must(template.New("browser-error").Parse(browserHTML))
	browserScriptHash = func() string {
		digest := sha256.Sum256([]byte(browserJS))
		return "'sha256-" + base64.StdEncoding.EncodeToString(digest[:]) + "'"
	}()
)

// WithBrowserErrors enables HTML only for document requests. It does not buffer
// responses or change successful, fetched, or public inference responses.
func WithBrowserErrors(w http.ResponseWriter, r *http.Request, options BrowserPageOptions) http.ResponseWriter {
	if !browserDocument(r) {
		return w
	}
	if options.HomeURL == "" {
		options.HomeURL = "/"
	}
	return &browserWriter{ResponseWriter: w, options: options, head: r.Method == http.MethodHead}
}

func browserDocument(r *http.Request) bool {
	if r == nil || r.URL == nil || r.URL.Path == "/v1" || strings.HasPrefix(r.URL.Path, "/v1/") ||
		r.Header.Get("X-Requested-With") != "" {
		return false
	}
	htmlQuality, jsonQuality := 0.0, 0.0
	accept := strings.Join(r.Header.Values("Accept"), ",")
	if len(accept) > 4096 {
		return false
	}
	for _, entry := range strings.Split(accept, ",") {
		mediaType, parameters, err := mime.ParseMediaType(strings.TrimSpace(entry))
		if err != nil {
			continue
		}
		quality := 1.0
		if raw, ok := parameters["q"]; ok {
			quality, err = strconv.ParseFloat(raw, 64)
			if err != nil || !(quality >= 0 && quality <= 1) {
				continue
			}
		}
		switch mediaType {
		case "text/html":
			htmlQuality = max(htmlQuality, quality)
		case "application/json", "application/*":
			jsonQuality = max(jsonQuality, quality)
		default:
			if strings.HasPrefix(mediaType, "application/") && strings.HasSuffix(mediaType, "+json") {
				jsonQuality = max(jsonQuality, quality)
			}
		}
	}
	if htmlQuality == 0 || jsonQuality >= htmlQuality {
		return false
	}
	if _, present := r.Header["Sec-Fetch-Dest"]; present {
		return r.Header.Get("Sec-Fetch-Dest") == "document" && r.Header.Get("Sec-Fetch-Mode") == "navigate"
	}
	// Older browsers do not send Fetch Metadata. An explicit HTML preference
	// is required; wildcard-only clients retain the JSON contract.
	return r.Header.Get("Sec-Fetch-Mode") == "" && (r.Method == http.MethodGet || r.Method == http.MethodHead)
}

type browserWriter struct {
	http.ResponseWriter
	options BrowserPageOptions
	reason  BrowserReason
	head    bool
}

func (w *browserWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *browserWriter) Flush()                      { _ = http.NewResponseController(w.ResponseWriter).Flush() }

// SetBrowserReason accepts only internal semantic classifications. It is a no-op
// for JSON clients; provider text, callback URLs and diagnostics are never used.
func SetBrowserReason(w http.ResponseWriter, reason BrowserReason) {
	if target := browserResponse(w); target != nil {
		target.reason = reason
	}
}

// WriteBrowserError upgrades a legacy plain-text response only for document
// requests. A false result leaves headers and body untouched for its caller.
func WriteBrowserError(w http.ResponseWriter, e Error) bool {
	if browserResponse(w) == nil {
		return false
	}
	WriteError(w, e)
	return true
}

// BrowserErrorsEnabled reports whether this response was negotiated as an
// HTML document, including through API response-writer wrappers.
func BrowserErrorsEnabled(w http.ResponseWriter) bool { return browserResponse(w) != nil }

func browserResponse(w http.ResponseWriter) *browserWriter {
	for w != nil {
		if target, ok := w.(*browserWriter); ok {
			return target
		}
		wrapper, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			break
		}
		w = wrapper.Unwrap()
	}
	return nil
}

type browserCopy struct{ Zh, En string }
type browserPage struct {
	Language                  string
	Status                    int
	Title, Body, Action, Home browserCopy
	ActionURL, HomeURL        string
	Styles                    template.CSS
	Script                    template.JS
}

func writeBrowserError(w http.ResponseWriter, e Error, status int) bool {
	target := browserResponse(w)
	if target == nil {
		return false
	}
	page := browserPageFor(e.Code, target.reason, target.options)
	page.Language, page.Status = Language(w), status
	// These are embedded resources and trusted startup configuration. No
	// request data or error text can enter either executable context.
	page.Styles = template.CSS(target.options.Styles + "\n" + browserCSS)
	page.Script = template.JS(browserJS)
	policy := w.Header().Get("Content-Security-Policy")
	if policy == "" {
		policy = "default-src 'none'; script-src " + browserScriptHash + "; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"
	} else {
		policy = strings.Replace(policy, "script-src 'self'", "script-src 'self' "+browserScriptHash, 1)
	}
	w.Header().Set("Content-Security-Policy", policy)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Language", page.Language)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Del("Content-Length")
	w.WriteHeader(status)
	if !target.head {
		_ = browserTemplate.Execute(w, page)
	}
	return true
}

func browserPageFor(code string, reason BrowserReason, options BrowserPageOptions) browserPage {
	page := browserPage{
		Title:  browserCopy{"页面暂时无法打开", "We could not open this page"},
		Body:   browserCopy{"请返回站点，稍后重试。如问题持续，请联系站点管理员。", "Return to the site and try again shortly. If the problem continues, contact the site administrator."},
		Action: browserCopy{"返回首页", "Return home"}, Home: browserCopy{"返回首页", "Return home"},
		ActionURL: options.HomeURL, HomeURL: options.HomeURL,
	}
	switch code {
	case CodeUnauthorized:
		page.Title = browserCopy{"请重新登录", "Please sign in again"}
		page.Body = browserCopy{"无法确认本次登录。请返回站点，重新登录后继续。", "We could not verify your sign-in. Return to the site and sign in again to continue."}
		page.Action = browserCopy{"返回登录", "Return to sign in"}
	case CodeForbidden, CodeStewardPermissionDenied:
		page.Title = browserCopy{"无法访问此页面", "You cannot access this page"}
		page.Body = browserCopy{"当前账号或请求来源无法访问此页面。请从本站首页重新进入；如仍无法访问，请联系站点管理员。", "Your account or request origin cannot access this page. Open it again from the site home page. Contact the site administrator if access is still unavailable."}
	case CodeMFARequired, CodeElevationRequired:
		page.Title = browserCopy{"请先完成身份验证", "Please verify your identity"}
		page.Body = browserCopy{"请返回站点，完成登录或额外身份验证后继续。", "Return to the site and complete sign-in or additional verification to continue."}
	case CodeNotFound:
		page.Title = browserCopy{"没有找到此页面", "Page not found"}
		page.Body = browserCopy{"这个地址已不可用，或当前站点没有此页面。请返回首页继续浏览。", "This address is unavailable or does not belong to this site. Return home to continue."}
	case CodeRateLimited:
		page.Title = browserCopy{"请稍等片刻", "Please wait a moment"}
		page.Body = browserCopy{"刚才的请求过于频繁。请稍后从站点重新操作。", "There have been too many requests. Wait a little, then try again from the site."}
	case CodeMaintenance:
		page.Title = browserCopy{"站点正在维护", "The site is under maintenance"}
		page.Body = browserCopy{"维护期间暂时无法完成此操作。请稍后返回站点重试。", "This action is temporarily unavailable during maintenance. Return to the site and try again later."}
	case CodeInvalidRequest, CodeMethodNotAllowed, CodePayloadTooLarge:
		page.Title = browserCopy{"无法完成此请求", "This request could not be completed"}
		page.Body = browserCopy{"页面地址或提交内容无法使用。请返回站点，检查后重新操作。", "The page address or submitted content could not be used. Return to the site, check the details and try again."}
	case CodeConflict, CodeRefreshRequired:
		page.Title = browserCopy{"页面内容已变化", "This page has changed"}
		page.Body = browserCopy{"请从站点重新打开页面，核对最新内容后继续。", "Open the page again from the site and check the latest details before continuing."}
	}
	if (code == CodeUnauthorized || code == CodeInvalidRequest) && (reason == BrowserReasonOAuthStart || reason == BrowserReasonOAuthCallback || reason == BrowserReasonOAuthState || reason == BrowserReasonDiscordRejected || reason == BrowserReasonDiscordIdentity) {
		page.Title = browserCopy{"Discord 登录未完成", "Discord sign-in did not complete"}
		page.Body = browserCopy{"无法完成本次 Discord 登录。请从本站重新开始登录。", "This Discord sign-in could not be completed. Start a new sign-in from this site."}
		if reason == BrowserReasonOAuthState {
			page.Body = browserCopy{"本次登录已过期或无法验证。请重新开始登录，并允许本站使用 Cookie。", "This sign-in has expired or could not be verified. Start again and allow cookies for this site."}
		} else if reason == BrowserReasonDiscordRejected {
			page.Body = browserCopy{"Discord 未接受本次授权。请重新登录，并在 Discord 完成授权。", "Discord did not accept this authorization. Sign in again and complete the authorization in Discord."}
		} else if reason == BrowserReasonDiscordIdentity {
			page.Body = browserCopy{"暂时无法确认 Discord 账号资料。请重新登录；如问题持续，请稍后再试。", "Your Discord account details could not be verified. Sign in again, or try later if the problem continues."}
		}
		page.Action, page.ActionURL = browserCopy{"重新使用 Discord 登录", "Sign in with Discord again"}, "/api/auth/discord/start"
	}
	if code == CodeUnauthorized && reason == BrowserReasonDiscordGate {
		page.Title = browserCopy{"暂未满足 Discord 访问条件", "Discord access requirements were not met"}
		page.Body = browserCopy{"无法确认当前账号已加入本站要求的 Discord 服务器并拥有所需身份组。请确认账号和身份组，或联系站点管理员，再重新登录。", "We could not confirm that this account belongs to the required Discord server and has the required role. Check your Discord account and role, or contact the site administrator, then sign in again."}
	}
	if code == CodeServiceUnavailable && reason == BrowserReasonDiscordUnavailable {
		page.Title = browserCopy{"登录服务暂时不可用", "Sign-in is temporarily unavailable"}
		page.Body = browserCopy{"暂时无法完成 Discord 登录。请稍后从本站重新登录；如问题持续，请联系站点管理员。", "Discord sign-in could not be completed right now. Try signing in again from this site later. Contact the site administrator if the problem continues."}
	}
	if code == CodeForbidden && reason == BrowserReasonWrongStation {
		page.Body = browserCopy{"请从对应站点的首页重新进入，再完成登录。", "Start again from the home page of the appropriate site and sign in there."}
	}
	if options.Admin {
		page.Home = browserCopy{"返回管理站", "Return to the admin site"}
		if page.ActionURL == page.HomeURL {
			page.Action = page.Home
		}
	}
	return page
}
