package db

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func TestDuelUpgradeFromReleasedBinary(t *testing.T) {
	source := os.Getenv("NONBIRI_DUAL_WALLET_FIXTURE")
	if source == "" {
		t.Skip("released-source gate supplies the fixture")
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	path := bootstrapTestPath(t, "dual-upgrade.sqlite")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	prior, err := openSQLite(path, "ro")
	if err != nil {
		t.Fatal(err)
	}
	assertRetainedManifest(t, prior, preProgressionManifestHash)
	before := retainedTableImages(t, prior, nil)
	var wallets, awards int
	if err := prior.QueryRow(`SELECT COUNT(*) FROM credit_accounts WHERE kind='user' AND balance_sign<>0`).Scan(&wallets); err != nil || wallets != 4 {
		t.Fatal(wallets, err)
	}
	if err := prior.QueryRow(`SELECT COUNT(*) FROM game_checkins`).Scan(&awards); err != nil || awards != 2 {
		t.Fatal(awards, err)
	}
	if err := prior.Close(); err != nil {
		t.Fatal(err)
	}
	vault, err := secret.New(bytes.Repeat([]byte{0x42}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	for attempt := 0; attempt < 2; attempt++ {
		store, err := Open(path, vault)
		if err != nil {
			t.Fatal(err)
		}
		assertRetainedManifest(t, store.DB(), PinnedGenerationTwoManifestHash)
		assertRetainedImages(t, store.DB(), before)
		var total, achievedSeq []byte
		var achievedAt, ledgerSeq int64
		if err := store.DB().QueryRow(`SELECT u.donation_credit_mag,u.donation_credit_achieved_at,u.donation_credit_achieved_seq,o.ledger_seq FROM users u JOIN credit_operations o ON o.ledger_seq=(SELECT max(ledger_seq) FROM credit_operations WHERE donation_credit_user_id=u.id AND donation_credit_delta_sign<>0) WHERE u.donation_credit_mag>X'00000000000000000000000000000000'`).Scan(&total, &achievedAt, &achievedSeq, &ledgerSeq); err != nil {
			t.Fatal(err)
		}
		amount, err := DecodeU128(total)
		if err != nil || amount.Decimal() != "100000" || achievedAt != 1700000000 {
			t.Fatal("donation achievement", amount, achievedAt, err)
		}
		sequence, err := DecodeU128(achievedSeq)
		if err != nil || sequence.Big().Int64() != ledgerSeq {
			t.Fatal("same-second donation order", sequence, ledgerSeq, err)
		}
		for key, want := range duelConfigDefaults() {
			var value string
			if err := store.DB().QueryRow(`SELECT value FROM site_config WHERE key=?`, key).Scan(&value); err != nil || value != want {
				t.Fatal(key, value, err)
			}
		}
		tx, err := store.DB().BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateAssetLedger(context.Background(), tx); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := validateAssetCapacity(context.Background(), tx); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		before = retainedTableImages(t, store.DB(), nil)
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if destination := os.Getenv("NONBIRI_DUAL_UPGRADED_FIXTURE"); destination != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
