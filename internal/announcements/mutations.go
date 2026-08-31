package announcements

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

func (service *Service) Create(ctx context.Context, adminID int64, mutation ControlMutation, draft DraftPatch) (MutationResult[AnnouncementMutationReceipt], error) {
	if service == nil || service.repository == nil || !validMutation(mutation, http.MethodPost, routeAdminAnnouncements) || len(mutation.PathIDs) != 0 {
		return MutationResult[AnnouncementMutationReceipt]{}, ErrInvalidRequest
	}
	repository := service.repository
	tx, now, err := repository.beginAuthorizedAdminTx(ctx, adminID)
	if err != nil {
		return MutationResult[AnnouncementMutationReceipt]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	decision, err := beginAnnouncementMutation(ctx, tx, adminID, mutation, now)
	if err != nil {
		return MutationResult[AnnouncementMutationReceipt]{}, err
	}
	if decision.Kind == idempotency.Replay {
		result, err := replayMutation[AnnouncementMutationReceipt](decision)
		if err != nil {
			return MutationResult[AnnouncementMutationReceipt]{}, err
		}
		if err := commitTx(tx, &committed); err != nil {
			return MutationResult[AnnouncementMutationReceipt]{}, err
		}
		return result, nil
	}
	values := applyDraftPatch(draftValues{}, draft)
	if err := repository.validateDraft(values, now); err != nil {
		return MutationResult[AnnouncementMutationReceipt]{}, err
	}
	if draft.Severity == nil || draft.Pinned == nil || draft.Dismissible == nil ||
		draft.TitleZH == nil || draft.BodyZH == nil || draft.TitleEN == nil || draft.BodyEN == nil {
		return MutationResult[AnnouncementMutationReceipt]{}, ErrInvalidRequest
	}
	var count int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM announcements`).Scan(&count); err != nil {
		return MutationResult[AnnouncementMutationReceipt]{}, fmt.Errorf("announcements: count active announcements: %w", err)
	}
	if count >= maxAnnouncements {
		return MutationResult[AnnouncementMutationReceipt]{}, ErrResourceLimit
	}
	id, err := repository.allocateID(ctx, tx)
	if err != nil {
		return MutationResult[AnnouncementMutationReceipt]{}, err
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO announcements(
 id,state,revision,draft_title_zh,draft_body_zh,draft_title_en,draft_body_en,
 severity,pinned,dismissible,expires_at,created_at,updated_at
) VALUES(?,'draft',1,?,?,?,?,?,?,?,?,?,?)`,
		id, values.titleZH, values.bodyZH, values.titleEN, values.bodyEN,
		values.severity, boolInt(values.pinned), boolInt(values.dismissible), values.expiresAt, now, now)
	if err != nil {
		return MutationResult[AnnouncementMutationReceipt]{}, fmt.Errorf("announcements: insert draft: %w", err)
	}
	if err := appendAudit(ctx, tx, id, adminID, "create", 0, 1, "", now); err != nil {
		return MutationResult[AnnouncementMutationReceipt]{}, err
	}
	revision, err := decimalRevision(1)
	if err != nil {
		return MutationResult[AnnouncementMutationReceipt]{}, err
	}
	response := AnnouncementMutationReceipt{ID: id, Revision: revision}
	result, err := finishJSONMutation(ctx, tx, decision, http.StatusCreated, response)
	if err != nil {
		return MutationResult[AnnouncementMutationReceipt]{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return MutationResult[AnnouncementMutationReceipt]{}, err
	}
	return result, nil
}

func (service *Service) Edit(ctx context.Context, adminID int64, id string, mutation ControlMutation, expectedRevision int64, patch DraftPatch) (MutationResult[AnnouncementMutationReceipt], error) {
	if service == nil || service.repository == nil || !dbAnnouncementID(id) || expectedRevision < 1 ||
		!validMutationForID(mutation, http.MethodPatch, routeAdminAnnouncement, id) || !patchHasAny(patch) {
		return MutationResult[AnnouncementMutationReceipt]{}, ErrInvalidRequest
	}
	repository := service.repository
	tx, now, err := repository.beginAuthorizedAdminTx(ctx, adminID)
	if err != nil {
		return MutationResult[AnnouncementMutationReceipt]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	decision, err := beginAnnouncementMutation(ctx, tx, adminID, mutation, now)
	if err != nil {
		return MutationResult[AnnouncementMutationReceipt]{}, err
	}
	if decision.Kind == idempotency.Replay {
		result, err := replayMutation[AnnouncementMutationReceipt](decision)
		if err != nil {
			return MutationResult[AnnouncementMutationReceipt]{}, err
		}
		if err := commitTx(tx, &committed); err != nil {
			return MutationResult[AnnouncementMutationReceipt]{}, err
		}
		return result, nil
	}
	if _, err := expireOneTx(ctx, tx, id, now); err != nil {
		return MutationResult[AnnouncementMutationReceipt]{}, err
	}
	row, err := readAnnouncementTx(ctx, tx, id)
	if err != nil {
		return MutationResult[AnnouncementMutationReceipt]{}, err
	}
	if row.revision != expectedRevision {
		return MutationResult[AnnouncementMutationReceipt]{}, ErrConflict
	}
	values := applyDraftPatch(draftFromRow(row), patch)
	if err := repository.validateDraft(values, now); err != nil {
		return MutationResult[AnnouncementMutationReceipt]{}, err
	}
	next, err := nextRevision(row.revision)
	if err != nil {
		return MutationResult[AnnouncementMutationReceipt]{}, err
	}
	resultSQL, err := tx.ExecContext(ctx, `
UPDATE announcements SET revision=?,draft_title_zh=?,draft_body_zh=?,draft_title_en=?,draft_body_en=?,
 severity=?,pinned=?,dismissible=?,expires_at=?,updated_at=? WHERE id=? AND revision=?`,
		next, values.titleZH, values.bodyZH, values.titleEN, values.bodyEN,
		values.severity, boolInt(values.pinned), boolInt(values.dismissible), values.expiresAt, now, id, row.revision)
	if err != nil {
		return MutationResult[AnnouncementMutationReceipt]{}, fmt.Errorf("announcements: edit draft: %w", err)
	}
	if !exactlyOne(resultSQL) {
		return MutationResult[AnnouncementMutationReceipt]{}, ErrConflict
	}
	if err := appendAudit(ctx, tx, id, adminID, "edit", row.revision, next, "", now); err != nil {
		return MutationResult[AnnouncementMutationReceipt]{}, err
	}
	revision, err := decimalRevision(next)
	if err != nil {
		return MutationResult[AnnouncementMutationReceipt]{}, err
	}
	response := AnnouncementMutationReceipt{ID: id, Revision: revision}
	result, err := finishJSONMutation(ctx, tx, decision, http.StatusOK, response)
	if err != nil {
		return MutationResult[AnnouncementMutationReceipt]{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return MutationResult[AnnouncementMutationReceipt]{}, err
	}
	return result, nil
}

func (service *Service) Publish(ctx context.Context, adminID int64, id string, mutation ControlMutation, expectedRevision int64) (MutationResult[struct{}], error) {
	if service == nil || service.repository == nil || !dbAnnouncementID(id) || expectedRevision < 1 ||
		!validMutationForID(mutation, http.MethodPost, routeAdminPublish, id) {
		return MutationResult[struct{}]{}, ErrInvalidRequest
	}
	repository := service.repository
	tx, now, err := repository.beginAuthorizedAdminTx(ctx, adminID)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	decision, err := beginAnnouncementMutation(ctx, tx, adminID, mutation, now)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	if decision.Kind == idempotency.Replay {
		result, err := replayMutation[struct{}](decision)
		if err != nil {
			return MutationResult[struct{}]{}, err
		}
		if err := commitTx(tx, &committed); err != nil {
			return MutationResult[struct{}]{}, err
		}
		return result, nil
	}
	if _, err := expireOneTx(ctx, tx, id, now); err != nil {
		return MutationResult[struct{}]{}, err
	}
	row, err := readAnnouncementTx(ctx, tx, id)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	if row.revision != expectedRevision {
		return MutationResult[struct{}]{}, ErrConflict
	}
	values := draftFromRow(row)
	if err := repository.validateDraft(values, now); err != nil ||
		(!completeLanguage(values.titleZH, values.bodyZH) && !completeLanguage(values.titleEN, values.bodyEN)) {
		return MutationResult[struct{}]{}, ErrInvalidRequest
	}
	next, err := nextRevision(row.revision)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	resultSQL, err := tx.ExecContext(ctx, `
UPDATE announcements SET state='published',revision=?,published_revision=?,published_at=?,withdrawn_at=NULL,
 published_title_zh=draft_title_zh,published_body_zh=draft_body_zh,
 published_title_en=draft_title_en,published_body_en=draft_body_en,updated_at=?
WHERE id=? AND revision=?`, next, next, now, now, id, row.revision)
	if err != nil {
		return MutationResult[struct{}]{}, fmt.Errorf("announcements: publish: %w", err)
	}
	if !exactlyOne(resultSQL) {
		return MutationResult[struct{}]{}, ErrConflict
	}
	if err := appendAudit(ctx, tx, id, adminID, "publish", row.revision, next, "", now); err != nil {
		return MutationResult[struct{}]{}, err
	}
	result, err := finishEmptyMutation(ctx, tx, decision, http.StatusOK)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return MutationResult[struct{}]{}, err
	}
	return result, nil
}

func (service *Service) Withdraw(ctx context.Context, adminID int64, id string, mutation ControlMutation, expectedRevision int64, reason string) (MutationResult[struct{}], error) {
	if service == nil || service.repository == nil || !dbAnnouncementID(id) || expectedRevision < 1 || !validReason(reason, true) ||
		!validMutationForID(mutation, http.MethodPost, routeAdminWithdraw, id) {
		return MutationResult[struct{}]{}, ErrInvalidRequest
	}
	repository := service.repository
	tx, now, err := repository.beginAuthorizedAdminTx(ctx, adminID)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	decision, err := beginAnnouncementMutation(ctx, tx, adminID, mutation, now)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	if decision.Kind == idempotency.Replay {
		result, err := replayMutation[struct{}](decision)
		if err != nil {
			return MutationResult[struct{}]{}, err
		}
		if err := commitTx(tx, &committed); err != nil {
			return MutationResult[struct{}]{}, err
		}
		return result, nil
	}
	if _, err := expireOneTx(ctx, tx, id, now); err != nil {
		return MutationResult[struct{}]{}, err
	}
	row, err := readAnnouncementTx(ctx, tx, id)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	if row.revision != expectedRevision || row.state != "published" {
		return MutationResult[struct{}]{}, ErrConflict
	}
	next, err := nextRevision(row.revision)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	resultSQL, err := tx.ExecContext(ctx, `
UPDATE announcements SET state='withdrawn',revision=?,withdrawn_at=?,updated_at=? WHERE id=? AND revision=? AND state='published'`,
		next, now, now, id, row.revision)
	if err != nil {
		return MutationResult[struct{}]{}, fmt.Errorf("announcements: withdraw: %w", err)
	}
	if !exactlyOne(resultSQL) {
		return MutationResult[struct{}]{}, ErrConflict
	}
	if err := appendAudit(ctx, tx, id, adminID, "withdraw", row.revision, next, reason, now); err != nil {
		return MutationResult[struct{}]{}, err
	}
	result, err := finishEmptyMutation(ctx, tx, decision, http.StatusOK)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return MutationResult[struct{}]{}, err
	}
	return result, nil
}

func (service *Service) Delete(ctx context.Context, adminID int64, id string, mutation ControlMutation, expectedRevision int64, confirmation, reason string) (MutationResult[struct{}], error) {
	if service == nil || service.repository == nil || !dbAnnouncementID(id) || expectedRevision < 1 ||
		confirmation != PermanentDeleteConfirmation || !validReason(reason, true) ||
		!validMutationForID(mutation, http.MethodDelete, routeAdminAnnouncement, id) {
		return MutationResult[struct{}]{}, ErrInvalidRequest
	}
	repository := service.repository
	tx, now, err := repository.beginAuthorizedAdminTx(ctx, adminID)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	decision, err := beginAnnouncementMutation(ctx, tx, adminID, mutation, now)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	if decision.Kind == idempotency.Replay {
		result, err := replayMutation[struct{}](decision)
		if err != nil {
			return MutationResult[struct{}]{}, err
		}
		if err := commitTx(tx, &committed); err != nil {
			return MutationResult[struct{}]{}, err
		}
		return result, nil
	}
	row, err := readAnnouncementTx(ctx, tx, id)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	if row.revision != expectedRevision {
		return MutationResult[struct{}]{}, ErrConflict
	}
	next, err := nextRevision(row.revision)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	resultSQL, err := tx.ExecContext(ctx, `DELETE FROM announcements WHERE id=? AND revision=?`, id, row.revision)
	if err != nil {
		return MutationResult[struct{}]{}, fmt.Errorf("announcements: permanently delete content: %w", err)
	}
	if !exactlyOne(resultSQL) {
		return MutationResult[struct{}]{}, ErrConflict
	}
	if err := appendAudit(ctx, tx, id, adminID, "delete", row.revision, next, reason, now); err != nil {
		return MutationResult[struct{}]{}, err
	}
	result, err := finishEmptyMutation(ctx, tx, decision, http.StatusNoContent)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return MutationResult[struct{}]{}, err
	}
	return result, nil
}

