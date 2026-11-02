package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	BrowserAuditCookieName     = "nb_browser_audit"
	browserAuditTTL            = 30 * 24 * time.Hour
	browserObservationInterval = time.Hour
	browserObservationBudget   = 250 * time.Millisecond
	MaxUserBrowsers            = 32
)

var browserAuditPurpose = []byte("NonbiriAPI/browser-association/v1")

func browserMAC(key []byte, origin, payload string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(origin))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(payload))
	return mac.Sum(nil)
}

func signedBrowserToken(key []byte, origin string, now int64) (string, error) {
	random, err := randomOpaque(32)
	if err != nil {
		return "", err
	}
	payload := "v1." + strconv.FormatInt(now, 10) + "." + random
	return payload + "." + base64.RawURLEncoding.EncodeToString(browserMAC(key, origin, payload)), nil
}

func browserTokenIssued(key []byte, origin, token string, now int64) (int64, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0] != "v1" {
		return 0, false
	}
	issued, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || issued < 0 || issued > maxUnixSecond-int64(browserAuditTTL/time.Second) || strconv.FormatInt(issued, 10) != parts[1] || issued > now || now >= issued+int64(browserAuditTTL/time.Second) {
		return 0, false
	}
	random, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if err != nil || len(random) != 32 {
		return 0, false
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(parts[3])
	if err != nil || len(signature) != sha256.Size {
		return 0, false
	}
	payload := strings.Join(parts[:3], ".")
	return issued, hmac.Equal(signature, browserMAC(key, origin, payload))
}

// observeUserBrowser is best effort and runs only at successful user login
// and session-envelope entry points. It never changes authentication results.
func (r *Runtime) observeUserBrowser(w http.ResponseWriter, req *http.Request, userID int64, sessionHash string) {
	r.mu.Lock()
	var key []byte
	if !r.closed {
		key = append(key, r.browserAuditKey...)
	}
	r.mu.Unlock()
	defer clear(key)
	if len(key) < 32 || userID <= 0 || sessionHash == "" {
		return
	}
	now := r.now().Unix()
	if now < 0 || now > maxUnixSecond-int64(browserAuditTTL/time.Second) {
		return
	}
	token, present, unique := uniqueCookieValue(req, BrowserAuditCookieName)
	issued, valid := browserTokenIssued(key, r.siteOrigin, token, now)
	fresh := !present || !unique || !valid
	if fresh {
		var err error
		token, err = signedBrowserToken(key, r.siteOrigin, now)
		if err != nil {
			return
		}
		issued = now
	}
	expiry := issued + int64(browserAuditTTL/time.Second)
	ctx, cancel := context.WithTimeout(req.Context(), browserObservationBudget)
	defer cancel()
	if err := r.recordBrowserAssociation(ctx, userID, sessionHash, sessionLookupHash(token), issued, now, expiry); err != nil {
		return
	}
	if fresh {
		w.Header().Set("Cache-Control", "no-store")
		http.SetCookie(w, sessionCookie(BrowserAuditCookieName, token, "/api", secureCookieForRequest(req, r.siteOrigin), maxAgeUntil(time.Unix(expiry, 0), time.Unix(now, 0)), time.Unix(expiry, 0)))
	}
}

func (r *Runtime) recordBrowserAssociation(ctx context.Context, userID int64, sessionHash, digest string, issued, now, expiry int64) error {
	var seen int64
	err := r.readDatabase().QueryRowContext(ctx, `SELECT last_seen FROM browser_associations WHERE user_id=? AND browser_digest=?`, userID, digest).Scan(&seen)
	if err == nil && seen > now-int64(browserObservationInterval/time.Second) {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Session existence is checked again under the writer. Revocation or
	// deletion wins over a delayed observation, including a reused user ID.
	result, err := tx.ExecContext(ctx, `INSERT INTO browser_associations(user_id,browser_digest,issued_at,first_seen,last_seen,expires_at)
SELECT u.id,?,?,?,?,? FROM users u JOIN sessions s ON s.user_id=u.id
WHERE u.id=? AND u.is_admin=0 AND s.token_hash=? AND s.expires_at>? AND s.absolute_expires_at>?
ON CONFLICT(user_id,browser_digest) DO UPDATE SET last_seen=excluded.last_seen
WHERE browser_associations.last_seen<=?`, digest, issued, now, now, expiry, userID, sessionHash, now, now, now-int64(browserObservationInterval/time.Second))
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return errSessionUnauthorized
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM browser_associations WHERE user_id=? AND (expires_at<=? OR browser_digest IN (
SELECT browser_digest FROM browser_associations WHERE user_id=? AND browser_digest<>? ORDER BY last_seen DESC,browser_digest LIMIT -1 OFFSET ?))`, userID, now, userID, digest, MaxUserBrowsers-1); err != nil {
		return err
	}
	return tx.Commit()
}
