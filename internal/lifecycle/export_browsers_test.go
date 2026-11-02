package lifecycle

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBrowserAssociationsExportOnlyOwnSafeObservations(t *testing.T) {
	f := newLifecycleTestFixture(t, 2592001)
	other := seedLifecycleUser(t, f.store.DB(), "other-browser-user", false, 1)
	digest := strings.Repeat("a", 64)
	for _, user := range []int64{7, other} {
		if _, err := f.store.DB().Exec("INSERT INTO browser_associations VALUES(?,?,2,3,4,2592002)", user, digest); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.store.DB().Exec("INSERT INTO browser_associations VALUES(7,?,0,1,2,2592000)", strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	body, err := mustNewLifecycleCoordinator(t, f.config).Export(t.Context(), 7, 2592001)
	if err != nil {
		t.Fatal(err)
	}
	var document ExportDocument
	if err = json.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	if document.SchemaVersion != 33 || len(document.BrowserAssociations) != 1 || document.BrowserAssociations[0].FirstSeen != 3 {
		t.Fatal("unsafe or incomplete browser export", document.BrowserAssociations)
	}
	if strings.Contains(string(body), digest) || strings.Contains(string(body), "browser_digest") || strings.Contains(string(body), "other-browser-user") {
		t.Fatal("export leaked association material")
	}
}
