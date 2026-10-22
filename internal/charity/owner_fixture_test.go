package charity

import (
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	"github.com/waiting-here/NonbiriAPI/internal/connectionstore/testseed"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/routing"
)

func (e *charityTestEnv) selfClaimInput(t *testing.T, requestID string) claim.ClaimInput {
	t.Helper()
	tx := beginTestTx(t, e.store.DB())
	defer tx.Rollback()
	result, err := tx.Exec(`INSERT INTO models(user_id,provider,model,full_name,created_at,updated_at) VALUES(?,'private','model','private/model',?,?)`, e.donorID, charityTestNow, charityTestNow)
	if err != nil {
		t.Fatal(err)
	}
	modelID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	grant, err := connectionstore.LoadSelfGrantTx(t.Context(), tx, e.donorID, e.endpointKey)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := testseed.InsertModelBindingTx(t.Context(), tx, testseed.BindingInput{ModelID: modelID, KeyID: e.endpointKey, UpstreamModelID: "upstream-model", At: charityTestNow})
	if err != nil {
		t.Fatal(err)
	}
	commitTestTx(t, tx)
	return claim.ClaimInput{RequestID: requestID, ActorUserID: e.donorID, PersonalModelID: modelID, AttemptSeq: 1, Purpose: claim.PurposeSelf,
		Candidate: claim.Candidate{EndpointID: grant.EndpointID, EndpointKeyID: grant.EndpointKeyID, ConnectorType: contract.TypeOpenAICompatible, CanonicalBaseURL: grant.CanonicalBaseURL, UpstreamModelID: "upstream-model",
			Authorization: routing.TargetAuthorization{BindingID: binding.BindingID, TargetID: binding.TargetID, TargetRevision: 1, CredentialID: grant.CredentialID, CredentialVersionID: grant.CredentialVersionID, CredentialVersion: grant.CredentialVersion, GrantRevisionID: grant.GrantRevisionID, GrantRevision: grant.GrantRevision, ConfiguredType: grant.ConfiguredType, NativeService: contract.NativeServiceChatCompletions, RerankFormat: grant.RerankFormat}}}
}
