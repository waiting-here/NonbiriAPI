package config

import (
	"errors"
	"os"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

// LoadSecretVault loads only the existing encryption root for offline tools.
// It never creates a key or requires HTTP/OAuth configuration.
func LoadSecretVault() (*secret.Vault, error) {
	raw, file := os.Getenv("NONBIRI_MASTER_KEY"), os.Getenv("NONBIRI_MASTER_KEY_FILE")
	keySet, fileSet := strings.TrimSpace(raw) != "", strings.TrimSpace(file) != ""
	if keySet == fileSet {
		return nil, errors.New("configure exactly one master-key source")
	}
	var key []byte
	var err error
	if keySet {
		key, err = decodeMasterKey(raw)
	} else {
		key, _, err = loadMasterKeyFile(file)
	}
	defer clear(key)
	if err != nil {
		return nil, errors.New("master-key configuration is invalid")
	}
	return secret.New(key)
}
