package config

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"nonbiriapi/internal/secret"
)

func assertLoadedMasterKey(t *testing.T, c *Config, expected []byte) {
	t.Helper()
	loaded := c.TakeSecretVault()
	if loaded == nil {
		t.Fatal("configuration did not retain a secret vault")
	}
	t.Cleanup(func() { _ = loaded.Close() })
	reference, err := secret.New(expected)
	if err != nil {
		t.Fatalf("construct reference vault: %v", err)
	}
	t.Cleanup(func() { _ = reference.Close() })

	envelope, err := loaded.Seal([]byte("configuration-key-check"))
	if err != nil {
		t.Fatalf("loaded vault Seal: %v", err)
	}
	opened, err := reference.Open(envelope)
	if err != nil {
		t.Fatal("loaded vault does not use the expected master key")
	}
	clear(opened)
}

func masterKeyFixture() []byte {
	key := make([]byte, secret.MasterKeyBytes)
	for i := range key {
		key[i] = byte(i*7 + 3)
	}
	return key
}

func TestMasterKeyEnvironmentEncodings(t *testing.T) {
	key := masterKeyFixture()
	defer clear(key)
	encodings := map[string]string{
		"hex":             hex.EncodeToString(key),
		"standard_base64": base64.StdEncoding.EncodeToString(key),
		"raw_base64":      base64.RawStdEncoding.EncodeToString(key),
		"url_base64":      base64.URLEncoding.EncodeToString(key),
		"raw_url_base64":  base64.RawURLEncoding.EncodeToString(key),
	}
	for name, encoded := range encodings {
		t.Run(name, func(t *testing.T) {
			allEnvs(t)
			t.Setenv("NONBIRI_MASTER_KEY", encoded)
			c, err := Load()
			if err != nil {
				t.Fatalf("Load rejected a supported encoding: %v", err)
			}
			if c.MasterSource != "env" {
				t.Fatalf("MasterSource = %q, want env", c.MasterSource)
			}
			assertLoadedMasterKey(t, c, key)
		})
	}
}

func TestConfigFormattingDoesNotExposeMasterKey(t *testing.T) {
	allEnvs(t)
	key := bytes.Repeat([]byte{0xce}, secret.MasterKeyBytes)
	encoded := hex.EncodeToString(key)
	clear(key)
	t.Setenv("NONBIRI_MASTER_KEY", encoded)
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer func() {
		if vault := c.TakeSecretVault(); vault != nil {
			_ = vault.Close()
		}
	}()
	formatted := fmt.Sprintf("%+v %#v", c, c)
	if strings.Contains(formatted, encoded) || strings.Contains(formatted, "0xce") || strings.Contains(formatted, "206 206 206") {
		t.Fatal("Config formatting exposed master-key bytes")
	}
}

func TestMasterKeyEnvironmentRejectsMissingInvalidAndWrongLength(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "missing", value: ""},
		{name: "invalid_encoding", value: "not-a-master-key-value"},
		{name: "31_bytes", value: hex.EncodeToString(make([]byte, secret.MasterKeyBytes-1))},
		{name: "33_bytes", value: base64.StdEncoding.EncodeToString(make([]byte, secret.MasterKeyBytes+1))},
		{name: "embedded_newline", value: base64.StdEncoding.EncodeToString(make([]byte, secret.MasterKeyBytes))[:20] + "\n" + base64.StdEncoding.EncodeToString(make([]byte, secret.MasterKeyBytes))[20:]},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			allEnvs(t)
			t.Setenv("NONBIRI_MASTER_KEY", test.value)
			t.Setenv("NONBIRI_MASTER_KEY_FILE", "")
			_, err := Load()
			if err == nil {
				t.Fatal("Load accepted a missing, malformed, or wrong-length master key")
			}
			if test.value != "" && strings.Contains(err.Error(), test.value) {
				t.Fatal("configuration error exposed the master-key input")
			}
		})
	}
}

