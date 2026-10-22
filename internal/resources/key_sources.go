package resources

import (
	"context"
	"database/sql"
	"errors"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func defaultDiscoveryFormat(kind contract.Type) string {
	switch kind.ConfigurationType() {
	case contract.TypeOpenAICompatible:
		return "openai"
	case contract.TypeAnthropicCompatible:
		return "anthropic"
	case contract.TypeGoogleGemini:
		return "gemini"
	case contract.TypeAISDKGatewayV3:
		return "ai-sdk-gateway-v3"
	case contract.TypeAISDKGatewayV4:
		return "ai-sdk-gateway-v4"
	default:
		return ""
	}
}

func createDiscoverySourceTx(ctx context.Context, tx *sql.Tx, userID, connectionID, credentialID, versionID int64, name, baseURL, listFormat string, enabled bool, now int64) (int64, int64, error) {
	revisionID, err := nextConnectionIdentityTx(ctx, tx, "discovery_source_revisions")
	if err != nil {
		return 0, 0, err
	}
	var connection any
	if connectionID > 0 {
		connection = connectionID
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO discovery_sources(user_id,connection_id,name,enabled,revision,current_revision_id,created_at,updated_at) VALUES(?,?,?,?,1,?,?,?)`, userID, connection, name, boolInt(enabled), revisionID, now, now)
	if err != nil {
		return 0, 0, err
	}
	sourceID, err := result.LastInsertId()
	if err != nil {
		return 0, 0, err
	}
	if err := insertSourceRevisionTx(ctx, tx, revisionID, sourceID, userID, credentialID, versionID, 1, baseURL, listFormat, now); err != nil {
		return 0, 0, err
	}
	return sourceID, revisionID, nil
}

func insertSourceRevisionTx(ctx context.Context, tx *sql.Tx, revisionID, sourceID, userID, credentialID, versionID, revision int64, baseURL, listFormat string, now int64) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO discovery_source_revisions(id,source_id,owner_user_id,source_revision,credential_id,credential_version_id,canonical_base_url,list_format,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, revisionID, sourceID, userID, revision, credentialID, versionID, baseURL, listFormat, now); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO discovery_source_states(source_revision_id,result_generation,updated_at) VALUES(?,1,?)`, revisionID, now)
	return err
}

func setKeySourceTx(ctx context.Context, tx *sql.Tx, userID, keyID int64, sourceID *int64) error {
	if sourceID == nil {
		_, err := tx.ExecContext(ctx, `DELETE FROM endpoint_key_discovery_sources WHERE endpoint_key_id=? AND EXISTS(SELECT 1 FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id WHERE k.id=? AND e.user_id=?)`, keyID, keyID, userID)
		return err
	}
	var grantID, revisionID, versionID int64
	err := tx.QueryRowContext(ctx, `SELECT k.current_grant_revision_id,ds.current_revision_id,g.credential_version_id
 FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id
 JOIN endpoint_key_grant_revisions g ON g.id=k.current_grant_revision_id
 JOIN discovery_sources ds ON ds.id=? AND ds.user_id=e.user_id
 JOIN discovery_source_revisions sr ON sr.id=ds.current_revision_id AND sr.credential_version_id=g.credential_version_id
 WHERE k.id=? AND e.user_id=?`, *sourceID, keyID, userID).Scan(&grantID, &revisionID, &versionID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO endpoint_key_discovery_sources(endpoint_key_id,source_id,grant_revision_id,source_revision_id,credential_version_id) VALUES(?,?,?,?,?)
 ON CONFLICT(endpoint_key_id) DO UPDATE SET source_id=excluded.source_id,grant_revision_id=excluded.grant_revision_id,source_revision_id=excluded.source_revision_id,credential_version_id=excluded.credential_version_id`, keyID, *sourceID, grantID, revisionID, versionID)
	return err
}

func initializeKeySourceTx(ctx context.Context, tx *sql.Tx, userID, keyID, credentialID, versionID int64, endpoint Endpoint, selection **int64, now int64) error {
	if selection != nil {
		return setKeySourceTx(ctx, tx, userID, keyID, *selection)
	}
	format := defaultDiscoveryFormat(contract.Type(endpoint.ConnectorType))
	if format == "" {
		return nil
	}
	var connectionID int64
	if endpoint.ConnectionID != nil {
		connectionID, _ = parseDecimalID(*endpoint.ConnectionID)
	}
	sourceID, _, err := createDiscoverySourceTx(ctx, tx, userID, connectionID, credentialID, versionID, "", endpoint.BaseURL, format, true, now)
	if err != nil {
		return err
	}
	return setKeySourceTx(ctx, tx, userID, keyID, &sourceID)
}

func discoveryConnectorType(format string) (contract.Type, error) {
	switch format {
	case "openai":
		return contract.TypeOpenAICompatible, nil
	case "anthropic":
		return contract.TypeAnthropicCompatible, nil
	case "gemini":
		return contract.TypeGoogleGemini, nil
	case "ai-sdk-gateway-v3":
		return contract.TypeAISDKGatewayV3, nil
	case "ai-sdk-gateway-v4":
		return contract.TypeAISDKGatewayV4, nil
	default:
		return "", ErrInvalidRequest
	}
}
