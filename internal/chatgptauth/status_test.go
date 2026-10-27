package chatgptauth

import "testing"

func TestStatusReportsConnectionUpdatesWithoutRefreshing(t *testing.T) {
	f := newAuthFixture(t)
	ctx := t.Context()
	empty, err := f.m.Status(ctx)
	if err != nil || empty.Connected || empty.UpdatedAt != 0 || empty.OfficialUsage.Availability != "unavailable" || empty.OfficialUsage.UsageURL != "https://chatgpt.com/settings/usage" {
		t.Fatal(empty, err)
	}
	imported := f.importCredentials(t)
	if imported.UpdatedAt != f.clock.Load()*1000 || imported.ExpiresAt != f.clock.Load()+30 {
		t.Fatal(imported)
	}
	f.clock.Add(31)
	before, err := f.m.Status(ctx)
	if err != nil || before.UpdatedAt != imported.UpdatedAt || f.refreshes.Load() != 0 {
		t.Fatal("status refreshed the connection", before, err)
	}
	if _, err = f.m.AccessToken(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := f.m.Status(ctx)
	if err != nil || after.UpdatedAt != f.clock.Load()*1000 || after.ExpiresAt != f.clock.Load()+3600 || after.Revision != imported.Revision || f.refreshes.Load() != 1 {
		t.Fatal(after, err)
	}
}
