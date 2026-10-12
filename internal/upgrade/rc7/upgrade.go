// Package rc7 implements the one-time conversion from the final prerelease.
// The server does not import this package.
package rc7

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/continuity"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/gatewaypolicy"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const SourceCommit = db.RC7SourceCommit
const SourceSchemaVersion = db.RC7SourceSchemaVersion
const SourceManifestHash = db.RC7SourceManifestHash

//go:embed convert.sql
var conversionSQL string

type Options struct {
	Apply               bool
	LegacyGatewayModels string
}

type Result struct {
	Status              string `json:"status"`
	SourceSchemaVersion int    `json:"source_schema_version"`
	TargetSchemaVersion int    `json:"target_schema_version"`
}

func Run(ctx context.Context, path string, vault *secret.Vault, options Options) (Result, error) {
	invalidLegacyConfiguration := false
	needed, err := db.ConvertOffline(ctx, path, vault, db.OfflineSchema{Version: SourceSchemaVersion, ManifestHash: SourceManifestHash}, options.Apply,
		func(ctx context.Context, database *sql.DB) error {
			var initialized int
			if err := database.QueryRowContext(ctx, "SELECT count(*) FROM gateway_model_capabilities_state WHERE singleton=1").Scan(&initialized); err != nil {
				return err
			}
			if initialized == 0 && strings.TrimSpace(options.LegacyGatewayModels) != "" {
				_, err := gatewaypolicy.Parse(options.LegacyGatewayModels)
				invalidLegacyConfiguration = err != nil
				return err
			}
			return nil
		},
		func(ctx context.Context, database *sql.DB, tx *sql.Tx) error {
			return convert(ctx, database, tx, vault, options.LegacyGatewayModels)
		})
	if err != nil {
		if invalidLegacyConfiguration {
			return Result{}, errors.New("legacy Gateway model configuration is invalid")
		}
		return Result{}, err
	}
	status := "current"
	if needed {
		status = "ready"
		if options.Apply {
			status = "converted"
		}
	}
	return Result{Status: status, SourceSchemaVersion: SourceSchemaVersion, TargetSchemaVersion: db.CurrentSchemaVersion}, nil
}

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
