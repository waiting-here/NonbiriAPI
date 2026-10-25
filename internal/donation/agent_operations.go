package donation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
)

type agentReviewWire struct {
	ExpectedRevision string `json:"expected_revision"`
	Decision         string `json:"decision"`
	Reason           string `json:"reason"`
	Signature        string `json:"signature,omitempty"`
}
type agentKeyWire struct {
	keyManagementWire
	KeyID string `json:"key_id"`
}
type agentRefWire struct {
	KeyID            string `json:"key_id,omitempty"`
	UseID            string `json:"use_id,omitempty"`
	ExpectedRevision string `json:"expected_revision"`
	Threshold        string `json:"threshold,omitempty"`
}
type agentDonationInput struct {
	id, keyID, useID, expected int64
	review                     agentReviewWire
	key                        KeyManagementInput
	ref                        agentRefWire
}

func parseAgentOperation(op agentplan.Operation) (agentDonationInput, error) {
	var in agentDonationInput
	var err error
	in.id, err = parseID(op.TargetID)
	if err != nil {
		return in, err
	}
	switch op.Kind {
	case "donation.review":
		err = agentDecode(op.Input, &in.review)
		if err == nil {
			in.expected, err = parseID(in.review.ExpectedRevision)
		}
		if !validReviewReason(in.review.Reason) || in.review.Decision != "approve" && in.review.Decision != "reject" && in.review.Decision != "force_reject" {
			err = ErrInvalidRequest
		}
		if in.review.Decision != "force_reject" && len(in.review.Signature) != 64 {
			err = ErrInvalidRequest
		}
		if in.review.Decision == "force_reject" && !validReviewInput(ReviewInput{ExpectedRevision: in.expected, Decision: in.review.Decision, Reason: in.review.Reason}) {
			err = ErrInvalidRequest
		}
	case "donation.key.update":
		var wire agentKeyWire
		err = agentDecode(op.Input, &wire)
		if err == nil {
			in.key, _, err = parseKeyManagementWire(wire.keyManagementWire)
		}
		if err == nil {
			in.keyID, err = parseID(wire.KeyID)
		}
		in.expected = in.key.ExpectedRevision
		if !validKeyManagement(in.key) {
			err = ErrInvalidRequest
		}
	case "donation.handle", "donation.key.failure_policy", "donation.key.reset", "donation.use.reset":
		err = agentDecode(op.Input, &in.ref)
		if err == nil {
			in.expected, err = parseID(in.ref.ExpectedRevision)
		}
		if op.Kind == "donation.use.reset" {
			if err == nil {
				in.useID, err = parseID(in.ref.UseID)
			}
			if in.ref.KeyID != "" || in.ref.Threshold != "" {
				err = ErrInvalidRequest
			}
		} else if op.Kind != "donation.handle" {
			if err == nil {
				in.keyID, err = parseID(in.ref.KeyID)
			}
			if in.ref.UseID != "" {
				err = ErrInvalidRequest
			}
		} else if in.ref.KeyID != "" || in.ref.UseID != "" || in.ref.Threshold != "" {
			err = ErrInvalidRequest
		}
		if op.Kind == "donation.key.reset" && in.ref.Threshold != "" {
			err = ErrInvalidRequest
		}
	default:
		err = ErrInvalidRequest
	}
	return in, err
}

type agentAuthority struct {
	actor     agentplan.Actor
	kind      string
	authorize agentplan.Authorize
}

func (a agentAuthority) check(ctx context.Context, tx *sql.Tx, id int64, admin bool) error {
	if id != a.actor.ID || (a.actor.Role == "admin") != admin {
		return ErrForbidden
	}
	return a.authorize(ctx, tx, a.actor, a.kind)
}
func (a agentAuthority) AuthorizeAdminMutation(ctx context.Context, tx *sql.Tx, id int64) error {
	return a.check(ctx, tx, id, true)
}
func (a agentAuthority) AuthorizeStewardMutation(ctx context.Context, tx *sql.Tx, id int64) error {
	return a.check(ctx, tx, id, false)
}

