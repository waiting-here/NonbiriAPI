package sessionheader

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
)

// Acquire is part of the dispatch transaction, after authority and capacity checks.
func Acquire(ctx context.Context, tx *sql.Tx, request string, user, model, at int64) (http.Header, error) {
	return acquireScoped(ctx, tx, request, user, model, at, "charity")
}

// AcquirePersonal uses separate model and session identities from public models.
func AcquirePersonal(ctx context.Context, tx *sql.Tx, request string, user, model, at int64) (http.Header, error) {
	return acquireScoped(ctx, tx, request, user, model, at, "personal")
}

func acquireScoped(ctx context.Context, tx *sql.Tx, request string, user, model, at int64, scope string) (http.Header, error) {
	var encoded string
	query := "SELECT session_headers FROM charity_models WHERE id=?"
	args := []any{model}
	if scope == "personal" {
		query = "SELECT session_headers FROM models WHERE id=? AND user_id=?"
		args = append(args, user)
	}
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&encoded); err != nil {
		return nil, err
	}
	sessions, uses := scope+"_header_sessions", scope+"_header_uses"
	headers := http.Header{}
	rows, err := tx.QueryContext(ctx, "SELECT name,value FROM "+uses+" WHERE logical_request_id=? AND user_id=? AND model_id=? ORDER BY name", request, user, model)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name, value string
		if err = rows.Scan(&name, &value); err != nil {
			rows.Close()
			return nil, err
		}
		headers.Set(name, value)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(headers) > 0 {
		return headers, nil
	}
	config, err := Decode(encoded)
	if err != nil {
		return nil, err
	}
	for _, entry := range config {
		var value, format string
		var idle, expires, active int64
		if entry.IdleSeconds == 0 {
			if _, err = tx.ExecContext(ctx, "DELETE FROM "+sessions+" WHERE user_id=? AND model_id=? AND name=?", user, model, entry.Name); err != nil {
				return nil, err
			}
			value, err = randomValue(entry.Format)
			if err != nil {
				return nil, err
			}
		} else {
			err = tx.QueryRowContext(ctx, `SELECT s.value,s.format,s.idle_seconds,s.expires_at,
 EXISTS(SELECT 1 FROM `+uses+` u WHERE u.user_id=s.user_id AND u.model_id=s.model_id AND u.name=s.name AND u.value=s.value)
 FROM `+sessions+` s WHERE user_id=? AND model_id=? AND name=?`, user, model, entry.Name).Scan(&value, &format, &idle, &expires, &active)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
			if errors.Is(err, sql.ErrNoRows) || format != entry.Format || idle != int64(entry.IdleSeconds) || (active == 0 && expires <= at) {
				value, err = randomValue(entry.Format)
				if err != nil {
					return nil, err
				}
				_, err = tx.ExecContext(ctx, `INSERT INTO `+sessions+`(user_id,model_id,name,value,format,idle_seconds,expires_at) VALUES(?,?,?,?,?,?,0)
 ON CONFLICT(user_id,model_id,name) DO UPDATE SET value=excluded.value,format=excluded.format,idle_seconds=excluded.idle_seconds,expires_at=0`, user, model, entry.Name, value, entry.Format, entry.IdleSeconds)
				if err != nil {
					return nil, err
				}
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO `+uses+`(logical_request_id,user_id,model_id,name,value,idle_seconds,sent) VALUES(?,?,?,?,?,?,0)`, request, user, model, entry.Name, value, entry.IdleSeconds)
		if err != nil {
			return nil, err
		}
		headers.Set(entry.Name, value)
	}
	return headers, nil
}

// Finish cannot recreate deleted state or extend a replacement generation.
func Finish(ctx context.Context, tx *sql.Tx, request string, at int64) error {
	for _, scope := range []string{"charity", "personal"} {
		sessions, uses := scope+"_header_sessions", scope+"_header_uses"
		_, err := tx.ExecContext(ctx, `UPDATE `+sessions+` AS s SET expires_at=MAX(expires_at,?+idle_seconds)
 WHERE s.rowid IN (SELECT current.rowid FROM `+uses+` u JOIN `+sessions+` current
 ON u.user_id=current.user_id AND u.model_id=current.model_id AND u.name=current.name AND u.value=current.value
 WHERE u.logical_request_id=? AND u.sent=1)`, at, request)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM "+uses+" WHERE logical_request_id=?", request); err != nil {
			return err
		}
	}
	return nil
}
func MarkSent(ctx context.Context, database *sql.DB, request string) error {
	for _, scope := range []string{"charity", "personal"} {
		if _, err := database.ExecContext(ctx, "UPDATE "+scope+"_header_uses SET sent=1 WHERE logical_request_id=? AND sent=0", request); err != nil {
			return err
		}
	}
	return nil
}
func Cleanup(ctx context.Context, tx *sql.Tx, at int64, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	total := 0
	for _, scope := range []string{"charity", "personal"} {
		sessions, uses := scope+"_header_sessions", scope+"_header_uses"
		result, err := tx.ExecContext(ctx, `DELETE FROM `+sessions+` WHERE rowid IN (
 SELECT s.rowid FROM `+sessions+` s WHERE s.expires_at<=? AND NOT EXISTS(
 SELECT 1 FROM `+uses+` u WHERE u.user_id=s.user_id AND u.model_id=s.model_id AND u.name=s.name AND u.value=s.value)
 ORDER BY s.expires_at,s.rowid LIMIT ?)`, at, limit-total)
		if err != nil {
			return total, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return total, err
		}
		total += int(n)
		if total == limit {
			break
		}
	}
	return total, nil
}
