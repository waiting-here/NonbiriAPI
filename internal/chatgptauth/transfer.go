package chatgptauth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

type Target struct {
	NewAccount bool   `json:"new_account,omitempty"`
	Version    int    `json:"version"`
	HostID     string `json:"host_id"`
	Revision   int64  `json:"revision,string"`
	PublicKey  string `json:"public_key"`
	ClientID   string `json:"client_id,omitempty"`
	Subject    string `json:"subject,omitempty"`
}
type Envelope struct {
	NewAccount   bool   `json:"new_account,omitempty"`
	Version      int    `json:"version"`
	HostID       string `json:"host_id"`
	Revision     int64  `json:"revision,string"`
	EphemeralKey string `json:"ephemeral_key"`
	Nonce        string `json:"nonce"`
	Ciphertext   string `json:"ciphertext"`
}

func NewHostID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
func transferCipher(private *ecdh.PrivateKey, public *ecdh.PublicKey, host string) (cipher.AEAD, error) {
	shared, err := private.ECDH(public)
	if err != nil {
		return nil, ErrInvalid
	}
	defer clear(shared)
	key, err := hkdf.Key(sha256.New, shared, nil, "NonbiriAPI/ChatGPT-transfer/v1:"+host, 32)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func transferAAD(e Envelope) []byte {
	context := fmt.Sprintf("NonbiriAPI/ChatGPT-transfer/v1:%s:%d", e.HostID, e.Revision)
	if e.NewAccount {
		context += ":new-account"
	}
	return []byte(context)
}
func Protect(target Target, credentials Credentials) (Envelope, error) {
	if target.Version != 1 || target.Revision < 1 || target.HostID == "" || !validCredentials(credentials) {
		return Envelope{}, ErrInvalid
	}
	pub, err := base64.RawURLEncoding.DecodeString(target.PublicKey)
	if err != nil {
		return Envelope{}, ErrInvalid
	}
	public, err := ecdh.X25519().NewPublicKey(pub)
	if err != nil {
		return Envelope{}, ErrInvalid
	}
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return Envelope{}, err
	}
	aead, err := transferCipher(private, public, target.HostID)
	if err != nil {
		return Envelope{}, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return Envelope{}, err
	}
	e := Envelope{NewAccount: target.NewAccount, Version: 1, HostID: target.HostID, Revision: target.Revision, EphemeralKey: base64.RawURLEncoding.EncodeToString(private.PublicKey().Bytes()), Nonce: base64.RawURLEncoding.EncodeToString(nonce)}
	plain, err := json.Marshal(credentials)
	if err != nil || len(plain) > maxCredentialBytes {
		return Envelope{}, ErrInvalid
	}
	defer clear(plain)
	e.Ciphertext = base64.RawURLEncoding.EncodeToString(aead.Seal(nil, nonce, plain, transferAAD(e)))
	return e, nil
}
func unprotect(target Target, privateBytes []byte, e Envelope) (Credentials, error) {
	if e.Version != 1 || e.HostID != target.HostID || e.Revision != target.Revision || len(e.Ciphertext) > 256<<10 {
		return Credentials{}, ErrInvalid
	}
	private, err := ecdh.X25519().NewPrivateKey(privateBytes)
	if err != nil {
		return Credentials{}, ErrInvalid
	}
	pub, err := base64.RawURLEncoding.DecodeString(e.EphemeralKey)
	if err != nil {
		return Credentials{}, ErrInvalid
	}
	public, err := ecdh.X25519().NewPublicKey(pub)
	if err != nil {
		return Credentials{}, ErrInvalid
	}
	aead, err := transferCipher(private, public, e.HostID)
	if err != nil {
		return Credentials{}, ErrInvalid
	}
	nonce, err := base64.RawURLEncoding.DecodeString(e.Nonce)
	if err != nil || len(nonce) != aead.NonceSize() {
		return Credentials{}, ErrInvalid
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(e.Ciphertext)
	if err != nil {
		return Credentials{}, ErrInvalid
	}
	plain, err := aead.Open(nil, nonce, ciphertext, transferAAD(e))
	if err != nil {
		return Credentials{}, ErrInvalid
	}
	defer clear(plain)
	var creds Credentials
	if json.Unmarshal(plain, &creds) != nil || !validCredentials(creds) {
		return Credentials{}, ErrInvalid
	}
	return creds, nil
}
