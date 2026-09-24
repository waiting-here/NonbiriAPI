package resources

import (
	"context"
	"net/http"
	"time"
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
}

// Worker cancellation and deadlines remain authoritative while the original
// session binding is retained for live authorization inside each transaction.
type discoveryIdentityContext struct {
	context.Context
	identity context.Context
}

func (ctx discoveryIdentityContext) Value(key any) any { return ctx.identity.Value(key) }

func (r *Repository) RefreshManagedDiscovery(ctx context.Context, role string, actorID, donationID, donationKeyID int64, mutation ControlMutation) (MutationResult[DiscoveryAccepted], error) {
	if r == nil || ctx == nil || isNilInterface(r.managedDiscovery) || actorID <= 0 || donationID <= 0 || donationKeyID <= 0 ||
		(role != "admin" && role != "level6") || mutation.Method != http.MethodPost || mutation.Query != "" ||
		!mutationPathIDs(mutation, donationID, donationKeyID) ||
		(role == "admin" && mutation.Route != routeAdminDiscovery) || (role == "level6" && mutation.Route != routeStewardDiscovery) {
		return MutationResult[DiscoveryAccepted]{}, ErrInvalidRequest
	}
	return r.startDiscovery(ctx, actorID, 0, 0, mutation, false, &managedDiscoveryRequest{role: role, donationID: donationID, keyID: donationKeyID})
}

func (r *Repository) ManagedDiscoveryEvidence(ctx context.Context, role string, actorID, donationID, donationKeyID int64) (DiscoveryEvidence, error) {
	if r == nil || ctx == nil || isNilInterface(r.managedDiscovery) || actorID <= 0 || donationID <= 0 || donationKeyID <= 0 || (role != "admin" && role != "level6") {
		return DiscoveryEvidence{}, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := beginTx(ctx, r.db)
	if err != nil {
		return DiscoveryEvidence{}, err
	}
	defer tx.Rollback()
	target, err := r.managedDiscovery.AuthorizeManagedDiscovery(ctx, tx, role, actorID, donationID, donationKeyID, true)
	if err != nil {
		return DiscoveryEvidence{}, err
	}
	row, err := discoveryOwnerTx(ctx, tx, target.OwnerUserID, target.EndpointID, target.EndpointKeyID)
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
	donationID, ok := parsePathID(w, req, "id")
	if !ok {
		return
	}
	keyID, ok := parsePathID(w, req, "keyId")
	if !ok {
		return
	}
	if req.Method == http.MethodGet {
		if !requireEmptyQuery(w, req) || !requireNoBody(w, req) {
			return
		}
		evidence, err := api.repository.ManagedDiscoveryEvidence(req.Context(), role, actorID, donationID, keyID)
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
	mutation, ok := noBodyMutation(w, req, route, donationID, keyID)
	if !ok {
		return
	}
	result, err := api.repository.RefreshManagedDiscovery(req.Context(), role, actorID, donationID, keyID, mutation)
	if err != nil {
		writeResourceError(w, err)
		return
	}
	writeMutation(w, result)
}
