package claim

import "testing"

func TestDecisionsPreserveLedgerDeletionAndLateCompletion(t *testing.T) {
	t.Run("personal undispatched", func(t *testing.T) { testDeletionUndispatched(t, RouteOpenAIDecisions) })
	t.Run("personal dispatched", func(t *testing.T) { testDeletionDispatched(t, RouteOpenAIDecisions) })
	t.Run("charity capacity and late callback", func(t *testing.T) { testDeletionCharity(t, RouteCharityDecisions) })
	t.Run("charity dispatch transaction", func(t *testing.T) { testCharityDispatchAdmission(t, RouteCharityDecisions) })
}
