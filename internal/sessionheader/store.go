package sessionheader

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
)

// Acquire is part of the dispatch transaction, after authority and capacity checks.
func Acquire(ctx context.Context, tx *sql.Tx, request string, user, model, at int64) (http.Header, error) {
	headers := http.Header{}
	rows, err := tx.QueryContext(ctx, "SELECT name,value FROM charity_header_uses WHERE logical_request_id=? ORDER BY name", request)
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
	var encoded string
	if err = tx.QueryRowContext(ctx, "SELECT session_headers FROM charity_models WHERE id=?", model).Scan(&encoded); err != nil {
		return nil, err
	}
	config, err := Decode(encoded)
	if err != nil {
		return nil, err
	}
	for _, entry := range config {
		var value, format string
		var idle, expires, active int64
		err = tx.QueryRowContext(ctx, `SELECT s.value,s.format,s.idle_seconds,s.expires_at,
 EXISTS(SELECT 1 FROM charity_header_uses u WHERE u.user_id=s.user_id AND u.model_id=s.model_id AND u.name=s.name AND u.value=s.value)
 FROM charity_header_sessions s WHERE user_id=? AND model_id=? AND name=?`, user, model, entry.Name).Scan(&value, &format, &idle, &expires, &active)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if errors.Is(err, sql.ErrNoRows) || format != entry.Format || idle != int64(entry.IdleSeconds) || (active == 0 && expires <= at) {
			value, err = randomValue(entry.Format)
			if err != nil {
				return nil, err
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO charity_header_sessions(user_id,model_id,name,value,format,idle_seconds,expires_at) VALUES(?,?,?,?,?,?,0)
 ON CONFLICT(user_id,model_id,name) DO UPDATE SET value=excluded.value,format=excluded.format,idle_seconds=excluded.idle_seconds,expires_at=0`, user, model, entry.Name, value, entry.Format, entry.IdleSeconds)
			if err != nil {
				return nil, err
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO charity_header_uses(logical_request_id,user_id,model_id,name,value,idle_seconds,sent) VALUES(?,?,?,?,?,?,0)`, request, user, model, entry.Name, value, entry.IdleSeconds)
		if err != nil {
			return nil, err
		}
		headers.Set(entry.Name, value)
	}
	return headers, nil
}

// Finish cannot recreate deleted state or extend a replacement generation.
func Finish(ctx context.Context, tx *sql.Tx, request string, at int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE charity_header_sessions AS s SET expires_at=MAX(expires_at,?+idle_seconds)
 WHERE s.rowid IN (SELECT current.rowid FROM charity_header_uses u JOIN charity_header_sessions current
 ON u.user_id=current.user_id AND u.model_id=current.model_id AND u.name=current.name AND u.value=current.value
 WHERE u.logical_request_id=? AND u.sent=1)`, at, request)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM charity_header_uses WHERE logical_request_id=?", request)
	return err
}
func MarkSent(ctx context.Context, database *sql.DB, request string) error {
	_, err := database.ExecContext(ctx, "UPDATE charity_header_uses SET sent=1 WHERE logical_request_id=? AND sent=0", request)
	return err
}
func Cleanup(ctx context.Context, tx *sql.Tx, at int64, limit int) (int, error) {
	result, err := tx.ExecContext(ctx, `DELETE FROM charity_header_sessions WHERE rowid IN (
 SELECT s.rowid FROM charity_header_sessions s WHERE s.expires_at<=? AND NOT EXISTS(
 SELECT 1 FROM charity_header_uses u WHERE u.user_id=s.user_id AND u.model_id=s.model_id AND u.name=s.name AND u.value=s.value)
 ORDER BY s.expires_at,s.rowid LIMIT ?)`, at, limit)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	return int(n), err
}
