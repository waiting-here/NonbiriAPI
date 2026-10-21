package routing

import (
	"strconv"
	"testing"
)

func TestModelCloudflareFrozenInPersonalSnapshots(t *testing.T) {
	e := newSnapshotTestEnvironment(t)
	owner := e.seedUser(t, "cloudflare")
	id := e.seedModel(t, owner, "cloudflare", "model")
	e.seedAvailableBinding(t, owner, id, "upstream")
	for _, enabled := range []bool{true, false} {
		if _, err := e.store.DB().Exec(`UPDATE models SET cloudflare_proxy_enabled=? WHERE id=?`, enabled, id); err != nil {
			t.Fatal(err)
		}
		preflight, err := e.routing.Preflight(t.Context(), owner, strconv.FormatInt(id, 10))
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := e.routing.Snapshot(t.Context(), owner, Identity{ModelID: strconv.FormatInt(id, 10)})
		if err != nil {
			t.Fatal(err)
		}
		if preflight.CloudflareProxyEnabled() != enabled || snapshot.CloudflareProxyEnabled() != enabled {
			t.Fatal("setting not frozen")
		}
		if _, err := e.store.DB().Exec(`UPDATE models SET cloudflare_proxy_enabled=? WHERE id=?`, !enabled, id); err != nil {
			t.Fatal(err)
		}
		if preflight.CloudflareProxyEnabled() != enabled || snapshot.CloudflareProxyEnabled() != enabled {
			t.Fatal("frozen setting changed")
		}
	}
}
