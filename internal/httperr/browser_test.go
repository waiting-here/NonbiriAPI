package httperr

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrowserDocumentNegotiation(t *testing.T) {
	for _, tc := range []struct {
		name, path, accept, dest, mode, xhr string
		want                                bool
	}{
		{"document", "/api/auth/discord/callback", "text/html,application/xhtml+xml,*/*;q=0.8", "document", "navigate", "", true},
		{"legacy", "/api/session", "text/html,application/json;q=0.5", "", "", "", true},
		{"JSON preferred", "/api/session", "text/html;q=0.5,application/json", "", "", "", false},
		{"equal preference", "/api/session", "text/html,application/json", "document", "navigate", "", false},
		{"API accept", "/api/session", "application/json", "", "", "", false},
		{"structured JSON preferred", "/api/session", "application/problem+json,text/html;q=0.5", "", "", "", false},
		{"wildcard", "/api/session", "*/*", "", "", "", false},
		{"fetch", "/api/session", "text/html", "empty", "cors", "", false},
		{"same-origin fetch", "/api/session", "text/html", "empty", "same-origin", "", false},
		{"XHR", "/api/session", "text/html", "", "", "XMLHttpRequest", false},
		{"iframe", "/api/session", "text/html", "iframe", "navigate", "", false},
		{"incomplete metadata", "/api/session", "text/html", "document", "", "", false},
		{"public inference", "/v1/chat/completions", "text/html", "document", "navigate", "", false},
		{"public inference root", "/v1", "text/html", "document", "navigate", "", false},
		{"HTML disabled", "/", "text/html;q=0,*/*", "document", "navigate", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("Accept", tc.accept)
			if tc.dest != "" {
				req.Header.Set("Sec-Fetch-Dest", tc.dest)
			}
			if tc.mode != "" {
				req.Header.Set("Sec-Fetch-Mode", tc.mode)
			}
			if tc.xhr != "" {
				req.Header.Set("X-Requested-With", tc.xhr)
			}
			if got := browserDocument(req); got != tc.want {
				t.Fatalf("document=%v; want %v", got, tc.want)
			}
		})
	}
}

func TestBrowserErrorPrivacyStatusAndCSP(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/auth/discord/callback?code=private-code&state=private-state&return=https://evil.example", nil)
	request.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; object-src 'none'")
	rec.Header().Set("Retry-After", "60")
	w := WithBrowserErrors(WithLanguage(rec, "zh-CN"), request, BrowserPageOptions{})
	SetBrowserReason(w, BrowserReasonOAuthState)
	WriteError(w, New(CodeUnauthorized, "private-message<script>alert(1)</script>").WithDiag("private-diagnostic"))
	if rec.Code != 401 || rec.Header().Get("Content-Type") != "text/html; charset=utf-8" || rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Content-Language") != "zh" || rec.Header().Get("Retry-After") != "60" {
		t.Fatalf("status=%d headers=%v", rec.Code, rec.Header())
	}
	body := rec.Body.String()
	for _, private := range []string{"private-code", "private-state", "private-message", "private-diagnostic", "evil.example"} {
		if strings.Contains(body, private) {
			t.Fatalf("page exposed %q", private)
		}
	}
	for _, expected := range []string{`<html lang="zh">`, "本次登录已过期或无法验证", `href="/api/auth/discord/start"`, `href="/"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("page lacks %q", expected)
		}
	}
	_, script, ok := strings.Cut(body, "<script>")
	if !ok {
		t.Fatal("missing script")
	}
	script, _, ok = strings.Cut(script, "</script>")
	if !ok {
		t.Fatal("unterminated script")
	}
	digest := sha256.Sum256([]byte(script))
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "'sha256-"+base64.StdEncoding.EncodeToString(digest[:])+"'") {
		t.Fatal("rendered script does not match the CSP hash")
	}
	if strings.Contains(rec.Header().Get("Content-Security-Policy"), "script-src 'unsafe-inline'") {
		t.Fatal("unsafe script policy")
	}
}

func TestBrowserReasonLeavesMachineErrorsUnchanged(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/auth/discord/callback", nil)
	req.Header.Set("Accept", "application/json")
	expected, actual := httptest.NewRecorder(), httptest.NewRecorder()
	WriteError(expected, New(CodeUnauthorized, "authentication failed"))
	w := WithBrowserErrors(actual, req, BrowserPageOptions{})
	SetBrowserReason(w, BrowserReasonDiscordGate)
	WriteError(w, New(CodeUnauthorized, "authentication failed"))
	if actual.Code != expected.Code || actual.Body.String() != expected.Body.String() || actual.Header().Get("Content-Type") != expected.Header().Get("Content-Type") {
		t.Fatalf("machine contract changed: status=%d body=%s", actual.Code, actual.Body.String())
	}
}

func TestBrowserPageKeepsSuccessAndHeadSemantics(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		req := httptest.NewRequest(method, "/api/session", nil)
		req.Header.Set("Accept", "text/html")
		rec := httptest.NewRecorder()
		WriteError(WithBrowserErrors(rec, req, BrowserPageOptions{}), New(CodeNotFound, "not found"))
		if rec.Code != 404 || (method == http.MethodHead && rec.Body.Len() != 0) {
			t.Fatalf("%s status=%d bytes=%d", method, rec.Code, rec.Body.Len())
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	WriteJSON(WithBrowserErrors(rec, req, BrowserPageOptions{}), 200, map[string]string{"ok": "yes"})
	if rec.Code != 200 || rec.Body.String() != "{\"ok\":\"yes\"}\n" || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatal("success response changed")
	}
}