func TestMasterKeySourcesAreMutuallyExclusive(t *testing.T) {
	allEnvs(t)
	keyFile := filepath.Join(t.TempDir(), "master.key")
	if err := os.WriteFile(keyFile, bytes.Repeat([]byte{0x5a}, secret.MasterKeyBytes), 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	t.Setenv("NONBIRI_MASTER_KEY_FILE", keyFile)
	value := os.Getenv("NONBIRI_MASTER_KEY")
	_, err := Load()
	if err == nil {
		t.Fatal("Load accepted both master-key sources")
	}
	if strings.Contains(err.Error(), value) {
		t.Fatal("mutually-exclusive-source error exposed the environment key")
	}
}

func TestMasterKeyFileEncodingsAndRawBytes(t *testing.T) {
	key := masterKeyFixture()
	defer clear(key)
	contents := map[string][]byte{
		"hex":            []byte(hex.EncodeToString(key) + "\n"),
		"base64":         []byte(base64.StdEncoding.EncodeToString(key) + "\n"),
		"raw_url_base64": []byte(base64.RawURLEncoding.EncodeToString(key)),
		"raw_binary":     append([]byte(nil), key...),
	}
	for name, content := range contents {
		t.Run(name, func(t *testing.T) {
			allEnvs(t)
			t.Setenv("NONBIRI_MASTER_KEY", "")
			keyFile := filepath.Join(t.TempDir(), "master.key")
			if err := os.WriteFile(keyFile, content, 0o600); err != nil {
				t.Fatalf("write key file: %v", err)
			}
			t.Setenv("NONBIRI_MASTER_KEY_FILE", keyFile)
			c, err := Load()
			if err != nil {
				t.Fatalf("Load rejected a supported key file: %v", err)
			}
			if c.MasterSource != "file" {
				t.Fatalf("MasterSource = %q, want file", c.MasterSource)
			}
			assertLoadedMasterKey(t, c, key)
		})
	}
}

func TestMasterKeyFileModePolicyIsPlatformAdapted(t *testing.T) {
	for _, mode := range []os.FileMode{0o400, 0o600} {
		if !secureMasterKeyFileMode(mode, "linux") {
			t.Fatalf("owner-readable, owner-only mode %04o was rejected", mode)
		}
	}
	for _, mode := range []os.FileMode{
		0o000, 0o100, 0o200, 0o300, 0o500, 0o700,
		0o604, 0o640, 0o660, 0o644, 0o666,
		os.ModeSetuid | 0o400, os.ModeSetgid | 0o400, os.ModeSticky | 0o400,
	} {
		if secureMasterKeyFileMode(mode, "linux") {
			t.Fatalf("unsafe or owner-unreadable mode %v was accepted", mode)
		}
	}
	if !secureMasterKeyFileMode(0o666, "windows") {
		t.Fatal("Windows policy relied on synthetic POSIX permission bits")
	}
}

func TestMasterKeyFileLoaderDoesNotCreateOrModify(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.key")
	if key, _, err := loadMasterKeyFile(missing); err == nil || key != nil {
		clear(key)
		t.Fatal("missing key file was accepted")
	}
	if _, err := os.Lstat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("loader created a missing key file: %v", err)
	}

	path := filepath.Join(t.TempDir(), "existing.key")
	content := bytes.Repeat([]byte{0xa7}, secret.MasterKeyBytes)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write existing key: %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat existing key before load: %v", err)
	}

	loaded, source, err := loadMasterKeyFile(path)
	if err != nil {
		t.Fatalf("load existing key: %v", err)
	}
	if source != "file" || !bytes.Equal(loaded, content) {
		clear(loaded)
		t.Fatal("loader returned unexpected existing key material")
	}
	clear(loaded)

	afterContent, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read existing key after load: %v", err)
	}
	defer clear(afterContent)
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat existing key after load: %v", err)
	}
	if !bytes.Equal(afterContent, content) || before.Mode() != after.Mode() ||
		before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("loader modified or replaced the existing key file")
	}
}

