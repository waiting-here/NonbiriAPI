package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type transactionalFishExport struct {
	finalizer *testFinalizer
}

func (a transactionalFishExport) ExportFatFish(ctx context.Context, tx *sql.Tx, r ExportRequest) (FatFishExport, ExportFinalizer, error) {
	_, err := tx.ExecContext(ctx, `UPDATE users SET username='export-converged' WHERE id=?`, r.UserID)
	return FatFishExport{}, a.finalizer, err
}

func TestInteractionExportFailureRollsBackPriorDomainWrites(t *testing.T) {
	fixture := newLifecycleTestFixture(t, 100)
	user := seedLifecycleUser(t, fixture.store.DB(), "original", false, 100)
	for _, failure := range []string{"request_adaptation", "continuity", "size"} {
		t.Run(failure, func(t *testing.T) {
			finalizer := &testFinalizer{}
			fixture.exports.errAt = failure
			fixture.exports.user.Username = ""
			if failure == "size" {
				fixture.exports.user.Username = strings.Repeat("x", MaxExportBytes)
			}
			config := fixture.config
			config.Export.FatFish = transactionalFishExport{finalizer}
			coordinator := mustNewLifecycleCoordinator(t, config)
			body, err := coordinator.Export(context.Background(), user, 100)
			if err == nil || body != nil || finalizer.commits != 0 || finalizer.aborts != 1 {
				t.Fatalf("failed export published state: bytes=%d error=%v finalizer=%+v", len(body), err, finalizer)
			}
			if failure == "size" && !errors.Is(err, ErrTooLarge) {
				t.Fatal(err)
			}
			var name string
			if err := fixture.store.DB().QueryRow(`SELECT username FROM users WHERE id=?`, user).Scan(&name); err != nil || name != "original" {
				t.Fatalf("domain write survived rollback: %q %v", name, err)
			}
		})
	}
}

func TestInteractionExportUsesExplicitSafeFields(t *testing.T) {
	assertClosedJSONKeys(t, InteractionExport{}, "request_adaptations", "continuity", "fat_fish")
	assertClosedJSONKeys(t, RequestAdaptationExport{}, "endpoint_id", "revision", "forward_headers", "fixed_headers", "body_defaults", "body_forced", "native_extension_paths")
	assertClosedJSONKeys(t, AdaptationValueExport{}, "path", "has_value")
	assertClosedJSONKeys(t, ContinuityEligibilityExport{}, "kind", "scope", "window", "state", "expires_at")
	assertClosedJSONKeys(t, FatFishExport{}, "summaries", "progress")
	assertClosedJSONKeys(t, FatFishSummaryExport{}, "id", "period_id", "node_id", "version_id", "engine_version", "scoring_version", "state", "prepared_at_ms", "started_at_ms", "completed_at_ms", "passed", "stars", "score_units", "ticket_charge", "ticket_refund", "rewards", "seed_commit", "commitment_verified")
	assertClosedJSONKeys(t, FatFishProgressExport{}, "period_id", "node_id", "unlocked_at", "unlock_operation_id", "passed", "best_stars", "best_score_units", "best_at_ms", "best_version_id")
	var document ExportDocument
	document.RequestAdaptations = []RequestAdaptationExport{{EndpointID: "1", Revision: "1"}}
	normalizeExportDocument(&document)
	raw, err := json.Marshal(document.RequestAdaptations[0])
	if err != nil || strings.Contains(string(raw), "null") {
		t.Fatalf("empty configuration arrays must be present: %s %v", raw, err)
	}
}
