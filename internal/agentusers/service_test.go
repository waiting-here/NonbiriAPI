package agentusers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/adminusers"
	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type finalAuth struct{}

func (finalAuth) AuthorizeAdmin(context.Context, *sql.Tx, int64) error           { return nil }
func (finalAuth) AuthorizeStewardMutation(context.Context, *sql.Tx, int64) error { return nil }
func (finalAuth) AuthorizeUserManagement(context.Context, *sql.Tx, int64) (bool, error) {
	return false, nil
}

type invalidations struct{ count int }

func (i *invalidations) InvalidateUserAuthority(int64) { i.count++ }
func facadeFixture(t *testing.T) (*Service, *db.Store, *invalidations, *int, *int) {
	t.Helper()
	v, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close() })
	path := filepath.Join(t.TempDir(), "users.sqlite")
	dbfixture.Materialize(t, path, v)
	store, err := db.Open(path, v)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	tx, err := store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	z := db.EncodeU128(db.U128{})
	one, _ := db.ParseU128Decimal("1")
	for id := int64(1); id <= 2; id++ {
		if _, err = tx.Exec(`INSERT INTO users(id,discord_id,username,is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(?,?,?,0,?,?,?,?,?,?,?,?,100,100)`, id, "identity-"+strconv.FormatInt(id, 10), "user", z, z, z, z, z, z, z, db.EncodeU128(one)); err != nil {
			t.Fatal(err)
		}
		if _, err = ledger.CreateUserAccount(t.Context(), tx, id, 100); err != nil {
			t.Fatal(err)
		}
		if _, err = ledger.CreateUserAssetAccount(t.Context(), tx, id, ledger.Game, 100); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec("INSERT INTO caller_keys(user_id,generation,updated_at) VALUES(?,0,100)", id); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	sink := &invalidations{}
	users, err := adminusers.NewService(adminusers.ServiceConfig{Database: store.DB(), CursorKeys: v, FinalAuth: finalAuth{}, Invalidator: sink, Now: func() time.Time { return time.Unix(100, 0) }})
	if err != nil {
		t.Fatal(err)
	}
	acquired, released := new(int), new(int)
	active := false
	if err = users.AttachIdentityMutationBarrier(func(context.Context, string) (func(), error) {
		if active {
			return nil, errors.New("duplicate identity lock")
		}
		active = true
		*acquired++
		return func() { active = false; *released++ }, nil
	}); err != nil {
		t.Fatal(err)
	}
	service, err := New(Config{Users: users, Accounts: &lifecycle.Coordinator{}, Auth: &auth.Runtime{}})
	if err != nil {
		t.Fatal(err)
	}
	return service, store, sink, acquired, released
}
func TestPreparedUserBatchSharesGateAndInterleavesAtomically(t *testing.T) {
	service, store, sink, acquired, released := facadeFixture(t)
	actor := agentplan.Actor{ID: 99, Role: "admin"}
	authorize := func(context.Context, *sql.Tx, agentplan.Actor, string) error { return nil }
	first := agentplan.Operation{Kind: "users.profile", TargetID: "1", Input: json.RawMessage(`{"expected_revision":"1","level":3}`)}
	second := agentplan.Operation{Kind: "users.ban", TargetID: "1", Input: json.RawMessage(`{"expected_revision":"1","reason":"test","duration_seconds":60}`)}
	batch, err := service.Prepare(t.Context(), actor, []agentplan.Operation{first, second}, authorize)
	if err != nil {
		t.Fatal(err)
	}
	if *acquired != 1 {
		t.Fatalf("locks=%d", *acquired)
	}
	tx, err := store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = batch.ApplyOperation(t.Context(), tx, 0); err != nil {
		t.Fatal(err)
	}
	// This represents a workflow/domain operation between the two user writes.
	if _, err = tx.Exec("UPDATE users SET lang='en' WHERE id=2"); err != nil {
		t.Fatal(err)
	}
	if _, err = batch.ApplyOperation(t.Context(), tx, 1); !errors.Is(err, adminusers.ErrConflict) {
		t.Fatalf("stale second=%v", err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	batch.Finalize(false)
	batch.Finalize(false)
	var level sql.NullInt64
	var lang string
	if err = store.DB().QueryRow("SELECT level FROM users WHERE id=1").Scan(&level); err != nil || level.Valid {
		t.Fatal("first operation survived rollback", err)
	}
	if err = store.DB().QueryRow("SELECT lang FROM users WHERE id=2").Scan(&lang); err != nil || lang == "en" {
		t.Fatal("interleaved write survived rollback", err)
	}
	if *released != 1 || sink.count != 0 {
		t.Fatalf("release=%d notifications=%d", *released, sink.count)
	}
	second.Input = json.RawMessage(`{"expected_revision":"2","reason":"test","duration_seconds":60}`)
	batch, err = service.Prepare(t.Context(), actor, []agentplan.Operation{first, second}, authorize)
	if err != nil {
		t.Fatal(err)
	}
	tx, err = store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if _, err = batch.ApplyOperation(t.Context(), tx, i); err != nil {
			t.Fatal(err)
		}
	}
	if sink.count != 0 {
		t.Fatal("notification before commit")
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	batch.Finalize(true)
	batch.Finalize(true)
	if *acquired != 2 || *released != 2 || sink.count != 2 {
		t.Fatalf("locks=%d releases=%d notifications=%d", *acquired, *released, sink.count)
	}
}
func TestPreparedUserBatchRejectsAutomaticAndStaleBeforeGate(t *testing.T) {
	service, _, _, acquired, _ := facadeFixture(t)
	op := agentplan.Operation{Kind: "users.profile", TargetID: "1", Input: json.RawMessage(`{"expected_revision":"2","level":3}`)}
	authorize := func(context.Context, *sql.Tx, agentplan.Actor, string) error { return nil }
	if _, err := service.Prepare(t.Context(), agentplan.Actor{ID: 99, Role: "admin", Automatic: true}, []agentplan.Operation{op}, authorize); err == nil {
		t.Fatal("automatic actor accepted")
	}
	if _, err := service.Prepare(t.Context(), agentplan.Actor{ID: 99, Role: "admin"}, []agentplan.Operation{op}, authorize); !errors.Is(err, adminusers.ErrConflict) {
		t.Fatalf("stale prepare=%v", err)
	}
	if *acquired != 0 {
		t.Fatal("invalid plan acquired identity barrier")
	}
}

func TestAgentUserReadSchemaHasOptionalFiltersWithoutPagination(t *testing.T) {
	var schema struct {
		Properties map[string]any `json:"properties"`
		Required   []string       `json:"required"`
	}
	if err := json.Unmarshal(ReadSchema(), &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Properties["page"] != nil || schema.Properties["page_size"] != nil || len(schema.Required) != 0 || schema.Properties["kind"] == nil {
		t.Fatal("wrong Agent account read contract", string(ReadSchema()))
	}
}