func (repository *Repository) allocateID(ctx context.Context, tx *sql.Tx) (string, error) {
	for attempt := 0; attempt < 8; attempt++ {
		id, err := repository.newID()
		if err != nil {
			return "", fmt.Errorf("announcements: generate identifier: %w", err)
		}
		if !dbAnnouncementID(id) {
			return "", ErrUnavailable
		}
		var exists int
		if err := tx.QueryRowContext(ctx, `
SELECT EXISTS(SELECT 1 FROM announcements WHERE id=? UNION ALL SELECT 1 FROM announcement_audits WHERE announcement_id_text=? LIMIT 1)`, id, id).Scan(&exists); err != nil {
			return "", fmt.Errorf("announcements: check identifier reuse: %w", err)
		}
		if exists == 0 {
			return id, nil
		}
	}
	return "", ErrUnavailable
}

func appendAudit(ctx context.Context, tx *sql.Tx, id string, actorID int64, action string, fromRevision, toRevision int64, reason string, now int64) error {
	var actor any
	if actorID > 0 {
		actor = actorID
	}
	_, err := tx.ExecContext(ctx, `
INSERT INTO announcement_audits(
 announcement_id_text,actor_user_id,action,from_revision,to_revision,reason,created_at,actor_deidentify_at
) VALUES(?,?,?,?,?,?,?,?)`, id, actor, action, fromRevision, toRevision, reason, now, now+announcementAuditActorTTL)
	if err != nil {
		return fmt.Errorf("announcements: append %s audit: %w", action, err)
	}
	return nil
}

