package donation

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/httpapi"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

const routeAdminPendingReview = routeAdminDonation + "/pending-review"
const routeStewardPendingReview = routeStewardDonation + "/pending-review"

func pendingReviewItems(input PendingReviewInput) ([]UseReviewItem, error) {
	revision, err := parseID(input.ExpectedRevision)
	if err != nil || revision == math.MaxInt64 || !validReviewReason(input.Reason) || input.Decision != "approve" && input.Decision != "reject" || len(input.Uses) == 0 || len(input.Uses) > maxDonationKeys*19 {
		return nil, ErrInvalidRequest
	}
	items := make([]UseReviewItem, 0, len(input.Uses))
	seen := map[int64]bool{}
	for _, v := range input.Uses {
		id, e := parseID(v.UseID)
		rev, e2 := parseID(v.ExpectedRevision)
		if e != nil || e2 != nil || rev == math.MaxInt64 || seen[id] {
			return nil, ErrInvalidRequest
		}
		seen[id] = true
		item := UseReviewItem{UseID: id, ExpectedRevision: rev, Decision: input.Decision, ReviewNote: input.Reason}
		if v.ExpectedReviewRevision != nil {
			r, e := parseID(*v.ExpectedReviewRevision)
			if e != nil {
				return nil, ErrInvalidRequest
			}
			item.ExpectedReviewRevision = &r
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UseID < items[j].UseID })
	return items, nil
}

func validatePendingReviewTx(ctx context.Context, tx *sql.Tx, id int64, input PendingReviewInput) ([]UseReviewItem, error) {
	items, err := pendingReviewItems(input)
	if err != nil {
		return nil, err
	}
	snap, _, err := agentPendingTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if snap.Revision != input.ExpectedRevision || len(items) != len(snap.Pending) {
		return nil, ErrConflict
	}
	for i, v := range snap.Pending {
		want := items[i]
		if v.UseID != strconv.FormatInt(want.UseID, 10) || v.ExpectedRevision != strconv.FormatInt(want.ExpectedRevision, 10) {
			return nil, ErrConflict
		}
		if (v.ExpectedReviewRevision == nil) != (want.ExpectedReviewRevision == nil) {
			return nil, ErrConflict
		}
		if v.ExpectedReviewRevision != nil && *v.ExpectedReviewRevision != strconv.FormatInt(*want.ExpectedReviewRevision, 10) {
			return nil, ErrConflict
		}
	}
	return items, nil
}

