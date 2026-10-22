// Package testseed constructs complete relationship fixtures for tests.
// Callers own the transaction, original encrypted secrets and domain budgets.
package testseed

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

type EndpointInput struct {
	ID, OwnerUserID int64
	Type            connectorcontract.Type
	BaseURL, Note   string
	NativeServices  connectorcontract.NativeServiceSet
	RerankFormat    connectorcontract.RerankFormat
	Disabled        bool
	At              int64
}

func InsertEndpointTx(ctx context.Context, tx *sql.Tx, in EndpointInput) (int64, error) {
	services := in.NativeServices
	if services.Empty() {
		var err error
		services, err = connectorcontract.LegacyNativeServices(in.Type)
		if err != nil {
			return 0, err
		}
	}
	if err := connectorcontract.ValidateNativeServices(in.Type, services, in.RerankFormat); err != nil {
		return 0, err
	}
	var id any
	if in.ID > 0 {
		id = in.ID
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO endpoints(id,user_id,connector_type,base_url,note,enabled,native_service_mask,rerank_format,capability_origin,created_at,updated_at)
 VALUES(?,?,?,?,?,?,?,?,'legacy',?,?)`, id, in.OwnerUserID, string(in.Type), in.BaseURL, in.Note, !in.Disabled, services.Mask(), string(in.RerankFormat), in.At, in.At)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

type KeyInput struct {
	ID, EndpointID, SecretRefID int64
	// A nonzero version explicitly shares an existing owner's version.
	CredentialVersionID            int64
	Fingerprint                    []byte
	DisplayHead, DisplayTail, Note string
	NativeServices                 connectorcontract.NativeServiceSet
	Disabled, ForceStoreFalse      bool
	At                             int64
}
type KeyIdentity struct{ KeyID, CredentialID, CredentialVersionID, GrantRevisionID int64 }

func InsertEndpointKeyTx(ctx context.Context, tx *sql.Tx, in KeyInput) (KeyIdentity, error) {
	var out KeyIdentity
	if tx == nil || in.EndpointID <= 0 || len(in.Fingerprint) != 32 || (in.CredentialVersionID > 0) == (in.SecretRefID > 0) {
		return out, errors.New("invalid key fixture")
	}
	var owner, mask int64
	var format string
	if err := tx.QueryRowContext(ctx, "SELECT user_id,native_service_mask,rerank_format FROM endpoints WHERE id=?", in.EndpointID).Scan(&owner, &mask, &format); err != nil {
		return out, err
	}
	if !in.NativeServices.Empty() {
		mask = in.NativeServices.Mask()
	}
	var err error
	out.KeyID = in.ID
	if out.KeyID == 0 {
		out.KeyID, err = nextID(ctx, tx, "endpoint_keys")
		if err != nil {
			return out, err
		}
	}
	out.GrantRevisionID, err = nextID(ctx, tx, "endpoint_key_grant_revisions")
	if err != nil {
		return out, err
	}
	if in.CredentialVersionID > 0 {
		var versionOwner int64
		out.CredentialVersionID = in.CredentialVersionID
		if err := tx.QueryRowContext(ctx, "SELECT v.credential_id,c.user_id FROM credential_versions v JOIN credentials c ON c.id=v.credential_id WHERE v.id=?", in.CredentialVersionID).Scan(&out.CredentialID, &versionOwner); err != nil {
			return out, err
		}
		if versionOwner != owner {
			return out, errors.New("fixture credential owner mismatch")
		}
	} else {
		out.CredentialID, err = nextID(ctx, tx, "credentials")
		if err != nil {
			return out, err
		}
		out.CredentialVersionID, err = nextID(ctx, tx, "credential_versions")
		if err != nil {
			return out, err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO credentials(id,user_id,current_version_id,created_at,updated_at) VALUES(?,?,?,?,?)", out.CredentialID, owner, out.CredentialVersionID, in.At, in.At); err != nil {
			return out, err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO credential_versions(id,credential_id,version,secret_ref_id,display_head,display_tail,created_at) VALUES(?,?,1,?,?,?,?)", out.CredentialVersionID, out.CredentialID, in.SecretRefID, in.DisplayHead, in.DisplayTail, in.At); err != nil {
			return out, err
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO endpoint_keys(id,endpoint_id,current_grant_revision_id,note,enabled,force_store_false,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)", out.KeyID, in.EndpointID, out.GrantRevisionID, in.Note, !in.Disabled, in.ForceStoreFalse, in.At, in.At); err != nil {
		return out, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO endpoint_key_grant_revisions(id,endpoint_key_id,endpoint_id,owner_user_id,credential_id,credential_version_id,grant_revision,native_service_mask,rerank_format,report_fingerprint,origin,created_at)
 VALUES(?,?,?,?,?,?,1,?,?,?,'legacy',?)`, out.GrantRevisionID, out.KeyID, in.EndpointID, owner, out.CredentialID, out.CredentialVersionID, mask, format, in.Fingerprint, in.At)
	return out, err
}

