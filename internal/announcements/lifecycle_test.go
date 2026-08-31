package announcements

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestAnnouncementLifecycleDraftIsolationFallbackExpiryAndDeletion(t *testing.T) {
	environment := newAnnouncementTestEnvironment(t)
	adminID := environment.seedUser(t, "", "en", true)
	zhUserID := environment.seedUser(t, "announcement-zh", "zh", false)
	enUserID := environment.seedUser(t, "announcement-en", "en", false)

	draft := environment.createDraft(t, adminID, 'a', "中文标题", "## 中文\n\n旧内容", "English title", "## English\n\nOld body", nil)
	if draft.State != "draft" || draft.Revision != "1" || draft.Published != nil {
		t.Fatalf("unexpected new draft: %+v", draft)
	}
	page, err := environment.service.ListUser(context.Background(), zhUserID, PageQuery{})
	if err != nil || len(page.Data) != 0 {
		t.Fatalf("draft leaked into user list: page=%+v err=%v", page, err)
	}
	preview, err := environment.service.Preview(context.Background(), adminID, draft.ID, PreviewInput{ExpectedRevision: 1})
	if err != nil || preview.RenderedZH == nil || preview.RenderedEN == nil {
		t.Fatalf("Preview: value=%+v err=%v", preview, err)
	}
	environment.publish(t, adminID, 'b', draft)

	zhDetail, err := environment.service.GetUser(context.Background(), zhUserID, draft.ID)
	if err != nil {
		t.Fatalf("GetUser zh: %v", err)
	}
	if zhDetail.Revision != "2" || zhDetail.EffectiveLanguage != "zh" || zhDetail.FallbackFrom != nil ||
		zhDetail.RenderedBody != *preview.RenderedZH {
		t.Fatalf("unexpected zh publication: %+v preview=%+v", zhDetail, preview)
	}
	enDetail, err := environment.service.GetUser(context.Background(), enUserID, draft.ID)
	if err != nil {
		t.Fatalf("GetUser en: %v", err)
	}
	if enDetail.EffectiveLanguage != "en" || enDetail.FallbackFrom != nil || enDetail.RenderedBody != *preview.RenderedEN {
		t.Fatalf("unexpected en publication: %+v", enDetail)
	}

	newEnglish := "## English\n\nNew unpublished body"
	editMutation := announcementTestMutation(t, 'c', "PATCH", routeAdminAnnouncement, []string{draft.ID}, map[string]any{
		"expected_revision": "2", "body_en": newEnglish,
	})
	edited, err := environment.service.Edit(context.Background(), adminID, draft.ID, editMutation, 2, DraftPatch{BodyEN: &newEnglish})
	if err != nil || edited.Value.Revision != "3" {
		t.Fatalf("Edit: result=%+v err=%v", edited, err)
	}
	stillPublished, err := environment.service.GetUser(context.Background(), enUserID, draft.ID)
	if err != nil || stillPublished.Revision != "2" || stillPublished.RenderedBody != enDetail.RenderedBody {
		t.Fatalf("draft edit changed published projection: value=%+v err=%v", stillPublished, err)
	}
	updatedPreview, err := environment.service.Preview(context.Background(), adminID, draft.ID, PreviewInput{ExpectedRevision: 3})
	if err != nil || updatedPreview.RenderedEN == nil || !strings.Contains(*updatedPreview.RenderedEN, "New unpublished body") {
		t.Fatalf("updated Preview: value=%+v err=%v", updatedPreview, err)
	}
	publishMutation := announcementTestMutation(t, 'd', "POST", routeAdminPublish, []string{draft.ID}, map[string]any{"expected_revision": "3"})
	if result, err := environment.service.Publish(context.Background(), adminID, draft.ID, publishMutation, 3); err != nil || result.Status != 200 {
		t.Fatalf("republish: result=%+v err=%v", result, err)
	}
	updatedDetail, err := environment.service.GetUser(context.Background(), enUserID, draft.ID)
	if err != nil || updatedDetail.Revision != "4" || updatedDetail.RenderedBody != *updatedPreview.RenderedEN {
		t.Fatalf("updated publication differs from preview: value=%+v err=%v", updatedDetail, err)
	}

	zhOnly := environment.createDraft(t, adminID, 'e', "仅中文", "中文正文", "", "", nil)
	environment.publish(t, adminID, 'f', zhOnly)
	fallback, err := environment.service.GetUser(context.Background(), enUserID, zhOnly.ID)
	if err != nil || fallback.EffectiveLanguage != "zh" || fallback.FallbackFrom == nil || *fallback.FallbackFrom != "en" {
		t.Fatalf("language fallback: value=%+v err=%v", fallback, err)
	}
	withdrawMutation := announcementTestMutation(t, 'y', "POST", routeAdminWithdraw, []string{zhOnly.ID}, map[string]any{
		"expected_revision": "2", "reason": "temporarily hidden",
	})
	withdrawn, err := environment.service.Withdraw(context.Background(), adminID, zhOnly.ID, withdrawMutation, 2, "temporarily hidden")
	if err != nil || withdrawn.Status != 200 || withdrawn.Replayed {
		t.Fatalf("Withdraw: result=%+v err=%v", withdrawn, err)
	}
	withdrawReplay, err := environment.service.Withdraw(context.Background(), adminID, zhOnly.ID, withdrawMutation, 2, "temporarily hidden")
	if err != nil || !withdrawReplay.Replayed || withdrawReplay.Status != 200 || len(withdrawReplay.Body) != 0 {
		t.Fatalf("Withdraw replay: result=%+v err=%v", withdrawReplay, err)
	}
	if _, err := environment.service.GetUser(context.Background(), enUserID, zhOnly.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("withdrawn announcement remained visible: %v", err)
	}
	withdrawnAdmin, err := environment.service.GetAdmin(context.Background(), adminID, zhOnly.ID)
	if err != nil || withdrawnAdmin.State != "withdrawn" || withdrawnAdmin.Revision != "3" || withdrawnAdmin.Published == nil {
		t.Fatalf("withdrawn admin projection: value=%+v err=%v", withdrawnAdmin, err)
	}
	republishMutation := announcementTestMutation(t, 'z', "POST", routeAdminPublish, []string{zhOnly.ID}, map[string]any{"expected_revision": "3"})
	republished, err := environment.service.Publish(context.Background(), adminID, zhOnly.ID, republishMutation, 3)
	if err != nil || republished.Status != 200 || republished.Replayed {
		t.Fatalf("Republish: result=%+v err=%v", republished, err)
	}
	republishReplay, err := environment.service.Publish(context.Background(), adminID, zhOnly.ID, republishMutation, 3)
	if err != nil || !republishReplay.Replayed || republishReplay.Status != 200 || len(republishReplay.Body) != 0 {
		t.Fatalf("Republish replay: result=%+v err=%v", republishReplay, err)
	}
	republishedUser, err := environment.service.GetUser(context.Background(), enUserID, zhOnly.ID)
	if err != nil || republishedUser.Revision != "4" || republishedUser.EffectiveLanguage != "zh" {
		t.Fatalf("republished user projection: value=%+v err=%v", republishedUser, err)
	}

	expiresAt := environment.clock.Load() + 10
	expiring := environment.createDraft(t, adminID, 'g', "到期", "到期正文", "Expiry", "Expiry body", &expiresAt)
	environment.publish(t, adminID, 'h', expiring)
	environment.clock.Store(expiresAt)
	if _, err := environment.service.GetUser(context.Background(), zhUserID, expiring.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired detail err=%v, want not found", err)
	}
	var state string
	var revision int64
	if err := environment.store.DB().QueryRow(`SELECT state,revision FROM announcements WHERE id=?`, expiring.ID).Scan(&state, &revision); err != nil {
		t.Fatalf("read expired row: %v", err)
	}
	if state != "expired" || revision != 3 {
		t.Fatalf("expiry state=%q revision=%d", state, revision)
	}
	var expiryAudits int
	if err := environment.store.DB().QueryRow(`SELECT count(*) FROM announcement_audits WHERE announcement_id_text=? AND action='expire'`, expiring.ID).Scan(&expiryAudits); err != nil || expiryAudits != 1 {
		t.Fatalf("expiry audit count=%d err=%v", expiryAudits, err)
	}

	deleteMutation := announcementTestMutation(t, 'i', "DELETE", routeAdminAnnouncement, []string{draft.ID}, map[string]any{
		"expected_revision": "4", "confirmation": PermanentDeleteConfirmation, "reason": "obsolete",
	})
	deleted, err := environment.service.Delete(context.Background(), adminID, draft.ID, deleteMutation, 4, PermanentDeleteConfirmation, "obsolete")
	if err != nil || deleted.Status != 204 {
		t.Fatalf("Delete: result=%+v err=%v", deleted, err)
	}
	deleteReplay, err := environment.service.Delete(context.Background(), adminID, draft.ID, deleteMutation, 4, PermanentDeleteConfirmation, "obsolete")
	if err != nil || !deleteReplay.Replayed || deleteReplay.Status != 204 || len(deleteReplay.Body) != 0 {
		t.Fatalf("Delete replay: result=%+v err=%v", deleteReplay, err)
	}
	var contentCount, deleteAuditCount int
	if err := environment.store.DB().QueryRow(`SELECT count(*) FROM announcements WHERE id=?`, draft.ID).Scan(&contentCount); err != nil {
		t.Fatalf("count deleted content: %v", err)
	}
	if err := environment.store.DB().QueryRow(`SELECT count(*) FROM announcement_audits WHERE announcement_id_text=? AND action='delete' AND reason='obsolete'`, draft.ID).Scan(&deleteAuditCount); err != nil {
		t.Fatalf("count delete audit: %v", err)
	}
	if contentCount != 0 || deleteAuditCount != 1 {
		t.Fatalf("permanent delete content=%d delete_audits=%d", contentCount, deleteAuditCount)
	}
}

