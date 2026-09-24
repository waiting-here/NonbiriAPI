package openai

import "github.com/waiting-here/NonbiriAPI/internal/requestbody"

// RequestBodyLimit is the immutable ingress budget retained across attempts.
func (r *ChatRequest) RequestBodyLimit() int64 {
	if r == nil {
		return requestbody.DefaultBytes
	}
	return requestbody.DecoderLimit(r.bodyLimit)
}

// RequestBodyLimit is the immutable ingress budget retained across attempts.
func (r *EmbeddingRequest) RequestBodyLimit() int64 {
	if r == nil {
		return requestbody.DefaultBytes
	}
	return requestbody.DecoderLimit(r.bodyLimit)
}
