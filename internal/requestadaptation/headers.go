package requestadaptation

import (
	"net/http"
	"strings"
)

// AddedHeaders returns only explicitly selected, validated client headers and
// fixed values. Connector authority headers are added after this projection.
func AddedHeaders(inbound http.Header, d Document) (http.Header, error) {
	if len(d.ForwardHeaders.Values) == 0 && len(d.FixedHeaders.Values) == 0 {
		return make(http.Header), nil
	}
	fixed := make(map[string]string, len(d.FixedHeaders.Values))
	for name, value := range d.FixedHeaders.Values {
		if !ValidHeaderName(name) || !validHeaderValue(value) {
			return nil, ErrInvalid
		}
		fixed[strings.ToLower(name)] = value
	}
	nominated := make(map[string]bool)
	for name, values := range inbound {
		if !strings.EqualFold(name, "Connection") {
			continue
		}
		for _, value := range values {
			for _, token := range strings.Split(value, ",") {
				nominated[strings.ToLower(strings.TrimSpace(token))] = true
			}
		}
	}
	out := make(http.Header)
	added := 0
	for _, name := range d.ForwardHeaders.Values {
		if !ValidHeaderName(name) {
			return nil, ErrInvalid
		}
		lower := strings.ToLower(name)
		if _, overridden := fixed[lower]; overridden {
			continue
		}
		if nominated[lower] {
			return nil, ErrInvalid
		}
		var values []string
		for incoming, entries := range inbound {
			if strings.EqualFold(incoming, name) {
				values = append(values, entries...)
			}
		}
		if len(values) == 0 {
			continue
		}
		if len(values) != 1 || !validHeaderValue(values[0]) {
			return nil, ErrInvalid
		}
		added += len(name) + len(values[0])
		if added > MaxAddedHeaderBytes {
			return nil, ErrInvalid
		}
		out.Set(canonicalHeader(name), values[0])
	}
	for name, value := range d.FixedHeaders.Values {
		if nominated[strings.ToLower(name)] {
			return nil, ErrInvalid
		}
		added += len(name) + len(value)
		if added > MaxAddedHeaderBytes {
			return nil, ErrInvalid
		}
		out.Set(canonicalHeader(name), value)
	}
	return out, nil
}

// ApplyAddedHeaders writes the bounded projection into the one-attempt HTTP
// request. Callers apply connector protocol/auth headers after it.
func ApplyAddedHeaders(outbound http.Header, added http.Header) error {
	if outbound == nil {
		return ErrInvalid
	}
	for name, values := range added {
		if !ValidHeaderName(name) || len(values) != 1 || !validHeaderValue(values[0]) {
			return ErrInvalid
		}
		outbound.Set(name, values[0])
	}
	return nil
}
