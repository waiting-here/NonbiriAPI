package charityrouting

import (
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestOwnBindingDiscoveryMissingModelPreservesAuthorizationErrors(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	level := int64(6)
	owner := e.seedUser(t, false, &level)
	other := e.seedUser(t, false, &level)
	model := e.createModel(t, 'a')
	modelID, err := parsePositiveID(model.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, member, physical := e.seedCandidate(t, owner, 'a', "exact/model")
	e.seedBinding(t, modelID, member, "exact/model", 0)
	tx, err := e.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	use, err := connectionstore.LoadApprovedUseTx(t.Context(), tx, member, contract.NativeServiceChatCompletions)
	_ = tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	identity := OwnBindingDiscovery{UseID: use.UseID, ApprovedRevisionID: use.ApprovedRevisionID, SourceRevisionID: use.DiscoverySourceRevisionID, CredentialVersionID: use.Grant.CredentialVersionID, NativeService: use.NativeService}
	readGeneration := func() int64 {
		t.Helper()
		var generation int64
		if err := e.store.DB().QueryRow(`SELECT result_generation FROM discovery_source_states WHERE source_revision_id=?`, identity.SourceRevisionID).Scan(&generation); err != nil {
			t.Fatal(err)
		}
		return generation
	}
	oldGeneration := readGeneration()
	// A newer successful empty result must not borrow the previous run, the
	// donation's manual entry, or its already-persisted binding.
	e.seedAutomaticCatalog(t, physical)
	currentGeneration := readGeneration()
	if currentGeneration <= oldGeneration {
		t.Fatal("empty discovery did not advance its generation")
	}
	for _, test := range []struct {
		name       string
		actor      int64
		generation int64
		mutation   string
		want       error
	}{
		{"authorized empty success", owner, currentGeneration, "", ErrDiscoveryModelMissing},
		{"old generation", owner, oldGeneration, "", ErrNotFound},
		{"foreign owner", other, currentGeneration, "", ErrNotFound},
		{"disabled use", owner, currentGeneration, `UPDATE donation_uses SET enabled=0,revision=revision+1 WHERE id=?`, ErrNotFound},
		{"sharing withdrawn", owner, currentGeneration, `UPDATE donation_uses SET discovery_enabled=0,revision=revision+1 WHERE id=?`, ErrConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx, err := e.store.DB().BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if test.mutation != "" {
				if _, err := tx.ExecContext(t.Context(), test.mutation, use.UseID); err != nil {
					t.Fatal(err)
				}
			}
			revision, count, err := readBindingHeadTx(t.Context(), tx, modelID)
			if err != nil {
				t.Fatal(err)
			}
			if err := e.service.EnsureOwnBindingInTransaction(t.Context(), tx, test.actor, modelID, member, "exact/model", test.generation, identity); !errors.Is(err, test.want) {
				t.Fatalf("binding classification: %v, want %v", err, test.want)
			}
			afterRevision, afterCount, err := readBindingHeadTx(t.Context(), tx, modelID)
			if err != nil || afterRevision != revision || afterCount != count {
				t.Fatalf("rejected discovery changed bindings: revision=%d count=%d err=%v", afterRevision, afterCount, err)
			}
		})
	}
}