func TestMasterKeyFileRejectsUnsafeAndMalformedInputs(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		allEnvs(t)
		t.Setenv("NONBIRI_MASTER_KEY", "")
		missing := filepath.Join(t.TempDir(), "missing.key")
		t.Setenv("NONBIRI_MASTER_KEY_FILE", missing)
		if _, err := Load(); err == nil {
			t.Fatal("Load accepted a missing key file")
		}
		if _, err := os.Lstat(missing); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Load generated a missing production key: %v", err)
		}
	})

	t.Run("directory", func(t *testing.T) {
		allEnvs(t)
		t.Setenv("NONBIRI_MASTER_KEY", "")
		t.Setenv("NONBIRI_MASTER_KEY_FILE", t.TempDir())
		if _, err := Load(); err == nil {
			t.Fatal("Load accepted a directory as a key file")
		}
	})

	for _, size := range []int{secret.MasterKeyBytes - 1, secret.MasterKeyBytes + 1, maxMasterKeyFileBytes + 1} {
		t.Run("wrong_or_excessive_length", func(t *testing.T) {
			allEnvs(t)
			t.Setenv("NONBIRI_MASTER_KEY", "")
			keyFile := filepath.Join(t.TempDir(), "bad.key")
			content := bytes.Repeat([]byte{0xff}, size)
			if err := os.WriteFile(keyFile, content, 0o600); err != nil {
				t.Fatalf("write key file: %v", err)
			}
			t.Setenv("NONBIRI_MASTER_KEY_FILE", keyFile)
			if _, err := Load(); err == nil {
				t.Fatal("Load accepted a wrong-length key file")
			}
		})
	}

	t.Run("content_not_in_error", func(t *testing.T) {
		allEnvs(t)
		t.Setenv("NONBIRI_MASTER_KEY", "")
		keyFile := filepath.Join(t.TempDir(), "bad.key")
		content := []byte("PRIVATE-CONTENT-MUST-NOT-APPEAR-IN-ERRORS")
		if err := os.WriteFile(keyFile, content, 0o600); err != nil {
			t.Fatalf("write key file: %v", err)
		}
		t.Setenv("NONBIRI_MASTER_KEY_FILE", keyFile)
		_, err := Load()
		if err == nil {
			t.Fatal("Load accepted malformed key-file content")
		}
		if strings.Contains(err.Error(), string(content)) {
			t.Fatal("configuration error exposed key-file content")
		}
	})

	t.Run("symlink", func(t *testing.T) {
		allEnvs(t)
		t.Setenv("NONBIRI_MASTER_KEY", "")
		dir := t.TempDir()
		target := filepath.Join(dir, "target.key")
		link := filepath.Join(dir, "link.key")
		if err := os.WriteFile(target, bytes.Repeat([]byte{0x44}, secret.MasterKeyBytes), 0o600); err != nil {
			t.Fatalf("write target: %v", err)
		}
		if err := os.Symlink(target, link); err != nil {
			if runtime.GOOS == "windows" && (errors.Is(err, os.ErrPermission) || strings.Contains(strings.ToLower(err.Error()), "privilege")) {
				t.Skip("symlink creation is unavailable without the Windows privilege")
			}
			t.Fatalf("create symlink: %v", err)
		}
		t.Setenv("NONBIRI_MASTER_KEY_FILE", link)
		if _, err := Load(); err == nil {
			t.Fatal("Load followed a master-key symlink")
		}
	})

	if runtime.GOOS != "windows" {
		t.Run("group_or_other_permissions", func(t *testing.T) {
			allEnvs(t)
			t.Setenv("NONBIRI_MASTER_KEY", "")
			keyFile := filepath.Join(t.TempDir(), "permissive.key")
			if err := os.WriteFile(keyFile, bytes.Repeat([]byte{0x55}, secret.MasterKeyBytes), 0o600); err != nil {
				t.Fatalf("write key file: %v", err)
			}
			if err := os.Chmod(keyFile, 0o644); err != nil {
				t.Fatalf("chmod key file: %v", err)
			}
			t.Setenv("NONBIRI_MASTER_KEY_FILE", keyFile)
			if _, err := Load(); err == nil {
				t.Fatal("Load accepted group/other-readable key permissions")
			}
		})
	}
}

func TestBlankEnvironmentSourceAllowsConfiguredFile(t *testing.T) {
	allEnvs(t)
	t.Setenv("NONBIRI_MASTER_KEY", "   ")
	key := bytes.Repeat([]byte{0x6c}, secret.MasterKeyBytes)
	defer clear(key)
	keyFile := filepath.Join(t.TempDir(), "master.key")
	if err := os.WriteFile(keyFile, key, 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	t.Setenv("NONBIRI_MASTER_KEY_FILE", keyFile)
	c, err := Load()
	if err != nil {
		t.Fatalf("Load rejected the sole non-empty source: %v", err)
	}
	assertLoadedMasterKey(t, c, key)
}
