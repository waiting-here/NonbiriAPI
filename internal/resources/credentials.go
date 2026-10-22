package resources

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

type CreateCredentialInput struct {
	Secret             []byte
	Note               string
	OwnershipConfirmed bool
}

type PatchCredentialInput struct {
	Note             *string
	Enabled          *bool
	ExpectedRevision int64
}

const credentialSelect = `SELECT cr.id,cr.connection_id,cr.note,cr.enabled,cr.revision,v.version,v.display_head,v.display_tail,cr.created_at,cr.updated_at
FROM credentials cr JOIN credential_versions v ON v.id=cr.current_version_id AND v.credential_id=cr.id
WHERE cr.user_id=? AND cr.deleted_at IS NULL`

func scanCredential(scanner pageScanner) (Credential, error) {
	var item Credential
	var id, revision, version int64
	var connectionID sql.NullInt64
	var enabled int
	if err := scanner.Scan(&id, &connectionID, &item.Note, &enabled, &revision, &version, &item.DisplayHead, &item.DisplayTail, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return Credential{}, err
	}
	if id <= 0 || revision <= 0 || version <= 0 || enabled < 0 || enabled > 1 || !validateNote(item.Note) || len(item.DisplayHead) > 16 || len(item.DisplayTail) > 16 {
		return Credential{}, ErrUnavailable
	}
	var err error
	item.ConnectionID, err = optionalDecimal(connectionID)
	if err != nil {
		return Credential{}, err
	}
	item.ID, item.Revision, item.CredentialVersion = strconv.FormatInt(id, 10), strconv.FormatInt(revision, 10), strconv.FormatInt(version, 10)
	item.Enabled = enabled == 1
	return item, nil
}

func getCredentialTx(ctx context.Context, tx *sql.Tx, userID, credentialID int64) (Credential, error) {
	item, err := scanCredential(tx.QueryRowContext(ctx, credentialSelect+` AND cr.id=?`, userID, credentialID))
	if errors.Is(err, sql.ErrNoRows) {
		return Credential{}, ErrNotFound
	}
	return item, err
}

func (r *Repository) GetCredential(ctx context.Context, userID, credentialID int64) (Credential, error) {
	if credentialID <= 0 {
		return Credential{}, ErrNotFound
	}
	return userPageRead(ctx, r, userID, pagination.Default(), func(ctx context.Context, tx *sql.Tx) (Credential, error) {
		return getCredentialTx(ctx, tx, userID, credentialID)
	})
}

func (r *Repository) listConnectionCredentials(ctx context.Context, userID, connectionID int64, search, enabled string, window connectionListWindow) (Page[Credential], error) {
	if connectionID <= 0 || !validateFreeText(search, 0, 128) || !optionalChoice(enabled, "true", "false") {
		return Page[Credential]{}, ErrInvalidRequest
	}
	return userPageRead(ctx, r, userID, pagination.Default(), func(ctx context.Context, tx *sql.Tx) (Page[Credential], error) {
		if err := requireConnectionTx(ctx, tx, userID, connectionID); err != nil {
			return Page[Credential]{}, err
		}
		query := credentialSelect + ` AND EXISTS(SELECT 1 FROM service_connections sc WHERE sc.id=? AND sc.user_id=cr.user_id AND ` + connectionCredentialMembership + `)`
		args := []any{userID, connectionID}
		if search = strings.TrimSpace(search); search != "" {
			query += ` AND (instr(lower(cr.note),lower(?))>0 OR instr(lower(v.display_head),lower(?))>0 OR instr(lower(v.display_tail),lower(?))>0)`
			args = append(args, search, search, search)
		}
		if enabled != "" {
			query += ` AND cr.enabled=?`
			args = append(args, enabled == "true")
		}
		return readConnectionList(ctx, tx, r, window, "connection-credentials", query, "cr.id", args, scanCredential, func(item Credential) string { return item.ID })
	})
}

