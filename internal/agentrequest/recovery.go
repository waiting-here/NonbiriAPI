package agentrequest

import (
	"context"
	"encoding/json"
)

// Recovery never sends an unknown request again. Response checkpoints and
// independently counted usage remain available even after actor deletion.
func (r *Runtime) recoverInterrupted(ctx context.Context) error {
	rows, err := r.readDB.QueryContext(ctx, "SELECT id,input_json,response_body,status_code FROM agent_requests WHERE state='running' ORDER BY id")
	if err != nil {
		return err
	}
	type interrupted struct {
		id     int64
		input  Input
		body   []byte
		status int
	}
	pending := []interrupted{}
	for rows.Next() {
		var item interrupted
		var encoded string
		if err = rows.Scan(&item.id, &encoded, &item.body, &item.status); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal([]byte(encoded), &item.input); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range pending {
		redacted := false
		if len(item.body) == 0 {
			item.body, redacted, err = readResponseChunks(ctx, r.readDB, item.id)
			if err != nil {
				return err
			}
		}
		result, _ := parseResult(item.id, item.body)
		if err = r.complete(item.id, item.input, "interrupted", "interrupted", item.status, item.body, false, redacted, result.Usage, true); err != nil {
			return err
		}
	}
	return nil
}
