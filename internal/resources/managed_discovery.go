package resources

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
)

const (
	routeAdminDiscovery           = "/admin/api/donations/{id}/keys/{keyId}/models/refresh"
	routeStewardDiscovery         = "/api/steward/donations/{id}/keys/{keyId}/models/refresh"
	routeAdminDiscoveryEvidence   = "/admin/api/donations/{id}/keys/{keyId}/models/discovery"
	routeStewardDiscoveryEvidence = "/api/steward/donations/{id}/keys/{keyId}/models/discovery"
)

type managedDiscoveryRequest struct {
	role              string
	donationID, keyID int64
	selection         ManagedDiscoverySelection
}

// Worker cancellation and deadlines remain authoritative while the original
// session binding is retained for live authorization inside each transaction.
type discoveryIdentityContext struct {
	context.Context
	identity context.Context
}

func (ctx discoveryIdentityContext) Value(key any) any { return ctx.identity.Value(key) }

func (r *Repository) RefreshManagedDiscovery(ctx context.Context, role string, actorID, donationID, donationKeyID int64, mutation ControlMutation) (MutationResult[DiscoveryAccepted], error) {
	return r.refreshManagedDiscovery(ctx, role, actorID, donationID, donationKeyID, mutation, ManagedDiscoverySelection{})
}

func (r *Repository) refreshManagedDiscovery(ctx context.Context, role string, actorID, donationID, donationKeyID int64, mutation ControlMutation, selection ManagedDiscoverySelection) (MutationResult[DiscoveryAccepted], error) {
	if r == nil || ctx == nil || isNilInterface(r.managedDiscovery) || actorID <= 0 || donationID <= 0 || donationKeyID <= 0 ||
		(role != "admin" && role != "level6") || mutation.Method != http.MethodPost || mutation.Query != charityscope.Query(ctx) ||
		!mutationPathIDs(mutation, donationID, donationKeyID) ||
		(role == "admin" && mutation.Route != routeAdminDiscovery) || (role == "level6" && mutation.Route != routeStewardDiscovery) {
		return MutationResult[DiscoveryAccepted]{}, ErrInvalidRequest
	}
	return r.startDiscovery(ctx, actorID, 0, 0, mutation, false, &managedDiscoveryRequest{role: role, donationID: donationID, keyID: donationKeyID, selection: selection})
}

func (r *Repository) ManagedDiscoveryEvidence(ctx context.Context, role string, actorID, donationID, donationKeyID int64) (DiscoveryEvidence, error) {
	return r.managedDiscoveryEvidence(ctx, role, actorID, donationID, donationKeyID, ManagedDiscoverySelection{})
}

