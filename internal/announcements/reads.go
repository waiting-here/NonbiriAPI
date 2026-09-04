package announcements

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func (service *Service) ListUser(ctx context.Context, userID int64, query PageQuery) (Page[AnnouncementSummary], error) {
	empty := Page[AnnouncementSummary]{Data: []AnnouncementSummary{}}
	if service == nil || service.repository == nil || userID <= 0 || !validLimit(query.Limit) {
		return empty, ErrInvalidRequest
	}
	repository := service.repository
	now, err := repository.nowUnix()
	if err != nil {
		return empty, err
	}
	if _, err := repository.ExpireDue(ctx, maxPageLimit); err != nil {
		return empty, err
	}
	tx, err := beginTx(ctx, repository.db)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	language, err := readUserLanguage(ctx, tx, userID)
	if err != nil {
		return empty, err
	}
	epoch, err := readEpoch(ctx, tx)
	if err != nil {
		return empty, err
	}
	limit := normalizedLimit(query.Limit)
	args := []any{now}
	cursorPredicate := ""
	if query.Cursor != "" {
		payload, err := repository.cursors.decode(query.Cursor, "announcements:user", userCursorOwner(userID, language), now)
		if err != nil || payload.Pinned < 0 || payload.ID == "" {
			return empty, ErrInvalidRequest
		}
		cursorPredicate = ` AND (pinned<? OR (pinned=? AND (published_at<? OR (published_at=? AND id>?))))`
		args = append(args, payload.Pinned, payload.Pinned, payload.Time, payload.Time, payload.ID)
	}
	args = append(args, limit+1)
	rows, err := tx.QueryContext(ctx, `SELECT `+announcementSelectColumns+`
FROM announcements WHERE state='published' AND (expires_at IS NULL OR expires_at>?)`+cursorPredicate+`
ORDER BY pinned DESC,published_at DESC,id ASC LIMIT ?`, args...)
	if err != nil {
		return empty, fmt.Errorf("announcements: list user announcements: %w", err)
	}
	defer rows.Close()
	rawRows := make([]rawAnnouncement, 0, limit+1)
	for rows.Next() {
		row, err := scanAnnouncement(rows)
		if err != nil {
			return empty, fmt.Errorf("announcements: scan user announcement: %w", err)
		}
		rawRows = append(rawRows, row)
	}
	if err := rows.Err(); err != nil {
		return empty, fmt.Errorf("announcements: list user announcements: %w", err)
	}
	hasMore := len(rawRows) > limit
	if hasMore {
		rawRows = rawRows[:limit]
	}
	out := Page[AnnouncementSummary]{Data: make([]AnnouncementSummary, 0, len(rawRows))}
	for _, row := range rawRows {
		summary, err := repository.userSummary(row, epoch, language)
		if err != nil {
			return empty, err
		}
		out.Data = append(out.Data, summary)
	}
	if hasMore && len(rawRows) > 0 {
		last := rawRows[len(rawRows)-1]
		cursor, err := repository.cursors.encode(announcementCursorPayload{
			Version: announcementCursorVersion, Scope: "announcements:user", Owner: userCursorOwner(userID, language),
			Expiry: now + cursorLifetimeSeconds, Pinned: int(last.pinned), Time: last.publishedAt.Int64, ID: last.id,
		})
		if err != nil {
			return empty, err
		}
		out.NextCursor = &cursor
	}
	if err := tx.Commit(); err != nil {
		return empty, fmt.Errorf("announcements: commit user list read: %w", err)
	}
	return out, nil
}

func (service *Service) GetUser(ctx context.Context, userID int64, id string) (AnnouncementDetail, error) {
	if service == nil || service.repository == nil || userID <= 0 || !dbAnnouncementID(id) {
		return AnnouncementDetail{}, ErrNotFound
	}
	repository := service.repository
	now, err := repository.nowUnix()
	if err != nil {
		return AnnouncementDetail{}, err
	}
	if _, err := repository.ExpireDue(ctx, maxPageLimit); err != nil {
		return AnnouncementDetail{}, err
	}
	tx, err := beginTx(ctx, repository.db)
	if err != nil {
		return AnnouncementDetail{}, err
	}
	defer tx.Rollback()
	language, err := readUserLanguage(ctx, tx, userID)
	if err != nil {
		return AnnouncementDetail{}, err
	}
	epoch, err := readEpoch(ctx, tx)
	if err != nil {
		return AnnouncementDetail{}, err
	}
	row, err := scanAnnouncement(tx.QueryRowContext(ctx, `SELECT `+announcementSelectColumns+`
FROM announcements WHERE id=? AND state='published' AND (expires_at IS NULL OR expires_at>?)`, id, now))
	if errors.Is(err, sql.ErrNoRows) {
		return AnnouncementDetail{}, ErrNotFound
	}
	if err != nil {
		return AnnouncementDetail{}, fmt.Errorf("announcements: read user announcement: %w", err)
	}
	detail, err := repository.userDetail(row, epoch, language)
	if err != nil {
		return AnnouncementDetail{}, err
	}
	if err := tx.Commit(); err != nil {
		return AnnouncementDetail{}, fmt.Errorf("announcements: commit user detail read: %w", err)
	}
	return detail, nil
}

