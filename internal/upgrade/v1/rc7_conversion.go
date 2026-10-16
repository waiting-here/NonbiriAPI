// Package v1 implements the one-time conversion to the final storage schema.
// The server does not import this package.
package v1

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/continuity"
	"github.com/waiting-here/NonbiriAPI/internal/gatewaypolicy"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const SourceCommit = "82f1711b674425745a768f781e0e0c34dcb37ac3"
const SourceSchemaVersion = 16
const SourceManifestHash = "484bf7ee92aec1dfb9c8750e9bb1a73b2b4b5d1e9761e1f86c5dcd31d2719f23"

//go:embed rc7_convert.sql
var conversionSQL string

func convert(ctx context.Context, database *sql.DB, tx *sql.Tx, vault *secret.Vault, legacyGateway string) error {
	identities, err := continuity.New(database, vault)
	if err != nil {
		return err
	}
	defer identities.Close()
	review, err := secret.NewDonationReview(vault)
	if err != nil {
		return err
	}
	for cursor := int64(0); ; {
		ids, err := readIDs(ctx, tx, `SELECT u.id FROM users u WHERE u.id>? AND u.is_admin=0 AND NOT EXISTS(SELECT 1 FROM user_continuity_identities c WHERE c.user_id=u.id) ORDER BY u.id LIMIT 1000`, cursor)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := identities.BindUserTx(ctx, tx, id); err != nil {
				return err
			}
			cursor = id
		}
		if len(ids) < 1000 {
			break
		}
	}
	for cursor := int64(0); ; {
		ids, err := readIDs(ctx, tx, `SELECT id FROM endpoint_key_secrets WHERE id>? AND key_body_review_hmac IS NULL ORDER BY id LIMIT 1000`, cursor)
		if err != nil {
			return err
		}
		if err := review.BackfillLegacySecrets(ctx, tx, ids, time.Now()); err != nil {
			return err
		}
		if len(ids) < 1000 {
			break
		}
		cursor = ids[len(ids)-1]
	}
	if err := initializeGateway(ctx, tx, legacyGateway); err != nil {
		return err
	}
	if err := claim.InitializeUsageTotalsTx(ctx, tx, time.Now().Unix()); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, conversionSQL)
	return err
}

func readIDs(ctx context.Context, tx *sql.Tx, query string, after int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, query, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0, 1000)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func initializeGateway(ctx context.Context, tx *sql.Tx, raw string) error {
	var initialized int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM gateway_model_capabilities_state WHERE singleton=1").Scan(&initialized); err != nil {
		return err
	}
	if initialized != 0 {
		return nil
	}
	config := gatewaypolicy.Config{}
	if strings.TrimSpace(raw) != "" {
		var err error
		config, err = gatewaypolicy.Parse(raw)
		if err != nil {
			return err
		}
	}
	now := time.Now().Unix()
	for _, entry := range config.Entries() {
		policy, err := json.Marshal(entry.Policy)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO gateway_model_capabilities(base_url,model,policy_json,revision,updated_at) VALUES(?,?,?,1,?) ON CONFLICT(base_url,model) DO NOTHING`, entry.BaseURL, entry.Model, string(policy), now); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO gateway_model_capabilities_state(singleton,initialized_at) VALUES(1,?)", now)
	return err
}