func agentAccess(ctx context.Context, tx *sql.Tx, actor agentplan.Actor, op agentplan.Operation, authorize agentplan.Authorize) (useAccess, error) {
	a := useAccess{actorID: actor.ID, role: reviewerSteward, scope: charityscope.Scope{ActorID: actor.ID, DonationScope: authz.DonationScopeFull}}
	if ctx == nil || tx == nil || authorize == nil || actor.ID <= 0 {
		return a, ErrInvalidRequest
	}
	if actor.Automatic && (actor.Role != "admin" || op.Kind != "donation.review" && op.Kind != "donations" && op.Kind != "donation_models" && op.Kind != "review_history") {
		return a, ErrForbidden
	}
	if err := authorize(ctx, tx, actor, op.Kind); err != nil {
		return a, err
	}
	var admin, banned bool
	var level int
	if err := tx.QueryRowContext(ctx, `SELECT is_admin,is_banned,COALESCE(level,auto_level) FROM users WHERE id=?`, actor.ID).Scan(&admin, &banned, &level); errors.Is(err, sql.ErrNoRows) {
		return a, ErrUnauthorized
	} else if err != nil {
		return a, err
	}
	if banned {
		return a, ErrForbidden
	}
	if actor.Role == "admin" {
		if !admin {
			return a, ErrForbidden
		}
		a.role = reviewerAdmin
		return a, nil
	}
	if admin || level != 5 && level != 6 || actor.Role != "level"+strconv.Itoa(level) {
		return a, ErrForbidden
	}
	if level == 5 {
		a.role = reviewerGranted
	}
	p, err := authz.ReadStewardPermissions(ctx, tx, actor.ID)
	if err != nil {
		return a, err
	}
	a.scope.ActorLevel = level
	a.scope.Trainee = !p.Permissions["charity.non_mainstream"]
	a.scope.Redact = !p.Permissions["donations.read_all"]
	a.scope.DonationScope = p.DonationScope
	grant := "donations.manage_keys"
	if op.Kind == "donation.review" {
		grant = "donations.review"
	}
	if op.Kind == "donation.handle" {
		grant = "donations.handle"
	}
	if op.Kind == "donations" || op.Kind == "donation_models" || op.Kind == "review_history" {
		grant = "donations.browse"
	}
	if err = authz.RequireStewardPermission(ctx, tx, actor.ID, grant); err != nil {
		return a, err
	}
	if p.DonationScope == authz.DonationScopeNone {
		return a, authz.ErrPermissionDenied
	}
	return a, nil
}