func (service *Service) ListAdmin(ctx context.Context, adminID int64, query AdminListQuery) (Page[AdminAnnouncement], error) {
	empty := Page[AdminAnnouncement]{Data: []AdminAnnouncement{}}
	if service == nil || service.repository == nil || !validLimit(query.Limit) ||
		(query.State != "" && query.State != "draft" && query.State != "published" && query.State != "withdrawn" && query.State != "expired") ||
		(query.Severity != "" && !validSeverity(query.Severity)) {
		return empty, ErrInvalidRequest
	}
	repository := service.repository
	tx, now, err := repository.beginAuthorizedAdminTx(ctx, adminID)
	if err != nil {
		return empty, err
	}
	committed := false
	defer finishTx(tx, &committed)
	if _, err := expireDueTx(ctx, tx, now, maxPageLimit); err != nil {
		return empty, err
	}
	limit := normalizedLimit(query.Limit)
	where := " WHERE 1=1"
	args := make([]any, 0, 8)
	if query.State != "" {
		where += " AND state=?"
		args = append(args, query.State)
	}
	if query.Severity != "" {
		where += " AND severity=?"
		args = append(args, query.Severity)
	}
	owner := query.State + ":" + query.Severity
	if query.Cursor != "" {
		payload, err := repository.cursors.decode(query.Cursor, "announcements:admin", owner, now)
		if err != nil || payload.Pinned != -1 || payload.ID == "" {
			return empty, ErrInvalidRequest
		}
		where += " AND (updated_at<? OR (updated_at=? AND id>?))"
		args = append(args, payload.Time, payload.Time, payload.ID)
	}
	args = append(args, limit+1)
	rows, err := tx.QueryContext(ctx, `SELECT `+announcementSelectColumns+` FROM announcements`+where+`
ORDER BY updated_at DESC,id ASC LIMIT ?`, args...)
	if err != nil {
		return empty, fmt.Errorf("announcements: list administrator announcements: %w", err)
	}
	defer rows.Close()
	rawRows := make([]rawAnnouncement, 0, limit+1)
	for rows.Next() {
		row, err := scanAnnouncement(rows)
		if err != nil {
			return empty, fmt.Errorf("announcements: scan administrator announcement: %w", err)
		}
		rawRows = append(rawRows, row)
	}
	if err := rows.Err(); err != nil {
		return empty, fmt.Errorf("announcements: list administrator announcements: %w", err)
	}
	if err := rows.Close(); err != nil {
		return empty, fmt.Errorf("announcements: close administrator announcement rows: %w", err)
	}
	hasMore := len(rawRows) > limit
	if hasMore {
		rawRows = rawRows[:limit]
	}
	out := Page[AdminAnnouncement]{Data: make([]AdminAnnouncement, 0, len(rawRows))}
	for _, row := range rawRows {
		value, err := repository.adminDTO(row)
		if err != nil {
			return empty, err
		}
		out.Data = append(out.Data, value)
	}
	if hasMore && len(rawRows) > 0 {
		last := rawRows[len(rawRows)-1]
		cursor, err := repository.cursors.encode(announcementCursorPayload{
			Version: announcementCursorVersion, Scope: "announcements:admin", Owner: owner,
			Expiry: now + cursorLifetimeSeconds, Pinned: -1, Time: last.updatedAt, ID: last.id,
		})
		if err != nil {
			return empty, err
		}
		out.NextCursor = &cursor
	}
	if err := commitTx(tx, &committed); err != nil {
		return empty, err
	}
	return out, nil
}

