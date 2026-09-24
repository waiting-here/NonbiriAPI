package donation

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func scopeError(err error) error {
	if errors.Is(err, charityscope.ErrNotFound) {
		return ErrNotFound
	}
	if errors.Is(err, charityscope.ErrInvalid) {
		return ErrInvalidRequest
	}
	return mapAuthorization(err)
}

func (s *Service) managementScope(ctx context.Context, tx *sql.Tx, role reviewerRole, actorID int64) (charityscope.Scope, error) {
	if role != reviewerAdmin && role != reviewerSteward {
		return charityscope.Scope{}, ErrInvalidRequest
	}
	scope, err := charityscope.Authorize(ctx, tx, s.roleAuth, role == reviewerAdmin, actorID, charityscope.ModelID(ctx), false)
	return scope, scopeError(err)
}

func (s *Service) beginScopedTx(ctx context.Context, role reviewerRole, actorID int64, readOnly bool) (*sql.Tx, charityscope.Scope, error) {
	if s == nil || s.db == nil || ctx == nil || nilDependency(s.roleAuth) {
		return nil, charityscope.Scope{}, ErrUnavailable
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: readOnly})
	if err != nil {
		return nil, charityscope.Scope{}, err
	}
	scope, err := s.managementScope(ctx, tx, role, actorID)
	if err != nil {
		tx.Rollback()
		return nil, scope, err
	}
	return tx, scope, nil
}

func validScopedMutation(ctx context.Context, mutation resources.ControlMutation, method, route string, ids ...int64) bool {
	if mutation.Query != charityscope.Query(ctx) {
		return false
	}
	mutation.Query = ""
	return validMutation(mutation, method, route, ids...)
}

func scopeHandler(next AuthorizedUserHandler) AuthorizedUserHandler {
	return func(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
		selected, err := charityscope.SelectRequest(r)
		if err != nil {
			writeDonationError(w, ErrInvalidRequest)
			return
		}
		next(w, selected, p)
	}
}

func scopedDonationRoute(route string) bool {
	switch route {
	case routeStewardSources, routeStewardSourceKeys, routeStewardKeys, routeStewardKey,
		routeStewardRecurring, routeStewardFailurePolicy, routeStewardFailureReset,
		routeStewardFailureReset + "/selection", routeStewardDiscoverySelection:
		return true
	}
	return false
}

type ManagedKeyReceipt struct {
	DonationID       string            `json:"donation_id"`
	KeyID            string            `json:"key_id"`
	DonationRevision string            `json:"donation_revision"`
	Key              ManagedKeySummary `json:"key"`
}

func (s *Service) managedKeyTx(ctx context.Context, tx *sql.Tx, scope charityscope.Scope, donationID, keyID, now int64) (ManagedKeySummary, error) {
	if err := scope.RequireKey(ctx, tx, donationID, keyID, now, false); err != nil {
		return ManagedKeySummary{}, scopeError(err)
	}
	header, err := logicalHeaderTx(ctx, tx, donationID, now)
	if err != nil {
		return ManagedKeySummary{}, err
	}
	keys, err := readDonationKeySelectionTx(ctx, tx, donationID, header.Status, now, []int64{keyID})
	if err != nil {
		return ManagedKeySummary{}, err
	}
	if len(keys) != 1 {
		return ManagedKeySummary{}, ErrNotFound
	}
	rules, err := recurringSummaryTx(ctx, tx, keyID, now)
	if err != nil {
		return ManagedKeySummary{}, err
	}
	value := managedKeySummary(keys[0], header, rules)
	if scope.Trainee {
		value.EndpointKeyID = nil
	}
	impact, err := scope.Impact(ctx, tx, keyID)
	if err != nil {
		return ManagedKeySummary{}, err
	}
	value.BindingCount, value.Idle = impact.BindingCount, impact.BindingCount == "0"
	value.VisibleModels, value.VisibleModelsTruncated = impact.VisibleModels, impact.VisibleModelsTruncated
	return value, nil
}

func (s *Service) KeyStewardSession(ctx context.Context, userID, donationID, keyID int64) (ManagedKeySummary, error) {
	tx, scope, err := s.beginScopedTx(ctx, reviewerSteward, userID, true)
	if err != nil {
		return ManagedKeySummary{}, err
	}
	defer tx.Rollback()
	now, err := s.nowUnix()
	if err != nil {
		return ManagedKeySummary{}, err
	}
	if err := requireManagedDonationTx(ctx, tx, reviewerSteward, donationID, now); err != nil {
		return ManagedKeySummary{}, err
	}
	value, err := s.managedKeyTx(ctx, tx, scope, donationID, keyID, now)
	if err != nil {
		return ManagedKeySummary{}, err
	}
	return value, tx.Commit()
}

func (api *httpAPI) keySteward(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	donationID, ok := parsePathID(w, r, "id")
	if !ok {
		return
	}
	keyID, ok := parsePathID(w, r, "keyId")
	if !ok || !requireEmptyQuery(w, r) || !requireNoBody(w, r) {
		return
	}
	value, err := api.service.KeyStewardSession(r.Context(), p.UserID, donationID, keyID)
	if err != nil {
		writeDonationError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, value)
}

// ManageKeySession preserves the established full steward receipt, while a
// trainee receives only the key authorized by the selected model.
func (s *Service) ManageKeySession(ctx context.Context, userID, donationID, keyID int64, mutation resources.ControlMutation, input KeyManagementInput) (resources.MutationResult[any], error) {
	var empty resources.MutationResult[any]
	if userID <= 0 || !validScopedMutation(ctx, mutation, http.MethodPatch, routeStewardKey, donationID, keyID) {
		return empty, ErrInvalidRequest
	}
	result, err := s.manageKey(ctx, reviewerSteward, userID, donationID, keyID, mutation, input)
	if err != nil {
		return empty, err
	}
	var value any = result.steward
	if result.trainee != nil {
		value = *result.trainee
	}
	return resources.MutationResult[any]{Value: value, Status: result.status, Body: result.body, Replayed: result.replayed}, nil
}

func receiptKeyIDs(receipt ManagedKeyReceipt) (int64, int64, error) {
	donationID, err := strconv.ParseInt(receipt.DonationID, 10, 64)
	if err != nil {
		return 0, 0, ErrInvariant
	}
	keyID, err := strconv.ParseInt(receipt.KeyID, 10, 64)
	return donationID, keyID, err
}
