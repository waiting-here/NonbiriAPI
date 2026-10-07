package forward

import (
	"bytes"
	"context"
	"errors"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/modelname"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
	"github.com/waiting-here/NonbiriAPI/internal/requestbody"
	"github.com/waiting-here/NonbiriAPI/internal/requestkind"
)

type preparedIngressKey struct{}

type preparedIngress struct {
	body      []byte
	envelope  *openai.RequestEnvelope
	mediaType string
	failure   *wireFailure
}

func (p *preparedIngress) clear() {
	clear(p.body)
	p.envelope.Clear()
}

// RPMClassifier reads the same envelope that ingress will use. Concurrency
// admission precedes this read; a shared gate also bounds simultaneous reads
// across users. The HTTP server's body timeout bounds slow clients.
func RPMClassifier() func(*http.Request) (*http.Request, bool, func(), error) {
	gate := make(chan struct{}, 16)
	return func(r *http.Request) (*http.Request, bool, func(), error) {
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		default:
			return r, false, nil, errors.New("forward: ingress reads busy")
		}
		p := prepareIngress(r)
		charity := false
		if p.envelope != nil {
			requestattempt.Model(r.Context(), p.envelope.Model)
			charity = modelname.IsCharity(p.envelope.Model)
		}
		return r.WithContext(context.WithValue(r.Context(), preparedIngressKey{}, p)), charity, p.clear, nil
	}
}

func prepareIngress(r *http.Request) *preparedIngress {
	p := &preparedIngress{}
	fail := func(code, message string) *preparedIngress {
		f := platformFailure(code, message)
		p.failure = &f
		return p
	}
	failDetail := func(code, message, field, reason string) *preparedIngress {
		fail(code, message)
		p.failure.detail = requestattempt.NewDetail(field, reason)
		return p
	}
	if exactIngressFailure(r.Method, r.URL.Path, r.URL.EscapedPath()) != nil {
		return fail(httperr.CodeInvalidRequest, "invalid request")
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		return failDetail(httperr.CodeInvalidRequest, "invalid request", "query", "query parameters are not supported")
	}
	var ok bool
	p.mediaType, ok = validateChatMedia(r)
	if !ok {
		if len(r.Header.Values("Content-Encoding")) != 0 {
			return failDetail(httperr.CodeInvalidRequest, "invalid request", "Content-Encoding", "content encoding is not supported")
		}
		return failDetail(httperr.CodeInvalidRequest, "invalid request", "Content-Type", "expected application/json with optional UTF-8 charset")
	}
	limit, err := requestbody.Limit(r.Context())
	if err != nil {
		return fail(httperr.CodeServiceUnavailable, "request configuration unavailable")
	}
	if r.ContentLength > limit {
		return failDetail(httperr.CodePayloadTooLarge, "request body too large", "body", "request body exceeds the configured limit")
	}
	p.body, err = readBoundedBody(r.Body, limit)
	if err != nil && !errors.Is(err, openai.ErrPayloadTooLarge) {
		return failDetail(httperr.CodeInvalidRequest, "invalid request", "body", "request body could not be read")
	}
	if err == nil {
		p.envelope, err = openai.DecodeRequestEnvelope(bytes.NewReader(p.body), limit, requestkind.OperationForPath(r.URL.Path))
	}
	if err != nil {
		if errors.Is(err, openai.ErrPayloadTooLarge) {
			return failDetail(httperr.CodePayloadTooLarge, "request body too large", "body", "request body exceeds the configured limit")
		}
		p.failure = new(wireFailure)
		*p.failure = failureForError(err, false)
		return p
	}
	return p
}