func TestSelectPublishedLanguageFallbackMatrix(t *testing.T) {
	language := func(title, body string) (sql.NullString, sql.NullString) {
		return sql.NullString{String: title, Valid: true}, sql.NullString{String: body, Valid: true}
	}
	zhTitle, zhBody := language("中文", "中文正文")
	enTitle, enBody := language("English", "English body")

	tests := []struct {
		name             string
		requested        string
		zhTitle, zhBody  sql.NullString
		enTitle, enBody  sql.NullString
		wantLanguage     string
		wantFallbackFrom string
	}{
		{
			name: "zh falls back to en", requested: "zh", enTitle: enTitle, enBody: enBody,
			wantLanguage: "en", wantFallbackFrom: "zh",
		},
		{
			name: "en falls back to zh", requested: "en", zhTitle: zhTitle, zhBody: zhBody,
			wantLanguage: "zh", wantFallbackFrom: "en",
		},
		{
			name: "unset language defaults to en without synthetic fallback", requested: "", enTitle: enTitle, enBody: enBody,
			wantLanguage: "en",
		},
		{
			name: "requested language is available", requested: "zh", zhTitle: zhTitle, zhBody: zhBody, enTitle: enTitle, enBody: enBody,
			wantLanguage: "zh",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			selected, err := selectPublishedLanguage(rawAnnouncement{
				publishedTitleZH:  testCase.zhTitle,
				publishedBodyZH:   testCase.zhBody,
				publishedTitleEN:  testCase.enTitle,
				publishedBodyEN:   testCase.enBody,
				publishedRevision: sql.NullInt64{Int64: 1, Valid: true},
				publishedAt:       sql.NullInt64{Int64: announcementTestNow, Valid: true},
			}, testCase.requested)
			if err != nil {
				t.Fatalf("selectPublishedLanguage: %v", err)
			}
			if selected.language != testCase.wantLanguage {
				t.Fatalf("effective language=%q, want %q", selected.language, testCase.wantLanguage)
			}
			if testCase.wantFallbackFrom == "" {
				if selected.fallback != nil {
					t.Fatalf("fallback_from=%q, want null", *selected.fallback)
				}
				return
			}
			if selected.fallback == nil || *selected.fallback != testCase.wantFallbackFrom {
				t.Fatalf("fallback_from=%v, want %q", selected.fallback, testCase.wantFallbackFrom)
			}
		})
	}
}