func (s *Service) agentValidateTx(ctx context.Context, tx *sql.Tx, access useAccess, in agentDonationInput, op agentplan.Operation, now int64) error {
	if err := requireManagedDonationTx(ctx, tx, access.role, in.id, now); err != nil {
		return err
	}
	if err := access.scope.RequireDonation(ctx, tx, in.id, now); err != nil {
		return scopeError(err)
	}
	if op.Kind == "donation.review" {
		if in.review.Decision == "force_reject" {
			if access.role != reviewerAdmin {
				if err := authz.RequireStewardPermission(ctx, tx, access.actorID, "donations.force_reject"); err != nil {
					return err
				}
			}
		} else {
			snap, sig, err := agentPendingTx(ctx, tx, in.id)
			if err != nil {
				return err
			}
			if sig != in.review.Signature || snap.Revision != in.review.ExpectedRevision {
				return ErrConflict
			}
		}
	}
	if in.useID > 0 {
		if err := requireUseAccessTx(ctx, tx, access, in.id, in.useID, now, true); err != nil {
			return err
		}
		var rev int64
		if err := tx.QueryRowContext(ctx, `SELECT revision FROM donation_uses WHERE id=?`, in.useID).Scan(&rev); err != nil {
			return err
		}
		if rev != in.expected {
			return ErrConflict
		}
		return nil
	}
	if op.Kind == "donation.handle" {
		h, err := readHandlingTx(ctx, tx, in.id)
		if err != nil {
			return err
		}
		if h.Revision != in.ref.ExpectedRevision || h.State != "pending" {
			return ErrConflict
		}
		return nil
	}
	var rev int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM donations WHERE id=?`, in.id).Scan(&rev); err != nil {
		return err
	}
	if rev != in.expected {
		return ErrConflict
	}
	if in.keyID > 0 {
		if err := access.scope.RequireKey(ctx, tx, in.id, in.keyID, now, false); err != nil {
			return scopeError(err)
		}
		state, err := readResetKeyTx(ctx, tx, in.id, in.keyID, now)
		if err != nil {
			return err
		}
		if !state.eligible {
			return ErrConflict
		}
	}
	return nil
}

func (s *Service) AgentPreview(ctx context.Context, tx *sql.Tx, actor agentplan.Actor, op agentplan.Operation, authorize agentplan.Authorize) (agentplan.Preview, error) {
	var out agentplan.Preview
	if s == nil {
		return out, ErrUnavailable
	}
	in, err := parseAgentOperation(op)
	if err != nil {
		return out, err
	}
	access, err := agentAccess(ctx, tx, actor, op, authorize)
	if err != nil {
		return out, err
	}
	now, err := s.nowUnix()
	if err != nil {
		return out, err
	}
	if err = s.agentValidateTx(ctx, tx, access, in, op, now); err != nil {
		return out, err
	}
	before, err := s.agentReceiptTx(ctx, tx, access, in.id, now)
	if err != nil {
		return out, err
	}
	return agentplan.Preview{Title: op.Kind, Description: "Apply this donation action to the pinned resource version.", Before: string(before), After: string(op.Input), Dangerous: op.Kind == "donation.review" && in.review.Decision != "approve"}, nil
}

func (s *Service) AgentApply(ctx context.Context, tx *sql.Tx, actor agentplan.Actor, op agentplan.Operation, authorize agentplan.Authorize) (json.RawMessage, error) {
	if s == nil {
		return nil, ErrUnavailable
	}
	in, err := parseAgentOperation(op)
	if err != nil {
		return nil, err
	}
	access, err := agentAccess(ctx, tx, actor, op, authorize)
	if err != nil {
		return nil, err
	}
	if actor.Automatic && in.review.Decision == "force_reject" {
		return nil, ErrForbidden
	}
	now, err := s.nowUnix()
	if err != nil {
		return nil, err
	}
	if err = s.agentValidateTx(ctx, tx, access, in, op, now); err != nil {
		return nil, err
	}
	local := *s
	local.roleAuth = agentAuthority{actor, op.Kind, authorize}
	audit := string(access.role)
	switch op.Kind {
	case "donation.review":
		if in.review.Decision == "force_reject" {
			err = local.reviewDonationTx(ctx, tx, in.id, actor.ID, audit, ReviewInput{ExpectedRevision: in.expected, Decision: in.review.Decision, Reason: in.review.Reason}, now)
		} else {
			snap, _, e := agentPendingTx(ctx, tx, in.id)
			if e != nil {
				return nil, e
			}
			input := PendingReviewInput{ExpectedRevision: snap.Revision, Decision: in.review.Decision, Reason: in.review.Reason, Uses: []PendingReviewUse{}}
			for _, v := range snap.Pending {
				input.Uses = append(input.Uses, v.PendingReviewUse)
			}
			err = local.reviewPendingSubmissionTx(ctx, tx, access, in.id, input, now)
		}
	case "donation.key.update":
		expiry, e := materializeDonationExpiryStateTx(ctx, tx, in.id, now)
		if e != nil {
			return nil, e
		}
		if expiry.changed {
			return nil, ErrConflict
		}
		err = manageDonationKeyTx(ctx, tx, in.id, in.keyID, actor.ID, audit, in.key, now)
	case "donation.key.failure_policy":
		_, err = updateFailurePolicyTx(ctx, tx, actor.ID, audit, in.id, in.keyID, in.expected, in.ref.Threshold, now)
	case "donation.key.reset":
		state, e := readResetKeyTx(ctx, tx, in.id, in.keyID, now)
		if e != nil {
			return nil, e
		}
		err = resetDonationKeyTx(ctx, tx, failureResetItem{in.id, in.keyID, in.expected}, state, actor.ID, audit, now)
	case "donation.use.reset":
		err = agentResetUseTx(ctx, tx, in.useID, in.expected, now)
	case "donation.handle":
		role := audit
		if role == string(reviewerTrainee) {
			role = "level5"
		}
		result, e := tx.ExecContext(ctx, `UPDATE donation_handling SET state='processed',revision=revision+1,processed_at=?,processed_by_user_id=?,processed_by_role=?,updated_at=? WHERE donation_id=? AND state='pending' AND revision=? AND revision<9223372036854775807 AND EXISTS(SELECT 1 FROM donations d WHERE d.id=donation_id AND `+logicallyActiveDonationSQL+`)`, now, actor.ID, role, now, in.id, in.expected, now)
		err = e
		if err == nil {
			err = requireOne(result)
		}
	}
	if err != nil {
		return nil, err
	}
	return s.agentReceiptTx(ctx, tx, access, in.id, now)
}
func (s *Service) agentReceiptTx(ctx context.Context, tx *sql.Tx, access useAccess, id, now int64) (json.RawMessage, error) {
	value, err := getAdminDonationTx(ctx, tx, id, now)
	if err != nil {
		return nil, err
	}
	scope, err := readDonationAccessScope(ctx, tx, access.role, access.actorID)
	if err != nil {
		return nil, err
	}
	value, err = scope.projectReceiptTx(ctx, tx, value)
	if err != nil {
		return nil, err
	}
	for i := range value.Keys {
		value.Keys[i].DisplayHead = ""
		value.Keys[i].DisplayTail = ""
	}
	if access.role == reviewerAdmin {
		return json.Marshal(value)
	}
	return json.Marshal(stewardFromAdmin(value, access.actorID))
}

func agentResetUseTx(ctx context.Context, tx *sql.Tx, id, expected, now int64) error {
	var approved sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT current_approved_revision_id FROM donation_uses WHERE id=? AND revision=?`, id, expected).Scan(&approved); errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	} else if err != nil {
		return err
	}
	if !approved.Valid {
		return ErrConflict
	}
	var generation []byte
	var healthID int64
	err := tx.QueryRowContext(ctx, `SELECT h.id,h.streak_generation FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id JOIN donation_use_revisions r ON r.id=u.current_approved_revision_id JOIN endpoint_key_grant_revisions g ON g.id=r.grant_revision_id JOIN donation_service_health h ON h.use_id=u.id AND h.endpoint_key_id=dk.endpoint_key_id AND h.native_service_bit=u.native_service_bit AND h.credential_version_id=g.credential_version_id WHERE u.id=?`, id).Scan(&healthID, &generation)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		values, e := streakResetValues(generation)
		if e != nil {
			return e
		}
		r, e := tx.ExecContext(ctx, `UPDATE donation_service_health SET `+streakResetAssignments+`,updated_at=? WHERE id=?`, append(values, now, healthID)...)
		if e != nil {
			return e
		}
		if e = requireOne(r); e != nil {
			return e
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE donation_uses SET revision=revision+1,updated_at=? WHERE id=? AND revision=? AND revision<9223372036854775807`, now, id, expected)
	if err != nil {
		return err
	}
	return requireOne(result)
}
