package announcements

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestAnnouncementOrderingCursorBindingTamperAndExpiry(t *testing.T) {
	environment := newAnnouncementTestEnvironment(t)
	adminID := environment.seedUser(t, "", "en", true)
	userID := environment.seedUser(t, "announcement-page-owner", "en", false)
	otherUserID := environment.seedUser(t, "announcement-page-other", "en", false)

	pinned := environment.createDraft(t, adminID, 'n', "置顶", "置顶正文", "Pinned", "Pinned body", nil)
	environment.publish(t, adminID, 'o', pinned)
	environment.clock.Add(1)
	unpinned := environment.createDraft(t, adminID, 'p', "普通", "普通正文", "Normal", "Normal body", nil)
	value := false
	editMutation := announcementTestMutation(t, 'q', "PATCH", routeAdminAnnouncement, []string{unpinned.ID}, map[string]any{
		"expected_revision": "1", "pinned": false,
	})
	edited, err := environment.service.Edit(context.Background(), adminID, unpinned.ID, editMutation, 1, DraftPatch{Pinned: &value})
	if err != nil {
		t.Fatalf("Edit unpinned: %v", err)
	}
	environment.publish(t, adminID, 'r', AdminAnnouncement{ID: edited.Value.ID, Revision: edited.Value.Revision})

	first, err := environment.service.ListUser(context.Background(), userID, PageQuery{Limit: 1})
	if err != nil || len(first.Data) != 1 || first.Data[0].ID != pinned.ID || first.NextCursor == nil {
		t.Fatalf("first page: %+v err=%v", first, err)
	}
	second, err := environment.service.ListUser(context.Background(), userID, PageQuery{Limit: 1, Cursor: *first.NextCursor})
	if err != nil || len(second.Data) != 1 || second.Data[0].ID != unpinned.ID || second.NextCursor != nil {
		t.Fatalf("second page: %+v err=%v", second, err)
	}

	tampered := []byte(*first.NextCursor)
	if tampered[len(tampered)-1] == 'A' {
		tampered[len(tampered)-1] = 'B'
	} else {
		tampered[len(tampered)-1] = 'A'
	}
	if _, err := environment.service.ListUser(context.Background(), userID, PageQuery{Limit: 1, Cursor: string(tampered)}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("tampered cursor err=%v", err)
	}
	if _, err := environment.service.ListUser(context.Background(), otherUserID, PageQuery{Limit: 1, Cursor: *first.NextCursor}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("cross-account cursor err=%v", err)
	}
	environment.clock.Add(cursorLifetimeSeconds)
	if _, err := environment.service.ListUser(context.Background(), userID, PageQuery{Limit: 1, Cursor: *first.NextCursor}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expired cursor err=%v", err)
	}
}

func TestAnnouncementLimitsAtExactBoundaries(t *testing.T) {
	environment := newAnnouncementTestEnvironment(t)
	adminID := environment.seedUser(t, "", "en", true)
	titleAtLimit := strings.Repeat("界", 160)
	bodyAtLimit := strings.Repeat("界", maxAnnouncementBodyBytes/3) + strings.Repeat("x", maxAnnouncementBodyBytes%3)
	severity := "info"
	pinned, dismissible := false, true
	patch := DraftPatch{
		TitleZH: &titleAtLimit, BodyZH: &bodyAtLimit,
		TitleEN: stringPointer(""), BodyEN: stringPointer(""), Severity: &severity,
		Pinned: &pinned, Dismissible: &dismissible,
	}
	canonical := map[string]any{
		"title_zh": titleAtLimit, "body_zh": bodyAtLimit, "title_en": "", "body_en": "",
		"severity": severity, "pinned": pinned, "dismissible": dismissible,
	}
	if _, err := environment.service.Create(context.Background(), adminID,
		announcementTestMutation(t, 's', "POST", routeAdminAnnouncements, nil, canonical), patch); err != nil {
		t.Fatalf("exact title/body boundary rejected: %v", err)
	}

	bodyAtLimitEN := strings.Repeat("🐟", maxAnnouncementBodyBytes/4)
	dualTitle := "English"
	dualPatch := DraftPatch{
		TitleZH: &titleAtLimit, BodyZH: &bodyAtLimit, TitleEN: &dualTitle, BodyEN: &bodyAtLimitEN,
		Severity: &severity, Pinned: &pinned, Dismissible: &dismissible,
	}
	dual, err := environment.service.Create(context.Background(), adminID,
		announcementTestMutation(t, 'x', "POST", routeAdminAnnouncements, nil, map[string]any{
			"title_zh": titleAtLimit, "body_zh": bodyAtLimit, "title_en": dualTitle, "body_en": bodyAtLimitEN,
			"severity": severity, "pinned": pinned, "dismissible": dismissible,
		}), dualPatch)
	if err != nil {
		t.Fatalf("dual-language exact body boundaries rejected: %v", err)
	}
	stored, err := environment.service.GetAdmin(context.Background(), adminID, dual.Value.ID)
	if err != nil || stored.Draft.ZH == nil || stored.Draft.EN == nil ||
		len(stored.Draft.ZH.Body) != 64*1024 || len(stored.Draft.EN.Body) != 64*1024 {
		t.Fatalf("dual-language boundary round trip: value=%+v err=%v", stored, err)
	}
	replacementBody := strings.Repeat("z", 64*1024)
	editAtLimit, err := environment.service.Edit(context.Background(), adminID, dual.Value.ID,
		announcementTestMutation(t, 'y', "PATCH", routeAdminAnnouncement, []string{dual.Value.ID}, map[string]any{
			"expected_revision": "1", "body_en": replacementBody,
		}), 1, DraftPatch{BodyEN: &replacementBody})
	if err != nil || editAtLimit.Value.ID != dual.Value.ID || editAtLimit.Value.Revision != "2" {
		t.Fatalf("exact edit boundary: result=%+v err=%v", editAtLimit, err)
	}
	stored, err = environment.service.GetAdmin(context.Background(), adminID, dual.Value.ID)
	if err != nil || stored.Draft.EN == nil || stored.Draft.EN.Body != replacementBody {
		t.Fatalf("exact edit boundary round trip: value=%+v err=%v", stored, err)
	}

	tooLongTitle := titleAtLimit + "界"
	if _, err := environment.service.Create(context.Background(), adminID,
		announcementTestMutation(t, 't', "POST", routeAdminAnnouncements, nil, map[string]any{
			"title_zh": tooLongTitle, "body_zh": "body", "title_en": "", "body_en": "",
			"severity": severity, "pinned": pinned, "dismissible": dismissible,
		}), DraftPatch{
			TitleZH: &tooLongTitle, BodyZH: stringPointer("body"), TitleEN: stringPointer(""), BodyEN: stringPointer(""),
			Severity: &severity, Pinned: &pinned, Dismissible: &dismissible,
		}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("overlong title err=%v", err)
	}

	tooLongBody := bodyAtLimit + "x"
	if _, err := environment.service.Create(context.Background(), adminID,
		announcementTestMutation(t, 'u', "POST", routeAdminAnnouncements, nil, map[string]any{
			"title_zh": "title", "body_zh": tooLongBody, "title_en": "", "body_en": "",
			"severity": severity, "pinned": pinned, "dismissible": dismissible,
		}), DraftPatch{
			TitleZH: stringPointer("title"), BodyZH: &tooLongBody, TitleEN: stringPointer(""), BodyEN: stringPointer(""),
			Severity: &severity, Pinned: &pinned, Dismissible: &dismissible,
		}); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("overlong body err=%v", err)
	}
}

