package donation

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
)

// PendingReviewUse pins one member of the complete pending submission.
type PendingReviewUse struct {
	UseID                  string  `json:"use_id"`
	ExpectedRevision       string  `json:"expected_revision"`
	ExpectedReviewRevision *string `json:"expected_review_revision"`
}
type PendingReviewInput struct {
	ExpectedRevision string             `json:"expected_revision"`
	Decision         string             `json:"decision"`
	Reason           string             `json:"reason"`
	Uses             []PendingReviewUse `json:"uses"`
}
type agentPendingVersion struct {
	PendingReviewUse
	PendingID                 string  `json:"pending_id"`
	GrantID                   string  `json:"grant_id"`
	CredentialVersion         string  `json:"credential_version"`
	DiscoverySourceRevisionID *string `json:"discovery_source_revision_id"`
	EffectiveSourceRevisionID string  `json:"effective_source_revision_id"`
	CatalogGeneration         string  `json:"catalog_generation"`
}
type agentPendingSnapshot struct {
	DonationID string                `json:"donation_id"`
	Revision   string                `json:"revision"`
	Status     string                `json:"status"`
	Pending    []agentPendingVersion `json:"pending"`
}

func agentPendingTx(ctx context.Context, tx *sql.Tx, id int64) (agentPendingSnapshot, string, error) {
	out := agentPendingSnapshot{DonationID: strconv.FormatInt(id, 10), Pending: []agentPendingVersion{}}
	var revision int64
	if ctx == nil || tx == nil || id <= 0 {
		return out, "", ErrInvalidRequest
	}
	if err := tx.QueryRowContext(ctx, `SELECT revision,status FROM donations WHERE id=? AND terminal_at IS NULL`, id).Scan(&revision, &out.Status); errors.Is(err, sql.ErrNoRows) {
		return out, "", ErrNotFound
	} else if err != nil {
		return out, "", err
	}
	out.Revision = strconv.FormatInt(revision, 10)
	rows, err := tx.QueryContext(ctx, `SELECT u.id,u.revision,ur.id,ur.grant_revision_id,ur.credential_version,ur.discovery_source_revision_id,rr.revision,`+connectionstore.DiscoverySourceRevisionSQL+`,COALESCE(st.result_generation,0)
 FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id JOIN donation_use_revisions ur ON ur.id=u.current_pending_revision_id AND ur.use_id=u.id
 LEFT JOIN donation_key_review_requirements rr ON rr.hmac=ur.key_body_review_hmac
 LEFT JOIN endpoint_grant_authorizations a ON a.grant_revision_id=ur.grant_revision_id LEFT JOIN discovery_source_states st ON st.source_revision_id=`+connectionstore.DiscoverySourceRevisionSQL+`
 WHERE dk.donation_id=? AND ur.review_state='pending' ORDER BY u.id`, id)
	if err != nil {
		return out, "", err
	}
	for rows.Next() {
		var v agentPendingVersion
		var use, rev, pending, grant, credential, effective, generation int64
		var source, review sql.NullInt64
		if err = rows.Scan(&use, &rev, &pending, &grant, &credential, &source, &review, &effective, &generation); err != nil {
			rows.Close()
			return out, "", err
		}
		v.UseID = strconv.FormatInt(use, 10)
		v.ExpectedRevision = strconv.FormatInt(rev, 10)
		v.ExpectedReviewRevision = decimalPointer(review)
		v.PendingID = strconv.FormatInt(pending, 10)
		v.GrantID = strconv.FormatInt(grant, 10)
		v.CredentialVersion = strconv.FormatInt(credential, 10)
		v.DiscoverySourceRevisionID = decimalPointer(source)
		v.EffectiveSourceRevisionID = strconv.FormatInt(effective, 10)
		v.CatalogGeneration = strconv.FormatInt(generation, 10)
		out.Pending = append(out.Pending, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, "", err
	}
	if len(out.Pending) == 0 {
		return out, "", ErrConflict
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return out, "", err
	}
	sum := sha256.Sum256(encoded)
	return out, hex.EncodeToString(sum[:]), nil
}

// AgentIntelligence is a server-owned automatic-review read. Its caller must
// authorize the configured administrator before exposing this safe projection.
func (s *Service) AgentIntelligence(ctx context.Context, tx *sql.Tx, donationID int64) (json.RawMessage, string, error) {
	snapshot, signature, err := agentPendingTx(ctx, tx, donationID)
	if err != nil {
		return nil, "", err
	}
	now, err := s.nowUnix()
	if err != nil {
		return nil, "", err
	}
	header, err := getAdminDonationTx(ctx, tx, donationID, now)
	if err != nil {
		return nil, "", err
	}
	// Never include donor identity or even partial credential display material in
	// model context. Source URLs are the same safe facts as management pages.
	header.Owner = nil
	header.Reviewer = nil
	header.DiscordPublicThanks = nil
	for i := range header.Keys {
		header.Keys[i].DisplayHead = ""
		header.Keys[i].DisplayTail = ""
		header.Keys[i].EndpointKeyID = nil
		header.Keys[i].OwnerEndpointID = nil
	}
	uses := []Use{}
	for start := 0; start < len(snapshot.Pending); start += 100 {
		end := start + 100
		if end > len(snapshot.Pending) {
			end = len(snapshot.Pending)
		}
		ids := []int64{}
		for _, v := range snapshot.Pending[start:end] {
			id, _ := strconv.ParseInt(v.UseID, 10, 64)
			ids = append(ids, id)
		}
		values, err := readUsesTx(ctx, tx, useAccess{role: reviewerAdmin}, donationID, ids, now)
		if err != nil {
			return nil, "", err
		}
		for i := range values {
			values[i].EndpointKeyID = nil
			for _, r := range []*UseRevision{values[i].Pending, values[i].Approved, values[i].Latest} {
				if r != nil {
					r.DisplayHead = ""
					r.DisplayTail = ""
				}
			}
		}
		uses = append(uses, values...)
	}
	type catalogModel struct {
		KeyID  string `json:"key_id"`
		Model  string `json:"model"`
		Name   string `json:"name"`
		Source string `json:"source"`
	}
	models := []catalogModel{}
	// Bound catalog data to this donation's manual entries and pinned pending
	// discovery runs; never read credentials or unrelated catalog history.
	rows, err := tx.QueryContext(ctx, `SELECT CAST(dk.id AS TEXT),m.normalized_model_id,m.display_name,'manual' FROM donation_key_manual_models m JOIN donation_keys dk ON dk.id=m.donation_key_id WHERE dk.donation_id=?
 UNION SELECT CAST(dk.id AS TEXT),c.normalized_model_id,c.normalized_model_id,'automatic' FROM donation_keys dk JOIN donation_uses u ON u.donation_key_id=dk.id JOIN donation_use_revisions ur ON ur.id=u.current_pending_revision_id JOIN endpoint_grant_authorizations a ON a.grant_revision_id=ur.grant_revision_id JOIN discovery_source_states st ON st.source_revision_id=`+connectionstore.DiscoverySourceRevisionSQL+` JOIN model_catalog_entries c ON c.discovery_run_id=st.current_run_id AND c.source_type='automatic' WHERE dk.donation_id=? ORDER BY 1,2,4`, donationID, donationID)
	if err != nil {
		return nil, "", err
	}
	for rows.Next() {
		var v catalogModel
		if err = rows.Scan(&v.KeyID, &v.Model, &v.Name, &v.Source); err != nil {
			rows.Close()
			return nil, "", err
		}
		models = append(models, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, "", err
	}
	data, err := json.Marshal(struct {
		Donation  AdminDonation        `json:"donation"`
		Version   agentPendingSnapshot `json:"version"`
		Uses      []Use                `json:"uses"`
		Models    []catalogModel       `json:"models"`
		Signature string               `json:"signature"`
	}{header, snapshot, uses, models, signature})
	return data, signature, err
}

func (s *Service) AgentReviewOperations(ctx context.Context, tx *sql.Tx, donationID int64, signature, decision, note string) ([]agentplan.Operation, error) {
	snap, current, err := agentPendingTx(ctx, tx, donationID)
	if err != nil {
		return nil, err
	}
	if signature == "" || signature != current {
		return nil, ErrConflict
	}
	if decision == "manual" {
		return []agentplan.Operation{}, nil
	}
	if decision != "approve" && decision != "reject" || !validReviewReason(note) {
		return nil, ErrInvalidRequest
	}
	raw, err := json.Marshal(agentReviewWire{ExpectedRevision: snap.Revision, Decision: decision, Reason: note, Signature: signature})
	if err != nil {
		return nil, err
	}
	return []agentplan.Operation{{Kind: "donation.review", TargetID: snap.DonationID, Input: raw}}, nil
}
