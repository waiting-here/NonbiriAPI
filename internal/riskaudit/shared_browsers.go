package riskaudit

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type BrowserAssociation struct {
	UserID    int64 `json:"user_id,string"`
	FirstSeen int64 `json:"first_seen"`
	LastSeen  int64 `json:"last_seen"`
}
type SharedBrowser struct {
	BrowserID string `json:"browser_id"`
	Users     int    `json:"users"`
	FirstSeen int64  `json:"first_seen"`
	LastSeen  int64  `json:"last_seen"`
}

type SharedBrowserDetail struct {
	BrowserID    string               `json:"browser_id"`
	Users        int                  `json:"users"`
	FirstSeen    int64                `json:"first_seen"`
	LastSeen     int64                `json:"last_seen"`
	Associations []BrowserAssociation `json:"associations"`
	Page         string               `json:"page"`
	PageSize     int                  `json:"page_size"`
	TotalItems   string               `json:"total_items"`
	TotalPages   string               `json:"total_pages"`
}

const browserGroupsSQL = ` FROM browser_associations b JOIN users u ON u.id=b.user_id WHERE b.expires_at>? AND b.last_seen<=? AND u.is_admin=0 GROUP BY b.browser_digest HAVING COUNT(*)>=2`
const browserMembersSQL = ` FROM browser_associations b JOIN users u ON u.id=b.user_id WHERE b.browser_digest=? AND b.expires_at>? AND b.last_seen<=? AND u.is_admin=0`

func validBrowserPage(number int64, size int) bool {
	return number >= 1 && number <= 2147483647 && (size == 10 || size == 20 || size == 50 || size == 100)
}

// SharedBrowsers counts and projects current accounts in one authorized read
// snapshot. An identifier is a human-review grouping, never an identity proof.
func (r *Repository) SharedBrowsers(ctx context.Context, actor Actor, number int64, size int) (Page[SharedBrowser], error) {
	page := Page[SharedBrowser]{Items: []SharedBrowser{}, Coverage: "authenticated_browser_sessions"}
	if !validBrowserPage(number, size) {
		return page, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.begin(ctx, actor, false)
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	now := r.now().Unix()
	page.From, page.To = max(int64(0), now-int64(Retention/time.Second)), now
	var total int64
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT b.browser_digest`+browserGroupsSQL+`)`, now, now).Scan(&total); err != nil {
		return page, ErrUnavailable
	}
	pages := max(int64(1), (total+int64(size)-1)/int64(size))
	number = min(number, pages)
	page.Page, page.PageSize = strconv.FormatInt(number, 10), size
	page.TotalItems, page.TotalPages = strconv.FormatInt(total, 10), strconv.FormatInt(pages, 10)
	page.HasMore = number < pages
	rows, err := tx.QueryContext(ctx, `SELECT b.browser_digest,COUNT(*),MIN(b.first_seen),MAX(b.last_seen)`+browserGroupsSQL+` ORDER BY MAX(b.last_seen) DESC,b.browser_digest LIMIT ? OFFSET ?`, now, now, size, (number-1)*int64(size))
	if err != nil {
		return page, ErrUnavailable
	}
	for rows.Next() {
		var item SharedBrowser
		if err = rows.Scan(&item.BrowserID, &item.Users, &item.FirstSeen, &item.LastSeen); err != nil {
			rows.Close()
			return page, ErrUnavailable
		}
		page.Items = append(page.Items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, ErrUnavailable
	}
	return page, nil
}

// SharedBrowserMembers provides access to every member without unbounded
// response expansion for a copied cookie or a widely shared browser.
func (r *Repository) SharedBrowserMembers(ctx context.Context, actor Actor, digest string, number int64, size int) (SharedBrowserDetail, error) {
	page := SharedBrowserDetail{BrowserID: digest, Associations: []BrowserAssociation{}}
	if !validBrowserPage(number, size) || len(digest) != 64 || digest != strings.ToLower(digest) {
		return page, ErrInvalid
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return page, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.begin(ctx, actor, false)
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	now := r.now().Unix()
	var first, last int64
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(MIN(b.first_seen),0),COALESCE(MAX(b.last_seen),0)`+browserMembersSQL, digest, now, now).Scan(&page.Users, &first, &last); err != nil {
		return page, ErrUnavailable
	}
	if page.Users < 2 {
		return page, ErrNotFound
	}
	page.FirstSeen, page.LastSeen = first, last
	pages := max(int64(1), (int64(page.Users)+int64(size)-1)/int64(size))
	number = min(number, pages)
	page.Page, page.PageSize = strconv.FormatInt(number, 10), size
	page.TotalItems, page.TotalPages = strconv.Itoa(page.Users), strconv.FormatInt(pages, 10)
	rows, err := tx.QueryContext(ctx, `SELECT b.user_id,b.first_seen,b.last_seen`+browserMembersSQL+` ORDER BY b.user_id LIMIT ? OFFSET ?`, digest, now, now, size, (number-1)*int64(size))
	if err != nil {
		return page, ErrUnavailable
	}
	defer rows.Close()
	for rows.Next() {
		var item BrowserAssociation
		if err = rows.Scan(&item.UserID, &item.FirstSeen, &item.LastSeen); err != nil {
			return page, ErrUnavailable
		}
		page.Associations = append(page.Associations, item)
	}
	if rows.Err() != nil {
		return page, ErrUnavailable
	}
	return page, nil
}

func serveSharedBrowsers(repository *Repository, actor Actor, w http.ResponseWriter, r *http.Request) {
	serveBrowserPage(repository, actor, w, r, false)
}

func serveSharedBrowser(repository *Repository, actor Actor, w http.ResponseWriter, r *http.Request) {
	serveBrowserPage(repository, actor, w, r, true)
}

func serveBrowserPage(repository *Repository, actor Actor, w http.ResponseWriter, r *http.Request, detail bool) {
	w.Header().Set("Cache-Control", "no-store")
	if len(r.URL.RawQuery) > 256 || r.ContentLength != 0 {
		auditError(w, ErrInvalid)
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		auditError(w, ErrInvalid)
		return
	}
	for key, values := range q {
		if (key != "page" && key != "page_size") || len(values) != 1 {
			auditError(w, ErrInvalid)
			return
		}
	}
	number, err := intQuery(q, "page", 1)
	if err != nil {
		auditError(w, err)
		return
	}
	size, err := intQuery(q, "page_size", 20)
	if err != nil || size > 100 {
		auditError(w, ErrInvalid)
		return
	}
	var result any
	if detail {
		result, err = repository.SharedBrowserMembers(r.Context(), actor, r.PathValue("id"), number, int(size))
	} else {
		result, err = repository.SharedBrowsers(r.Context(), actor, number, int(size))
	}
	if err != nil {
		auditError(w, err)
		return
	}
	auditJSON(w, http.StatusOK, result)
}
