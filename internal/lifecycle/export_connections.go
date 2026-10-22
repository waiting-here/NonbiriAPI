package lifecycle

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

// ConnectionExportCollections contains only public metadata, never secret or internal authorization IDs.
type ConnectionExportCollections struct {
	Connections           []ConnectionExport            `json:"connections"`
	Credentials           []CredentialExport            `json:"credentials"`
	CredentialVersions    []CredentialVersionExport     `json:"credential_versions"`
	Grants                []GrantExport                 `json:"grants"`
	DiscoverySources      []DiscoverySourceExport       `json:"discovery_sources"`
	SourceRevisions       []SourceRevisionExport        `json:"source_revisions"`
	BindingTargets        []BindingTargetExport         `json:"binding_targets"`
	NativeServiceEvidence []NativeServiceEvidenceExport `json:"native_service_evidence"`
	DonationUses          []DonationUseExport           `json:"donation_uses"`
}
type ConnectionExport struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Note      string `json:"note"`
	Revision  string `json:"revision"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}
type CredentialExport struct {
	ID                string  `json:"id"`
	ConnectionID      *string `json:"connection_id"`
	Note              string  `json:"note"`
	Enabled           bool    `json:"enabled"`
	Revision          string  `json:"revision"`
	CredentialVersion *string `json:"credential_version"`
	DeletedAt         *int64  `json:"deleted_at"`
	CreatedAt         int64   `json:"created_at"`
	UpdatedAt         int64   `json:"updated_at"`
}
type CredentialVersionExport struct {
	CredentialID      string `json:"credential_id"`
	CredentialVersion string `json:"credential_version"`
	DisplayHead       string `json:"display_head"`
	DisplayTail       string `json:"display_tail"`
	Enabled           bool   `json:"enabled"`
	Revision          string `json:"revision"`
	CreatedAt         int64  `json:"created_at"`
	RetiredAt         *int64 `json:"retired_at"`
}
type GrantExport struct {
	EndpointID        string   `json:"endpoint_id"`
	EndpointKeyID     string   `json:"endpoint_key_id"`
	CredentialID      string   `json:"credential_id"`
	CredentialVersion string   `json:"credential_version"`
	GrantRevision     string   `json:"grant_revision"`
	NativeServices    []string `json:"native_services"`
	RerankFormat      string   `json:"rerank_format"`
	Current           bool     `json:"current"`
	CreatedAt         int64    `json:"created_at"`
}
type DiscoverySourceExport struct {
	ID                      string  `json:"id"`
	ConnectionID            *string `json:"connection_id"`
	Name                    string  `json:"name"`
	Enabled                 bool    `json:"enabled"`
	Revision                string  `json:"revision"`
	DiscoverySourceRevision string  `json:"discovery_source_revision"`
	CredentialID            string  `json:"credential_id"`
	CredentialVersion       string  `json:"credential_version"`
	BaseURL                 string  `json:"base_url"`
	ListFormat              string  `json:"list_format"`
	ResultGeneration        string  `json:"result_generation"`
	State                   string  `json:"state"`
	SafeClass               string  `json:"safe_class"`
	ModelCount              string  `json:"model_count"`
	ObservedAt              *int64  `json:"observed_at"`
	CreatedAt               int64   `json:"created_at"`
	UpdatedAt               int64   `json:"updated_at"`
}
type SourceRevisionExport struct {
	DiscoverySourceID       string `json:"discovery_source_id"`
	DiscoverySourceRevision string `json:"discovery_source_revision"`
	CredentialID            string `json:"credential_id"`
	CredentialVersion       string `json:"credential_version"`
	BaseURL                 string `json:"base_url"`
	ListFormat              string `json:"list_format"`
	CreatedAt               int64  `json:"created_at"`
}
type BindingTargetExport struct {
	ID              string   `json:"id"`
	ModelID         string   `json:"model_id"`
	BindingID       string   `json:"binding_id"`
	EndpointKeyID   string   `json:"endpoint_key_id"`
	UpstreamModelID string   `json:"upstream_model_id"`
	NativeServices  []string `json:"native_services"`
	Order           int      `json:"ord"`
	Revision        string   `json:"revision"`
	CreatedAt       int64    `json:"created_at"`
	UpdatedAt       int64    `json:"updated_at"`
}
type NativeServiceEvidenceExport struct {
	CatalogEntryID    string   `json:"catalog_entry_id"`
	EndpointKeyID     *string  `json:"endpoint_key_id"`
	DiscoverySourceID *string  `json:"discovery_source_id"`
	ResultGeneration  *string  `json:"result_generation"`
	UpstreamModelID   string   `json:"upstream_model_id"`
	NativeServices    []string `json:"native_services"`
	Source            string   `json:"source"`
	Revision          *string  `json:"revision"`
}
type DonationUseExport struct {
	ID               string                      `json:"id"`
	DonationID       string                      `json:"donation_id"`
	DonationKeyID    string                      `json:"donation_key_id"`
	NativeServices   []string                    `json:"native_services"`
	Enabled          bool                        `json:"enabled"`
	DiscoveryEnabled bool                        `json:"discovery_enabled"`
	Revision         string                      `json:"revision"`
	ApprovedRevision *string                     `json:"approved_revision"`
	PendingRevision  *string                     `json:"pending_revision"`
	CreatedAt        int64                       `json:"created_at"`
	UpdatedAt        int64                       `json:"updated_at"`
	Revisions        []DonationUseRevisionExport `json:"revisions"`
	Health           []DonationUseHealthExport   `json:"health"`
}
type DonationUseHealthExport struct {
	CredentialID      string  `json:"credential_id"`
	CredentialVersion string  `json:"credential_version"`
	FailureStreak     string  `json:"failure_streak"`
	FailureDisabled   bool    `json:"failure_disabled"`
	RetryAfterUntil   *int64  `json:"retry_after_until"`
	BackoffRetryAt    *int64  `json:"backoff_retry_at"`
	BackoffPeriod     *string `json:"backoff_period"`
	BackoffSource     *string `json:"backoff_source"`
	BackoffStage      int     `json:"backoff_stage"`
	UpdatedAt         int64   `json:"updated_at"`
}

type DonationUseRevisionExport struct {
	Revision                string  `json:"revision"`
	CredentialID            string  `json:"credential_id"`
	CredentialVersion       string  `json:"credential_version"`
	GrantRevision           string  `json:"grant_revision"`
	DisplayHead             string  `json:"display_head"`
	DisplayTail             string  `json:"display_tail"`
	DiscoverySourceID       *string `json:"discovery_source_id"`
	DiscoverySourceRevision *string `json:"discovery_source_revision"`
	Origin                  string  `json:"origin"`
	ReviewState             string  `json:"review_state"`
	ReviewReason            string  `json:"review_reason"`
	ReviewedAt              *int64  `json:"reviewed_at"`
	CreatedAt               int64   `json:"created_at"`
}

func exportConnections(ctx context.Context, tx *sql.Tx, request ExportRequest) (ConnectionExportCollections, error) {
	var out ConnectionExportCollections
	var err error
	out.Connections, err = readConnectionExportRows(ctx, tx, request, `SELECT CAST(id AS TEXT),name,note,CAST(revision AS TEXT),created_at,updated_at FROM service_connections WHERE user_id=? ORDER BY id LIMIT ?`, func(rows *sql.Rows) (ConnectionExport, error) {
		var item ConnectionExport
		if err := rows.Scan(&item.ID, &item.Name, &item.Note, &item.Revision, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return item, err
		}
		return item, nil
	})
	if err != nil {
		return out, err
	}
	out.Credentials, err = readConnectionExportRows(ctx, tx, request, `SELECT CAST(c.id AS TEXT),CAST(c.connection_id AS TEXT),c.note,c.enabled,CAST(c.revision AS TEXT),CAST(v.version AS TEXT),c.created_at,c.updated_at,c.deleted_at FROM credentials c LEFT JOIN credential_versions v ON v.id=c.current_version_id WHERE c.user_id=? ORDER BY c.id LIMIT ?`, func(rows *sql.Rows) (CredentialExport, error) {
		var item CredentialExport
		if err := rows.Scan(&item.ID, &item.ConnectionID, &item.Note, &item.Enabled, &item.Revision, &item.CredentialVersion, &item.CreatedAt, &item.UpdatedAt, &item.DeletedAt); err != nil {
			return item, err
		}
		return item, nil
	})
	if err != nil {
		return out, err
	}
	out.CredentialVersions, err = readConnectionExportRows(ctx, tx, request, `SELECT CAST(c.id AS TEXT),CAST(v.version AS TEXT),v.display_head,v.display_tail,v.enabled,CAST(v.revision AS TEXT),v.created_at,v.retired_at FROM credential_versions v JOIN credentials c ON c.id=v.credential_id WHERE c.user_id=? ORDER BY c.id,v.version LIMIT ?`, func(rows *sql.Rows) (CredentialVersionExport, error) {
		var item CredentialVersionExport
		if err := rows.Scan(&item.CredentialID, &item.CredentialVersion, &item.DisplayHead, &item.DisplayTail, &item.Enabled, &item.Revision, &item.CreatedAt, &item.RetiredAt); err != nil {
			return item, err
		}
		return item, nil
	})
	if err != nil {
		return out, err
	}
	out.Grants, err = readConnectionExportRows(ctx, tx, request, `SELECT CAST(g.endpoint_id AS TEXT),CAST(g.endpoint_key_id AS TEXT),CAST(g.credential_id AS TEXT),CAST(v.version AS TEXT),CAST(g.grant_revision AS TEXT),g.native_service_mask,g.rerank_format,g.id=k.current_grant_revision_id,g.created_at FROM endpoint_key_grant_revisions g JOIN endpoint_keys k ON k.id=g.endpoint_key_id JOIN credential_versions v ON v.id=g.credential_version_id WHERE g.owner_user_id=? ORDER BY g.endpoint_key_id,g.grant_revision LIMIT ?`, func(rows *sql.Rows) (GrantExport, error) {
		var item GrantExport
		var mask int64
		if err := rows.Scan(&item.EndpointID, &item.EndpointKeyID, &item.CredentialID, &item.CredentialVersion, &item.GrantRevision, &mask, &item.RerankFormat, &item.Current, &item.CreatedAt); err != nil {
			return item, err
		}
		item.NativeServices, err = exportNativeServices(mask)
		if err != nil {
			return item, err
		}
		return item, nil
	})
	if err != nil {
		return out, err
	}
	out.DiscoverySources, err = readConnectionExportRows(ctx, tx, request, `SELECT CAST(s.id AS TEXT),CAST(s.connection_id AS TEXT),s.name,s.enabled,CAST(s.revision AS TEXT),CAST(sr.source_revision AS TEXT),CAST(sr.credential_id AS TEXT),CAST(v.version AS TEXT),sr.canonical_base_url,sr.list_format,CAST(st.result_generation AS TEXT),COALESCE(r.state,'unknown'),COALESCE(r.safe_class,'none'),CAST(COALESCE(r.model_count,0) AS TEXT),r.completed_at,s.created_at,s.updated_at FROM discovery_sources s JOIN discovery_source_revisions sr ON sr.id=s.current_revision_id JOIN credential_versions v ON v.id=sr.credential_version_id JOIN discovery_source_states st ON st.source_revision_id=sr.id LEFT JOIN discovery_runs r ON r.id=st.current_run_id WHERE s.user_id=? ORDER BY s.id LIMIT ?`, func(rows *sql.Rows) (DiscoverySourceExport, error) {
		var item DiscoverySourceExport
		if err := rows.Scan(&item.ID, &item.ConnectionID, &item.Name, &item.Enabled, &item.Revision, &item.DiscoverySourceRevision, &item.CredentialID, &item.CredentialVersion, &item.BaseURL, &item.ListFormat, &item.ResultGeneration, &item.State, &item.SafeClass, &item.ModelCount, &item.ObservedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return item, err
		}
		return item, nil
	})
	if err != nil {
		return out, err
	}
	out.SourceRevisions, err = readConnectionExportRows(ctx, tx, request, `SELECT CAST(sr.source_id AS TEXT),CAST(sr.source_revision AS TEXT),CAST(sr.credential_id AS TEXT),CAST(v.version AS TEXT),sr.canonical_base_url,sr.list_format,sr.created_at FROM discovery_source_revisions sr JOIN credential_versions v ON v.id=sr.credential_version_id WHERE sr.owner_user_id=? ORDER BY sr.source_id,sr.source_revision LIMIT ?`, func(rows *sql.Rows) (SourceRevisionExport, error) {
		var item SourceRevisionExport
		if err := rows.Scan(&item.DiscoverySourceID, &item.DiscoverySourceRevision, &item.CredentialID, &item.CredentialVersion, &item.BaseURL, &item.ListFormat, &item.CreatedAt); err != nil {
			return item, err
		}
		return item, nil
	})
	if err != nil {
		return out, err
	}
	out.BindingTargets, err = readConnectionExportRows(ctx, tx, request, `SELECT CAST(t.id AS TEXT),CAST(b.model_id AS TEXT),CAST(t.binding_id AS TEXT),CAST(t.endpoint_key_id AS TEXT),t.upstream_model_id,t.native_service_mask,t.ord,CAST(t.revision AS TEXT),t.created_at,t.updated_at FROM model_binding_targets t JOIN model_bindings b ON b.id=t.binding_id JOIN models m ON m.id=b.model_id WHERE m.user_id=? ORDER BY m.id,b.ord,t.ord LIMIT ?`, func(rows *sql.Rows) (BindingTargetExport, error) {
		var item BindingTargetExport
		var mask int64
		if err := rows.Scan(&item.ID, &item.ModelID, &item.BindingID, &item.EndpointKeyID, &item.UpstreamModelID, &mask, &item.Order, &item.Revision, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return item, err
		}
		item.NativeServices, err = exportNativeServices(mask)
		if err != nil {
			return item, err
		}
		return item, nil
	})
	if err != nil {
		return out, err
	}
	out.NativeServiceEvidence, err = readConnectionExportRows(ctx, tx, request, `SELECT CAST(c.id AS TEXT),CAST(c.endpoint_key_id AS TEXT),CAST(sr.source_id AS TEXT),CAST(r.result_generation AS TEXT),c.normalized_model_id,c.native_service_mask,CASE c.evidence_source WHEN 'metadata' THEN 'provider' WHEN 'user' THEN 'selected' ELSE c.evidence_source END,CAST(c.evidence_revision AS TEXT) FROM model_catalog_entries c LEFT JOIN endpoint_keys k ON k.id=c.endpoint_key_id LEFT JOIN endpoints e ON e.id=k.endpoint_id LEFT JOIN discovery_runs r ON r.id=c.discovery_run_id LEFT JOIN discovery_source_revisions sr ON sr.id=r.source_revision_id WHERE COALESCE(e.user_id,sr.owner_user_id)=? ORDER BY c.id LIMIT ?`, func(rows *sql.Rows) (NativeServiceEvidenceExport, error) {
		var item NativeServiceEvidenceExport
		var mask int64
		if err := rows.Scan(&item.CatalogEntryID, &item.EndpointKeyID, &item.DiscoverySourceID, &item.ResultGeneration, &item.UpstreamModelID, &mask, &item.Source, &item.Revision); err != nil {
			return item, err
		}
		item.NativeServices, err = exportNativeServices(mask)
		if err != nil {
			return item, err
		}
		return item, nil
	})
	if err != nil {
		return out, err
	}
	out.DonationUses, err = readConnectionExportRows(ctx, tx, request, `SELECT CAST(u.id AS TEXT),CAST(dk.donation_id AS TEXT),CAST(dk.id AS TEXT),u.native_service_bit,u.enabled,u.discovery_enabled,CAST(u.revision AS TEXT),CAST(a.use_revision AS TEXT),CAST(p.use_revision AS TEXT),u.created_at,u.updated_at FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id JOIN donations d ON d.id=dk.donation_id LEFT JOIN donation_use_revisions a ON a.id=u.current_approved_revision_id LEFT JOIN donation_use_revisions p ON p.id=u.current_pending_revision_id WHERE d.user_id=? ORDER BY dk.donation_id,dk.id,u.native_service_bit LIMIT ?`, func(rows *sql.Rows) (DonationUseExport, error) {
		var item DonationUseExport
		var mask int64
		if err := rows.Scan(&item.ID, &item.DonationID, &item.DonationKeyID, &mask, &item.Enabled, &item.DiscoveryEnabled, &item.Revision, &item.ApprovedRevision, &item.PendingRevision, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return item, err
		}
		item.NativeServices, err = exportNativeServices(mask)
		if err != nil {
			return item, err
		}
		item.Revisions = []DonationUseRevisionExport{}
		item.Health = []DonationUseHealthExport{}
		return item, nil
	})
	if err != nil {
		return out, err
	}
	if err := exportUseRevisions(ctx, tx, request, &out); err != nil {
		return out, err
	}
	if err := exportUseHealth(ctx, tx, request, &out); err != nil {
		return out, err
	}
	return out, nil
}
func readConnectionExportRows[T any](ctx context.Context, tx *sql.Tx, request ExportRequest, query string, scan func(*sql.Rows) (T, error)) ([]T, error) {
	rows, err := tx.QueryContext(ctx, query, request.UserID, request.Limit+1)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: read connection metadata: %w", err)
	}
	result := []T{}
	for rows.Next() {
		if len(result) >= request.Limit {
			rows.Close()
			return nil, ErrTooLarge
		}
		item, err := scan(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		result = append(result, item)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	return result, nil
}
func exportNativeServices(mask int64) ([]string, error) {
	services, err := connectorcontract.NativeServicesFromMask(mask)
	if err != nil {
		return nil, ErrUnavailable
	}
	result := []string{}
	for _, service := range services.Services() {
		result = append(result, string(service))
	}
	return result, nil
}
func exportUseRevisions(ctx context.Context, tx *sql.Tx, request ExportRequest, out *ConnectionExportCollections) error {
	positions := map[string]int{}
	for i, item := range out.DonationUses {
		positions[item.ID] = i
	}
	rows, err := tx.QueryContext(ctx, `SELECT CAST(ur.use_id AS TEXT),CAST(ur.use_revision AS TEXT),CAST(ur.credential_id_snapshot AS TEXT),CAST(ur.credential_version AS TEXT),CAST(ur.grant_revision AS TEXT),ur.display_head,ur.display_tail,CAST(sr.source_id AS TEXT),CAST(sr.source_revision AS TEXT),ur.origin,CASE ur.review_state WHEN 'cancelled' THEN 'withdrawn' ELSE ur.review_state END,ur.review_reason,ur.reviewed_at,ur.created_at
 FROM donation_use_revisions ur JOIN donation_uses u ON u.id=ur.use_id JOIN donation_keys dk ON dk.id=u.donation_key_id JOIN donations d ON d.id=dk.donation_id LEFT JOIN discovery_source_revisions sr ON sr.id=ur.discovery_source_revision_id
 WHERE d.user_id=? ORDER BY ur.use_id,ur.use_revision LIMIT ?`, request.UserID, request.Limit+1)
	if err != nil {
		return err
	}
	count := len(out.DonationUses)
	for rows.Next() {
		count++
		if count > request.Limit {
			rows.Close()
			return ErrTooLarge
		}
		var id string
		var item DonationUseRevisionExport
		if err := rows.Scan(&id, &item.Revision, &item.CredentialID, &item.CredentialVersion, &item.GrantRevision, &item.DisplayHead, &item.DisplayTail, &item.DiscoverySourceID, &item.DiscoverySourceRevision, &item.Origin, &item.ReviewState, &item.ReviewReason, &item.ReviewedAt, &item.CreatedAt); err != nil {
			rows.Close()
			return err
		}
		index, ok := positions[id]
		if !ok {
			rows.Close()
			return ErrUnavailable
		}
		out.DonationUses[index].Revisions = append(out.DonationUses[index].Revisions, item)
	}
	return errors.Join(rows.Err(), rows.Close())
}

func exportUseHealth(ctx context.Context, tx *sql.Tx, request ExportRequest, out *ConnectionExportCollections) error {
	positions := map[string]int{}
	count := len(out.DonationUses)
	for i, item := range out.DonationUses {
		positions[item.ID] = i
		count += len(item.Revisions)
	}
	rows, err := tx.QueryContext(ctx, `SELECT CAST(h.use_id AS TEXT),CAST(v.credential_id AS TEXT),CAST(v.version AS TEXT),h.failure_streak,h.failure_disabled,h.retry_after_until,h.backoff_retry_at,h.backoff_period,h.backoff_source,h.backoff_stage,h.updated_at
 FROM donation_service_health h JOIN donation_uses u ON u.id=h.use_id JOIN donation_keys dk ON dk.id=u.donation_key_id JOIN donations d ON d.id=dk.donation_id JOIN credential_versions v ON v.id=h.credential_version_id
 WHERE d.user_id=? ORDER BY h.use_id,v.credential_id,v.version,h.id LIMIT ?`, request.UserID, request.Limit+1)
	if err != nil {
		return err
	}
	for rows.Next() {
		count++
		if count > request.Limit {
			rows.Close()
			return ErrTooLarge
		}
		var id string
		var item DonationUseHealthExport
		var streak []byte
		if err := rows.Scan(&id, &item.CredentialID, &item.CredentialVersion, &streak, &item.FailureDisabled, &item.RetryAfterUntil, &item.BackoffRetryAt, &item.BackoffPeriod, &item.BackoffSource, &item.BackoffStage, &item.UpdatedAt); err != nil {
			rows.Close()
			return err
		}
		value, err := db.DecodeU128(streak)
		if err != nil {
			rows.Close()
			return ErrUnavailable
		}
		item.FailureStreak = value.Decimal()
		index, ok := positions[id]
		if !ok {
			rows.Close()
			return ErrUnavailable
		}
		out.DonationUses[index].Health = append(out.DonationUses[index].Health, item)
	}
	return errors.Join(rows.Err(), rows.Close())
}