func readAnnouncementTx(ctx context.Context, tx *sql.Tx, id string) (rawAnnouncement, error) {
	row, err := scanAnnouncement(tx.QueryRowContext(ctx, `SELECT `+announcementSelectColumns+` FROM announcements WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return rawAnnouncement{}, ErrNotFound
	}
	if err != nil {
		return rawAnnouncement{}, fmt.Errorf("announcements: read announcement: %w", err)
	}
	return row, nil
}

func validMutation(mutation ControlMutation, method, route string) bool {
	return mutation.IdempotencyKey != "" && mutation.Method == method && mutation.Route == route && mutation.Query == "" &&
		len(mutation.CanonicalBody) <= 256*1024
}

func validMutationForID(mutation ControlMutation, method, route, id string) bool {
	return validMutation(mutation, method, route) && len(mutation.PathIDs) == 1 && mutation.PathIDs[0] == id
}

func patchHasAny(patch DraftPatch) bool {
	return patch.TitleZH != nil || patch.BodyZH != nil || patch.TitleEN != nil || patch.BodyEN != nil ||
		patch.Severity != nil || patch.Pinned != nil || patch.Dismissible != nil || patch.ExpiresAt.Set
}

func exactlyOne(result sql.Result) bool {
	if result == nil {
		return false
	}
	rows, err := result.RowsAffected()
	return err == nil && rows == 1
}