// A deferred parent/current-version reference can be created atomically with
// an explicit next child ID. Only these internal table names are admitted.
func nextConnectionIdentityTx(ctx context.Context, tx *sql.Tx, table string) (int64, error) {
	switch table {
	case "credential_versions", "endpoint_key_grant_revisions", "discovery_source_revisions":
	default:
		return 0, ErrInvalidRequest
	}
	var latest int64
	if err := tx.QueryRowContext(ctx, `SELECT max(COALESCE((SELECT seq FROM sqlite_sequence WHERE name=?),0),COALESCE((SELECT max(id) FROM `+table+`),0))`, table).Scan(&latest); err != nil {
		return 0, err
	}
	if latest < 0 || latest == math.MaxInt64 {
		return 0, ErrResourceLimit
	}
	return latest + 1, nil
}

func checkCredentialCapacityTx(ctx context.Context, tx *sql.Tx, userID int64) error {
	endpoints, err := effectiveEndpointLimitTx(ctx, tx, userID)
	if err != nil {
		return err
	}
	keys, err := readSiteLimitTx(ctx, tx, "default_endpoint_key_limit", 1, 10000)
	if err != nil {
		return err
	}
	count, err := countTx(ctx, tx, `SELECT count(*) FROM credentials WHERE user_id=? AND deleted_at IS NULL`, userID)
	if err != nil {
		return err
	}
	if count >= endpoints*keys {
		return ErrResourceLimit
	}
	return nil
}

func createCredentialFromSecretTx(ctx context.Context, tx *sql.Tx, userID, connectionID int64, note string, stored StoredSecret, now int64) (int64, int64, error) {
	if stored.RefID <= 0 || len(stored.DisplayHead) > 16 || len(stored.DisplayTail) > 16 || !validateNote(note) {
		return 0, 0, ErrUnavailable
	}
	versionID, err := nextConnectionIdentityTx(ctx, tx, "credential_versions")
	if err != nil {
		return 0, 0, err
	}
	var connection any
	if connectionID != 0 {
		if err := requireConnectionTx(ctx, tx, userID, connectionID); err != nil {
			return 0, 0, err
		}
		connection = connectionID
	}
	if err := checkCredentialCapacityTx(ctx, tx, userID); err != nil {
		return 0, 0, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO credentials(user_id,connection_id,note,enabled,revision,current_version_id,created_at,updated_at) VALUES(?,?,?,1,1,?,?,?)`, userID, connection, note, versionID, now, now)
	if err != nil {
		return 0, 0, err
	}
	credentialID, err := result.LastInsertId()
	if err != nil {
		return 0, 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO credential_versions(id,credential_id,version,secret_ref_id,display_head,display_tail,enabled,revision,created_at) VALUES(?,?,1,?,?,?,1,1,?)`, versionID, credentialID, stored.RefID, stored.DisplayHead, stored.DisplayTail, now); err != nil {
		return 0, 0, fmt.Errorf("resources: create credential version: %w", err)
	}
	return credentialID, versionID, nil
}

func (r *Repository) CreateCredential(ctx context.Context, userID, connectionID int64, mutation ControlMutation, input CreateCredentialInput) (MutationResult[Credential], error) {
	if r == nil || userID <= 0 || connectionID <= 0 || !validResourceMutation(mutation, http.MethodPost, routeConnectionCredentials, connectionID) ||
		!input.OwnershipConfirmed || !validateEndpointSecretPlaintext(input.Secret) || !validateNote(input.Note) {
		return MutationResult[Credential]{}, ErrInvalidRequest
	}
	writer, ok := r.secrets.(SharedSecretWriter)
	if !ok || isNilInterface(writer) {
		return MutationResult[Credential]{}, ErrUnavailable
	}
	plaintext := append([]byte(nil), input.Secret...)
	defer clear(plaintext)
	return ownerResourceMutation(ctx, r, userID, mutation, http.StatusCreated, "credential", func(ctx context.Context, tx *sql.Tx, now int64) (Credential, ResourceOperationResult, error) {
		if err := requireConnectionTx(ctx, tx, userID, connectionID); err != nil {
			return Credential{}, ResourceOperationResult{}, err
		}
		stored, err := writer.WriteCredentialSecret(ctx, tx, plaintext, now)
		if err != nil {
			return Credential{}, ResourceOperationResult{}, err
		}
		id, _, err := createCredentialFromSecretTx(ctx, tx, userID, connectionID, input.Note, stored, now)
		if err != nil {
			return Credential{}, ResourceOperationResult{}, err
		}
		item, err := getCredentialTx(ctx, tx, userID, id)
		return item, ResourceOperationResult{ConnectionID: strconv.FormatInt(connectionID, 10), CredentialID: item.ID, CredentialVersion: item.CredentialVersion}, err
	})
}

