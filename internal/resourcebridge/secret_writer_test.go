package resourcebridge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func TestWrittenEndpointCredentialsSurviveDatabaseRestart(t *testing.T) {
	fixture := newBridgeFixture(t)
	owner := fixture.seedUser(t, "restart")
	fixture.seedEndpointKey(t, owner, "restart-openai", connectorcontract.TypeOpenAICompatible, "restart-credential-one")
	fixture.seedEndpointKey(t, owner, "restart-anthropic", connectorcontract.TypeAnthropicCompatible, "restart-credential-two")
	var sequence int
	var name, path string
	if err := fixture.store.DB().QueryRow(`PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := fixture.runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := db.Open(path, fixture.vault)
	if err != nil {
		t.Fatalf("reopen database after writing endpoint credentials: %v", err)
	}
	defer reopened.Close()
	var count int
	if err := reopened.DB().QueryRow(`SELECT COUNT(*) FROM endpoint_keys`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("reopened credential count = %d, error = %v", count, err)
	}
}

func TestWriteEndpointSecretUsesUniqueContextAndExactAAD(t *testing.T) {
	fixture := newBridgeFixture(t)
	first := fixture.writeUnreferencedSecret(t, "same-credential-value", true)
	second := fixture.writeUnreferencedSecret(t, "same-credential-value", true)
	if first.RefID == second.RefID {
		t.Fatal("distinct secret writes reused row identity")
	}
	type persisted struct {
		contextID  []byte
		ciphertext string
	}
	read := func(refID int64) persisted {
		var value persisted
		if err := fixture.store.DB().QueryRow(`SELECT context_id,encrypted_secret
FROM endpoint_key_secrets WHERE id=?`, refID).Scan(&value.contextID, &value.ciphertext); err != nil {
			t.Fatalf("read persisted secret: %v", err)
		}
		return value
	}
	firstRow := read(first.RefID)
	secondRow := read(second.RefID)
	defer clear(firstRow.contextID)
	defer clear(secondRow.contextID)
	if len(firstRow.contextID) != contextIDBytes || len(secondRow.contextID) != contextIDBytes ||
		bytes.Equal(firstRow.contextID, secondRow.contextID) {
		t.Fatal("context identifiers are malformed or reused")
	}
	if firstRow.ciphertext == secondRow.ciphertext {
		t.Fatal("credential envelopes unexpectedly match")
	}
	for _, row := range []persisted{firstRow, secondRow} {
		credentialContext, err := secret.NewGenerationTwoEndpointKeyContext(row.contextID)
		if err != nil {
			t.Fatalf("NewGenerationTwoEndpointKeyContext: %v", err)
		}
		plaintext, err := fixture.vault.OpenForGenerationTwoContext(row.ciphertext, credentialContext)
		if err != nil {
			t.Fatalf("AAD round trip: %v", err)
		}
		if string(plaintext) != "same-credential-value" {
			t.Fatalf("round-trip plaintext mismatch")
		}
		clear(plaintext)
	}
	wrongContext, err := secret.NewGenerationTwoEndpointKeyContext(secondRow.contextID)
	if err != nil {
		t.Fatalf("wrong context construction: %v", err)
	}
	if plaintext, err := fixture.vault.OpenForGenerationTwoContext(firstRow.ciphertext, wrongContext); err == nil {
		clear(plaintext)
		t.Fatal("ciphertext opened under a different context")
	}
}

func TestWriteEndpointSecretRollsBackWithCallerTransaction(t *testing.T) {
	fixture := newBridgeFixture(t)
	stored := fixture.writeUnreferencedSecret(t, "rollback-credential", false)
	var count int
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM endpoint_key_secrets WHERE id=?`, stored.RefID).Scan(&count); err != nil {
		t.Fatalf("count rolled-back secret: %v", err)
	}
	if count != 0 {
		t.Fatalf("rolled-back secret rows = %d, want 0", count)
	}
}

