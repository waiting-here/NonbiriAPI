package contract

import (
	"errors"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrInvalidRerankPath = errors.New("invalid rerank path")

// SupportsRerankPath distinguishes HTTP reranking from Gateway RPC.
func SupportsRerankPath(kind Type) bool {
	if kind.OpenAIServiceFamily() {
		return true
	}
	switch kind {
	case TypeCohereRerank, TypeJinaRerank, TypeSiliconFlowRerank, TypeVoyageRerank:
		return true
	}
	return false
}

// ValidateRerankPath accepts only an unambiguous same-origin absolute path.
func ValidateRerankPath(value string) error {
	if value == "" {
		return nil
	}
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.HasPrefix(value, "/\\") {
		return ErrInvalidRerankPath
	}
	if len(value) > 2048 || !utf8.ValidString(value) || strings.ContainsAny(value, "?#\\") {
		return ErrInvalidRerankPath
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.Opaque != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return ErrInvalidRerankPath
	}
	if !utf8.ValidString(parsed.Path) || strings.HasPrefix(parsed.Path, "//") || strings.ContainsAny(parsed.Path, "\\%") {
		return ErrInvalidRerankPath
	}
	if strings.Count(parsed.Path, "/") != strings.Count(value, "/") {
		return ErrInvalidRerankPath
	}
	for _, r := range parsed.Path {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return ErrInvalidRerankPath
		}
	}
	for _, segment := range strings.Split(parsed.Path, "/") {
		if segment == "." || segment == ".." {
			return ErrInvalidRerankPath
		}
	}
	return nil
}

// RerankURL leaves the original endpoint available for transport selection.
func RerankURL(baseURL, path string) (string, error) {
	if err := ValidateRerankPath(path); err != nil {
		return "", err
	}
	if path == "" {
		return strings.TrimSuffix(baseURL, "/") + "/rerank", nil
	}
	base, err := url.Parse(baseURL)
	if err != nil || base.Scheme != "https" && base.Scheme != "http" || base.Host == "" || base.User != nil {
		return "", ErrInvalidRerankPath
	}
	selected, err := url.ParseRequestURI(path)
	if err != nil {
		return "", ErrInvalidRerankPath
	}
	base.Path, base.RawPath = selected.Path, selected.RawPath
	base.RawQuery, base.Fragment, base.RawFragment = "", "", ""
	base.ForceQuery = false
	return base.String(), nil
}
