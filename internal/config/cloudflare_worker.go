package config

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"
	"strings"
)

// CloudflareWorker contains private deployment settings, never public output.
type CloudflareWorker struct{ url, token string }

func (CloudflareWorker) String() string                 { return "[private Cloudflare Worker]" }
func (w CloudflareWorker) GoString() string             { return w.String() }
func (w CloudflareWorker) LogValue() slog.Value         { return slog.StringValue(w.String()) }
func (w CloudflareWorker) MarshalJSON() ([]byte, error) { return json.Marshal(w.String()) }
func (w CloudflareWorker) URL() string                  { return w.url }
func (w CloudflareWorker) Token() string                { return w.token }

func ParseCloudflareWorker(rawURL, token string) (CloudflareWorker, error) {
	if rawURL == "" && token == "" {
		return CloudflareWorker{}, nil
	}
	fail := func() (CloudflareWorker, error) {
		return CloudflareWorker{}, errors.New("Worker requires a valid HTTPS URL and a 32–4096 character visible ASCII token")
	}
	u, err := url.Parse(rawURL)
	if err != nil || u == nil || len(rawURL) > 4096 || strings.TrimSpace(rawURL) != rawURL || u.Scheme != "https" || u.Host == "" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(rawURL, "?#\\") {
		return fail()
	}
	for _, c := range rawURL {
		if c < 32 || c == 127 {
			return fail()
		}
	}
	if len(token) < 32 || len(token) > 4096 {
		return fail()
	}
	for _, c := range []byte(token) {
		if c < 33 || c > 126 {
			return fail()
		}
	}
	return CloudflareWorker{url: rawURL, token: token}, nil
}