func TestCredentialFingerprintGoldenFramingAndDomainChanges(t *testing.T) {
	fixture := newBridgeFixture(t)
	stored := fixture.writeUnreferencedSecret(t, "golden-credential", true)
	const want = "bb716a179f68f977e7dba0b4b5484e19192f629939fbe405508146090cae1be9"
	if got := hex.EncodeToString(stored.Fingerprint[:]); got != want {
		t.Fatalf("fingerprint = %s, want %s", got, want)
	}
	base := fixture.runtime.fingerprint(
		string(connectorcontract.TypeOpenAICompatible),
		"https://secret.example.test/v1",
		[]byte("golden-credential"),
	)
	variants := [][sha256.Size]byte{
		fixture.runtime.fingerprint(string(connectorcontract.TypeAnthropicCompatible), "https://secret.example.test/v1", []byte("golden-credential")),
		fixture.runtime.fingerprint(string(connectorcontract.TypeOpenAICompatible), "https://other.example.test/v1", []byte("golden-credential")),
		fixture.runtime.fingerprint(string(connectorcontract.TypeOpenAICompatible), "https://secret.example.test/v1", []byte("golden-credential-2")),
		fixture.runtime.fingerprint("a", "bc", []byte("d")),
	}
	for index, variant := range variants[:3] {
		if variant == base {
			t.Fatalf("fingerprint variant %d did not change", index)
		}
	}
	if variants[3] == fixture.runtime.fingerprint("ab", "c", []byte("d")) {
		t.Fatal("length framing did not distinguish field boundaries")
	}
	if base == sha256.Sum256([]byte("golden-credential")) {
		t.Fatal("fingerprint degraded to plain SHA-256")
	}
}

func TestDisplayFragmentMatrix(t *testing.T) {
	tests := []struct {
		name      string
		plaintext string
		head      string
		tail      string
	}{
		{name: "one", plaintext: "a"},
		{name: "four", plaintext: "abcd"},
		{name: "five", plaintext: "abcde", head: "abcd"},
		{name: "eight", plaintext: "abcdefgh", head: "abcd"},
		{name: "nine", plaintext: "abcdefghi", head: "abcd", tail: "fghi"},
		{name: "non ascii head", plaintext: "界bcde"},
		{name: "non ascii head keeps tail", plaintext: "界bcdefghi", tail: "fghi"},
		{name: "non ascii tail", plaintext: "abcdef界hi", head: "abcd"},
		{name: "non ascii middle", plaintext: "abcd界fghi", head: "abcd", tail: "fghi"},
		{name: "scalar not byte", plaintext: "🎉abcd", tail: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			head, tail := displayFragments([]byte(test.plaintext))
			if head != test.head || tail != test.tail {
				t.Fatalf("displayFragments(%q) = (%q,%q), want (%q,%q)", test.plaintext, head, tail, test.head, test.tail)
			}
		})
	}
	fixture := newBridgeFixture(t)
	stored := fixture.writeUnreferencedSecret(t, "abcd界fghi", true)
	if stored.DisplayHead != "abcd" || stored.DisplayTail != "fghi" {
		t.Fatalf("stored fragments = (%q,%q)", stored.DisplayHead, stored.DisplayTail)
	}
}

func TestWriteEndpointSecretRejectsInvalidInputAndClearsPlaintext(t *testing.T) {
	fixture := newBridgeFixture(t)
	tests := []struct {
		name      string
		connector string
		baseURL   string
		plaintext []byte
		createdAt int64
	}{
		{name: "empty", connector: "openai-compatible", baseURL: "https://valid.example.test/v1", plaintext: []byte{}, createdAt: bridgeTestNow},
		{name: "unknown connector", connector: "unknown", baseURL: "https://valid.example.test/v1", plaintext: []byte("credential"), createdAt: bridgeTestNow},
		{name: "invalid utf8", connector: "openai-compatible", baseURL: "https://valid.example.test/v1", plaintext: []byte{0xff}, createdAt: bridgeTestNow},
		{name: "control", connector: "openai-compatible", baseURL: "https://valid.example.test/v1", plaintext: []byte("credential\n"), createdAt: bridgeTestNow},
		{name: "base control", connector: "openai-compatible", baseURL: "https://valid.example.test/\n", plaintext: []byte("credential"), createdAt: bridgeTestNow},
		{name: "negative time", connector: "openai-compatible", baseURL: "https://valid.example.test/v1", plaintext: []byte("credential"), createdAt: -1},
		{name: "time overflow", connector: "openai-compatible", baseURL: "https://valid.example.test/v1", plaintext: []byte("credential"), createdAt: maxUnixSecond + 1},
		{name: "too large", connector: "openai-compatible", baseURL: "https://valid.example.test/v1", plaintext: bytes.Repeat([]byte{'x'}, secret.MaxPlaintextBytes+1), createdAt: bridgeTestNow},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatalf("BeginTx: %v", err)
			}
			defer tx.Rollback()
			_, err = fixture.runtime.WriteEndpointSecret(context.Background(), tx, resources.SecretWriteInput{
				CanonicalBaseURL: test.baseURL, ConnectorType: test.connector,
				Plaintext: test.plaintext, CreatedAt: test.createdAt,
			})
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error = %v, want ErrInvalidInput", err)
			}
			if !allZero(test.plaintext) {
				t.Fatal("rejected plaintext was not cleared")
			}
		})
	}

	fixture.backend.mu.Lock()
	fixture.backend.rewriteTo = "https://rewritten.example.test/v1"
	fixture.backend.mu.Unlock()
	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	plaintext := []byte("canonicality-check")
	_, err = fixture.runtime.WriteEndpointSecret(context.Background(), tx, resources.SecretWriteInput{
		CanonicalBaseURL: "https://original.example.test/v1", ConnectorType: "openai-compatible",
		Plaintext: plaintext, CreatedAt: bridgeTestNow,
	})
	_ = tx.Rollback()
	if !errors.Is(err, ErrInvalidInput) || !allZero(plaintext) {
		t.Fatalf("noncanonical write = (%v, cleared=%v)", err, allZero(plaintext))
	}
}