func TestAnnouncementCapacityIsHardBoundWithoutEviction(t *testing.T) {
	environment := newAnnouncementTestEnvironment(t)
	adminID := environment.seedUser(t, "", "en", true)
	tx, err := environment.store.DB().Begin()
	if err != nil {
		t.Fatalf("begin seed: %v", err)
	}
	defer tx.Rollback()
	for index := int64(0); index < maxAnnouncements; index++ {
		id, err := db.GenerateOpaqueID("ann_")
		if err != nil {
			t.Fatalf("GenerateOpaqueID %d: %v", index, err)
		}
		if _, err := tx.Exec(`
INSERT INTO announcements(id,state,revision,draft_title_zh,draft_body_zh,draft_title_en,draft_body_en,severity,pinned,dismissible,created_at,updated_at)
VALUES(?,'draft',1,'标题','正文','','','info',0,1,?,?)`, id, announcementTestNow, announcementTestNow); err != nil {
			t.Fatalf("seed announcement %d: %v", index, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit seed: %v", err)
	}
	severity := "info"
	pinned, dismissible := false, true
	patch := DraftPatch{
		TitleZH: stringPointer("capacity"), BodyZH: stringPointer("body"),
		TitleEN: stringPointer(""), BodyEN: stringPointer(""), Severity: &severity,
		Pinned: &pinned, Dismissible: &dismissible,
	}
	mutation := announcementTestMutation(t, 'v', "POST", routeAdminAnnouncements, nil, map[string]any{
		"title_zh": "capacity", "body_zh": "body", "title_en": "", "body_en": "",
		"severity": severity, "pinned": pinned, "dismissible": dismissible,
	})
	if _, err := environment.service.Create(context.Background(), adminID, mutation, patch); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("capacity create err=%v", err)
	}
	var count int64
	if err := environment.store.DB().QueryRow(`SELECT count(*) FROM announcements`).Scan(&count); err != nil || count != maxAnnouncements {
		t.Fatalf("capacity count=%d err=%v", count, err)
	}
	var auditCount int
	if err := environment.store.DB().QueryRow(`SELECT count(*) FROM announcement_audits`).Scan(&auditCount); err != nil || auditCount != 0 {
		t.Fatalf("capacity failure wrote audit: count=%d err=%v", auditCount, err)
	}
}

func stringPointer(value string) *string { return &value }

func TestAnnouncementInvalidExpiryAndRevisionAreRejected(t *testing.T) {
	environment := newAnnouncementTestEnvironment(t)
	adminID := environment.seedUser(t, "", "en", true)
	past := environment.clock.Load()
	severity := "info"
	pinned, dismissible := false, true
	patch := DraftPatch{
		TitleZH: stringPointer("title"), BodyZH: stringPointer("body"), TitleEN: stringPointer(""), BodyEN: stringPointer(""),
		Severity: &severity, Pinned: &pinned, Dismissible: &dismissible, ExpiresAt: NullableTime{Set: true, Value: &past},
	}
	mutation := announcementTestMutation(t, 'w', "POST", routeAdminAnnouncements, nil, map[string]any{
		"title_zh": "title", "body_zh": "body", "title_en": "", "body_en": "",
		"severity": severity, "pinned": pinned, "dismissible": dismissible, "expires_at": past,
	})
	if _, err := environment.service.Create(context.Background(), adminID, mutation, patch); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("non-future expiry err=%v", err)
	}
}
