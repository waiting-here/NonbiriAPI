package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/blacklist"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

const DenialCookieName = "nb_access_denied"
const denialCookiePath = "/api/auth"

func httpQuery(req *http.Request) (string, error) {
	if req == nil || req.URL == nil || req.URL.ForceQuery || len(req.URL.RawQuery) > 128 {
		return "", ErrStateInvalid
	}
	q, err := url.ParseQuery(req.URL.RawQuery)
	if err != nil {
		return "", err
	}
	if len(q) == 0 {
		return "", nil
	}
	if len(q) != 1 || len(q["cursor"]) != 1 || q.Get("cursor") == "" {
		return "", ErrStateInvalid
	}
	return q.Get("cursor"), nil
}

type DenialRuleLabel struct {
	Name string `json:"name"`
}

type DenialAutomaticReason struct {
	Kind          string            `json:"kind"`
	SchemaVersion int               `json:"schema_version"`
	Params        json.RawMessage   `json:"params"`
	ManualText    string            `json:"manual_text,omitempty"`
	Rules         []DenialRuleLabel `json:"rules,omitempty"`
}

type DenialReason struct {
	Kind        string                 `json:"kind"`
	Reason      string                 `json:"reason"`
	StartedAt   int64                  `json:"started_at"`
	EndsAt      *int64                 `json:"ends_at"`
	Automatic   *AutomaticRestriction  `json:"automatic,omitempty"`
	Metadata    *DenialAutomaticReason `json:"automatic_reason,omitempty"`
	ReasonCodes []string               `json:"reason_codes,omitempty"`
}
type DenialPage struct {
	Restricted bool           `json:"restricted"`
	Items      []DenialReason `json:"items"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

func clearDenialCookie(w http.ResponseWriter) {
	http.SetCookie(w, sessionCookie(DenialCookieName, "", denialCookiePath, true, -1, time.Unix(1, 0)))
}

func (r *Runtime) revokeDenial(req *http.Request) {
	if raw, ok := cookieValue(req, DenialCookieName); ok {
		hash := sha256.Sum256([]byte(raw))
		_, _ = r.db.ExecContext(req.Context(), `DELETE FROM auth_denial_grants WHERE token_hash=?`, hash[:])
	}
}

func (r *Runtime) issueDenial(ctx context.Context, discord string) (string, error) {
	if !blacklist.ValidDiscordID(discord) {
		return "", ErrInvalidIdentity
	}
	now := r.now().Unix()
	if now < 0 || now > maxUnixSecond-300 {
		return "", ErrProviderUnavailable
	}
	raw := make([]byte, 32)
	domain := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	defer clear(raw)
	if _, err := rand.Read(domain); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM auth_denial_grants WHERE token_hash IN (SELECT token_hash FROM auth_denial_grants WHERE expires_at<=? ORDER BY expires_at LIMIT 100)`, now); err != nil {
		return "", err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM auth_denial_grants`).Scan(&count); err != nil {
		return "", err
	}
	if count >= 5000 {
		return "", ErrStateCapacity
	}
	// A fresh verification replaces only the oldest of this identity's four
	// grants, allowing independent tabs without unbounded credential growth.
	if _, err = tx.ExecContext(ctx, `DELETE FROM auth_denial_grants WHERE token_hash IN (SELECT token_hash FROM auth_denial_grants WHERE discord_id=? ORDER BY created_at DESC,token_hash DESC LIMIT -1 OFFSET 3)`, discord); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO auth_denial_grants(token_hash,discord_id,cursor_domain,created_at,expires_at) VALUES(?,?,?,?,?)`, hash[:], discord, domain, now, now+300); err != nil {
		return "", err
	}
	return token, tx.Commit()
}

func denialCursor(domain []byte, offset uint64) string {
	bytes := make([]byte, 8, 40)
	binary.BigEndian.PutUint64(bytes, offset)
	mac := hmac.New(sha256.New, domain)
	mac.Write(bytes)
	return base64.RawURLEncoding.EncodeToString(append(bytes, mac.Sum(nil)...))
}
func denialOffset(domain []byte, cursor string) (uint64, error) {
	if cursor == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(raw) != 40 {
		return 0, ErrStateInvalid
	}
	mac := hmac.New(sha256.New, domain)
	mac.Write(raw[:8])
	if !hmac.Equal(raw[8:], mac.Sum(nil)) {
		return 0, ErrStateInvalid
	}
	offset := binary.BigEndian.Uint64(raw[:8])
	if offset > 1000000 {
		return 0, ErrStateInvalid
	}
	return offset, nil
}