// reviewPendingSubmissionTx shares one version check and one transaction across
// every pending service. Previously approved revisions remain unchanged.
func (s *Service) reviewPendingSubmissionTx(ctx context.Context, tx *sql.Tx, access useAccess, id int64, input PendingReviewInput, now int64) error {
	if err := requireDonationRangeTx(ctx, tx, access.role, access.actorID, id, now); err != nil {
		return err
	}
	items, err := validatePendingReviewTx(ctx, tx, id, input)
	if err != nil {
		return err
	}
	expiry, err := materializeDonationExpiryStateTx(ctx, tx, id, now)
	if err != nil {
		return err
	}
	if expiry.changed {
		return ErrConflict
	}
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT status FROM donations WHERE id=?`, id).Scan(&status); err != nil {
		return err
	}
	if status != "pending" && status != "approved" {
		return ErrConflict
	}
	for _, item := range items {
		if err = s.reviewUseTx(ctx, tx, access, id, item, now); err != nil {
			return err
		}
	}
	var approved bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id WHERE dk.donation_id=? AND u.current_approved_revision_id IS NOT NULL)`, id).Scan(&approved); err != nil {
		return err
	}
	next := "approved"
	var terminal any
	if !approved {
		next = "rejected"
		terminal = now
		if err = clearDonationMembershipsTx(ctx, tx, id, "terminated", now); err != nil {
			return err
		}
		if err = closePendingHandlingTx(ctx, tx, id, now, "rejected"); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE donations SET status=?,revision=revision+1,first_approval_origin=CASE WHEN ?='approve' AND first_approval_origin='unknown' THEN 'manual' ELSE first_approval_origin END,
 review_note=?,reviewed_by_user_id=?,reviewed_by_role=?,reviewed_at=?,updated_at=?,terminal_at=? WHERE id=? AND revision=? AND revision<9223372036854775807`, next, input.Decision, input.Reason, access.actorID, string(access.role), now, now, terminal, id, input.ExpectedRevision)
	if err != nil {
		return err
	}
	if err = requireOne(result); err != nil {
		return err
	}
	revision, _ := parseID(input.ExpectedRevision)
	_, err = tx.ExecContext(ctx, `INSERT INTO donation_reviews(donation_id,submission_revision,reviewer_user_id,reviewer_role,action,note,created_at) VALUES(?,?,?,?,?,?,?)`, id, revision+1, access.actorID, string(access.role), input.Decision, input.Reason, now)
	return err
}

func (s *Service) pendingReview(ctx context.Context, role reviewerRole, actorID, id int64, input PendingReviewInput, mutation resources.ControlMutation) (roleDonationMutation, error) {
	route := routeAdminPendingReview
	if role != reviewerAdmin {
		route = routeStewardPendingReview
	}
	if _, err := pendingReviewItems(input); err != nil || !validMutation(mutation, http.MethodPost, route, id) {
		return roleDonationMutation{}, ErrInvalidRequest
	}
	tx, actual, actorID, err := s.beginReviewTx(ctx, role, actorID, false)
	if err != nil {
		return roleDonationMutation{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	if actual != reviewerAdmin {
		if err = authz.RequireStewardPermission(ctx, tx, actorID, "donations.review"); err != nil {
			return roleDonationMutation{}, err
		}
	}
	now, err := s.nowUnix()
	if err != nil {
		return roleDonationMutation{}, err
	}
	if err = requireManagedDonationTx(ctx, tx, actual, id, now); err != nil {
		return roleDonationMutation{}, err
	}
	decision, err := beginMutation(ctx, tx, string(actual), actorID, idempotency.ScopeControlMutation, mutation, now)
	if err != nil {
		return roleDonationMutation{}, err
	}
	if decision.Kind == idempotency.Replay {
		if err = requireReviewReceiptRangeTx(ctx, tx, actual, actorID, id, now); err != nil {
			return roleDonationMutation{}, err
		}
		return replayRoleDonation(ctx, tx, decision, actual, actorID, id)
	}
	if err = s.reviewPendingSubmissionTx(ctx, tx, useAccess{role: actual, actorID: actorID}, id, input, now); err != nil {
		return roleDonationMutation{}, err
	}
	value, err := getAdminDonationTx(ctx, tx, id, now)
	if err != nil {
		return roleDonationMutation{}, err
	}
	out, err := finishRoleDonation(ctx, tx, decision, actual, actorID, value)
	if err != nil {
		return roleDonationMutation{}, err
	}
	if err = commitTx(tx, &committed); err != nil {
		return roleDonationMutation{}, err
	}
	return out, nil
}

func (api *httpAPI) pendingReviewHandler(role reviewerRole) AuthorizedUserHandler {
	return func(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
		id, ok := parsePathID(w, r, "id")
		if !ok || !requireEmptyQuery(w, r) {
			return
		}
		var input PendingReviewInput
		body, err := httpapi.ReadJSON(w, r, &input, httpapi.BodyOptions{MaxBytes: idempotency.MaxControlBodyBytes, Validate: func(raw []byte) error { return strictjson.ValidateObjectWithFieldLimit(raw, 4+maxDonationKeys*19*3) }})
		if err != nil {
			if errors.Is(err, httpapi.ErrTooLarge) {
				httperr.WriteError(w, httperr.New(httperr.CodePayloadTooLarge, "request body is too large"))
			} else {
				writeDonationError(w, ErrInvalidRequest)
			}
			return
		}
		clear(body)
		input.Reason = strings.ReplaceAll(strings.ReplaceAll(input.Reason, "\r\n", "\n"), "\r", "\n")
		if _, err := pendingReviewItems(input); err != nil {
			writeDonationError(w, err)
			return
		}
		route := routeAdminPendingReview
		if role != reviewerAdmin {
			route = routeStewardPendingReview
		}
		mutation, ok := mutationFor(w, r, route, []int64{id}, input)
		if !ok {
			return
		}
		result, err := api.service.pendingReview(r.Context(), role, p.UserID, id, input, mutation)
		if err != nil {
			writeDonationError(w, err)
			return
		}
		if role == reviewerAdmin {
			writeMutation(w, result.adminResult())
		} else {
			writeMutation(w, result.stewardResult())
		}
	}
}

func parseID(s string) (int64, error) {
	v, e := strconv.ParseInt(s, 10, 64)
	if e != nil || v <= 0 || strconv.FormatInt(v, 10) != s {
		return 0, ErrInvalidRequest
	}
	return v, nil
}

// Decode strictly without HTTP state so plan JSON follows the same field rules.
func agentDecode(data []byte, out any) error {
	if strictjson.ValidateObjectPerObject(data) != nil {
		return ErrInvalidRequest
	}
	if len(data) == 0 || len(data) > 1<<20 {
		return ErrInvalidRequest
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil || raw == nil {
		return ErrInvalidRequest
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return ErrInvalidRequest
	}
	return nil
}
