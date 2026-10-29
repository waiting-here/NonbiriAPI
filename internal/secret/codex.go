package secret

// CodexCodec keeps device authorization credentials separate from other sources.
type CodexCodec interface {
	SealCodex([]byte) (string, error)
	OpenCodex(string) ([]byte, error)
}

func (v *Vault) SealCodex(value []byte) (string, error) {
	return v.seal(value, []byte("NonbiriAPI/codex/v1:credentials"))
}

func (v *Vault) OpenCodex(value string) ([]byte, error) {
	return v.open(value, []byte("NonbiriAPI/codex/v1:credentials"))
}
