package fatfish

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
)

type cleanupRetention struct {
	Counts        map[string]int64 `json:"counts"`
	CreditTotal   string           `json:"credit_total"`
	FinancialHash string           `json:"financial_hash"`
}

func cleanupRetained(ctx context.Context, tx *sql.Tx) (cleanupRetention, error) {
	retained := cleanupRetention{Counts: map[string]int64{}}
	for _, table := range []string{"users", "credit_accounts", "credit_operations", "credit_entries", "fatfish_reward_claims", "fatfish_financial_receipts", "limited_activity_configs", "game_rank_events", "game_rank_totals"} {
		var count int64
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			return retained, err
		}
		retained.Counts[table] = count
	}
	rows, err := tx.QueryContext(ctx, "SELECT balance_sign,balance_mag FROM credit_accounts")
	if err != nil {
		return retained, err
	}
	defer rows.Close()
	total := new(big.Int)
	for rows.Next() {
		var sign int64
		var raw []byte
		if err = rows.Scan(&sign, &raw); err != nil {
			return retained, err
		}
		if len(raw) != 16 || sign < -1 || sign > 1 {
			return retained, ErrInvariant
		}
		amount := new(big.Int).SetBytes(raw)
		amount.Mul(amount, big.NewInt(sign))
		total.Add(total, amount)
	}
	retained.CreditTotal = total.String()

	return retained, rows.Err()
}
func checkCleanupRetained(before, after cleanupRetention, refunds int) error {
	if before.CreditTotal != after.CreditTotal {
		return ErrInvariant
	}
	for table, count := range before.Counts {
		addition := int64(0)
		switch table {
		case "credit_operations", "fatfish_financial_receipts":
			addition = int64(refunds)
		case "credit_entries":
			addition = int64(refunds) * 2
		}
		if after.Counts[table] != count+addition {
			return ErrInvariant
		}
	}
	return nil
}
func checkCleanupDatabase(ctx context.Context, tx *sql.Tx) error {
	var enabled int
	if err := tx.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enabled); err != nil {
		return err
	}
	if enabled != 1 {
		return ErrInvariant
	}
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	broken := rows.Next()
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if broken {
		return ErrInvariant
	}
	var integrity string
	if err = tx.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return ErrInvariant
	}
	var active, summary, actualActive, actualSummary int
	err = tx.QueryRowContext(ctx, `SELECT active_challenges,summary_rows,
       (SELECT count(*) FROM fatfish_challenges WHERE state IN ('prepared','active','verifying')),
       (SELECT count(*) FROM fatfish_challenges) FROM fatfish_capacity WHERE id=1`).Scan(&active, &summary, &actualActive, &actualSummary)
	if err != nil {
		return err
	}
	if active != actualActive || summary != actualSummary {
		return ErrInvariant
	}
	return nil
}
func cleanupPriorReceipt(ctx context.Context, tx *sql.Tx, manifest CleanupManifest, key string) (CleanupReceipt, bool, error) {
	var receipt CleanupReceipt
	var storedKey, schema, ids []byte
	var commit, tree, checkpoint string
	var completed int
	err := tx.QueryRowContext(ctx, `SELECT operation_key_hash,source_commit,source_schema_hash,source_tree,
      eligible_ids_hash,checkpoint_json,completed FROM instance_cleanup_receipts
      WHERE instance_identity=? AND json_extract(checkpoint_json,'$.kind')='fatfish-legacy'`, cleanupBlob(manifest.Source.InstanceIdentity)).Scan(
		&storedKey, &commit, &schema, &tree, &ids, &checkpoint, &completed)
	if errors.Is(err, sql.ErrNoRows) {
		return receipt, false, nil
	}
	if err != nil {
		return receipt, false, err
	}
	if completed != 1 || string(storedKey) != string(cleanupKeyHash(key)) ||
		commit != manifest.Source.SourceCommit || tree != manifest.Source.SourceTree ||
		string(schema) != string(cleanupBlob(manifest.Source.SourceSchemaHash)) ||
		string(ids) != string(cleanupBlob(manifest.EligibleIDsHash)) {
		return receipt, false, ErrConflict
	}
	if err = json.Unmarshal([]byte(checkpoint), &receipt); err != nil {
		return receipt, false, err
	}
	return receipt, true, nil
}
func writeCleanupReceipt(ctx context.Context, tx *sql.Tx, manifest CleanupManifest, key string, receipt CleanupReceipt, retained cleanupRetention) error {
	checkpoint, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	counts, err := json.Marshal(retained)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO instance_cleanup_receipts(instance_identity,operation_key_hash,
      source_commit,source_schema_hash,source_tree,eligible_ids_hash,checkpoint_json,retained_counts_json,
      completed,created_at,completed_at) VALUES(?,?,?,?,?,?,?,?,1,?,?)`,
		cleanupBlob(manifest.Source.InstanceIdentity), cleanupKeyHash(key), manifest.Source.SourceCommit,
		cleanupBlob(manifest.Source.SourceSchemaHash), manifest.Source.SourceTree, cleanupBlob(manifest.EligibleIDsHash),
		string(checkpoint), string(counts), receipt.CompletedAt, receipt.CompletedAt)
	return err
}
func deleteCleanupRows(ctx context.Context, tx *sql.Tx, rows []CleanupRow, receipt *CleanupReceipt) error {
	for _, kind := range []string{"mutation_receipts", "playtests", "challenges", "progress", "period_progress", "graph_layouts", "node_revisions", "nodes", "periods", "deleted_levels", "versions", "levels"} {
		for _, row := range rows {
			if row.Kind != kind {
				continue
			}
			query := ""
			args := []any{}
			switch kind {
			case "mutation_receipts":
				parts, err := cleanupComposite(row.ID, 2)
				if err != nil {
					return err
				}
				query = "DELETE FROM idempotency_records WHERE scope='control_mutation' AND actor_scope_hash=? AND key_hash=?"
				args = []any{cleanupBlob(parts[0]), cleanupBlob(parts[1])}
			case "progress":
				parts, err := cleanupComposite(row.ID, 3)
				if err != nil {
					return err
				}
				query = "DELETE FROM fatfish_progress WHERE user_id=? AND period_id=? AND node_id=?"
				args = []any{row.UserID, parts[1], parts[2]}
			case "period_progress":
				query = "DELETE FROM fatfish_period_progress WHERE user_id=? AND period_id=?"
				args = []any{row.UserID, row.ParentID}
			case "node_revisions":
				parts, err := cleanupComposite(row.ID, 2)
				if err != nil {
					return err
				}
				query = "DELETE FROM fatfish_node_revisions WHERE node_id=? AND revision=?"
				args = []any{parts[0], row.Revision}
			case "versions":
				query = "DELETE FROM fatfish_level_versions WHERE id=?"
				args = []any{row.ID}
			case "deleted_levels":
				query = "DELETE FROM fatfish_deleted_levels WHERE level_id=?"
				args = []any{row.ID}
			case "graph_layouts":
				query = "DELETE FROM fatfish_graph_layouts WHERE period_id=?"
				args = []any{row.ID}
			default:
				query = "DELETE FROM fatfish_" + kind + " WHERE id=?"
				args = []any{row.ID}
			}
			result, err := tx.ExecContext(ctx, query, args...)
			if err != nil {
				return err
			}
			count, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if count != 1 {
				return ErrConflict
			}
			receipt.DeletedCounts[kind]++
		}
	}
	return nil
}

// cleanupFinancialHash binds each retained account and settled ledger row after
// refunds. Comparing it across deletion detects transfers hidden by a zero sum.
func cleanupFinancialHash(ctx context.Context, tx *sql.Tx) (string, error) {
	digest := sha256.New()
	encoder := json.NewEncoder(digest)
	for _, table := range []struct{ name, order string }{
		{"credit_accounts", "id"}, {"credit_operations", "id"}, {"credit_entries", "operation_id,line_no"},
		{"fatfish_reward_claims", "identity_key,period_id,node_id,tier"}, {"fatfish_financial_receipts", "receipt_key"},
	} {
		if err := encoder.Encode(table.name); err != nil {
			return "", err
		}
		rows, err := tx.QueryContext(ctx, "SELECT * FROM "+table.name+" ORDER BY "+table.order)
		if err != nil {
			return "", err
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			return "", err
		}
		if err = encoder.Encode(columns); err != nil {
			rows.Close()
			return "", err
		}
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for i := range values {
			destinations[i] = &values[i]
		}
		for rows.Next() {
			if err = rows.Scan(destinations...); err != nil {
				rows.Close()
				return "", err
			}
			if err = encoder.Encode(values); err != nil {
				rows.Close()
				return "", err
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}
