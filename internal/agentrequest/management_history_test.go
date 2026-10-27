package agentrequest

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
)

func TestRequestCleanupRecordsOnlyCommittedChunks(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		name := "complete"
		if interrupted {
			name = "authorization_revoked"
		}
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("cleanup called upstream") })
			if _, err := f.database.Exec(db.ManagementOperationsSQL); err != nil {
				t.Fatal(err)
			}
			if _, err := f.database.Exec(`WITH RECURSIVE n(id) AS (SELECT 1 UNION ALL SELECT id+1 FROM n WHERE id<205)
INSERT INTO agent_requests(client_request_id,source,kind,actor_id,actor_role,actor_label,input_json,state,created_at,completed_at,consumed_at)
SELECT 'cleanup-'||id,'admin_chat','model',1,'admin','Administrator','{}','succeeded',1,2,3 FROM n`); err != nil {
				t.Fatal(err)
			}
			denied := errors.New("administrator session revoked")
			calls := 0
			f.runtime.adminAuthorize = func(context.Context, *sql.Tx) error {
				calls++
				if interrupted && calls == 2 {
					return denied
				}
				return nil
			}
			ctx := managementlog.WithOperation(t.Context(), managementlog.Metadata{
				EventID: "cleanup", CreatedAt: 10, Source: managementlog.SourceWeb,
				ExecutorKind: "human", Actor: managementlog.Subject{ID: "1", Name: "Administrator", Role: "admin"},
				Domain: "agent-requests", Action: "cleanup",
			})
			result, err := f.runtime.Cleanup(ctx, CleanupInput{ThroughID: 205})
			wantDeleted, wantTargets, wantOutcome := int64(205), int64(205), "succeeded"
			if interrupted {
				wantDeleted, wantTargets, wantOutcome = 100, 101, "partial"
				if !errors.Is(err, denied) {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if result.DeletedCount != wantDeleted {
				t.Fatal(result)
			}
			var outcome, correlation string
			var targets, success, remaining, deletedItems, rolls int64
			if err := f.database.QueryRow("SELECT outcome,correlation_kind,targets,succeeded FROM management_operations WHERE event_id='cleanup'").Scan(&outcome, &correlation, &targets, &success); err != nil {
				t.Fatal(err)
			}
			if err := f.database.QueryRow("SELECT count(*) FROM agent_requests").Scan(&remaining); err != nil {
				t.Fatal(err)
			}
			if err := f.database.QueryRow("SELECT count(*) FROM management_operation_items WHERE action='delete' AND outcome='succeeded'").Scan(&deletedItems); err != nil {
				t.Fatal(err)
			}
			if err := f.database.QueryRow("SELECT sum(operations) FROM management_operation_rollups").Scan(&rolls); err != nil {
				t.Fatal(err)
			}
			if outcome != wantOutcome || correlation != "request_cleanup" || targets != wantTargets || success != wantDeleted || deletedItems != wantDeleted || remaining != 205-wantDeleted || rolls != 1 {
				t.Fatalf("outcome=%s correlation=%s targets=%d success=%d deletedItems=%d remaining=%d rollups=%d", outcome, correlation, targets, success, deletedItems, remaining, rolls)
			}
		})
	}
}