func TestWriteEndpointSecretAcceptsExactPlaintextLimit(t *testing.T) {
	fixture := newBridgeFixture(t)
	plaintext := bytes.Repeat([]byte{'x'}, secret.MaxPlaintextBytes)
	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()
	stored, err := fixture.runtime.WriteEndpointSecret(context.Background(), tx, resources.SecretWriteInput{
		CanonicalBaseURL: "https://limit.example.test/v1", ConnectorType: "openai-compatible",
		Plaintext: plaintext, CreatedAt: bridgeTestNow,
	})
	if err != nil || stored.RefID <= 0 || stored.DisplayHead != "xxxx" || stored.DisplayTail != "xxxx" {
		t.Fatalf("exact-limit write = (%+v, %v)", stored, err)
	}
	if !allZero(plaintext) {
		t.Fatal("exact-limit plaintext was not cleared")
	}
}

func TestSecretWriterCancellationDoesNotSealOrPersist(t *testing.T) {
	fixture := newBridgeFixture(t)
	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	plaintext := []byte("canceled-secret-value")
	_, err = fixture.runtime.WriteEndpointSecret(ctx, tx, resources.SecretWriteInput{
		CanonicalBaseURL: "https://canceled.example.test/v1", ConnectorType: "openai-compatible",
		Plaintext: plaintext, CreatedAt: bridgeTestNow,
	})
	if !errors.Is(err, ErrInterrupted) || !allZero(plaintext) {
		t.Fatalf("canceled write = (%v, cleared=%v)", err, allZero(plaintext))
	}
	if fixture.backend.openCount() != 0 {
		t.Fatalf("canceled write opened backend %d times", fixture.backend.openCount())
	}
}

func TestWriteEndpointSecretDependencyFailuresLeaveNoRow(t *testing.T) {
	t.Run("random", func(t *testing.T) {
		fixture := newBridgeFixtureWithRandom(t, errorReader{})
		assertFailedSecretWrite(t, fixture, ErrUnavailable)
	})
	t.Run("seal", func(t *testing.T) {
		fixture := newBridgeFixture(t)
		fixture.vault.mu.Lock()
		fixture.vault.failSeal = true
		fixture.vault.errorText = "seal leaked plaintext marker"
		fixture.vault.mu.Unlock()
		assertFailedSecretWrite(t, fixture, ErrUnavailable)
	})
	t.Run("duplicate context insert", func(t *testing.T) {
		fixture := newBridgeFixtureWithRandom(t, constantReader{value: 0x44})
		fixture.writeUnreferencedSecret(t, "first-context-owner", true)
		assertFailedSecretWrite(t, fixture, ErrUnavailable)
	})
}

