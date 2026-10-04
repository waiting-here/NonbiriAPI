package db

import (
	"testing"
)

func TestEmbeddingRouteChecksAcceptOnlyKnownOperations(t *testing.T) {
	database := openGenerationTwoConstraintFixture(t)
	defer database.Close()
	user := hostileInsertUser(t, database, "embedding-route-owner", 0, 0)
	for i, route := range []string{"openai_embeddings", "charity_embeddings"} {
		id := hostileOIDVariant("req_", byte('X'+i), 'Q')
		hostileInsertLogicalRequest(t, database, id, user, route, 1)
		hostileInsertRequestLog(t, database, id, user, route)
	}
	hostileMustFail(t, database, `INSERT INTO logical_requests(id,user_id,route_kind,state,attempt_limit,accounting_state,settlement_destination,ledger_rows_remaining,created_at) VALUES(?,?,'unknown_embeddings','accepted',1,'none','user',?,0)`, hostileOIDVariant("req_", 'Z', 'Q'), user, hostileBlob16(1))
	hostileMustFail(t, database, `UPDATE request_logs SET route_kind='unknown_embeddings'`)
}
