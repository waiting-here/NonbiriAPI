package v1

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type serviceScopeCodecKey struct{}

var serviceScopeImpact = &migrationImpact{
	addedTables: []string{"discovery_run_changes"},
	// These parents are rewritten together while their deferred identities are
	// remapped. Their incoming relationships are checked before commit.
	rebuiltTables:  []string{"credentials", "credential_versions", "endpoint_key_grant_revisions", "discovery_sources", "discovery_source_revisions", "discovery_runs", "donation_use_revisions", "donation_service_health", "dispatch_claims"},
	relationTables: []string{"endpoints", "endpoint_key_discovery_sources", "discovery_source_states", "model_catalog_entries", "dispatch_claim_targets", "discovery_source_keys", "gacha_pool_statistics", "gacha_card_statistics", "gacha_pool_participants", "gacha_card_participants", "gacha_shop_statistics", "gacha_shop_card_statistics", "gacha_shop_participants"},
	credentials:    credentialsRebuilt,
}

type serviceScopeMigration struct {
	ctx     context.Context
	tx      *sql.Tx
	codec   secret.GenerationTwoContextCodec
	columns map[string][]string
}

// The offline transaction preserves public key, grant, use and target IDs. Only
// shared credential bodies and catalog histories acquire independent copies.
func isolateServiceKeys(ctx context.Context, tx *sql.Tx) error {
	codec, _ := ctx.Value(serviceScopeCodecKey{}).(secret.GenerationTwoContextCodec)
	m := serviceScopeMigration{ctx: ctx, tx: tx, codec: codec, columns: map[string][]string{}}
	objects, err := captureConnectionSchemaObjects(ctx, tx)
	if err != nil {
		return err
	}
	// Immutable evidence is rewritten only by this explicit schema conversion.
	// Restoring the exact trigger definitions is part of the same transaction.
	for _, object := range objects {
		if object.Type == "trigger" {
			if _, err := tx.ExecContext(ctx, "DROP TRIGGER "+quoteSQLiteIdentifier(object.Name)); err != nil {
				return err
			}
		}
	}
	if err := m.assignServices(); err != nil {
		return err
	}
	if err := m.prepareMaps(); err != nil {
		return err
	}
	if err := m.copyCredentials(); err != nil {
		return err
	}
	if err := m.copySources(); err != nil {
		return err
	}
	if err := m.relink(); err != nil {
		return err
	}
	for _, object := range objects {
		if object.Type == "trigger" {
			if _, err := tx.ExecContext(ctx, object.SQL); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, db.ServiceKeyScopeSQL); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO discovery_source_keys(source_id,endpoint_key_id)
 SELECT new_id,key_id FROM temp.service_source_map WHERE key_id>0;
 DROP TABLE temp.service_source_map; DROP TABLE temp.service_version_map;
 DROP TABLE temp.service_credential_map; DROP TABLE temp.service_revision_map;
 DROP TABLE temp.service_run_map; DROP TABLE temp.service_use_sources`); err != nil {
		return err
	}
	return backfillGachaStatistics(ctx, tx)
}

func (m *serviceScopeMigration) ids(query string, args ...any) ([]int64, error) {
	rows, err := m.tx.QueryContext(m.ctx, query, args...)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, errors.Join(rows.Err(), rows.Close())
}

func (m *serviceScopeMigration) exec(query string, args ...any) error {
	_, err := m.tx.ExecContext(m.ctx, query, args...)
	return err
}

func (m *serviceScopeMigration) assignServices() error {
	ids, err := m.ids(`SELECT id FROM endpoints WHERE connection_id IS NULL ORDER BY id`)
	if err != nil {
		return err
	}
	for _, id := range ids {
		result, err := m.tx.ExecContext(m.ctx, `INSERT INTO service_connections(user_id,name,note,revision,created_at,updated_at)
 SELECT user_id,CASE WHEN trim(note)<>'' THEN substr(trim(note),1,128) ELSE 'Service '||id END,'',1,created_at,updated_at FROM endpoints WHERE id=?`, id)
		if err != nil {
			return err
		}
		connection, err := result.LastInsertId()
		if err != nil {
			return err
		}
		if err := m.exec(`UPDATE endpoints SET connection_id=? WHERE id=?`, connection, id); err != nil {
			return err
		}
	}
	if err := m.exec(`UPDATE credentials SET connection_id=COALESCE(
 (SELECT min(e.connection_id) FROM endpoint_key_grant_revisions g JOIN endpoints e ON e.id=g.endpoint_id WHERE g.credential_id=credentials.id),
 (SELECT min(s.connection_id) FROM discovery_source_revisions r JOIN discovery_sources s ON s.id=r.source_id WHERE r.credential_id=credentials.id))
 WHERE deleted_at IS NULL AND connection_id IS NULL`); err != nil {
		return err
	}
	owners, err := m.ids(`SELECT DISTINCT user_id FROM credentials WHERE deleted_at IS NULL AND connection_id IS NULL ORDER BY user_id`)
	if err != nil {
		return err
	}
	for _, owner := range owners {
		result, err := m.tx.ExecContext(m.ctx, `INSERT INTO service_connections(user_id,name,note,revision,created_at,updated_at)
 SELECT user_id,'Recovered keys','',1,min(created_at),max(updated_at) FROM credentials WHERE user_id=? AND deleted_at IS NULL AND connection_id IS NULL`, owner)
		if err != nil {
			return err
		}
		id, err := result.LastInsertId()
		if err != nil {
			return err
		}
		if err := m.exec(`UPDATE credentials SET connection_id=? WHERE user_id=? AND deleted_at IS NULL AND connection_id IS NULL`, id, owner); err != nil {
			return err
		}
	}
	// A retained source can outlive its credential and all endpoints. Keep it
	// visible in a service even when no live credential can supply an owner.
	owners, err = m.ids(`SELECT DISTINCT s.user_id FROM discovery_sources s WHERE s.connection_id IS NULL
 AND NOT EXISTS(SELECT 1 FROM service_connections sc WHERE sc.user_id=s.user_id) ORDER BY s.user_id`)
	if err != nil {
		return err
	}
	for _, owner := range owners {
		if err := m.exec(`INSERT INTO service_connections(user_id,name,note,revision,created_at,updated_at)
 SELECT user_id,'Recovered keys','',1,min(created_at),max(updated_at) FROM discovery_sources WHERE user_id=?`, owner); err != nil {
			return err
		}
	}
	return m.exec(`UPDATE discovery_sources SET connection_id=COALESCE(
 (SELECT min(e.connection_id) FROM endpoint_key_discovery_sources ks JOIN endpoint_keys k ON k.id=ks.endpoint_key_id JOIN endpoints e ON e.id=k.endpoint_id WHERE ks.source_id=discovery_sources.id),
 (SELECT c.connection_id FROM discovery_source_revisions r JOIN credentials c ON c.id=r.credential_id WHERE r.id=discovery_sources.current_revision_id),
 (SELECT min(sc.id) FROM service_connections sc WHERE sc.user_id=discovery_sources.user_id)) WHERE connection_id IS NULL`)
}

func (m *serviceScopeMigration) prepareMaps() error {
	// Capture old fallback selections before enforcing the new pair ownership.
	oldSelection := strings.ReplaceAll(connectionstore.DiscoverySourceRevisionSQL,
		" AND EXISTS(SELECT 1 FROM discovery_source_keys scope WHERE scope.source_id=ds.id AND scope.endpoint_key_id=dk.endpoint_key_id)\n", "")
	if err := m.exec(`CREATE TEMP TABLE service_use_sources AS SELECT ur.id,dk.endpoint_key_id AS key_id,` + oldSelection + ` AS revision_id
 FROM donation_use_revisions ur JOIN donation_uses u ON u.id=ur.use_id JOIN donation_keys dk ON dk.id=u.donation_key_id
 JOIN endpoint_grant_authorizations a ON a.grant_revision_id=ur.grant_revision_id WHERE dk.endpoint_key_id IS NOT NULL;
 CREATE UNIQUE INDEX temp.idx_service_use_sources ON service_use_sources(id);
 CREATE TEMP TABLE service_source_map(old_id INTEGER,key_id INTEGER,new_id INTEGER,connection_id INTEGER,PRIMARY KEY(old_id,key_id));
 INSERT INTO service_source_map(old_id,key_id,connection_id)
 SELECT source_id,key_id,e.connection_id FROM (
 SELECT source_id,endpoint_key_id AS key_id FROM endpoint_key_discovery_sources
 UNION SELECT r.source_id,u.key_id FROM service_use_sources u JOIN discovery_source_revisions r ON r.id=u.revision_id
 ) selected JOIN endpoint_keys k ON k.id=selected.key_id JOIN endpoints e ON e.id=k.endpoint_id;
 INSERT INTO service_source_map(old_id,key_id,connection_id)
 SELECT s.id,0,s.connection_id FROM discovery_sources s WHERE NOT EXISTS(SELECT 1 FROM service_source_map m WHERE m.old_id=s.id);
 UPDATE service_source_map SET new_id=old_id WHERE key_id=(SELECT min(other.key_id) FROM service_source_map other WHERE other.old_id=service_source_map.old_id);
 CREATE TEMP TABLE service_credential_map(old_id INTEGER,connection_id INTEGER,new_id INTEGER,PRIMARY KEY(old_id,connection_id));
 INSERT INTO service_credential_map(old_id,connection_id)
 SELECT id,connection_id FROM credentials WHERE deleted_at IS NULL
 UNION SELECT g.credential_id,e.connection_id FROM endpoint_key_grant_revisions g JOIN endpoints e ON e.id=g.endpoint_id
 UNION SELECT r.credential_id,m.connection_id FROM discovery_source_revisions r JOIN service_source_map m ON m.old_id=r.source_id WHERE m.connection_id IS NOT NULL;
 UPDATE service_credential_map SET new_id=old_id WHERE EXISTS(SELECT 1 FROM credentials c WHERE c.id=old_id AND (c.connection_id=service_credential_map.connection_id OR c.deleted_at IS NOT NULL));
 CREATE TEMP TABLE service_version_map(old_id INTEGER,connection_id INTEGER,new_id INTEGER,PRIMARY KEY(old_id,connection_id));
 CREATE TEMP TABLE service_revision_map(old_id INTEGER,key_id INTEGER,new_id INTEGER,PRIMARY KEY(old_id,key_id));
 CREATE TEMP TABLE service_run_map(old_id INTEGER,key_id INTEGER,new_id INTEGER,PRIMARY KEY(old_id,key_id));`); err != nil {
		return err
	}
	return nil
}

// cloneRow is used only with package-owned table names and column overrides.
// All values remain parameters; encrypted data never enters generated SQL.
func (m *serviceScopeMigration) cloneRow(table string, id int64, overrides map[string]any) (int64, error) {
	columns, ok := m.columns[table]
	if !ok {
		rows, err := m.tx.QueryContext(m.ctx, "PRAGMA table_info("+quoteSQLiteIdentifier(table)+")")
		if err != nil {
			return 0, err
		}
		for rows.Next() {
			var cid, notNull, pk int
			var name, kind string
			var def sql.NullString
			if err := rows.Scan(&cid, &name, &kind, &notNull, &def, &pk); err != nil {
				rows.Close()
				return 0, err
			}
			if name != "id" {
				columns = append(columns, name)
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return 0, err
		}
		m.columns[table] = columns
	}
	expressions := make([]string, len(columns))
	args := []any{}
	for i, column := range columns {
		if value, ok := overrides[column]; ok {
			expressions[i] = "?"
			args = append(args, value)
		} else {
			expressions[i] = quoteSQLiteIdentifier(column)
		}
	}
	args = append(args, id)
	result, err := m.tx.ExecContext(m.ctx, "INSERT INTO "+quoteSQLiteIdentifier(table)+"("+strings.Join(columns, ",")+") SELECT "+strings.Join(expressions, ",")+" FROM "+quoteSQLiteIdentifier(table)+" WHERE id=?", args...)
	if err != nil {
		return 0, fmt.Errorf("copy service %s: %w", table, err)
	}
	return result.LastInsertId()
}

func (m *serviceScopeMigration) copySecret(id int64) (int64, error) {
	if m.codec == nil {
		return 0, errors.New("credential isolation requires the database key")
	}
	var raw []byte
	var envelope string
	if err := m.tx.QueryRowContext(m.ctx, `SELECT context_id,encrypted_secret FROM endpoint_key_secrets WHERE id=?`, id).Scan(&raw, &envelope); err != nil {
		return 0, err
	}
	oldContext, err := secret.NewGenerationTwoEndpointKeyContext(raw)
	if err != nil {
		return 0, err
	}
	body, err := m.codec.OpenForGenerationTwoContext(envelope, oldContext)
	if err != nil {
		return 0, err
	}
	defer clear(body)
	var next [16]byte
	if _, err := rand.Read(next[:]); err != nil {
		return 0, err
	}
	newContext, err := secret.NewGenerationTwoEndpointKeyContext(next[:])
	if err != nil {
		return 0, err
	}
	envelope, err = m.codec.SealForGenerationTwoContext(body, newContext)
	if err != nil {
		return 0, err
	}
	return m.cloneRow("endpoint_key_secrets", id, map[string]any{"context_id": next[:], "encrypted_secret": envelope})
}

func (m *serviceScopeMigration) copyCredentials() error {
	ids, err := m.ids(`SELECT DISTINCT old_id FROM service_credential_map ORDER BY old_id`)
	if err != nil {
		return err
	}
	for _, oldID := range ids {
		connections, err := m.ids(`SELECT connection_id FROM service_credential_map WHERE old_id=? ORDER BY connection_id`, oldID)
		if err != nil {
			return err
		}
		versions, err := m.ids(`SELECT id FROM credential_versions WHERE credential_id=? ORDER BY id`, oldID)
		if err != nil {
			return err
		}
		for _, connection := range connections {
			var mapped sql.NullInt64
			if err := m.tx.QueryRowContext(m.ctx, `SELECT new_id FROM service_credential_map WHERE old_id=? AND connection_id=?`, oldID, connection).Scan(&mapped); err != nil {
				return err
			}
			newID := mapped.Int64
			if !mapped.Valid {
				newID, err = m.cloneRow("credentials", oldID, map[string]any{"connection_id": connection})
				if err != nil {
					return err
				}
				if err := m.exec(`UPDATE service_credential_map SET new_id=? WHERE old_id=? AND connection_id=?`, newID, oldID, connection); err != nil {
					return err
				}
			}
			for _, version := range versions {
				newVersion := version
				if newID != oldID {
					var ref sql.NullInt64
					if err := m.tx.QueryRowContext(m.ctx, `SELECT secret_ref_id FROM credential_versions WHERE id=?`, version).Scan(&ref); err != nil {
						return err
					}
					var secretID any
					if ref.Valid {
						secretID, err = m.copySecret(ref.Int64)
						if err != nil {
							return err
						}
					}
					newVersion, err = m.cloneRow("credential_versions", version, map[string]any{"credential_id": newID, "secret_ref_id": secretID})
					if err != nil {
						return err
					}
				}
				if err := m.exec(`INSERT INTO service_version_map VALUES(?,?,?)`, version, connection, newVersion); err != nil {
					return err
				}
			}
			if newID != oldID {
				if err := m.exec(`UPDATE credentials SET current_version_id=(SELECT new_id FROM service_version_map WHERE old_id=credentials.current_version_id AND connection_id=?) WHERE id=?`, connection, newID); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (m *serviceScopeMigration) copySources() error {
	now := time.Now().Unix()
	if err := m.exec(`UPDATE accepted_operations SET state='completed',checkpoint='',last_error_class=NULL,terminal_at=max(?,created_at)
 WHERE kind='model_discovery' AND state IN ('accepted','running') AND id IN(SELECT operation_id FROM discovery_runs WHERE state='checking');
 UPDATE discovery_runs SET state='failed',safe_class='interrupted',safe_diagnostic='',completed_at=max(?,started_at) WHERE state='checking'`, now, now); err != nil {
		return err
	}
	sources, err := m.ids(`SELECT DISTINCT old_id FROM service_source_map ORDER BY old_id`)
	if err != nil {
		return err
	}
	for _, source := range sources {
		keys, err := m.ids(`SELECT key_id FROM service_source_map WHERE old_id=? ORDER BY key_id DESC`, source)
		if err != nil {
			return err
		}
		revisions, err := m.ids(`SELECT id FROM discovery_source_revisions WHERE source_id=? ORDER BY id`, source)
		if err != nil {
			return err
		}
		for _, key := range keys {
			var mapped, connection sql.NullInt64
			if err := m.tx.QueryRowContext(m.ctx, `SELECT new_id,connection_id FROM service_source_map WHERE old_id=? AND key_id=?`, source, key).Scan(&mapped, &connection); err != nil {
				return err
			}
			newSource := mapped.Int64
			if !mapped.Valid {
				newSource, err = m.cloneRow("discovery_sources", source, map[string]any{"connection_id": connection})
				if err != nil {
					return err
				}
				if err := m.exec(`UPDATE service_source_map SET new_id=? WHERE old_id=? AND key_id=?`, newSource, source, key); err != nil {
					return err
				}
			}
			for _, revision := range revisions {
				var credential, version int64
				if err := m.tx.QueryRowContext(m.ctx, `SELECT COALESCE(cm.new_id,r.credential_id),COALESCE(vm.new_id,r.credential_version_id) FROM discovery_source_revisions r
 LEFT JOIN service_credential_map cm ON cm.old_id=r.credential_id AND cm.connection_id=?
 LEFT JOIN service_version_map vm ON vm.old_id=r.credential_version_id AND vm.connection_id=? WHERE r.id=?`, connection, connection, revision).Scan(&credential, &version); err != nil {
					return err
				}
				newRevision := revision
				if newSource != source {
					newRevision, err = m.cloneRow("discovery_source_revisions", revision, map[string]any{"source_id": newSource, "credential_id": credential, "credential_version_id": version})
					if err != nil {
						return err
					}
				} else if err := m.exec(`UPDATE discovery_source_revisions SET credential_id=?,credential_version_id=? WHERE id=?`, credential, version, revision); err != nil {
					return err
				}
				if err := m.exec(`INSERT INTO service_revision_map VALUES(?,?,?)`, revision, key, newRevision); err != nil {
					return err
				}
				runs, err := m.ids(`SELECT id FROM discovery_runs WHERE source_revision_id=? ORDER BY id`, revision)
				if err != nil {
					return err
				}
				for _, run := range runs {
					newRun := run
					if newSource != source {
						newRun, err = m.cloneRow("discovery_runs", run, map[string]any{"source_revision_id": newRevision, "credential_version_id": version, "operation_id": nil, "operation_hash": nil, "authorization_kind": "legacy", "authorization_use_id": nil, "authorization_use_revision_id": nil})
						if err != nil {
							return err
						}
						if err := m.exec(`INSERT INTO model_catalog_entries(endpoint_key_id,discovery_run_id,native_service_mask,evidence_source,evidence_revision,source_type,source_identity,normalized_model_id,provider,source_revision,created_at,updated_at)
 SELECT endpoint_key_id,?,native_service_mask,evidence_source,evidence_revision,source_type,source_identity,normalized_model_id,provider,source_revision,created_at,updated_at FROM model_catalog_entries WHERE discovery_run_id=?`, newRun, run); err != nil {
							return err
						}
					} else if err := m.exec(`UPDATE discovery_runs SET credential_version_id=CASE WHEN credential_version_id IS NULL THEN NULL ELSE ? END WHERE id=?`, version, run); err != nil {
						return err
					}
					if err := m.exec(`INSERT INTO service_run_map VALUES(?,?,?)`, run, key, newRun); err != nil {
						return err
					}
				}
				if newSource != source {
					if err := m.exec(`INSERT INTO discovery_source_states(source_revision_id,result_generation,current_run_id,last_successful_run_id,updated_at)
 SELECT ?,result_generation,(SELECT new_id FROM service_run_map WHERE old_id=st.current_run_id AND key_id=?),(SELECT new_id FROM service_run_map WHERE old_id=st.last_successful_run_id AND key_id=?),updated_at
 FROM discovery_source_states st WHERE source_revision_id=?`, newRevision, key, key, revision); err != nil {
						return err
					}
				}
			}
			if err := m.exec(`UPDATE discovery_sources SET connection_id=?,current_revision_id=(SELECT new_id FROM service_revision_map WHERE old_id=discovery_sources.current_revision_id AND key_id=?) WHERE id=?`, connection, key, newSource); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *serviceScopeMigration) relink() error {
	statements := []string{
		`UPDATE endpoint_key_grant_revisions SET credential_id=(SELECT new_id FROM service_credential_map WHERE old_id=credential_id AND connection_id=(SELECT connection_id FROM endpoints WHERE id=endpoint_id)),credential_version_id=(SELECT new_id FROM service_version_map WHERE old_id=credential_version_id AND connection_id=(SELECT connection_id FROM endpoints WHERE id=endpoint_id))`,
		`UPDATE endpoint_key_discovery_sources SET source_id=(SELECT new_id FROM service_source_map WHERE old_id=source_id AND key_id=endpoint_key_id),source_revision_id=(SELECT new_id FROM service_revision_map WHERE old_id=source_revision_id AND key_id=endpoint_key_id),credential_version_id=(SELECT credential_version_id FROM endpoint_key_grant_revisions WHERE id=grant_revision_id)`,
		`UPDATE donation_use_revisions SET credential_id_snapshot=(SELECT credential_id FROM endpoint_key_grant_revisions WHERE id=grant_revision_id) WHERE grant_revision_id IS NOT NULL`,
		`UPDATE donation_use_revisions SET discovery_source_revision_id=(SELECT rm.new_id FROM service_use_sources us JOIN service_revision_map rm ON rm.old_id=us.revision_id AND rm.key_id=us.key_id WHERE us.id=donation_use_revisions.id),source_discovery_revision_id=(SELECT rm.new_id FROM service_use_sources us JOIN service_revision_map rm ON rm.old_id=us.revision_id AND rm.key_id=us.key_id WHERE us.id=donation_use_revisions.id) WHERE id IN(SELECT id FROM service_use_sources WHERE revision_id>0)`,
		`UPDATE donation_service_health SET credential_version_id=(SELECT vm.new_id FROM service_version_map vm JOIN endpoint_keys k ON k.id=donation_service_health.endpoint_key_id JOIN endpoints e ON e.id=k.endpoint_id WHERE vm.old_id=donation_service_health.credential_version_id AND vm.connection_id=e.connection_id)`,
		// Active inference claims retain the same body under its new encrypted ID;
		// their health/authorization snapshots continue to name the same use/grant.
		`UPDATE dispatch_claim_targets SET credential_id_snapshot=(SELECT cm.new_id FROM service_credential_map cm JOIN endpoints e ON e.id=endpoint_id_snapshot WHERE cm.old_id=credential_id_snapshot AND cm.connection_id=e.connection_id),credential_version_id=(SELECT vm.new_id FROM service_version_map vm JOIN endpoints e ON e.id=endpoint_id_snapshot WHERE vm.old_id=credential_version_id AND vm.connection_id=e.connection_id) WHERE endpoint_id_snapshot IN(SELECT id FROM endpoints) AND authorization_kind<>'discovery'`,
		`UPDATE dispatch_claims SET secret_ref_id=(SELECT v.secret_ref_id FROM dispatch_claim_targets t JOIN credential_versions v ON v.id=t.credential_version_id WHERE t.claim_id=dispatch_claims.id) WHERE secret_ref_id IS NOT NULL AND id IN(SELECT claim_id FROM dispatch_claim_targets WHERE authorization_kind<>'discovery' AND endpoint_id_snapshot IN(SELECT id FROM endpoints))`,
	}
	for i, statement := range statements {
		if err := m.exec(statement); err != nil {
			return fmt.Errorf("service relationship %s: %w", strconv.Itoa(i+1), err)
		}
	}
	return nil
}
