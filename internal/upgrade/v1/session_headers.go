package v1

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

const personalSessionHeaderSchema = `ALTER TABLE models ADD COLUMN session_headers TEXT NOT NULL DEFAULT '[]' CHECK(length(session_headers)<=4096 AND json_valid(session_headers) AND json_type(session_headers)='array');
CREATE TABLE personal_header_sessions (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 model_id INTEGER NOT NULL REFERENCES models(id) ON DELETE CASCADE,
 name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 128),
 value TEXT NOT NULL CHECK(length(value) IN (30,32,36)),
 format TEXT NOT NULL CHECK(format IN ('uuid_v4','hex_32','opencode_session','opencode_message','opencode_part')),
 idle_seconds INTEGER NOT NULL CHECK(idle_seconds BETWEEN 1 AND 86400),
 expires_at INTEGER NOT NULL CHECK(expires_at BETWEEN 0 AND 253402387199),
 PRIMARY KEY(user_id,model_id,name)
) STRICT;
CREATE INDEX idx_personal_header_expiry ON personal_header_sessions(expires_at);
CREATE TABLE personal_header_uses (
 logical_request_id TEXT NOT NULL REFERENCES logical_requests(id) ON DELETE CASCADE,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 model_id INTEGER NOT NULL REFERENCES models(id) ON DELETE CASCADE,
 name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 128),
 value TEXT NOT NULL CHECK(length(value) IN (30,32,36)),
 idle_seconds INTEGER NOT NULL CHECK(idle_seconds BETWEEN 0 AND 86400),
 sent INTEGER NOT NULL DEFAULT 0 CHECK(sent IN (0,1)),
 PRIMARY KEY(logical_request_id,name)
) STRICT;
CREATE INDEX idx_personal_header_active ON personal_header_uses(user_id,model_id,name,value);
`

func extendSessionHeaders(ctx context.Context, tx *sql.Tx) error {
	changes := make([]tableRebuild, 0, 2)
	for _, name := range []string{"charity_header_sessions", "charity_header_uses"} {
		var source string
		if err := tx.QueryRowContext(ctx, "SELECT sql FROM sqlite_schema WHERE type='table' AND name=?", name).Scan(&source); err != nil {
			return err
		}
		target := strings.ReplaceAll(source, "length(value) IN (32,36)", "length(value) IN (30,32,36)")
		target = strings.ReplaceAll(target, "format IN ('uuid_v4','hex_32')", "format IN ('uuid_v4','hex_32','opencode_session','opencode_message','opencode_part')")
		if name == "charity_header_uses" {
			target = strings.ReplaceAll(target, "idle_seconds BETWEEN 1 AND 86400", "idle_seconds BETWEEN 0 AND 86400")
		}
		if target == source {
			return errors.New("session header migration source constraint is missing")
		}
		changes = append(changes, tableRebuild{Name: name, SQL: target})
	}
	if err := rebuildTables(ctx, tx, changes); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, personalSessionHeaderSchema)
	return err
}
