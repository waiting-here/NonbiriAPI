package db

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type countingStartupCodec struct {
	*secret.Vault
	credentials, material int
}

func (c *countingStartupCodec) OpenForGenerationTwoContext(value string, context secret.GenerationTwoEndpointKeyContext) ([]byte, error) {
	c.credentials++
	return c.Vault.OpenForGenerationTwoContext(value, context)
}

func (c *countingStartupCodec) ValidateDatabaseMaterial(value []byte) error {
	c.material++
	return c.Vault.ValidateDatabaseMaterial(value)
}

func TestRestartKeyValidationCostDoesNotGrowWithCredentials(t *testing.T) {
	for _, count := range []int{0, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			path := bootstrapTestPath(t, "credentials.sqlite")
			codec := &countingStartupCodec{Vault: bootstrapTestVault(t)}
			store, err := Open(path, codec)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := store.DB().Begin()
			if err != nil {
				t.Fatal(err)
			}
			for i := 1; i <= count; i++ {
				contextID := make([]byte, 16)
				binary.BigEndian.PutUint64(contextID[8:], uint64(i))
				context, err := secret.NewGenerationTwoEndpointKeyContext(contextID)
				if err != nil {
					t.Fatal(err)
				}
				envelope, err := codec.SealForGenerationTwoContext([]byte("synthetic credential"), context)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(`INSERT INTO endpoint_key_secrets(context_id,canonical_base_url,connector_type,encrypted_secret,created_at) VALUES(?,'https://synthetic.example/v1','openai-compatible',?,0)`, contextID, envelope); err != nil {
					t.Fatal(err)
				}
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			codec.credentials, codec.material = 0, 0
			store, err = Open(path, codec)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			if codec.credentials != 0 || codec.material != 1 {
				t.Fatalf("restart validated credentials=%d material=%d", codec.credentials, codec.material)
			}
			if err := VerifyContext(t.Context(), path, codec, nil); err != nil {
				t.Fatal(err)
			}
			if codec.credentials != count {
				t.Fatalf("offline audit checked %d credentials, want %d", codec.credentials, count)
			}
		})
	}
}