func (r *Runtime) accessDeniedReasons(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if !requireEmptyBody(w, req) {
		return
	}
	q, err := httpQuery(req)
	if err != nil {
		writeStableError(w, httperr.CodeInvalidRequest, "invalid request")
		return
	}
	if !r.allowOAuthStart(w, req) {
		return
	}
	raw, ok := cookieValue(req, DenialCookieName)
	if !ok || len(raw) != 43 {
		clearDenialCookie(w)
		writeStableError(w, httperr.CodeUnauthorized, "verify your identity again")
		return
	}
	hash := sha256.Sum256([]byte(raw))
	ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
	defer cancel()
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		writeAuthFailure(w, err)
		return
	}
	defer tx.Rollback()
	var discord string
	var domain []byte
	err = tx.QueryRowContext(ctx, `SELECT discord_id,cursor_domain FROM auth_denial_grants WHERE token_hash=? AND expires_at>?`, hash[:], r.now().Unix()).Scan(&discord, &domain)
	if errors.Is(err, sql.ErrNoRows) {
		clearDenialCookie(w)
		writeStableError(w, httperr.CodeUnauthorized, "verify your identity again")
		return
	}
	if err != nil {
		writeAuthFailure(w, err)
		return
	}
	offset, err := denialOffset(domain, q)
	if err != nil {
		writeStableError(w, httperr.CodeInvalidRequest, "invalid cursor")
		return
	}
	now := r.now().Unix()
	// Current authority and all displayed facts share one snapshot. Actor and
	// management-only fields never enter this projection.
	var restricted bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM discord_blacklist WHERE discord_id=?) OR EXISTS(SELECT 1 FROM users WHERE discord_id=? AND is_admin=0 AND is_banned=1 AND (banned_until IS NULL OR banned_until>?))`, discord, discord, now).Scan(&restricted)
	if err != nil {
		writeAuthFailure(w, err)
		return
	}
	page := DenialPage{Restricted: restricted, Items: []DenialReason{}}
	if restricted {
		rows, err := tx.QueryContext(ctx, `SELECT kind,reason,started_at,ends_at,codes FROM (
 SELECT 0 AS section,0 AS ordinal,'ban' AS kind,banned_reason AS reason,updated_at AS started_at,banned_until AS ends_at,'[]' AS codes FROM users WHERE discord_id=? AND is_admin=0 AND is_banned=1 AND (banned_until IS NULL OR banned_until>?)
 UNION ALL SELECT 1,0,'blacklist',reason,created_at,NULL,COALESCE((SELECT reason_codes_json FROM discord_blacklist_events e WHERE e.discord_id=b.discord_id ORDER BY e.created_at,e.id LIMIT 1),'[]') FROM discord_blacklist b WHERE discord_id=?
 UNION ALL SELECT 2,e.id,'blacklist_note',e.safe_note,e.created_at,NULL,e.reason_codes_json FROM discord_blacklist_events e WHERE e.discord_id=? AND e.safe_note<>'' AND e.id<>(SELECT f.id FROM discord_blacklist_events f WHERE f.discord_id=e.discord_id ORDER BY f.created_at,f.id LIMIT 1) AND EXISTS(SELECT 1 FROM discord_blacklist b WHERE b.discord_id=e.discord_id)
 ) ORDER BY section,ordinal LIMIT 21 OFFSET ?`, discord, now, discord, discord, offset)
		if err != nil {
			writeAuthFailure(w, err)
			return
		}
		for rows.Next() {
			var item DenialReason
			var ends sql.NullInt64
			var codes string
			if err = rows.Scan(&item.Kind, &item.Reason, &item.StartedAt, &ends, &codes); err != nil {
				break
			}
			if err = json.Unmarshal([]byte(codes), &item.ReasonCodes); err != nil {
				break
			}
			if ends.Valid {
				item.EndsAt = &ends.Int64
			}
			page.Items = append(page.Items, item)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			writeAuthFailure(w, err)
			return
		}
		if len(page.Items) > 20 {
			page.Items = page.Items[:20]
			page.NextCursor = denialCursor(domain, offset+20)
		}
		for i := range page.Items {
			item := &page.Items[i]
			if item.Kind != "ban" {
				continue
			}
			var user int64
			var language string
			err = tx.QueryRowContext(ctx, `SELECT id,lang FROM users WHERE discord_id=? AND is_admin=0`, discord).Scan(&user, &language)
			if err != nil {
				writeAuthFailure(w, err)
				return
			}
			metadata, err := observability.ReadAutomaticReasonTx(ctx, tx, "user_ban", strconv.FormatInt(user, 10))
			if err != nil {
				writeAuthFailure(w, err)
				return
			}
			if metadata != nil {
				item.Metadata = &DenialAutomaticReason{Kind: metadata.Kind, SchemaVersion: metadata.SchemaVersion, Params: metadata.Params, ManualText: metadata.ManualText}
				for _, label := range metadata.Rules {
					item.Metadata.Rules = append(item.Metadata.Rules, DenialRuleLabel{Name: label.Name})
				}
				// Labels are sent once in the typed facts; repeating all of them
				// in this summary could exceed the response byte budget.
				item.Reason = "触发了客户端使用规则。"
				if language == "en" {
					item.Reason = "Client usage rules were matched."
				}
			} else {
				cases, e := readAutomaticRestrictions(ctx, tx, user, now, language)
				if e != nil {
					writeAuthFailure(w, e)
					return
				}
				for _, c := range cases {
					if c.Kind == "ban" {
						item.Automatic = &c
						item.Reason = c.Reason
						break
					}
				}
			}
		}
	}
	body, err := json.Marshal(page)
	for err == nil && len(body)+1 > 128*1024 && len(page.Items) > 1 {
		page.Items = page.Items[:len(page.Items)-1]
		page.NextCursor = denialCursor(domain, offset+uint64(len(page.Items)))
		body, err = json.Marshal(page)
	}
	if err != nil || len(body)+1 > 128*1024 {
		writeStableError(w, httperr.CodeInternal, "reasons unavailable")
		return
	}
	if err = tx.Commit(); err != nil {
		writeAuthFailure(w, err)
		return
	}
	if !restricted {
		r.revokeDenial(req)
		clearDenialCookie(w)
	}
	writeJSONBytes(w, http.StatusOK, append(body, '\n'))
}
