package resources

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
)

// AutomationDiscoverySnapshot is private receipt material, never an HTTP or
// account-export projection. A credential reference is an immutable version.
type AutomationDiscoverySnapshot struct {
	EndpointID    int64  `json:"endpoint_id"`
	KeyID         int64  `json:"key_id"`
	SecretRefID   int64  `json:"secret_ref_id"`
	BaseURL       string `json:"base_url"`
	ConnectorType string `json:"connector_type"`
}
type automationDiscoverySnapshotKey struct{}

func (r *Repository) AutomationDiscoverySnapshotInTransaction(ctx context.Context, tx *sql.Tx, userID, endpointID, keyID int64) (AutomationDiscoverySnapshot, error) {
	if _, err := getEndpointKeyTx(ctx, tx, userID, endpointID, keyID); err != nil {
		return AutomationDiscoverySnapshot{}, err
	}
	var out AutomationDiscoverySnapshot
	err := tx.QueryRowContext(ctx, `SELECT e.id,k.id,k.secret_ref_id,e.base_url,e.connector_type FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id WHERE e.user_id=? AND e.id=? AND k.id=?`, userID, endpointID, keyID).Scan(&out.EndpointID, &out.KeyID, &out.SecretRefID, &out.BaseURL, &out.ConnectorType)
	return out, err
}
func (r *Repository) VerifyAutomationDiscoverySnapshotInTransaction(ctx context.Context, tx *sql.Tx, userID int64, snapshot AutomationDiscoverySnapshot) error {
	current, err := r.AutomationDiscoverySnapshotInTransaction(ctx, tx, userID, snapshot.EndpointID, snapshot.KeyID)
	if err != nil {
		return err
	}
	if current != snapshot {
		return ErrConflict
	}
	return nil
}

// RefreshAutomationDiscovery reuses an explicit stable receipt identity and
// guards worker completion against changed credentials/configuration.
func (r *Repository) RefreshAutomationDiscovery(ctx context.Context, userID int64, snapshot AutomationDiscoverySnapshot, key string, canonical []byte) (DiscoveryAccepted, error) {
	ctx = context.WithValue(ctx, automationDiscoverySnapshotKey{}, snapshot)
	out, err := r.refreshDiscovery(ctx, userID, snapshot.EndpointID, snapshot.KeyID, ControlMutation{IdempotencyKey: key, Method: http.MethodPost, Route: routeDiscovery, PathIDs: []string{strconv.FormatInt(snapshot.EndpointID, 10), strconv.FormatInt(snapshot.KeyID, 10)}, CanonicalBody: canonical}, true)
	return out.Value, err
}