func credentialLockedTx(ctx context.Context, tx *sql.Tx, userID, credentialID int64) (bool, error) {
	var locked bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM endpoint_key_grant_revisions g
JOIN endpoint_key_suspensions s ON s.endpoint_key_id=g.endpoint_key_id
WHERE g.credential_id=? AND g.owner_user_id=? AND EXISTS(
 SELECT 1 FROM endpoint_key_credential_evidence ev WHERE ev.endpoint_key_id=g.endpoint_key_id AND ev.grant_revision_id=g.id))`, credentialID, userID).Scan(&locked)
	return locked, err
}

func (r *Repository) PatchCredential(ctx context.Context, userID, credentialID int64, mutation ControlMutation, input PatchCredentialInput) (MutationResult[Credential], error) {
	if r == nil || userID <= 0 || credentialID <= 0 || !validResourceMutation(mutation, http.MethodPatch, routeCredential, credentialID) || input.ExpectedRevision <= 0 ||
		(input.Note == nil && input.Enabled == nil) || (input.Note != nil && !validateNote(*input.Note)) {
		return MutationResult[Credential]{}, ErrInvalidRequest
	}
	return ownerResourceMutation(ctx, r, userID, mutation, http.StatusOK, "", func(ctx context.Context, tx *sql.Tx, now int64) (Credential, ResourceOperationResult, error) {
		item, err := getCredentialTx(ctx, tx, userID, credentialID)
		if err != nil {
			return Credential{}, ResourceOperationResult{}, err
		}
		if item.Revision != strconv.FormatInt(input.ExpectedRevision, 10) || input.ExpectedRevision == math.MaxInt64 {
			return Credential{}, ResourceOperationResult{}, ErrConflict
		}
		locked, err := credentialLockedTx(ctx, tx, userID, credentialID)
		if err != nil {
			return Credential{}, ResourceOperationResult{}, err
		}
		if locked {
			return Credential{}, ResourceOperationResult{}, ErrResourceLocked
		}
		if input.Note != nil {
			item.Note = *input.Note
		}
		if input.Enabled != nil {
			item.Enabled = *input.Enabled
		}
		result, err := tx.ExecContext(ctx, `UPDATE credentials SET note=?,enabled=?,revision=revision+1,updated_at=? WHERE id=? AND user_id=? AND deleted_at IS NULL AND revision=?`, item.Note, boolInt(item.Enabled), now, credentialID, userID, input.ExpectedRevision)
		if err != nil {
			return Credential{}, ResourceOperationResult{}, err
		}
		if changed, err := result.RowsAffected(); err != nil || changed != 1 {
			return Credential{}, ResourceOperationResult{}, ErrConflict
		}
		if input.Enabled != nil {
			if err := r.reconcileCredentialRoutingTx(ctx, tx, userID, credentialID); err != nil {
				return Credential{}, ResourceOperationResult{}, err
			}
		}
		item.Revision, item.UpdatedAt = strconv.FormatInt(input.ExpectedRevision+1, 10), now
		return item, ResourceOperationResult{}, nil
	})
}
