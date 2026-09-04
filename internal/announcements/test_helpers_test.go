package announcements

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const announcementTestNow int64 = 1_700_000_000

type announcementTestEnvironment struct {
	store      *db.Store
	vault      *secret.Vault
	repository *Repository
	service    *Service
	authorizer *announcementTestAuthorizer
	clock      *atomic.Int64
}

func newAnnouncementTestEnvironment(t *testing.T) *announcementTestEnvironment {
	return newAnnouncementTestEnvironmentWithID(t, nil)
}

func newAnnouncementTestEnvironmentWithID(t *testing.T, newID func() (string, error)) *announcementTestEnvironment {
	t.Helper()
	master := bytes.Repeat([]byte{0x31}, secret.MasterKeyBytes)
	vault, err := secret.New(master)
	clear(master)
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "announcements.sqlite")
	dbfixture.Materialize(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	clock := &atomic.Int64{}
	clock.Store(announcementTestNow)
	authorizer := &announcementTestAuthorizer{}
	repository, err := NewRepository(Config{
		Store: store, CursorKeys: vault, FinalAuth: authorizer,
		Now:            func() time.Time { return time.Unix(clock.Load(), 0) },
		AnnouncementID: newID,
	})
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}
	service, err := NewService(repository)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return &announcementTestEnvironment{
		store: store, vault: vault, repository: repository, service: service,
		authorizer: authorizer, clock: clock,
	}
}

func (environment *announcementTestEnvironment) seedUser(t *testing.T, discordID, language string, admin bool) int64 {
	t.Helper()
	zero := make([]byte, 16)
	var discord any = discordID
	if admin {
		discord = nil
	}
	result, err := environment.store.DB().Exec(`
INSERT INTO users(
 discord_id,username,is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,
 total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,
 revision,lang,created_at,updated_at
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		discord, "announcement tester", boolInt(admin), zero, zero, zero, zero, zero, zero, zero, zero,
		language, announcementTestNow, announcementTestNow)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("seed user id: %v", err)
	}
	return id
}

func (environment *announcementTestEnvironment) createDraft(t *testing.T, adminID int64, key byte, titleZH, bodyZH, titleEN, bodyEN string, expiresAt *int64) AdminAnnouncement {
	t.Helper()
	severity, pinned, dismissible := "important", true, true
	patch := DraftPatch{
		TitleZH: &titleZH, BodyZH: &bodyZH, TitleEN: &titleEN, BodyEN: &bodyEN,
		Severity: &severity, Pinned: &pinned, Dismissible: &dismissible,
		ExpiresAt: NullableTime{Set: expiresAt != nil, Value: cloneInt64(expiresAt)},
	}
	canonical := map[string]any{
		"title_zh": titleZH, "body_zh": bodyZH, "title_en": titleEN, "body_en": bodyEN,
		"severity": severity, "pinned": pinned, "dismissible": dismissible,
	}
	if expiresAt != nil {
		canonical["expires_at"] = *expiresAt
	}
	result, err := environment.service.Create(context.Background(), adminID,
		announcementTestMutation(t, key, "POST", routeAdminAnnouncements, nil, canonical), patch)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if result.Status != 201 || result.Value.ID == "" || result.Value.Revision != "1" {
		t.Fatalf("Create receipt: %+v", result)
	}
	created, err := environment.service.GetAdmin(context.Background(), adminID, result.Value.ID)
	if err != nil {
		t.Fatalf("GetAdmin after Create: %v", err)
	}
	return created
}

func (environment *announcementTestEnvironment) publish(t *testing.T, adminID int64, key byte, value AdminAnnouncement) {
	t.Helper()
	result, err := environment.service.Publish(context.Background(), adminID, value.ID,
		announcementTestMutation(t, key, "POST", routeAdminPublish, []string{value.ID}, map[string]any{"expected_revision": value.Revision}),
		mustRevision(t, value.Revision))
	if err != nil || result.Status != 200 {
		t.Fatalf("Publish: status=%d err=%v", result.Status, err)
	}
}

func announcementTestMutation(t *testing.T, key byte, method, route string, ids []string, canonical any) ControlMutation {
	t.Helper()
	body, err := idempotency.CanonicalJSON(canonical)
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	return ControlMutation{
		IdempotencyKey: string(bytes.Repeat([]byte{key}, 22)), Method: method, Route: route,
		PathIDs: append([]string(nil), ids...), CanonicalBody: body,
	}
}

func mustRevision(t *testing.T, value string) int64 {
	t.Helper()
	var revision int64
	if _, err := fmt.Sscan(value, &revision); err != nil || revision < 1 {
		t.Fatalf("invalid revision %q", value)
	}
	return revision
}

type announcementTestAuthorizer struct {
	deny  atomic.Bool
	calls atomic.Int64
}

func (authorizer *announcementTestAuthorizer) AuthorizeAdminFinalTx(ctx context.Context, tx *sql.Tx, adminID int64) error {
	authorizer.calls.Add(1)
	if authorizer.deny.Load() {
		return ErrForbidden
	}
	var isAdmin, isBanned int
	err := tx.QueryRowContext(ctx, `SELECT is_admin,is_banned FROM users WHERE id=?`, adminID).Scan(&isAdmin, &isBanned)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUnauthorized
	}
	if err != nil {
		return err
	}
	if isAdmin != 1 || isBanned != 0 {
		return ErrForbidden
	}
	return nil
}
