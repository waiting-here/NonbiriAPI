package chatgptauth

import (
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
)

func TestLogoutRetainsUnconfirmedRevocationResult(t *testing.T) {
	f := newAuthFixture(t)
	connected := f.importCredentials(t)
	if _, err := f.m.config.DB.Exec(db.ManagementOperationsSQL); err != nil {
		t.Fatal(err)
	}
	f.mode.Store(3)
	ctx := managementlog.WithOperation(t.Context(), managementlog.Metadata{EventID: "logout", Source: managementlog.SourceWeb, ExecutorKind: "human", Domain: "ai", Action: "chatgpt.logout"})
	result, err := f.m.Logout(ctx, connected.Revision)
	if err != nil || result.RemoteRevocationConfirmed {
		t.Fatal(result, err)
	}
	var outcome, code, credentials string
	if err := f.m.config.DB.QueryRow("SELECT outcome,code FROM management_operations WHERE event_id='logout'").Scan(&outcome, &code); err != nil {
		t.Fatal(err)
	}
	if err := f.m.config.DB.QueryRow("SELECT encrypted_credentials FROM agent_chatgpt_session WHERE id=1").Scan(&credentials); err != nil {
		t.Fatal(err)
	}
	if outcome != "partial" || code != "remote_revocation_unconfirmed" || credentials != "" {
		t.Fatal(outcome, code, credentials != "")
	}
}