func (r *Repository) managedDiscoveryEvidence(ctx context.Context, role string, actorID, donationID, donationKeyID int64, selection ManagedDiscoverySelection) (DiscoveryEvidence, error) {
	if r == nil || ctx == nil || isNilInterface(r.managedDiscovery) || actorID <= 0 || donationID <= 0 || donationKeyID <= 0 || (role != "admin" && role != "level6") {
		return DiscoveryEvidence{}, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := beginTx(ctx, r.readDB)
	if err != nil {
		return DiscoveryEvidence{}, err
	}
	defer tx.Rollback()
	target, err := r.managedDiscovery.AuthorizeManagedDiscovery(ctx, tx, role, actorID, donationID, donationKeyID, selection, true)
	if err != nil {
		return DiscoveryEvidence{}, err
	}
	if target.SourceRevisionID == 0 {
		return DiscoveryEvidence{State: "unknown", Revision: "1", SafeClass: "none"}, nil
	}
	row, err := discoveryEvidenceForRevisionTx(ctx, tx, target.SourceRevisionID)
	if err != nil {
		return DiscoveryEvidence{}, err
	}
	return row.evidence()
}

func RegisterManagedDiscoveryRoutes(users UserRouteRegistrar, admins AdminRouteRegistrar, repository *Repository) error {
	if isNilInterface(users) || isNilInterface(admins) || repository == nil || isNilInterface(repository.managedDiscovery) {
		return ErrInvalidRequest
	}
	api := &httpAPI{repository: repository}
	for _, route := range []struct{ method, path string }{{http.MethodPost, routeAdminDiscovery}, {http.MethodGet, routeAdminDiscoveryEvidence}} {
		if err := admins.RegisterAdminRoute(route.method, route.path, func(w http.ResponseWriter, req *http.Request, principal AdminPrincipal) {
			api.managedDiscoveryHTTP(w, req, "admin", principal.UserID)
		}); err != nil {
			return err
		}
	}
	for _, route := range []struct{ method, path string }{{http.MethodPost, routeStewardDiscovery}, {http.MethodGet, routeStewardDiscoveryEvidence}} {
		if err := users.RegisterUserRoute(route.method, route.path, func(w http.ResponseWriter, req *http.Request, principal UserPrincipal) {
			api.managedDiscoveryHTTP(w, req, "level6", principal.UserID)
		}); err != nil {
			return err
		}
	}
	return nil
}

func (api *httpAPI) managedDiscoveryHTTP(w http.ResponseWriter, req *http.Request, role string, actorID int64) {
	selected, err := charityscope.SelectRequest(req)
	if err != nil {
		writeResourceError(w, ErrInvalidRequest)
		return
	}
	req = selected
	donationID, ok := parsePathID(w, req, "id")
	if !ok {
		return
	}
	keyID, ok := parsePathID(w, req, "keyId")
	if !ok {
		return
	}
	if req.Method == http.MethodGet {
		if !requireNoBody(w, req) {
			return
		}
		selection, ok := managedDiscoveryQuery(w, req)
		if !ok {
			return
		}
		evidence, err := api.repository.managedDiscoveryEvidence(req.Context(), role, actorID, donationID, keyID, selection)
		if err != nil {
			writeResourceError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, evidence)
		return
	}
	route := routeAdminDiscovery
	if role == "level6" {
		route = routeStewardDiscovery
	}
	mutation, selection, ok := managedDiscoveryMutation(w, req, route, donationID, keyID)
	if !ok {
		return
	}
	mutation.Query = charityscope.Query(req.Context())
	result, err := api.repository.refreshManagedDiscovery(req.Context(), role, actorID, donationID, keyID, mutation, selection)
	if err != nil {
		writeResourceError(w, err)
		return
	}
	writeMutation(w, result)
}

func discoveryEvidenceForRevisionTx(ctx context.Context, tx *sql.Tx, revisionID int64) (discoveryRow, error) {
	var row discoveryRow
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(run.state,'unknown'),MAX(st.result_generation,1),CASE WHEN run.state='checking' THEN run.operation_hash END,COALESCE(run.safe_class,'none'),COALESCE(run.safe_diagnostic,''),run.started_at,run.completed_at,COALESCE(run.model_count,0),success.completed_at,changes.added_count,changes.removed_count
 FROM discovery_source_states st LEFT JOIN discovery_runs run ON run.id=st.current_run_id
 LEFT JOIN discovery_runs success ON success.id=st.last_successful_run_id LEFT JOIN discovery_run_changes changes ON changes.run_id=st.current_run_id
 WHERE st.source_revision_id=?`, revisionID).Scan(&row.state, &row.revision, &row.operationHash, &row.safeClass, &row.safeDiag, &row.startedAt, &row.completedAt, &row.fetchedCount, &row.lastSuccessAt, &row.addedCount, &row.removedCount)
	return row, err
}

func managedDiscoveryQuery(w http.ResponseWriter, req *http.Request) (ManagedDiscoverySelection, bool) {
	values, err := url.ParseQuery(req.URL.RawQuery)
	if err != nil {
		writeResourceError(w, ErrInvalidRequest)
		return ManagedDiscoverySelection{}, false
	}
	for name, items := range values {
		if (name != "use_id" && name != "expected_revision") || len(items) != 1 {
			writeResourceError(w, ErrInvalidRequest)
			return ManagedDiscoverySelection{}, false
		}
	}
	if len(values) == 0 {
		return ManagedDiscoverySelection{}, true
	}
	useID, err1 := parseDecimalID(values.Get("use_id"))
	revision, err2 := parseDecimalID(values.Get("expected_revision"))
	if err1 != nil || err2 != nil {
		writeResourceError(w, ErrInvalidRequest)
		return ManagedDiscoverySelection{}, false
	}
	return ManagedDiscoverySelection{UseID: useID, ExpectedRevision: revision}, true
}

func managedDiscoveryMutation(w http.ResponseWriter, req *http.Request, route string, donationID, keyID int64) (ControlMutation, ManagedDiscoverySelection, bool) {
	if req.ContentLength == 0 && len(req.TransferEncoding) == 0 {
		mutation, ok := noBodyMutation(w, req, route, donationID, keyID)
		return mutation, ManagedDiscoverySelection{}, ok
	}
	if !requireEmptyQuery(w, req) {
		return ControlMutation{}, ManagedDiscoverySelection{}, false
	}
	var body struct {
		UseID            requestField[string] `json:"use_id"`
		ExpectedRevision requestField[string] `json:"expected_revision"`
	}
	if _, ok := decodeStrictObject(w, req, &body); !ok {
		return ControlMutation{}, ManagedDiscoverySelection{}, false
	}
	useID, err1 := parseDecimalID(body.UseID.Value)
	revision, err2 := parseDecimalID(body.ExpectedRevision.Value)
	if err1 != nil || err2 != nil {
		writeResourceError(w, ErrInvalidRequest)
		return ControlMutation{}, ManagedDiscoverySelection{}, false
	}
	canonical := struct {
		UseID            string `json:"use_id"`
		ExpectedRevision string `json:"expected_revision"`
	}{body.UseID.Value, body.ExpectedRevision.Value}
	mutation, ok := controlMutation(w, req, route, []int64{donationID, keyID}, canonical)
	return mutation, ManagedDiscoverySelection{UseID: useID, ExpectedRevision: revision}, ok
}
