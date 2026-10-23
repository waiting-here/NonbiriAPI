package connectionstore_test

import (
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestBoundedGrantReadsRetainMissingAuthorizationErrors(t *testing.T) {
	t.Run("self grant missing before deferred constraint check", func(t *testing.T) {
		_, tx := connectionFixture(t)
		execConnection(t, tx, "INSERT INTO endpoint_keys(id,endpoint_id,current_grant_revision_id,created_at,updated_at) VALUES(2,1,999,1,1)")
		if _, err := connectionstore.LoadSelfGrantTx(t.Context(), tx, 1, 2); !errors.Is(err, connectionstore.ErrInvariant) {
			t.Fatalf("missing grant was omitted: %v", err)
		}
		if _, err := connectionstore.LoadSelfGrantsTx(t.Context(), tx, 1, []int64{1, 2}); !errors.Is(err, connectionstore.ErrInvariant) {
			t.Fatalf("batch omitted missing grant: %v", err)
		}
	})
	t.Run("approved grant removed while current self grant remains", func(t *testing.T) {
		_, tx := connectionFixture(t)
		seedApprovedUse(t, tx, false)
		rotateSelf(t, tx)
		batch, err := connectionstore.LoadApprovedUsesTx(t.Context(), tx, []int64{99, 1, 1})
		if err != nil || len(batch) != 1 || len(batch[1]) != 1 || batch[1][0].Grant.CredentialVersion != 1 {
			t.Fatalf("approved version batch=%+v err=%v", batch, err)
		}
		execConnection(t, tx, "DELETE FROM endpoint_key_grant_revisions WHERE id=1")
		if _, err := connectionstore.LoadApprovedUseTx(t.Context(), tx, 1, connectorcontract.NativeServiceChatCompletions); !errors.Is(err, connectionstore.ErrInvariant) {
			t.Fatalf("missing approved grant was omitted: %v", err)
		}
		if _, err := connectionstore.LoadApprovedUsesTx(t.Context(), tx, []int64{1, 99}); !errors.Is(err, connectionstore.ErrInvariant) {
			t.Fatalf("batch omitted missing approved grant: %v", err)
		}
		self, err := connectionstore.LoadSelfGrantTx(t.Context(), tx, 1, 1)
		if err != nil || self.CredentialVersion != 2 {
			t.Fatalf("current self grant=%+v err=%v", self, err)
		}
	})
}
