package adapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const adapterTestNow int64 = 1_700_100_000

type adapterFixture struct {
	store *db.Store
	vault *secret.Vault
}

type adapterUser struct {
	id                 int64
	wallet             ledger.Account
	balanceOperationID string
}

func TestLedgerAdapterZeroesPositiveZeroAndNegativeWalletsBeforeDeletingIdentity(t *testing.T) {
	for _, test := range []struct {
		name                string
		balance             int64
		wantDeletionEntries int
	}{
		{name: "positive", balance: 55, wantDeletionEntries: 2},
		{name: "zero", balance: 0, wantDeletionEntries: 0},
		{name: "negative", balance: -10, wantDeletionEntries: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := openAdapterFixture(t)
			seedTx := beginAdapterTx(t, fixture.store.DB())
			user := seedAdapterUser(t, seedTx, test.name, test.balance)
			if err := seedTx.Commit(); err != nil {
				t.Fatal(err)
			}

			deletionID := mustAdapterID(t, "op_")
			deleteTx := beginAdapterTx(t, fixture.store.DB())
			adapter := NewLedgerAdapter()
			if err := adapter.ZeroAndDeleteAccount(context.Background(), deleteTx, lifecycle.DeleteRequest{
				UserID: user.id, DecisionNow: adapterTestNow + 10,
			}, deletionID); err != nil {
				t.Fatalf("zero and delete %s wallet: %v", test.name, err)
			}
			if err := deleteTx.Commit(); err != nil {
				t.Fatalf("commit %s deletion: %v", test.name, err)
			}

			verifyTx := beginAdapterTx(t, fixture.store.DB())
			defer verifyTx.Rollback()
			var users, wallets int
			if err := verifyTx.QueryRow(`SELECT COUNT(*) FROM users WHERE id=?`, user.id).Scan(&users); err != nil {
				t.Fatal(err)
			}
			if err := verifyTx.QueryRow(`SELECT COUNT(*) FROM credit_accounts WHERE id=?`, user.wallet.ID).Scan(&wallets); err != nil {
				t.Fatal(err)
			}
			if users != 0 || wallets != 0 {
				t.Fatalf("deleted identity rows users=%d wallets=%d", users, wallets)
			}
			var kind, sourceType, sourceID string
			var actor sql.NullInt64
			if err := verifyTx.QueryRow(`SELECT kind,source_type,source_id,actor_user_id
FROM credit_operations WHERE id=?`, deletionID).Scan(&kind, &sourceType, &sourceID, &actor); err != nil {
				t.Fatal(err)
			}
			if kind != string(ledger.KindAccountDeleteZero) || sourceType != "operation" ||
				sourceID != deletionID || actor.Valid {
				t.Fatalf("preserved deletion operation = %q/%q/%q actor=%+v", kind, sourceType, sourceID, actor)
			}
			var deletionEntries, detachedUserEntries int
			if err := verifyTx.QueryRow(`SELECT COUNT(*) FROM credit_entries WHERE operation_id=?`, deletionID).Scan(&deletionEntries); err != nil {
				t.Fatal(err)
			}
			if err := verifyTx.QueryRow(`SELECT COUNT(*) FROM credit_entries
WHERE operation_id=? AND account_kind_snapshot='user' AND account_id IS NULL`, deletionID).Scan(&detachedUserEntries); err != nil {
				t.Fatal(err)
			}
			wantDetached := 0
			if test.wantDeletionEntries != 0 {
				wantDetached = 1
			}
			if deletionEntries != test.wantDeletionEntries || detachedUserEntries != wantDetached {
				t.Fatalf("deletion entries=%d detached-user=%d, want %d/%d",
					deletionEntries, detachedUserEntries, test.wantDeletionEntries, wantDetached)
			}
			external, err := ledger.CodedAccount(context.Background(), verifyTx, "external")
			if err != nil || !external.Balance.IsZero() {
				t.Fatalf("external after %s deletion = (%s,%v)", test.name, external.Balance.Decimal(), err)
			}
			if err := ledger.ValidateRecovery(context.Background(), verifyTx); err != nil {
				t.Fatalf("validate %s deletion ledger: %v", test.name, err)
			}
		})
	}
}

