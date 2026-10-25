package agentrequest

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
)

type chunkReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readResponseChunks(ctx context.Context, q chunkReader, id int64) ([]byte, bool, error) {
	rows, err := q.QueryContext(ctx, "SELECT body,redacted FROM agent_request_response_chunks WHERE request_id=? ORDER BY seq", id)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	body := []byte{}
	redacted := false
	for rows.Next() {
		var chunk []byte
		var changed bool
		if err = rows.Scan(&chunk, &changed); err != nil {
			return nil, false, err
		}
		if len(body)+len(chunk) > MaxBodyBytes {
			return nil, false, ErrProtocol
		}
		body = append(body, chunk...)
		redacted = redacted || changed
	}
	return body, redacted, rows.Err()
}
func secretSafePrefix(pending, key []byte) int {
	encoded, _ := json.Marshal(string(key))
	variants := [][]byte{key, encoded[1 : len(encoded)-1]}
	cut := len(pending) - max(len(key), len(encoded)-2)
	if cut <= 0 {
		return 0
	}
	for _, variant := range variants {
		offset := 0
		for len(variant) > 0 {
			found := bytes.Index(pending[offset:], variant)
			if found < 0 {
				break
			}
			start := offset + found
			if start < cut && start+len(variant) > cut {
				cut = start
			}
			offset = start + len(variant)
			if offset >= len(pending) {
				break
			}
		}
	}
	return cut
}
func (r *Runtime) readResponse(ctx context.Context, id int64, body io.Reader, key []byte) ([]byte, error) {
	result := make([]byte, 0, 4096)
	pending := []byte{}
	chunk := make([]byte, 32<<10)
	seq := 0
	encodedKey, _ := json.Marshal(string(key))
	checkpointBytes := (64 << 10) + max(len(key), len(encodedKey)-2)
	save := func(prefix []byte) error {
		if len(prefix) == 0 {
			return nil
		}
		safe, redacted := sanitizeSecret(prefix, key)
		_, err := r.db.ExecContext(ctx, "INSERT INTO agent_request_response_chunks(request_id,seq,body,redacted) VALUES(?,?,?,?)", id, seq, safe, redacted)
		if err == nil {
			seq++
		}
		return err
	}
	for {
		n, err := body.Read(chunk)
		if n > 0 {
			remaining := MaxBodyBytes - len(result)
			if n > remaining {
				result = append(result, chunk[:remaining]...)
				return result, ErrProtocol
			}
			result = append(result, chunk[:n]...)
			pending = append(pending, chunk[:n]...)
			cut := 0
			if len(pending) >= checkpointBytes {
				cut = secretSafePrefix(pending, key)
			}
			if cut > 0 {
				if saveErr := save(pending[:cut]); saveErr != nil {
					return result, saveErr
				}
				pending = append(pending[:0], pending[cut:]...)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if saveErr := save(pending); saveErr != nil {
					return result, saveErr
				}
				return result, nil
			}
			return result, err
		}
		if err = ctx.Err(); err != nil {
			return result, err
		}
	}
}
