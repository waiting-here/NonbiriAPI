package db

import "testing"

func TestGenerationTwoUserDeleteRequiresEconomicHandoff(t *testing.T) {
	t.Run("terminal request does not block", func(t *testing.T) {
		database := openGenerationTwoDDLForTest(t)
		defer database.Close()
		userID := hostileInsertUser(t, database, "delete-terminal-request", 0, 0)
		hostileInsertTerminalRequest(t, database, hostileOIDVariant("req_", 'T', 'Q'), userID,
			"openai_chat_completions", "success", 200, nil)
		hostileMustExec(t, database, `DELETE FROM users WHERE id=?`, userID)
	})

	t.Run("reserved request blocks until released", func(t *testing.T) {
		database := openGenerationTwoDDLForTest(t)
		defer database.Close()
		userID := hostileInsertUser(t, database, "delete-reserved-request", 0, 0)
		requestID := hostileOIDVariant("req_", 'R', 'Q')
		hostileMustExec(t, database, `
INSERT INTO logical_requests(
 id,user_id,route_kind,state,attempt_limit,accounting_state,account_reserved_milli,
 settlement_destination,ledger_rows_remaining,created_at
) VALUES(?,?,'openai_chat_completions','accepted',1,'reserved',1,'user',?,0)`,
			requestID, userID, hostileBlob16(1))
		hostileMustFail(t, database, `DELETE FROM users WHERE id=?`, userID)
		hostileMustExec(t, database, `DELETE FROM logical_requests WHERE id=?`, requestID)
		hostileMustExec(t, database, `DELETE FROM users WHERE id=?`, userID)
	})

	t.Run("claimed request blocks until released", func(t *testing.T) {
		database := openGenerationTwoDDLForTest(t)
		defer database.Close()
		userID := hostileInsertUser(t, database, "delete-claimed-request", 0, 0)
		endpointID := hostileInsertEndpoint(t, database, userID, "https://claimed.example/v1")
		secretID := hostileInsertSecret(t, database, "https://claimed.example/v1", 0)
		hostileInsertEndpointKey(t, database, endpointID, secretID)
		requestID := hostileOIDVariant("req_", 'C', 'Q')
		hostileInsertLogicalRequest(t, database, requestID, userID, "openai_chat_completions", 1)
		hostileMustExec(t, database, `
INSERT INTO dispatch_claims(
 id,logical_request_id,attempt_seq,purpose,secret_ref_id,claim_now,state,donor_reward_state
) VALUES(?,?,1,'self',?,0,'claimed','not_applicable')`,
			hostileOIDVariant("clm_", 'C', 'Q'), requestID, secretID)
		hostileMustFail(t, database, `DELETE FROM users WHERE id=?`, userID)
		hostileMustExec(t, database, `DELETE FROM logical_requests WHERE id=?`, requestID)
		hostileMustExec(t, database, `DELETE FROM users WHERE id=?`, userID)
	})

	t.Run("dispatched request permits explicit external handoff", func(t *testing.T) {
		database := openGenerationTwoDDLForTest(t)
		defer database.Close()
		userID := hostileInsertUser(t, database, "delete-dispatched-request", 0, 0)
		endpointID := hostileInsertEndpoint(t, database, userID, "https://dispatched.example/v1")
		secretID := hostileInsertSecret(t, database, "https://dispatched.example/v1", 0)
		hostileInsertEndpointKey(t, database, endpointID, secretID)
		requestID := hostileOIDVariant("req_", 'D', 'Q')
		hostileInsertLogicalRequest(t, database, requestID, userID, "openai_chat_completions", 1)
		hostileMustExec(t, database, `
INSERT INTO dispatch_claims(
 id,logical_request_id,attempt_seq,purpose,secret_ref_id,claim_now,state,dispatched_at,donor_reward_state
) VALUES(?,?,1,'self',?,0,'dispatched',1,'not_applicable')`,
			hostileOIDVariant("clm_", 'D', 'Q'), requestID, secretID)
		hostileMustFail(t, database, `DELETE FROM users WHERE id=?`, userID)
		hostileMustExec(t, database, `
UPDATE logical_requests SET user_id=NULL,settlement_destination='external' WHERE id=?`, requestID)
		hostileMustExec(t, database, `DELETE FROM users WHERE id=?`, userID)
	})
}

func TestGenerationTwoUserDeleteRequiresGameReservationHandoff(t *testing.T) {
	t.Run("fishing reserved", func(t *testing.T) {
		database := openGenerationTwoDDLForTest(t)
		defer database.Close()
		userID := hostileInsertUser(t, database, "delete-fishing", 0, 0)
		batchID := hostileOIDVariant("fb_", 'F', 'Q')
		hostileInsertFishingBatch(t, database, batchID, userID, hostileOIDVariant("op_", 'F', 'Q'))
		hostileMustFail(t, database, `DELETE FROM users WHERE id=?`, userID)
		hostileMustExec(t, database, `DELETE FROM game_fishing_batches WHERE id=?`, batchID)
		hostileMustExec(t, database, `DELETE FROM users WHERE id=?`, userID)
	})

	t.Run("rps queue", func(t *testing.T) {
		database := openGenerationTwoDDLForTest(t)
		defer database.Close()
		queueID, _ := hostileInsertRPSQueue(t, database, 'Q', "quick", hostileBlob16(1), hostileBlob16(1))
		var userID int64
		if err := database.QueryRow(`SELECT user_id FROM game_rps_queue WHERE id=?`, queueID).Scan(&userID); err != nil {
			t.Fatalf("read queued user: %v", err)
		}
		hostileMustFail(t, database, `DELETE FROM users WHERE id=?`, userID)
		hostileMustExec(t, database, `DELETE FROM game_rps_queue WHERE id=?`, queueID)
		hostileMustExec(t, database, `DELETE FROM users WHERE id=?`, userID)
	})

	for _, state := range []string{"started", "terminal_processing"} {
		t.Run("rps "+state, func(t *testing.T) {
			database := openGenerationTwoDDLForTest(t)
			defer database.Close()
			userID := hostileInsertUser(t, database, "delete-rps-"+state, 0, 0)
			sessionID := hostileOIDVariant("rps_", state[0], 'Q')
			accountID := hostileInsertRPSAccount(t, database, sessionID)
			phase := "gesture"
			if state == "terminal_processing" {
				phase = "terminal_processing"
			}
			hostileInsertRPSSession(t, database, sessionID, phase, state, accountID)
			hostileInsertRPSSeat(t, database, sessionID, 0, userID, nil, nil, nil, nil, nil, "active")
			hostileMustFail(t, database, `DELETE FROM users WHERE id=?`, userID)
			hostileMustExec(t, database, `
UPDATE game_rps_seats SET user_id=NULL,deletion_state='deletion_pending'
WHERE session_id=? AND seat_no=0 AND user_id=?`, sessionID, userID)
			hostileMustExec(t, database, `DELETE FROM users WHERE id=?`, userID)
		})
	}
}