// SeedDonationUsesTx preserves the caller's membership and budget setup.
// The explicit approved flag is fixture input, never inferred from a channel.
func SeedDonationUsesTx(ctx context.Context, tx *sql.Tx, donationKeyID int64, approved bool, at int64) ([]int64, error) {
	return seedDonationUsesTx(ctx, tx, donationKeyID, approved, at, false)
}

// SeedDonationUsesWithDiscoveryTx explicitly shares the current exact source.
func SeedDonationUsesWithDiscoveryTx(ctx context.Context, tx *sql.Tx, donationKeyID int64, approved bool, at int64) ([]int64, error) {
	return seedDonationUsesTx(ctx, tx, donationKeyID, approved, at, true)
}

func seedDonationUsesTx(ctx context.Context, tx *sql.Tx, donationKeyID int64, approved bool, at int64, shareDiscovery bool) ([]int64, error) {
	var grant, credential, version, publicVersion, publicGrant, mask int64
	var head, tail string
	var fingerprint, hmac []byte
	var source sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT g.id,g.credential_id,g.credential_version_id,v.version,g.grant_revision,g.native_service_mask,v.display_head,v.display_tail,g.report_fingerprint,s.key_body_review_hmac,m.source_revision_id
 FROM donation_keys dk JOIN donations d ON d.id=dk.donation_id JOIN donation_key_memberships member ON member.donation_key_id=dk.id AND member.endpoint_key_id=dk.endpoint_key_id AND member.donation_id=d.id
 JOIN endpoint_keys k ON k.id=dk.endpoint_key_id JOIN endpoint_key_grant_revisions g ON g.id=k.current_grant_revision_id AND g.owner_user_id=d.user_id
 JOIN credential_versions v ON v.id=g.credential_version_id JOIN endpoint_key_secrets s ON s.id=v.secret_ref_id LEFT JOIN endpoint_key_discovery_sources m ON m.endpoint_key_id=k.id
 WHERE dk.id=? AND dk.ended_at IS NULL AND d.terminal_at IS NULL AND d.status IN('pending','approved')`, donationKeyID).Scan(&grant, &credential, &version, &publicVersion, &publicGrant, &mask, &head, &tail, &fingerprint, &hmac, &source)
	if err != nil {
		return nil, err
	}
	var sourceID any
	if shareDiscovery && !source.Valid {
		return nil, errors.New("fixture discovery sharing requires an exact source")
	}
	if shareDiscovery && source.Valid {
		sourceID = source.Int64
	}
	ids := []int64{}
	for bit := int64(1); bit <= 1<<18; bit <<= 1 {
		if mask&bit == 0 {
			continue
		}
		result, err := tx.ExecContext(ctx, "INSERT INTO donation_uses(donation_key_id,native_service_bit,created_at,updated_at) VALUES(?,?,?,?)", donationKeyID, bit, at, at)
		if err != nil {
			return nil, err
		}
		use, err := result.LastInsertId()
		if err != nil {
			return nil, err
		}
		state := "pending"
		var reviewed any
		if approved {
			state = "approved"
			reviewed = at
		}
		result, err = tx.ExecContext(ctx, `INSERT INTO donation_use_revisions(use_id,use_revision,grant_revision_id,discovery_source_revision_id,source_grant_revision_id,source_discovery_revision_id,credential_id_snapshot,credential_version,grant_revision,display_head,display_tail,report_fingerprint,key_body_review_hmac,origin,review_state,created_at,reviewed_at)
 VALUES(?,1,?,?,?,?,?,?,?,?,?,?,?,'legacy',?,?,?)`, use, grant, sourceID, grant, sourceID, credential, publicVersion, publicGrant, head, tail, fingerprint, hmac, state, at, reviewed)
		if err != nil {
			return nil, err
		}
		revision, err := result.LastInsertId()
		if err != nil {
			return nil, err
		}
		pointer := "current_pending_revision_id"
		if approved {
			pointer = "current_approved_revision_id"
		}
		if _, err := tx.ExecContext(ctx, "UPDATE donation_uses SET "+pointer+"=?,revision=revision+1 WHERE id=?", revision, use); err != nil {
			return nil, err
		}
		ids = append(ids, use)
	}
	return ids, nil
}

type BindingInput struct {
	ID, ModelID, KeyID int64
	UpstreamModelID    string
	NativeServices     connectorcontract.NativeServiceSet
	Order              int
	At                 int64
}
type BindingIdentity struct{ BindingID, TargetID int64 }

func InsertModelBindingTx(ctx context.Context, tx *sql.Tx, in BindingInput) (BindingIdentity, error) {
	return insertBinding(ctx, tx, in, false)
}
func InsertCharityBindingTx(ctx context.Context, tx *sql.Tx, in BindingInput) (BindingIdentity, error) {
	return insertBinding(ctx, tx, in, true)
}
func insertBinding(ctx context.Context, tx *sql.Tx, in BindingInput, charity bool) (BindingIdentity, error) {
	var out BindingIdentity
	root, parent, target, key := "model_bindings", "model_id", "model_binding_targets", "endpoint_key_id"
	mask := in.NativeServices.Mask()
	if charity {
		root, parent, target, key = "charity_model_bindings", "charity_model_id", "charity_binding_targets", "donation_key_id"
	}
	if mask == 0 {
		query := "SELECT e.native_service_mask FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id WHERE k.id=?"
		if charity {
			query = "SELECT COALESCE(sum(native_service_bit),0) FROM donation_uses WHERE donation_key_id=? AND (current_approved_revision_id IS NOT NULL OR current_pending_revision_id IS NOT NULL)"
		}
		if err := tx.QueryRowContext(ctx, query, in.KeyID).Scan(&mask); err != nil {
			return out, err
		}
	}
	var id any
	if in.ID > 0 {
		id = in.ID
	}
	result, err := tx.ExecContext(ctx, "INSERT INTO "+root+"(id,"+parent+",ord,created_at,updated_at) VALUES(?,?,?,?,?)", id, in.ModelID, in.Order, in.At, in.At)
	if err != nil {
		return out, err
	}
	out.BindingID, err = result.LastInsertId()
	if err != nil {
		return out, err
	}
	result, err = tx.ExecContext(ctx, "INSERT INTO "+target+"(binding_id,"+key+",upstream_model_id,native_service_mask,selection_origin,ord,created_at,updated_at) VALUES(?,?,?,?,'legacy',0,?,?)", out.BindingID, in.KeyID, in.UpstreamModelID, mask, in.At, in.At)
	if err != nil {
		return out, err
	}
	out.TargetID, err = result.LastInsertId()
	return out, err
}

func nextID(ctx context.Context, tx *sql.Tx, table string) (int64, error) {
	// Every table name originates in the fixed constructors above.
	var maximum int64
	if err := tx.QueryRowContext(ctx, "SELECT max(COALESCE((SELECT seq FROM sqlite_sequence WHERE name=?),0),COALESCE((SELECT max(id) FROM "+table+"),0))", table).Scan(&maximum); err != nil {
		return 0, err
	}
	if maximum < 0 || maximum == math.MaxInt64 {
		return 0, fmt.Errorf("fixture identity exhausted: %s", table)
	}
	return maximum + 1, nil
}
