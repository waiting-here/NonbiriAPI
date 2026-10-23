package adapters

import (
	"context"
	"database/sql"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore/testseed"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

func TestGatewayV4AccountDeletionRetainsAnonymousReportIdentity(t *testing.T) {
	fixture := openAdapterFixture(t)
	seed := beginAdapterTx(t, fixture.store.DB())
	owner := seedAdapterUser(t, seed, "gateway-owner", 0)
	other := seedAdapterUser(t, seed, "gateway-other", 0)
	exec := func(tx *sql.Tx, query string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, user := range []int64{owner.id, other.id} {
		if _, err := testseed.InsertEndpointTx(t.Context(), seed, testseed.EndpointInput{
			OwnerUserID: user, Type: connectorcontract.TypeAISDKGatewayV4, BaseURL: "https://gateway.example/v1", At: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	caseID, targetID := mustAdapterID(t, "rpc_"), mustAdapterID(t, "rpt_")
	exec(seed, `INSERT INTO report_cases(id,fingerprint,connector_type,canonical_base_url,status,progress_state,material_version,target_version,deadline,material_count,target_count,distinct_owner_count,created_at) VALUES(?,zeroblob(32),'ai-sdk-gateway-v4','https://gateway.example/v1','pending_review','complete',1,1,1000,0,0,0,1)`, caseID)
	exec(seed, `INSERT INTO report_targets(id,case_id,target_seq,source_endpoint_key_id,key_ref,owner_user_id,connector_type,canonical_base_url,state,discovered_version,created_at,updated_at) VALUES(?,?,1,1,zeroblob(32),?,'ai-sdk-gateway-v4','https://gateway.example/v1','released',1,1,1)`, targetID, caseID, owner.id)
	if err := seed.Commit(); err != nil {
		t.Fatal(err)
	}
	deletion := beginAdapterTx(t, fixture.store.DB())
	request := capturedDeleteRequest(t, deletion, owner.id, adapterTestNow+10, lifecycle.DeleteSystem)
	if err := NewLedgerAdapter().ZeroAndDeleteAccount(context.Background(), deletion, request, mustAdapterID(t, "op_")); err != nil {
		t.Fatal(err)
	}
	if err := deletion.Commit(); err != nil {
		t.Fatal(err)
	}
	var owned, foreign int
	if err := fixture.store.DB().QueryRow(`SELECT count(*) FROM endpoints WHERE user_id=?`, owner.id).Scan(&owned); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.DB().QueryRow(`SELECT count(*) FROM endpoints WHERE user_id=?`, other.id).Scan(&foreign); err != nil || owned != 0 || foreign != 1 {
		t.Fatalf("endpoint deletion owner=%d other=%d: %v", owned, foreign, err)
	}
	var remainingOwner sql.NullInt64
	var kind string
	if err := fixture.store.DB().QueryRow(`SELECT owner_user_id,connector_type FROM report_targets WHERE id=?`, targetID).Scan(&remainingOwner, &kind); err != nil || remainingOwner.Valid || kind != "ai-sdk-gateway-v4" {
		t.Fatalf("anonymous report owner=%+v connector=%q: %v", remainingOwner, kind, err)
	}
}
