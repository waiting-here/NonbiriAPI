package secret

// ChatGPTCodec separates renewable sessions and transfer keys from API keys.
type ChatGPTCodec interface {
	SealChatGPT([]byte, string) (string, error)
	OpenChatGPT(string, string) ([]byte, error)
}

func chatgptAAD(purpose string) ([]byte, error) {
	if purpose != "credentials" && purpose != "transfer-key" {
		return nil, ErrInvalidContext
	}
	return []byte("NonbiriAPI/chatgpt/v1:" + purpose), nil
}

func (v *Vault) SealChatGPT(value []byte, purpose string) (string, error) {
	aad, err := chatgptAAD(purpose)
	if err != nil {
		return "", err
	}
	return v.seal(value, aad)
}

func (v *Vault) OpenChatGPT(value, purpose string) ([]byte, error) {
	aad, err := chatgptAAD(purpose)
	if err != nil {
		return nil, ErrInvalidCiphertext
	}
	return v.open(value, aad)
}