func (service *Service) GetAdmin(ctx context.Context, adminID int64, id string) (AdminAnnouncement, error) {
	if service == nil || service.repository == nil || !dbAnnouncementID(id) {
		return AdminAnnouncement{}, ErrNotFound
	}
	repository := service.repository
	tx, now, err := repository.beginAuthorizedAdminTx(ctx, adminID)
	if err != nil {
		return AdminAnnouncement{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	if _, err := expireOneTx(ctx, tx, id, now); err != nil {
		return AdminAnnouncement{}, err
	}
	row, err := readAnnouncementTx(ctx, tx, id)
	if err != nil {
		return AdminAnnouncement{}, err
	}
	value, err := repository.adminDTO(row)
	if err != nil {
		return AdminAnnouncement{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return AdminAnnouncement{}, err
	}
	return value, nil
}

func (service *Service) Preview(ctx context.Context, adminID int64, id string, input PreviewInput) (Preview, error) {
	if service == nil || service.repository == nil || !dbAnnouncementID(id) || input.ExpectedRevision < 1 ||
		input.Draft.Severity != nil || input.Draft.Pinned != nil || input.Draft.Dismissible != nil || input.Draft.ExpiresAt.Set {
		return Preview{}, ErrInvalidRequest
	}
	repository := service.repository
	tx, now, err := repository.beginAuthorizedAdminTx(ctx, adminID)
	if err != nil {
		return Preview{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	if _, err := expireOneTx(ctx, tx, id, now); err != nil {
		return Preview{}, err
	}
	row, err := readAnnouncementTx(ctx, tx, id)
	if err != nil {
		return Preview{}, err
	}
	if row.revision != input.ExpectedRevision {
		return Preview{}, ErrConflict
	}
	values := applyDraftPatch(draftFromRow(row), input.Draft)
	if err := repository.validateDraft(values, now); err != nil {
		return Preview{}, err
	}
	preview := Preview{RenderProfileVersion: RenderProfileVersion}
	if completeLanguage(values.titleZH, values.bodyZH) {
		rendered, err := repository.renderer.render(values.bodyZH)
		if err != nil {
			return Preview{}, ErrInvalidRequest
		}
		preview.RenderedZH = &rendered.html
	}
	if completeLanguage(values.titleEN, values.bodyEN) {
		rendered, err := repository.renderer.render(values.bodyEN)
		if err != nil {
			return Preview{}, ErrInvalidRequest
		}
		preview.RenderedEN = &rendered.html
	}
	if err := commitTx(tx, &committed); err != nil {
		return Preview{}, err
	}
	return preview, nil
}

func readUserLanguage(ctx context.Context, tx *sql.Tx, userID int64) (string, error) {
	var language string
	err := tx.QueryRowContext(ctx, `SELECT lang FROM users WHERE id=? AND is_admin=0`, userID).Scan(&language)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrUnauthorized
	}
	if err != nil {
		return "", fmt.Errorf("announcements: read user language: %w", err)
	}
	if language != "" && language != "zh" && language != "en" {
		return "", ErrUnavailable
	}
	return language, nil
}

func readEpoch(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (string, error) {
	var epoch string
	if err := query.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key='announcement_epoch'`).Scan(&epoch); err != nil {
		return "", fmt.Errorf("announcements: read epoch: %w", err)
	}
	if !dbEpochID(epoch) {
		return "", ErrUnavailable
	}
	return epoch, nil
}

func dbEpochID(value string) bool {
	return db.ValidateOpaqueID(value, "b1e_")
}

type selectedLanguage struct {
	language, title, body string
	fallback              *string
}

func selectPublishedLanguage(row rawAnnouncement, requested string) (selectedLanguage, error) {
	if !row.publishedRevision.Valid || !row.publishedAt.Valid {
		return selectedLanguage{}, ErrUnavailable
	}
	available := map[string]struct{ title, body string }{
		"zh": {row.publishedTitleZH.String, row.publishedBodyZH.String},
		"en": {row.publishedTitleEN.String, row.publishedBodyEN.String},
	}
	preferences := []string{requested, "en", "zh"}
	seen := map[string]struct{}{}
	for _, language := range preferences {
		if language != "zh" && language != "en" {
			continue
		}
		if _, duplicate := seen[language]; duplicate {
			continue
		}
		seen[language] = struct{}{}
		candidate := available[language]
		if !completeLanguage(candidate.title, candidate.body) {
			continue
		}
		var fallback *string
		if (requested == "zh" || requested == "en") && language != requested {
			copyRequested := requested
			fallback = &copyRequested
		}
		return selectedLanguage{language: language, title: candidate.title, body: candidate.body, fallback: fallback}, nil
	}
	return selectedLanguage{}, ErrUnavailable
}

func (repository *Repository) userSummary(row rawAnnouncement, epoch, language string) (AnnouncementSummary, error) {
	selected, err := selectPublishedLanguage(row, language)
	if err != nil {
		return AnnouncementSummary{}, err
	}
	rendered, err := repository.renderer.render(selected.body)
	if err != nil {
		return AnnouncementSummary{}, ErrUnavailable
	}
	revision, err := decimalRevision(row.publishedRevision.Int64)
	if err != nil {
		return AnnouncementSummary{}, err
	}
	return AnnouncementSummary{
		Epoch: epoch, ID: row.id, Revision: revision, Severity: row.severity, Pinned: row.pinned == 1,
		Dismissible: row.dismissible == 1, PublishedAt: row.publishedAt.Int64, ExpiresAt: nullableInt64(row.expiresAt),
		EffectiveLanguage: selected.language, FallbackFrom: selected.fallback, Title: selected.title, Excerpt: rendered.plain,
	}, nil
}

func (repository *Repository) userDetail(row rawAnnouncement, epoch, language string) (AnnouncementDetail, error) {
	selected, err := selectPublishedLanguage(row, language)
	if err != nil {
		return AnnouncementDetail{}, err
	}
	rendered, err := repository.renderer.render(selected.body)
	if err != nil {
		return AnnouncementDetail{}, ErrUnavailable
	}
	revision, err := decimalRevision(row.publishedRevision.Int64)
	if err != nil {
		return AnnouncementDetail{}, err
	}
	return AnnouncementDetail{
		Epoch: epoch, ID: row.id, Revision: revision, Severity: row.severity, Pinned: row.pinned == 1,
		Dismissible: row.dismissible == 1, PublishedAt: row.publishedAt.Int64, ExpiresAt: nullableInt64(row.expiresAt),
		EffectiveLanguage: selected.language, FallbackFrom: selected.fallback, Title: selected.title, RenderedBody: rendered.html,
	}, nil
}

func (repository *Repository) adminDTO(row rawAnnouncement) (AdminAnnouncement, error) {
	revision, err := decimalRevision(row.revision)
	if err != nil {
		return AdminAnnouncement{}, err
	}
	value := AdminAnnouncement{
		ID: row.id, State: row.state, Revision: revision, Severity: row.severity, Pinned: row.pinned == 1,
		Dismissible: row.dismissible == 1, ExpiresAt: nullableInt64(row.expiresAt), WithdrawnAt: nullableInt64(row.withdrawnAt),
		CreatedAt: row.createdAt, UpdatedAt: row.updatedAt,
	}
	if row.draftTitleZH != "" || row.draftBodyZH != "" {
		if !completeLanguage(row.draftTitleZH, row.draftBodyZH) {
			return AdminAnnouncement{}, ErrUnavailable
		}
		value.Draft.ZH = &AnnouncementLanguageDraft{Title: row.draftTitleZH, Body: row.draftBodyZH}
	}
	if row.draftTitleEN != "" || row.draftBodyEN != "" {
		if !completeLanguage(row.draftTitleEN, row.draftBodyEN) {
			return AdminAnnouncement{}, ErrUnavailable
		}
		value.Draft.EN = &AnnouncementLanguageDraft{Title: row.draftTitleEN, Body: row.draftBodyEN}
	}
	if row.publishedRevision.Valid {
		publishedRevision, err := decimalRevision(row.publishedRevision.Int64)
		if err != nil {
			return AdminAnnouncement{}, err
		}
		published := &AnnouncementPublished{Revision: publishedRevision, PublishedAt: row.publishedAt.Int64}
		if completeLanguage(row.publishedTitleZH.String, row.publishedBodyZH.String) {
			rendered, err := repository.renderer.render(row.publishedBodyZH.String)
			if err != nil {
				return AdminAnnouncement{}, ErrUnavailable
			}
			published.ZH = &AnnouncementLanguagePublished{Title: row.publishedTitleZH.String, RenderedBody: rendered.html}
		} else if row.publishedTitleZH.String != "" || row.publishedBodyZH.String != "" {
			return AdminAnnouncement{}, ErrUnavailable
		}
		if completeLanguage(row.publishedTitleEN.String, row.publishedBodyEN.String) {
			rendered, err := repository.renderer.render(row.publishedBodyEN.String)
			if err != nil {
				return AdminAnnouncement{}, ErrUnavailable
			}
			published.EN = &AnnouncementLanguagePublished{Title: row.publishedTitleEN.String, RenderedBody: rendered.html}
		} else if row.publishedTitleEN.String != "" || row.publishedBodyEN.String != "" {
			return AdminAnnouncement{}, ErrUnavailable
		}
		value.Published = published
	}
	return value, nil
}
