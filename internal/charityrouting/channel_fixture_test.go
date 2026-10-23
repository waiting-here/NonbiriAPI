package charityrouting

import (
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func seedRoutingChannel(t *testing.T, e *routingTestEnv, id, name, baseURL string) {
	t.Helper()
	services, err := contract.LegacyNativeServices(contract.TypeOpenAICompatible)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := e.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO mainstream_channels(id,name,category,enabled,state,revision,created_at,updated_at) VALUES(?,?,'subscription',1,'active',1,?,?)`, id, name, routingTestNow, routingTestNow); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO mainstream_channel_entries(channel_id,ord,connector_type,canonical_base_url,native_service_mask) VALUES(?,0,'openai-compatible',?,?)`, id, baseURL, services.Mask()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