func TestLedgerAdapterRollsBackAndFailsClosedAtMaximumCapacity(t *testing.T) {
	t.Run("caller rollback", func(t *testing.T) {
		fixture := openAdapterFixture(t)
		seedTx := beginAdapterTx(t, fixture.store.DB())
		user := seedAdapterUser(t, seedTx, "rollback", 77)
		if err := seedTx.Commit(); err != nil {
			t.Fatal(err)
		}
		deletionID := mustAdapterID(t, "op_")
		deleteTx := beginAdapterTx(t, fixture.store.DB())
		if err := NewLedgerAdapter().ZeroAndDeleteAccount(context.Background(), deleteTx, lifecycle.DeleteRequest{
			UserID: user.id, DecisionNow: adapterTestNow + 10,
		}, deletionID); err != nil {
			t.Fatal(err)
		}
		if err := deleteTx.Rollback(); err != nil {
			t.Fatal(err)
		}

		verifyTx := beginAdapterTx(t, fixture.store.DB())
		defer verifyTx.Rollback()
		wallet, err := ledger.UserAccount(context.Background(), verifyTx, user.id)
		if err != nil || wallet.ID != user.wallet.ID || wallet.Balance.Decimal() != "77" {
			t.Fatalf("wallet after rollback = (%+v,%v)", wallet, err)
		}
		var operations int
		if err := verifyTx.QueryRow(`SELECT COUNT(*) FROM credit_operations WHERE id=?`, deletionID).Scan(&operations); err != nil {
			t.Fatal(err)
		}
		if operations != 0 {
			t.Fatalf("rolled-back deletion operations = %d", operations)
		}
	})

	t.Run("MAX capacity", func(t *testing.T) {
		fixture := openAdapterFixture(t)
		seedTx := beginAdapterTx(t, fixture.store.DB())
		user := seedAdapterUser(t, seedTx, "capacity-max", 0)
		if err := seedTx.Commit(); err != nil {
			t.Fatal(err)
		}
		deleteTx := beginAdapterTx(t, fixture.store.DB())
		if _, err := deleteTx.Exec(`UPDATE credit_capacity SET last_ledger_seq=? WHERE id=1`, int64(math.MaxInt64)); err != nil {
			t.Fatal(err)
		}
		deletionID := mustAdapterID(t, "op_")
		err := NewLedgerAdapter().ZeroAndDeleteAccount(context.Background(), deleteTx, lifecycle.DeleteRequest{
			UserID: user.id, DecisionNow: adapterTestNow + 10,
		}, deletionID)
		if !errors.Is(err, ledger.ErrCapacityExhausted) {
			t.Fatalf("MAX deletion error = %v, want capacity exhausted", err)
		}
		wallet, walletErr := ledger.UserAccount(context.Background(), deleteTx, user.id)
		if walletErr != nil || wallet.ID != user.wallet.ID || !wallet.Balance.IsZero() {
			t.Fatalf("wallet after MAX failure = (%+v,%v)", wallet, walletErr)
		}
		var users, operations int
		if err := deleteTx.QueryRow(`SELECT COUNT(*) FROM users WHERE id=?`, user.id).Scan(&users); err != nil {
			t.Fatal(err)
		}
		if err := deleteTx.QueryRow(`SELECT COUNT(*) FROM credit_operations WHERE id=?`, deletionID).Scan(&operations); err != nil {
			t.Fatal(err)
		}
		if users != 1 || operations != 0 {
			t.Fatalf("MAX failure users=%d operations=%d", users, operations)
		}
		if err := deleteTx.Rollback(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestLedgerAdapterExportIsSafeOwnerOnlyAndBounded(t *testing.T) {
	fixture := openAdapterFixture(t)
	seedTx := beginAdapterTx(t, fixture.store.DB())
	owner := seedAdapterUser(t, seedTx, "export-owner", 1_234)
	foreign := seedAdapterUser(t, seedTx, "export-foreign", 999)
	external, err := ledger.CodedAccount(context.Background(), seedTx, "external")
	if err != nil {
		t.Fatal(err)
	}
	penaltyID := mustAdapterID(t, "op_")
	penalty, err := ledger.NewAntiAbusePenalty(
		ledger.Meta{OperationID: penaltyID, ActorUserID: owner.id, CreatedAt: adapterTestNow + 1},
		owner.wallet.ID, external.ID, ledger.AmountFromMilli(7), "private reason must not export",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Apply(context.Background(), seedTx, penalty); err != nil {
		t.Fatal(err)
	}
	if err := seedTx.Commit(); err != nil {
		t.Fatal(err)
	}

	readTx := beginAdapterTx(t, fixture.store.DB())
	defer readTx.Rollback()
	adapter := NewLedgerAdapter()
	entries, err := adapter.ExportLedger(context.Background(), readTx, lifecycle.ExportRequest{
		UserID: owner.id, DecisionNow: adapterTestNow + 2, Limit: 2,
	})
	if err != nil || len(entries) != 2 {
		t.Fatalf("safe ledger export = (%+v,%v)", entries, err)
	}
	if entries[0].OperationID != owner.balanceOperationID || entries[0].Delta != "1.234" ||
		entries[1].OperationID != penaltyID || entries[1].Delta != "-0.007" {
		t.Fatalf("safe ledger export order/content = %+v", entries)
	}
	for _, entry := range entries {
		if entry.OperationID == foreign.balanceOperationID {
			t.Fatalf("foreign entry leaked: %+v", entry)
		}
	}
	if _, err := adapter.ExportLedger(context.Background(), readTx, lifecycle.ExportRequest{
		UserID: owner.id, DecisionNow: adapterTestNow + 2, Limit: 1,
	}); !errors.Is(err, lifecycle.ErrTooLarge) {
		t.Fatalf("adapter export limit error = %v", err)
	}

	encoded, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	var objects []map[string]any
	if err := json.Unmarshal(encoded, &objects); err != nil {
		t.Fatal(err)
	}
	wantKeys := []string{"created_at", "delta", "kind", "operation_id", "source_id", "source_type"}
	for _, object := range objects {
		keys := make([]string, 0, len(object))
		for key := range object {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if len(keys) != len(wantKeys) {
			t.Fatalf("exported unsafe fields: %v", keys)
		}
		for index := range wantKeys {
			if keys[index] != wantKeys[index] {
				t.Fatalf("export keys = %v, want %v", keys, wantKeys)
			}
		}
	}
}

func TestClaimAndLedgerAdaptersKeepLateCompletionExternalAfterIdentityDeletion(t *testing.T) {
	fixture := openAdapterFixture(t)
	var clock atomic.Int64
	clock.Store(adapterTestNow)
	service, err := claim.New(claim.Dependencies{
		DB:         fixture.store.DB(),
		Secrets:    fixture.vault,
		Accounting: claim.NewLedgerAccounting(),
		Acceptance: adapterAcceptanceGate{},
		Now:        func() time.Time { return time.Unix(clock.Load(), 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	claimAdapter, err := NewClaimDeleteAdapter(service)
	if err != nil {
		t.Fatal(err)
	}
	ledgerAdapter := NewLedgerAdapter()

	seedTx := beginAdapterTx(t, fixture.store.DB())
	user := seedAdapterUser(t, seedTx, "late-completion", 1_000)
	candidate := seedAdapterCandidate(t, seedTx, fixture.vault, user.id, "late-completion")
	if err := seedTx.Commit(); err != nil {
		t.Fatal(err)
	}
	request, err := service.Accept(context.Background(), claim.AcceptInput{
		UserID: user.id, Route: claim.RouteOpenAIChat, ModelSnapshot: "public/model",
		AttemptLimit: 1, ReservedMilli: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	handle, err := service.Claim(context.Background(), claim.ClaimInput{
		RequestID: request.ID, ActorUserID: user.id, AttemptSeq: 1,
		Purpose: claim.PurposeSelf, Candidate: candidate,
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := service.TakeForDispatch(context.Background(), handle)
	if err != nil {
		t.Fatal(err)
	}
	dispatch.Clear()

	clock.Store(adapterTestNow + 10)
	deleteTx := beginAdapterTx(t, fixture.store.DB())
	deleteRequest := lifecycle.DeleteRequest{UserID: user.id, DecisionNow: clock.Load()}
	if finalizer, err := claimAdapter.PrepareDelete(context.Background(), deleteTx, deleteRequest); err != nil || finalizer != nil {
		t.Fatalf("prepare claim handoff = (%v,%v)", finalizer, err)
	}
	deletionID := mustAdapterID(t, "op_")
	if err := ledgerAdapter.ZeroAndDeleteAccount(context.Background(), deleteTx, deleteRequest, deletionID); err != nil {
		t.Fatalf("delete handed-off account: %v", err)
	}
	if err := deleteTx.Commit(); err != nil {
		t.Fatal(err)
	}

	clock.Store(adapterTestNow + 20)
	attempt, err := service.CompleteAttempt(context.Background(), handle, claim.AttemptOutcome{
		Kind: claim.ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true,
	})
	if err != nil || attempt.State != claim.StateCommitted {
		t.Fatalf("late attempt completion = (%+v,%v)", attempt, err)
	}
	completed, err := service.CompleteRequest(context.Background(), claim.CompleteRequestInput{
		RequestID: request.ID,
		Caller: claim.CallerResult{
			Class:  claim.ResultSuccess,
			Status: 200,
		},
		Disposition:       claim.AccountingCommit,
		ActualChargeMilli: 60,
	})
	if err != nil || completed.UserID != nil || completed.Destination != claim.DestinationExternal {
		t.Fatalf("late request completion = (%+v,%v)", completed, err)
	}

	verifyTx := beginAdapterTx(t, fixture.store.DB())
	defer verifyTx.Rollback()
	if _, err := ledger.UserAccount(context.Background(), verifyTx, user.id); !errors.Is(err, ledger.ErrNotFound) {
		t.Fatalf("late path recreated wallet: %v", err)
	}
	var users, operationCount int
	if err := verifyTx.QueryRow(`SELECT COUNT(*) FROM users WHERE id=?`, user.id).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if err := verifyTx.QueryRow(`SELECT COUNT(*) FROM credit_operations
WHERE id=? AND kind='account_delete_zero' AND source_id=?`, deletionID, deletionID).Scan(&operationCount); err != nil {
		t.Fatal(err)
	}
	if users != 0 || operationCount != 1 {
		t.Fatalf("late path users=%d deletion operations=%d", users, operationCount)
	}
	if err := ledger.ValidateRecovery(context.Background(), verifyTx); err != nil {
		t.Fatalf("validate late-completion ledger: %v", err)
	}
}

type adapterAcceptanceGate struct{}

func (adapterAcceptanceGate) AuthorizeChatAcceptance(context.Context, *sql.Tx, int64, int64) error {
	return nil
}

func openAdapterFixture(t *testing.T) *adapterFixture {
	t.Helper()
	vault, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "claim-ledger-adapter.sqlite")
	dbtest.EnsureOwnerOnlyParent(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &adapterFixture{store: store, vault: vault}
}

func beginAdapterTx(t *testing.T, database *sql.DB) *sql.Tx {
	t.Helper()
	tx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return tx
}

func seedAdapterUser(t *testing.T, tx *sql.Tx, label string, balance int64) adapterUser {
	t.Helper()
	zero := db.EncodeU128(db.U128{})
	result, err := tx.Exec(`INSERT INTO users(
discord_id,username,donation_credit_mag,total_requests,total_uncached_input_tokens,
total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,
total_unknown_usage_requests,revision,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, "adapter-"+label, label, zero, zero, zero, zero, zero, zero, zero, zero,
		adapterTestNow, adapterTestNow)
	if err != nil {
		t.Fatal(err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	wallet, err := ledger.CreateUserAccount(context.Background(), tx, userID, adapterTestNow)
	if err != nil {
		t.Fatal(err)
	}
	user := adapterUser{id: userID, wallet: wallet}
	if balance == 0 {
		return user
	}
	external, err := ledger.CodedAccount(context.Background(), tx, "external")
	if err != nil {
		t.Fatal(err)
	}
	operationID := mustAdapterID(t, "op_")
	var plan ledger.Plan
	if balance > 0 {
		plan, err = ledger.NewAdminUserAdjustment(
			ledger.Meta{OperationID: operationID, ActorUserID: userID, CreatedAt: adapterTestNow},
			wallet.ID, external.ID, ledger.AmountFromMilli(balance), 0, ledger.Amount{}, "adapter funding",
		)
	} else {
		plan, err = ledger.NewAntiAbusePenalty(
			ledger.Meta{OperationID: operationID, ActorUserID: userID, CreatedAt: adapterTestNow},
			wallet.ID, external.ID, ledger.AmountFromMilli(-balance), "adapter penalty",
		)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Apply(context.Background(), tx, plan); err != nil {
		t.Fatal(err)
	}
	user.balanceOperationID = operationID
	return user
}

func seedAdapterCandidate(
	t *testing.T,
	tx *sql.Tx,
	vault *secret.Vault,
	userID int64,
	label string,
) claim.Candidate {
	t.Helper()
	baseURL := "https://" + label + ".example.test/v1"
	result, err := tx.Exec(`INSERT INTO endpoints(
user_id,connector_type,base_url,note,enabled,revision,created_at,updated_at)
VALUES(?,'openai-compatible',?,'',1,1,?,?)`, userID, baseURL, adapterTestNow, adapterTestNow)
	if err != nil {
		t.Fatal(err)
	}
	endpointID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	contextID := make([]byte, 16)
	contextID[len(contextID)-1] = 1
	secretContext, err := secret.NewGenerationTwoEndpointKeyContext(contextID)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := vault.SealForGenerationTwoContext([]byte("adapter-upstream-secret"), secretContext)
	if err != nil {
		t.Fatal(err)
	}
	result, err = tx.Exec(`INSERT INTO endpoint_key_secrets(
context_id,canonical_base_url,connector_type,encrypted_secret,created_at)
VALUES(?,?,'openai-compatible',?,?)`, contextID, baseURL, envelope, adapterTestNow)
	if err != nil {
		t.Fatal(err)
	}
	secretID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := make([]byte, 32)
	fingerprint[0] = 1
	result, err = tx.Exec(`INSERT INTO endpoint_keys(
endpoint_id,secret_ref_id,secret_fingerprint,display_head,display_tail,note,
enabled,force_store_false,revision,created_at,updated_at)
VALUES(?,?,?,'head','tail','',1,0,1,?,?)`, endpointID, secretID, fingerprint, adapterTestNow, adapterTestNow)
	if err != nil {
		t.Fatal(err)
	}
	keyID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return claim.Candidate{
		EndpointID: endpointID, EndpointKeyID: keyID,
		ConnectorType:    connectorcontract.TypeOpenAICompatible,
		CanonicalBaseURL: baseURL, UpstreamModelID: "provider/" + label,
	}
}

func mustAdapterID(t *testing.T, prefix string) string {
	t.Helper()
	id, err := db.GenerateOpaqueID(prefix)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
