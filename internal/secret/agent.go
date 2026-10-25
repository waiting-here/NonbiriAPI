package secret

// AgentCodec isolates administrator-owned model and search credentials from
// user endpoint keys and authentication secrets.
type AgentCodec interface {
	SealAgentKey([]byte, string) (string, error)
	OpenAgentKey(string, string) ([]byte, error)
}

func agentAAD(purpose string) ([]byte, error) {
	if purpose != "model" && purpose != "search" {
		return nil, ErrInvalidContext
	}
	return []byte("NonbiriAPI/generation/2/agent-key/v1:" + purpose), nil
}
func (v *Vault) SealAgentKey(key []byte, purpose string) (string, error) {
	aad, err := agentAAD(purpose)
	if err != nil {
		return "", err
	}
	defer clear(aad)
	return v.seal(key, aad)
}
func (v *Vault) OpenAgentKey(ciphertext, purpose string) ([]byte, error) {
	aad, err := agentAAD(purpose)
	if err != nil {
		return nil, ErrInvalidCiphertext
	}
	defer clear(aad)
	return v.open(ciphertext, aad)
}
