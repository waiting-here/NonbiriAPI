package openai

// NewResponseGuard lets translating connectors apply the same credential
// reflection boundary to their generated OpenAI response wire and text deltas.
func NewResponseGuard(materials ...[]byte) *responseGuard {
	return newResponseGuard(materials...)
}
