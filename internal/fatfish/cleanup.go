package fatfish

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"sort"
	"time"
)

type CleanupSource struct {
	InstanceIdentity string `json:"instance_identity"`
	SourceCommit     string `json:"source_commit"`
	SourceTree       string `json:"source_tree"`
	SourceSchemaHash string `json:"source_schema_hash"`
}
type CleanupRow struct {
	Kind          string `json:"kind"`
	ID            string `json:"id"`
	ParentID      string `json:"parent_id,omitempty"`
	UserID        int64  `json:"user_id,omitempty"`
	Revision      int64  `json:"revision,omitempty"`
	EngineVersion int    `json:"engine_version,omitempty"`
	ContentHash   string `json:"content_hash,omitempty"`
	State         string `json:"state,omitempty"`
}
type CleanupManifest struct {
	Format          string        `json:"format"`
	Source          CleanupSource `json:"source"`
	Rows            []CleanupRow  `json:"rows"`
	EligibleIDsHash string        `json:"eligible_ids_hash"`
}
type CleanupReceipt struct {
	Kind               string         `json:"kind"`
	DeletedCounts      map[string]int `json:"deleted_counts"`
	RefundedChallenges int            `json:"refunded_challenges"`
	CompletedAt        int64          `json:"completed_at"`
	Replayed           bool           `json:"-"`
}

func exactHex(value string, size int) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == size && hex.EncodeToString(raw) == value
}
func (source CleanupSource) valid() bool {
	return exactHex(source.InstanceIdentity, 32) && exactHex(source.SourceCommit, 20) &&
		exactHex(source.SourceTree, 20) && exactHex(source.SourceSchemaHash, 32)
}
func cleanupRowsHash(rows []CleanupRow) string {
	encoded, _ := json.Marshal(rows)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
func sortCleanupRows(rows []CleanupRow) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Kind != rows[j].Kind {
			return rows[i].Kind < rows[j].Kind
		}
		return rows[i].ID < rows[j].ID
	})
}

// PlanLegacyCleanup reads a consistent copy and never changes game state.
// Source is the trusted operational identity of that copy, not the new binary.
func PlanLegacyCleanup(ctx context.Context, database *sql.DB, source CleanupSource) (CleanupManifest, error) {
	if database == nil || !source.valid() {
		return CleanupManifest{}, ErrInvalid
	}
	tx, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return CleanupManifest{}, err
	}
	defer tx.Rollback()
	levels, err := legacyCleanupRoots(ctx, tx, "levels")
	if err != nil {
		return CleanupManifest{}, err
	}
	periods, err := legacyCleanupRoots(ctx, tx, "periods")
	if err != nil {
		return CleanupManifest{}, err
	}
	rows, err := readCleanupRows(ctx, tx, levels, periods)
	if err != nil {
		return CleanupManifest{}, err
	}
	return CleanupManifest{Format: "nonbiri-fatfish-cleanup-v1", Source: source, Rows: rows, EligibleIDsHash: cleanupRowsHash(rows)}, tx.Commit()
}

// RunOfflineLegacyCleanup is only for an explicit offline maintenance command.
// The caller owns the stopped instance, consistent source and expected identity.
// All terminal transitions, refunds, deletion and the receipt commit together.
func RunOfflineLegacyCleanup(ctx context.Context, database *sql.DB, expected CleanupSource, manifest CleanupManifest, key string, now time.Time) (CleanupReceipt, error) {
	if database == nil || !expected.valid() || expected != manifest.Source ||
		manifest.Format != "nonbiri-fatfish-cleanup-v1" || len(key) < 16 || len(key) > 128 ||
		now.Unix() < 0 || now.Unix() > maximumUnix || len(manifest.Rows) > 1_100_000 {
		return CleanupReceipt{}, ErrInvalid
	}
	sorted := append([]CleanupRow{}, manifest.Rows...)
	sortCleanupRows(sorted)
	if !reflect.DeepEqual(sorted, manifest.Rows) || cleanupRowsHash(sorted) != manifest.EligibleIDsHash {
		return CleanupReceipt{}, ErrInvalid
	}
	for i := 1; i < len(sorted); i++ {
		if sorted[i-1].Kind == sorted[i].Kind && sorted[i-1].ID == sorted[i].ID {
			return CleanupReceipt{}, ErrInvalid
		}
	}
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return CleanupReceipt{}, err
	}
	defer tx.Rollback()
	replay, found, err := cleanupPriorReceipt(ctx, tx, manifest, key)
	if err != nil {
		return CleanupReceipt{}, err
	}
	if found {
		replay.Replayed = true
		return replay, tx.Commit()
	}
	levels, periods := []string{}, []string{}
	for _, row := range sorted {
		switch row.Kind {
		case "levels":
			levels = append(levels, row.ID)
		case "periods":
			periods = append(periods, row.ID)
		}
	}
	actual, err := readCleanupRows(ctx, tx, levels, periods)
	if err != nil {
		return CleanupReceipt{}, err
	}
	if !reflect.DeepEqual(actual, manifest.Rows) {
		return CleanupReceipt{}, ErrConflict
	}
	retained, err := cleanupRetained(ctx, tx)
	if err != nil {
		return CleanupReceipt{}, err
	}
	receipt := CleanupReceipt{Kind: "fatfish-legacy", DeletedCounts: map[string]int{}, CompletedAt: now.Unix()}
	service := &Service{db: database}
	for _, row := range sorted {
		if row.Kind != "challenges" {
			continue
		}
		challenge, err := readChallengeTx(ctx, tx, row.ID)
		if err != nil {
			return CleanupReceipt{}, err
		}
		if challenge.state == "prepared" || challenge.state == "active" || challenge.state == "verifying" {
			if err = service.cancelChallengeTx(ctx, tx, challenge, now.UnixMilli(), "legacy_content_cleanup", true); err != nil {
				return CleanupReceipt{}, err
			}
			if !challenge.playtest && challenge.state != "prepared" && !zeroMagnitude(challenge.price) {
				receipt.RefundedChallenges++
			}
		}
	}
	settledHash, err := cleanupFinancialHash(ctx, tx)
	if err != nil {
		return CleanupReceipt{}, err
	}
	if err = deleteCleanupRows(ctx, tx, sorted, &receipt); err != nil {
		return CleanupReceipt{}, err
	}
	after, err := cleanupRetained(ctx, tx)
	if err != nil {
		return CleanupReceipt{}, err
	}
	after.FinancialHash, err = cleanupFinancialHash(ctx, tx)
	if err != nil {
		return CleanupReceipt{}, err
	}
	if after.FinancialHash != settledHash {
		return CleanupReceipt{}, ErrInvariant
	}
	if err = checkCleanupRetained(retained, after, receipt.RefundedChallenges); err != nil {
		return CleanupReceipt{}, err
	}
	if err = checkCleanupDatabase(ctx, tx); err != nil {
		return CleanupReceipt{}, err
	}
	if err = writeCleanupReceipt(ctx, tx, manifest, key, receipt, after); err != nil {
		return CleanupReceipt{}, err
	}
	return receipt, tx.Commit()
}
func zeroMagnitude(raw []byte) bool {
	for _, b := range raw {
		if b != 0 {
			return false
		}
	}
	return true
}
func cleanupBlob(value string) []byte  { decoded, _ := hex.DecodeString(value); return decoded }
func cleanupKeyHash(key string) []byte { digest := sha256.Sum256([]byte(key)); return digest[:] }