func assertFailedSecretWrite(t testing.TB, fixture *bridgeFixture, want error) {
	t.Helper()
	var before int
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM endpoint_key_secrets`).Scan(&before); err != nil {
		t.Fatalf("count before write: %v", err)
	}
	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()
	plaintext := []byte("failure-path-secret")
	_, err = fixture.runtime.WriteEndpointSecret(context.Background(), tx, resources.SecretWriteInput{
		CanonicalBaseURL: "https://failure.example.test/v1", ConnectorType: "openai-compatible",
		Plaintext: plaintext, CreatedAt: bridgeTestNow,
	})
	if !errors.Is(err, want) || !allZero(plaintext) {
		t.Fatalf("failed write = (%v, cleared=%v)", err, allZero(plaintext))
	}
	_ = tx.Rollback()
	var after int
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM endpoint_key_secrets`).Scan(&after); err != nil {
		t.Fatalf("count after write: %v", err)
	}
	if after != before {
		t.Fatalf("secret rows changed %d -> %d", before, after)
	}
}

func TestConcurrentSecretWritesUseDistinctContexts(t *testing.T) {
	fixture := newBridgeFixture(t)
	const writes = 24
	var wait sync.WaitGroup
	errorsSeen := make(chan error, writes)
	for index := 0; index < writes; index++ {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
			if err != nil {
				errorsSeen <- err
				return
			}
			defer tx.Rollback()
			plaintext := []byte(fmt.Sprintf("concurrent-secret-%02d", index))
			_, err = fixture.runtime.WriteEndpointSecret(context.Background(), tx, resources.SecretWriteInput{
				CanonicalBaseURL: "https://concurrent.example.test/v1", ConnectorType: "openai-compatible",
				Plaintext: plaintext, CreatedAt: bridgeTestNow,
			})
			if err == nil && !allZero(plaintext) {
				err = errors.New("plaintext not cleared")
			}
			if err == nil {
				err = tx.Commit()
			}
			errorsSeen <- err
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatalf("concurrent write: %v", err)
		}
	}
	var count, distinct int
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*),COUNT(DISTINCT hex(context_id)) FROM endpoint_key_secrets`).Scan(&count, &distinct); err != nil {
		t.Fatalf("count contexts: %v", err)
	}
	if count != writes || distinct != writes {
		t.Fatalf("context rows/distinct = %d/%d, want %d/%d", count, distinct, writes, writes)
	}
}

func TestMarkEndpointSecretOrphanedCASMatrix(t *testing.T) {
	fixture := newBridgeFixture(t)
	ownerID := fixture.seedUser(t, "orphan")

	t.Run("physical key reference", func(t *testing.T) {
		key := fixture.seedEndpointKey(t, ownerID, "orphan-key", connectorcontract.TypeOpenAICompatible, "orphan-key-credential")
		markOrphan(t, fixture, key.secretID, bridgeTestNow)
		assertOrphanMarker(t, fixture, key.secretID, sql.NullInt64{})
	})

	t.Run("claimed reference", func(t *testing.T) {
		key := fixture.seedEndpointKey(t, ownerID, "orphan-claimed", connectorcontract.TypeOpenAICompatible, "orphan-claimed-credential")
		request, handle, err := fixture.claims.ClaimDiscovery(context.Background(), claim.DiscoveryClaimInput{
			ActorUserID: ownerID,
			Candidate:   claim.Candidate{EndpointID: key.endpointID, EndpointKeyID: key.keyID, ConnectorType: key.connector, CanonicalBaseURL: key.baseURL},
		})
		if err != nil {
			t.Fatalf("ClaimDiscovery: %v", err)
		}
		deleteEndpointKey(t, fixture, key.keyID)
		markOrphan(t, fixture, key.secretID, bridgeTestNow)
		assertOrphanMarker(t, fixture, key.secretID, sql.NullInt64{})
		if _, err := fixture.claims.ReleaseUndispatched(context.Background(), handle); err != nil {
			t.Fatalf("ReleaseUndispatched: %v", err)
		}
		completeFixedRequest(t, fixture, request.ID)
		markOrphan(t, fixture, key.secretID, bridgeTestNow)
		assertOrphanMarker(t, fixture, key.secretID, sql.NullInt64{Int64: bridgeTestNow, Valid: true})
	})

	t.Run("dispatched then terminal and already marked", func(t *testing.T) {
		key := fixture.seedEndpointKey(t, ownerID, "orphan-dispatched", connectorcontract.TypeOpenAICompatible, "orphan-dispatched-credential")
		request, handle, err := fixture.claims.ClaimDiscovery(context.Background(), claim.DiscoveryClaimInput{
			ActorUserID: ownerID,
			Candidate:   claim.Candidate{EndpointID: key.endpointID, EndpointKeyID: key.keyID, ConnectorType: key.connector, CanonicalBaseURL: key.baseURL},
		})
		if err != nil {
			t.Fatalf("ClaimDiscovery: %v", err)
		}
		dispatch, err := fixture.claims.TakeForDispatch(context.Background(), handle)
		if err != nil {
			t.Fatalf("TakeForDispatch: %v", err)
		}
		dispatch.Clear()
		deleteEndpointKey(t, fixture, key.keyID)
		markOrphan(t, fixture, key.secretID, bridgeTestNow)
		assertOrphanMarker(t, fixture, key.secretID, sql.NullInt64{})
		if _, err := fixture.claims.CompleteAttempt(context.Background(), handle, claim.AttemptOutcome{
			Kind: claim.ResultSynthetic, Diagnostic: "terminal test",
		}); err != nil {
			t.Fatalf("CompleteAttempt: %v", err)
		}
		completeFixedRequest(t, fixture, request.ID)
		markOrphan(t, fixture, key.secretID, bridgeTestNow)
		markOrphan(t, fixture, key.secretID, bridgeTestNow+10)
		assertOrphanMarker(t, fixture, key.secretID, sql.NullInt64{Int64: bridgeTestNow, Valid: true})
	})
}

func TestMarkEndpointSecretOrphanedValidatesOnlyBoundedIdentityAndTime(t *testing.T) {
	fixture := newBridgeFixture(t)
	for _, input := range []struct {
		refID int64
		at    int64
	}{{refID: 0, at: bridgeTestNow}, {refID: 1, at: -1}, {refID: 1, at: maxUnixSecond + 1}} {
		tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatalf("BeginTx: %v", err)
		}
		err = fixture.runtime.MarkEndpointSecretOrphaned(context.Background(), tx, input.refID, input.at)
		_ = tx.Rollback()
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("MarkEndpointSecretOrphaned(%d,%d) error = %v", input.refID, input.at, err)
		}
	}
	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	if err := fixture.runtime.MarkEndpointSecretOrphaned(context.Background(), tx, 999999, bridgeTestNow); err != nil {
		t.Fatalf("missing orphan row should be an idempotent no-op: %v", err)
	}
	_ = tx.Rollback()
}

func markOrphan(t testing.TB, fixture *bridgeFixture, secretID, at int64) {
	t.Helper()
	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()
	if err := fixture.runtime.MarkEndpointSecretOrphaned(context.Background(), tx, secretID, at); err != nil {
		t.Fatalf("MarkEndpointSecretOrphaned: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit orphan marker: %v", err)
	}
}

func assertOrphanMarker(t testing.TB, fixture *bridgeFixture, secretID int64, want sql.NullInt64) {
	t.Helper()
	var got sql.NullInt64
	if err := fixture.store.DB().QueryRow(`SELECT orphaned_at FROM endpoint_key_secrets WHERE id=?`, secretID).Scan(&got); err != nil {
		t.Fatalf("read orphan marker: %v", err)
	}
	if got != want {
		t.Fatalf("orphan marker = %+v, want %+v", got, want)
	}
}

func deleteEndpointKey(t testing.TB, fixture *bridgeFixture, keyID int64) {
	t.Helper()
	if _, err := fixture.store.DB().Exec(`DELETE FROM endpoint_keys WHERE id=?`, keyID); err != nil {
		t.Fatalf("delete endpoint key: %v", err)
	}
}

func completeFixedRequest(t testing.TB, fixture *bridgeFixture, requestID string) {
	t.Helper()
	if _, err := fixture.claims.CompleteRequest(context.Background(), claim.CompleteRequestInput{
		RequestID:   requestID,
		Caller:      claim.CallerResult{Class: claim.ResultSuccess, Status: 202},
		Disposition: claim.AccountingNone,
	}); err != nil {
		t.Fatalf("CompleteRequest: %v", err)
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type constantReader struct{ value byte }

func (reader constantReader) Read(value []byte) (int, error) {
	for index := range value {
		value[index] = reader.value
	}
	return len(value), nil
}