func TestAnnouncementIdempotencyReauthorizesReplayAndNeverReusesAuditedID(t *testing.T) {
	firstID, err := db.GenerateOpaqueID("ann_")
	if err != nil {
		t.Fatalf("GenerateOpaqueID first: %v", err)
	}
	secondID, err := db.GenerateOpaqueID("ann_")
	if err != nil {
		t.Fatalf("GenerateOpaqueID second: %v", err)
	}
	var mu sync.Mutex
	ids := []string{firstID, firstID, secondID}
	environment := newAnnouncementTestEnvironmentWithID(t, func() (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if len(ids) == 0 {
			return "", errors.New("identifier queue exhausted")
		}
		id := ids[0]
		ids = ids[1:]
		return id, nil
	})
	adminID := environment.seedUser(t, "", "en", true)

	created := environment.createDraft(t, adminID, 'j', "标题", "正文", "Title", "Body", nil)
	deleteMutation := announcementTestMutation(t, 'k', "DELETE", routeAdminAnnouncement, []string{created.ID}, map[string]any{
		"expected_revision": "1", "confirmation": PermanentDeleteConfirmation, "reason": "remove",
	})
	if _, err := environment.service.Delete(context.Background(), adminID, created.ID, deleteMutation, 1, PermanentDeleteConfirmation, "remove"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	replacement := environment.createDraft(t, adminID, 'l', "替代", "正文", "Replacement", "Body", nil)
	if replacement.ID != secondID || replacement.ID == firstID {
		t.Fatalf("audited ID reused: first=%q replacement=%q want=%q", firstID, replacement.ID, secondID)
	}

	title := "Replay title"
	mutation := announcementTestMutation(t, 'm', "PATCH", routeAdminAnnouncement, []string{replacement.ID}, map[string]any{
		"expected_revision": "1", "title_en": title,
	})
	before := environment.authorizer.calls.Load()
	first, err := environment.service.Edit(context.Background(), adminID, replacement.ID, mutation, 1, DraftPatch{TitleEN: &title})
	if err != nil || first.Replayed {
		t.Fatalf("first edit: result=%+v err=%v", first, err)
	}
	replay, err := environment.service.Edit(context.Background(), adminID, replacement.ID, mutation, 1, DraftPatch{TitleEN: &title})
	if err != nil || !replay.Replayed || replay.Status != first.Status || string(replay.Body) != string(first.Body) {
		t.Fatalf("edit replay: result=%+v err=%v", replay, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(replay.Body, &fields); err != nil || len(fields) != 2 || fields["id"] == nil || fields["revision"] == nil ||
		replay.Value != first.Value || replay.Value.ID != replacement.ID || replay.Value.Revision != "2" {
		t.Fatalf("edit replay is not the exact receipt: first=%+v replay=%+v fields=%v err=%v", first, replay, fields, err)
	}
	if got := environment.authorizer.calls.Load() - before; got != 2 {
		t.Fatalf("final authorization calls=%d want=2", got)
	}

	environment.authorizer.deny.Store(true)
	if _, err := environment.service.Edit(context.Background(), adminID, replacement.ID, mutation, 1, DraftPatch{TitleEN: &title}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("replay bypassed revoked admin authorization: %v", err)
	}
	environment.authorizer.deny.Store(false)
	routeConflict := announcementTestMutation(t, 'm', "POST", routeAdminPublish, []string{replacement.ID}, map[string]any{
		"expected_revision": "2",
	})
	if _, err := environment.service.Publish(context.Background(), adminID, replacement.ID, routeConflict, 2); !errors.Is(err, ErrConflict) {
		t.Fatalf("idempotency-key route mismatch err=%v want conflict", err)
	}
	different := "different"
	conflictMutation := announcementTestMutation(t, 'm', "PATCH", routeAdminAnnouncement, []string{replacement.ID}, map[string]any{
		"expected_revision": "2", "title_en": different,
	})
	if _, err := environment.service.Edit(context.Background(), adminID, replacement.ID, conflictMutation, 2, DraftPatch{TitleEN: &different}); !errors.Is(err, ErrConflict) {
		t.Fatalf("idempotency-key body mismatch err=%v want conflict", err)
	}
}

func TestAnnouncementAdminReadsReauthorizeAndDenyBeforeLazyExpiry(t *testing.T) {
	for _, operation := range []string{"list", "detail", "preview"} {
		t.Run(operation, func(t *testing.T) {
			environment := newAnnouncementTestEnvironment(t)
			adminID := environment.seedUser(t, "", "en", true)
			expiresAt := environment.clock.Load() + 10
			announcement := environment.createDraft(t, adminID, '1', "标题", "正文", "Title", "Body", &expiresAt)
			environment.publish(t, adminID, '2', announcement)

			invoke := func(denied bool) {
				switch operation {
				case "list":
					page, err := environment.service.ListAdmin(context.Background(), adminID, AdminListQuery{Limit: 1})
					if denied {
						if !errors.Is(err, ErrForbidden) || len(page.Data) != 0 || page.NextCursor != nil {
							t.Fatalf("denied list returned content: page=%+v err=%v", page, err)
						}
						return
					}
					if err != nil || len(page.Data) != 1 || page.Data[0].ID != announcement.ID {
						t.Fatalf("authorized list: page=%+v err=%v", page, err)
					}
				case "detail":
					value, err := environment.service.GetAdmin(context.Background(), adminID, announcement.ID)
					if denied {
						if !errors.Is(err, ErrForbidden) || value != (AdminAnnouncement{}) {
							t.Fatalf("denied detail returned content: value=%+v err=%v", value, err)
						}
						return
					}
					if err != nil || value.ID != announcement.ID || value.Revision != "2" {
						t.Fatalf("authorized detail: value=%+v err=%v", value, err)
					}
				case "preview":
					value, err := environment.service.Preview(context.Background(), adminID, announcement.ID, PreviewInput{ExpectedRevision: 2})
					if denied {
						if !errors.Is(err, ErrForbidden) || value != (Preview{}) {
							t.Fatalf("denied preview returned content: value=%+v err=%v", value, err)
						}
						return
					}
					if err != nil || value.RenderedZH == nil || value.RenderedEN == nil {
						t.Fatalf("authorized preview: value=%+v err=%v", value, err)
					}
				default:
					t.Fatalf("unknown operation %q", operation)
				}
			}

			before := environment.authorizer.calls.Load()
			invoke(false)
			if got := environment.authorizer.calls.Load() - before; got != 1 {
				t.Fatalf("authorized final authorization calls=%d want=1", got)
			}

			environment.clock.Store(expiresAt)
			environment.authorizer.deny.Store(true)
			before = environment.authorizer.calls.Load()
			invoke(true)
			if got := environment.authorizer.calls.Load() - before; got != 1 {
				t.Fatalf("denied final authorization calls=%d want=1", got)
			}

			var state string
			var revision, updatedAt int64
			if err := environment.store.DB().QueryRow(`SELECT state,revision,updated_at FROM announcements WHERE id=?`, announcement.ID).
				Scan(&state, &revision, &updatedAt); err != nil {
				t.Fatalf("read announcement after denied admin read: %v", err)
			}
			if state != "published" || revision != 2 || updatedAt != announcementTestNow {
				t.Fatalf("denied admin read materialized expiry: state=%q revision=%d updated_at=%d", state, revision, updatedAt)
			}
			var expiryAudits int
			if err := environment.store.DB().QueryRow(`
SELECT count(*) FROM announcement_audits WHERE announcement_id_text=? AND action='expire'`, announcement.ID).Scan(&expiryAudits); err != nil {
				t.Fatalf("count expiry audits after denied admin read: %v", err)
			}
			if expiryAudits != 0 {
				t.Fatalf("denied admin read wrote expiry audits=%d", expiryAudits)
			}
		})
	}
}

func TestAnnouncementRecoveryRetentionAndActiveLegalHold(t *testing.T) {
	environment := newAnnouncementTestEnvironment(t)
	adminID := environment.seedUser(t, "", "en", true)
	oldAt := announcementTestNow - announcementAuditTTL - 100
	heldID, err := db.GenerateOpaqueID("ann_")
	if err != nil {
		t.Fatalf("GenerateOpaqueID held: %v", err)
	}
	unheldID, err := db.GenerateOpaqueID("ann_")
	if err != nil {
		t.Fatalf("GenerateOpaqueID unheld: %v", err)
	}
	insertAudit := func(id string) int64 {
		result, err := environment.store.DB().Exec(`
INSERT INTO announcement_audits(announcement_id_text,actor_user_id,action,from_revision,to_revision,reason,created_at,actor_deidentify_at)
VALUES(?,?,'create',0,1,'',?,?)`, id, adminID, oldAt, oldAt+announcementAuditActorTTL)
		if err != nil {
			t.Fatalf("insert old audit: %v", err)
		}
		auditID, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("old audit id: %v", err)
		}
		return auditID
	}
	heldAuditID := insertAudit(heldID)
	unheldAuditID := insertAudit(unheldID)
	holdID, err := db.GenerateOpaqueID("lgh_")
	if err != nil {
		t.Fatalf("GenerateOpaqueID hold: %v", err)
	}
	tx, err := environment.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin hold: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
INSERT INTO legal_holds(id,object_kind,object_ref,state,revision,basis,created_by_user_id,created_at,expires_at)
VALUES(?,'announcement_audit',?,'active',1,'preserve audit',?,?,?)`,
		holdID, fmt.Sprintf("%d", heldAuditID), adminID, announcementTestNow-10, announcementTestNow+3600); err != nil {
		t.Fatalf("insert hold: %v", err)
	}
	if _, err := tx.Exec(`UPDATE announcement_audits SET legal_hold_consumed=1 WHERE id=?`, heldAuditID); err != nil {
		t.Fatalf("mark held audit: %v", err)
	}
	if _, err := tx.Exec(`
INSERT INTO legal_hold_audits(hold_id_text,actor_user_id,action,reason,created_at)
VALUES(?,?,'create','preserve audit',?)`, holdID, adminID, announcementTestNow-10); err != nil {
		t.Fatalf("insert hold audit: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit hold: %v", err)
	}

	deidentified, deleted, err := environment.repository.RetainAudits(context.Background(), 100)
	if err != nil {
		t.Fatalf("RetainAudits: %v", err)
	}
	if deidentified != 2 || deleted != 1 {
		t.Fatalf("retention deidentified=%d deleted=%d", deidentified, deleted)
	}
	var actor sql.NullInt64
	if err := environment.store.DB().QueryRow(`SELECT actor_user_id FROM announcement_audits WHERE id=?`, heldAuditID).Scan(&actor); err != nil {
		t.Fatalf("held audit missing: %v", err)
	}
	if actor.Valid {
		t.Fatalf("active hold incorrectly retained actor identity: %+v", actor)
	}
	var unheldCount int
	if err := environment.store.DB().QueryRow(`SELECT count(*) FROM announcement_audits WHERE id=?`, unheldAuditID).Scan(&unheldCount); err != nil || unheldCount != 0 {
		t.Fatalf("unheld audit retained: count=%d err=%v", unheldCount, err)
	}

	recovery, err := environment.service.RecoverBeforeListener(context.Background(), 100)
	if err != nil {
		t.Fatalf("RecoverBeforeListener: %v", err)
	}
	if recovery.Expired != 0 || recovery.ActorsDeidentified != 0 || recovery.AuditsDeleted != 0 {
		t.Fatalf("recovery was not idempotent: %+v", recovery)
	}
}
