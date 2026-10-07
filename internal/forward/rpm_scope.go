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
	if exactIngressFailure(r.Method, r.URL.Path, r.URL.EscapedPath()) != nil || r.URL.RawQuery != "" || r.URL.ForceQuery {
		return fail(httperr.CodeInvalidRequest, "invalid request")
	}
	var ok bool
	p.mediaType, ok = validateChatMedia(r)
	if !ok {
		return fail(httperr.CodeInvalidRequest, "invalid request")
	}
	limit, err := requestbody.Limit(r.Context())
	if err != nil {
		return fail(httperr.CodeServiceUnavailable, "request configuration unavailable")
	}
	if r.ContentLength > limit {
		return fail(httperr.CodePayloadTooLarge, "request body too large")
	}
	p.body, err = readBoundedBody(r.Body, limit)
	if err == nil {
		p.envelope, err = openai.DecodeRequestEnvelope(bytes.NewReader(p.body), limit, requestkind.OperationForPath(r.URL.Path))
	}
	if err != nil {
		if errors.Is(err, openai.ErrPayloadTooLarge) {
			return fail(httperr.CodePayloadTooLarge, "request body too large")
		}
		return fail(httperr.CodeInvalidRequest, "invalid request")
	}
	return p
}
