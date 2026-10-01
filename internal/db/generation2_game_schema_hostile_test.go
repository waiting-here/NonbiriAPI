package db

import (
	"strings"
	"testing"
)

func TestGenerationTwoRPSUserSlotCardinality(t *testing.T) {
	db := openGenerationTwoConstraintFixture(t)
	defer db.Close()

	sessionID := hostileOIDVariant("rps_", 'S', 'Q')
	accountID := hostileInsertRPSAccount(t, db, sessionID)
	hostileInsertRPSSession(t, db, sessionID, "gesture", "started", accountID)
	for _, label := range []string{"rps-slot-one", "rps-slot-two", "rps-slot-three"} {
		userID := hostileInsertUser(t, db, label, 0, 0)
		hostileMustExec(t, db,
			`INSERT INTO game_rps_user_slots(user_id,queue_id,session_id,created_at) VALUES(?,NULL,?,0)`,
			userID, sessionID)
	}
	var sessionSlots int
	if err := db.QueryRow(`SELECT COUNT(*) FROM game_rps_user_slots WHERE session_id=?`, sessionID).Scan(&sessionSlots); err != nil {
		t.Fatalf("count RPS session slots: %v", err)
	}
	if sessionSlots != 3 {
		t.Fatalf("RPS session has %d user slots, want 3", sessionSlots)
	}

	queueID, _ := hostileInsertRPSQueue(t, db, 'U', "quick", hostileBlob16(1), hostileBlob16(1))
	var queuedUserID int64
	if err := db.QueryRow(`SELECT user_id FROM game_rps_queue WHERE id=?`, queueID).Scan(&queuedUserID); err != nil {
		t.Fatalf("read queued user: %v", err)
	}
	hostileMustExec(t, db,
		`INSERT INTO game_rps_user_slots(user_id,queue_id,session_id,created_at) VALUES(?,?,NULL,0)`,
		queuedUserID, queueID)
	secondQueuedUserID := hostileInsertUser(t, db, "rps-slot-duplicate-queue", 0, 0)
	hostileMustFail(t, db,
		`INSERT INTO game_rps_user_slots(user_id,queue_id,session_id,created_at) VALUES(?,?,NULL,0)`,
		secondQueuedUserID, queueID)
}

func TestGenerationTwoHostileRPSPhaseSeatEnvelope(t *testing.T) {
	db := openGenerationTwoConstraintFixture(t)
	uid := hostileInsertUser(t, db, "rps", 0, 0)
	zero := hostileBlob16(0)
	one := hostileBlob16(1)

	validPhases := []string{
		"gesture",
		"dealer_raise",
		"followers",
		"paid_pool_gesture",
		"free_pool_gesture",
		"ultimate_gesture",
	}
	phaseIDs := make(map[string]string, len(validPhases))
	for i, phase := range validPhases {
		id := hostileOIDVariant("rps_", byte('a'+i), 'Q')
		phaseIDs[phase] = id
		accountID := hostileInsertRPSAccount(t, db, id)
		hostileInsertRPSSession(t, db, id, phase, "started", accountID)
	}
	terminalID := hostileOIDVariant("rps_", 't', 'Q')
	terminalAccount := hostileInsertRPSAccount(t, db, terminalID)
	hostileInsertRPSSession(t, db, terminalID, "terminal_processing", "terminal_processing", terminalAccount)

	// Terminal-processing and the retained summary share the same closed
	// reason set.  Every one of the six frozen reasons is a legal terminal
	// fact, while summary deletion is exactly terminal+30d and cannot drift.
	terminalReasons := []string{
		"quick_resolved",
		"standard_round_limit",
		"standard_insufficient_balance",
		"deathmatch_balance_exhausted",
		"ultimate_resolved",
		"free_tie_limit",
	}
	for i, reason := range terminalReasons {
		id := hostileOIDVariant("rps_", byte('u'+i), 'Q')
		accountID := hostileInsertRPSAccount(t, db, id)
		hostileInsertRPSSessionWithReason(t, db, id, "terminal_processing", "terminal_processing", accountID, reason)
		hostileMustExec(t, db, `
INSERT INTO game_rps_summaries(
 session_id,mode,rules_version,base_milli,platform_bp,welfare_bp,thursday_bp,
 started_at,terminal_at,terminal_reason,base_round_count,paid_tie_count,free_tie_count,
 total_timeout_count,total_rock_count,total_scissors_count,total_paper_count,
 platform_total,welfare_total,thursday_total,delete_at
) VALUES(?, 'quick',1,5,0,0,0,100,200,?,?,?,?,?,?,?,?,?,?,?,2592200)`,
			id, reason, zero, zero, zero, zero, zero, zero, zero, zero, zero, zero)
	}
	hostileMustFail(t, db, `UPDATE game_rps_sessions SET terminal_reason='not_closed' WHERE id=?`, terminalID)
	invalidSummaryID := hostileOIDVariant("rps_", '0', 'Q')
	hostileMustFail(t, db, `
INSERT INTO game_rps_summaries(
 session_id,mode,rules_version,base_milli,platform_bp,welfare_bp,thursday_bp,
 started_at,terminal_at,terminal_reason,base_round_count,paid_tie_count,free_tie_count,
 total_timeout_count,total_rock_count,total_scissors_count,total_paper_count,
 platform_total,welfare_total,thursday_total,delete_at
) VALUES(?, 'quick',1,5,0,0,0,100,200,'quick_resolved',?,?,?,?,?,?,?,?,?,?,?,2592199)`,
		invalidSummaryID, zero, zero, zero, zero, zero, zero, zero, zero, zero, zero)
	hostileMustFail(t, db, `UPDATE game_rps_summaries SET delete_at=2592199 WHERE session_id=?`, hostileOIDVariant("rps_", 'u', 'Q'))

	for _, phase := range validPhases {
		id := phaseIDs[phase]
		if phase == "free_pool_gesture" {
			hostileMustExec(t, db, `UPDATE game_rps_sessions SET free_pool_streak=?,reminder_state='active' WHERE id=?`, hostileBlob16(3), id)
			hostileMustFail(t, db, `UPDATE game_rps_sessions SET free_pool_streak=? WHERE id=?`, hostileBlob16(2), id)
			hostileMustFail(t, db, `UPDATE game_rps_sessions SET free_pool_streak=? WHERE id=?`, hostileBlob16(6), id)
		}
	}

	gestureID := phaseIDs["gesture"]
	hostileMustFail(t, db, `UPDATE game_rps_sessions SET rules_version=0 WHERE id=?`, gestureID)
	hostileMustFail(t, db, `UPDATE game_rps_sessions SET phase='reveal' WHERE id=?`, gestureID)
	hostileMustFail(t, db, `UPDATE game_rps_sessions SET current_plan_multiplier=NULL WHERE id=?`, gestureID)
	hostileMustFail(t, db, `UPDATE game_rps_sessions SET pool_base_multiplier=? WHERE id=?`, one, gestureID)
	hostileMustFail(t, db, `UPDATE game_rps_sessions SET reminder_state='active' WHERE id=?`, gestureID)

	// Each gesture phase accepts both a canonical pair on INSERT and the first
	// null-to-pair binding on UPDATE. The two phase sequences are the current
	// session sequence; a half pair or a different/zero sequence is rejected.
	gesturePhases := []string{"gesture", "paid_pool_gesture", "free_pool_gesture", "ultimate_gesture"}
	gestureEnvelopeLengths := []int{33, 34, 37, 33}
	for i, phase := range gesturePhases {
		id := phaseIDs[phase]
		directUser := uid
		if phase != "gesture" {
			directUser = hostileInsertUser(t, db, "rps-direct-gesture-"+phase, 0, 0)
		}
		hostileInsertRPSSeat(t, db, id, 0, directUser, hostileEnvelope(gestureEnvelopeLengths[i], 0x01), one, nil, nil, nil, "active")
		bindingUser := hostileInsertUser(t, db, "rps-bound-gesture-"+phase, 0, 0)
		hostileInsertRPSSeat(t, db, id, 1, bindingUser, nil, nil, nil, nil, nil, "active")
		hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=? WHERE session_id=? AND seat_no=1`, hostileEnvelope(33, 0x01), id)
		hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=?,current_gesture_phase_seq=?,last_action_phase_seq=? WHERE session_id=? AND seat_no=1`, hostileEnvelope(33, 0x01), "1", one, id)
		hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=?,current_gesture_phase_seq=?,last_action_phase_seq=? WHERE session_id=? AND seat_no=1`, hostileEnvelope(33, 0x01), hostileBlob16(0), hostileBlob16(0), id)
		hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=?,current_gesture_phase_seq=?,last_action_phase_seq=? WHERE session_id=? AND seat_no=1`, hostileEnvelope(33, 0x01), hostileBlob16(2), hostileBlob16(2), id)
		hostileMustExec(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=?,current_gesture_phase_seq=?,last_action_phase_seq=? WHERE session_id=? AND seat_no=1`, hostileEnvelope(33, 0x01), one, one, id)
	}
	badPairUser := hostileInsertUser(t, db, "rps-half-pair", 0, 0)
	hostileMustFailRPSSeatWithActions(t, db, gestureID, 2, badPairUser, hostileEnvelope(33, 0x01), nil, nil, one, "active")
	hostileInsertRPSSeat(t, db, gestureID, 2, badPairUser, hostileEnvelope(37, 0x01), one, nil, nil, nil, "active")

	// A bound pair is immutable in place. It can only be consumed together,
	// after the session has advanced beyond the gesture's dedicated AAD phase.
	hostileMustExec(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=? WHERE session_id=? AND seat_no=0`, hostileEnvelope(33, 0x01), gestureID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=? WHERE session_id=? AND seat_no=0`, hostileEnvelope(34, 0x01), gestureID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=? WHERE session_id=? AND seat_no=0`, hostileEnvelope(37, 0x01), gestureID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=? WHERE session_id=? AND seat_no=0`, hostileEnvelope(33, 0x00), gestureID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=? WHERE session_id=? AND seat_no=0`, make([]byte, 32), gestureID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=? WHERE session_id=? AND seat_no=0`, make([]byte, 38), gestureID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=NULL WHERE session_id=? AND seat_no=0`, gestureID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=NULL,current_gesture_phase_seq=NULL,last_action_phase_seq=NULL WHERE session_id=? AND seat_no=0`, gestureID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET last_action_phase_seq=NULL WHERE session_id=? AND seat_no=0`, gestureID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_phase_seq=? WHERE session_id=? AND seat_no=0`, hostileBlob16(2), gestureID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET last_action_phase_seq=? WHERE session_id=? AND seat_no=0`, hostileBlob16(0), gestureID)

	// dealer_raise and followers INSERTs carry the original non-zero gesture
	// phase, which must be strictly older than the current session phase.
	two := hostileBlob16(2)
	three := hostileBlob16(3)
	four := hostileBlob16(4)
	hostileMustExec(t, db, `UPDATE game_rps_sessions SET phase='dealer_raise',revision=?,phase_seq=? WHERE id=?`, two, two, gestureID)
	hostileMustExec(t, db, `UPDATE game_rps_seats SET last_action_phase_seq=NULL WHERE session_id=?`, gestureID)
	var crossedDealerCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM game_rps_seats WHERE session_id=? AND current_gesture_envelope IS NOT NULL AND current_gesture_phase_seq=? AND follower_action IS NULL AND last_action_phase_seq IS NULL`, gestureID, one).Scan(&crossedDealerCount); err != nil {
		t.Fatalf("count dealer-retained RPS pairs: %v", err)
	}
	if crossedDealerCount != 3 {
		t.Fatalf("dealer transition retained %d gesture pairs, want 3", crossedDealerCount)
	}
	hostileMustExec(t, db, `UPDATE game_rps_sessions SET phase='followers',revision=?,phase_seq=?,dealer_raise=? WHERE id=?`, three, three, one, gestureID)
	hostileMustExec(t, db, `UPDATE game_rps_seats SET follower_action='call',last_action_phase_seq=? WHERE session_id=? AND seat_no=1`, three, gestureID)
	hostileMustExec(t, db, `UPDATE game_rps_seats SET follower_action='surrender',last_action_phase_seq=? WHERE session_id=? AND seat_no=2`, three, gestureID)
	hostileMustExec(t, db, `UPDATE game_rps_sessions SET phase='gesture',revision=?,phase_seq=?,dealer_raise=NULL WHERE id=?`, four, four, gestureID)
	hostileMustExec(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=NULL,current_gesture_phase_seq=NULL,follower_action=NULL,last_action_phase_seq=NULL WHERE session_id=?`, gestureID)

	dealerRaiseID := phaseIDs["dealer_raise"]
	hostileMustExec(t, db, `UPDATE game_rps_sessions SET revision=?,phase_seq=? WHERE id=?`, two, two, dealerRaiseID)
	dealerRaiseUser := hostileInsertUser(t, db, "rps-dealer-raise", 0, 0)
	hostileInsertRPSSeatWithActions(t, db, dealerRaiseID, 0, dealerRaiseUser, hostileEnvelope(33, 0x01), one, nil, nil, nil, nil, nil, "active")
	hostileMustFailRPSSeatWithActions(t, db, dealerRaiseID, 1, hostileInsertUser(t, db, "rps-dealer-current-phase", 0, 0), hostileEnvelope(33, 0x01), two, nil, nil, "active")
	hostileMustFailRPSSeatWithActions(t, db, dealerRaiseID, 1, hostileInsertUser(t, db, "rps-dealer-zero-phase", 0, 0), hostileEnvelope(33, 0x01), zero, nil, nil, "active")
	hostileMustFailRPSSeatWithActions(t, db, dealerRaiseID, 1, hostileInsertUser(t, db, "rps-dealer-last-action", 0, 0), hostileEnvelope(33, 0x01), one, nil, one, "active")
	hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=? WHERE session_id=? AND seat_no=0`, hostileEnvelope(34, 0x01), dealerRaiseID)

	followersID := phaseIDs["followers"]
	hostileMustExec(t, db, `UPDATE game_rps_sessions SET revision=?,phase_seq=? WHERE id=?`, three, three, followersID)
	followerDealer := hostileInsertUser(t, db, "rps-follower-dealer", 0, 0)
	followerOne := hostileInsertUser(t, db, "rps-follower-one", 0, 0)
	followerTwo := hostileInsertUser(t, db, "rps-follower-two", 0, 0)
	hostileMustFailRPSSeatWithActions(t, db, followersID, 0, followerDealer, hostileEnvelope(33, 0x01), one, "call", three, "active")
	hostileInsertRPSSeatWithActions(t, db, followersID, 0, followerDealer, hostileEnvelope(33, 0x01), one, nil, nil, nil, nil, nil, "active")
	hostileMustFailRPSSeatWithActions(t, db, followersID, 1, followerOne, hostileEnvelope(34, 0x01), three, nil, nil, "active")
	hostileMustFailRPSSeatWithActions(t, db, followersID, 1, followerOne, hostileEnvelope(34, 0x01), one, "call", nil, "active")
	hostileInsertRPSSeatWithActions(t, db, followersID, 1, followerOne, hostileEnvelope(34, 0x01), one, nil, nil, nil, nil, nil, "active")
	hostileInsertRPSSeatWithActions(t, db, followersID, 2, followerTwo, hostileEnvelope(37, 0x01), one, "surrender", three, nil, nil, nil, "active")
	hostileMustExec(t, db, `UPDATE game_rps_seats SET follower_action='call',last_action_phase_seq=? WHERE session_id=? AND seat_no=1`, three, followersID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET last_action_phase_seq=NULL WHERE session_id=? AND seat_no=1`, followersID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET follower_action='surrender',last_action_phase_seq=? WHERE session_id=? AND seat_no=1`, three, followersID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=NULL,current_gesture_phase_seq=NULL,follower_action=NULL,last_action_phase_seq=NULL WHERE session_id=? AND seat_no=2`, followersID)

	// De-identification retains both the gesture pair and an already submitted
	// follower action. It cannot use deletion as an early-consumption path.
	hostileMustExec(t, db, `UPDATE game_rps_seats SET user_id=NULL,deletion_state='deletion_pending' WHERE session_id=? AND seat_no=2`, followersID)
	var retainedEnvelope, retainedGesturePhase, retainedFollower, retainedLast any
	if err := db.QueryRow(`SELECT current_gesture_envelope,current_gesture_phase_seq,follower_action,last_action_phase_seq FROM game_rps_seats WHERE session_id=? AND seat_no=2`, followersID).Scan(&retainedEnvelope, &retainedGesturePhase, &retainedFollower, &retainedLast); err != nil {
		t.Fatalf("read retained deleted-seat actions: %v", err)
	}
	if retainedEnvelope == nil || retainedGesturePhase == nil || retainedFollower != "surrender" || retainedLast == nil {
		t.Fatal("de-identification did not retain submitted RPS recovery inputs")
	}

	// Once the session reaches the next stable gesture phase, the reducer may
	// clear the pair and all current actions together. Pair halves and follower
	// remnants remain invalid even after that phase transition.
	hostileMustExec(t, db, `UPDATE game_rps_sessions SET phase='gesture',revision=?,phase_seq=?,dealer_raise=NULL WHERE id=?`, four, four, followersID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=NULL WHERE session_id=? AND seat_no=2`, followersID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=NULL,current_gesture_phase_seq=NULL WHERE session_id=? AND seat_no=2`, followersID)
	hostileMustExec(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=NULL,current_gesture_phase_seq=NULL,follower_action=NULL,last_action_phase_seq=NULL WHERE session_id=?`, followersID)
	var retainedCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM game_rps_seats WHERE session_id=? AND (current_gesture_envelope IS NOT NULL OR current_gesture_phase_seq IS NOT NULL OR follower_action IS NOT NULL OR last_action_phase_seq IS NOT NULL)`, followersID).Scan(&retainedCount); err != nil {
		t.Fatalf("count cleared RPS actions: %v", err)
	}
	if retainedCount != 0 {
		t.Fatalf("legal final clear left %d RPS action rows", retainedCount)
	}

	// terminal_processing likewise rejects retained recovery inputs on any
	// seat write. Clearing the complete pair after the session transition may
	// be combined with freezing the terminal return facts.
	terminalClearID := hostileOIDVariant("rps_", 'T', 'Q')
	terminalClearAccount := hostileInsertRPSAccount(t, db, terminalClearID)
	hostileInsertRPSSession(t, db, terminalClearID, "gesture", "started", terminalClearAccount)
	terminalClearUser := hostileInsertUser(t, db, "rps-terminal-clear", 0, 0)
	hostileInsertRPSSeat(t, db, terminalClearID, 0, terminalClearUser, hostileEnvelope(33, 0x01), one, nil, nil, nil, "active")
	hostileMustExec(t, db, `
UPDATE game_rps_sessions
SET state='terminal_processing',phase='terminal_processing',revision=?,phase_seq=?,
    pool_base_multiplier=NULL,current_plan_multiplier=NULL,dealer_raise=NULL,phase_deadline=NULL,
    terminal_operation_id=?,terminal_reason='quick_resolved'
WHERE id=?`, two, two, hostileOIDVariant("op_", 'T', 'Q'), terminalClearID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET terminal_return=?,wallet_net_sign=1,wallet_net_mag=? WHERE session_id=? AND seat_no=0`, one, one, terminalClearID)
	hostileMustExec(t, db, `
UPDATE game_rps_seats
SET current_gesture_envelope=NULL,current_gesture_phase_seq=NULL,follower_action=NULL,last_action_phase_seq=NULL,
    terminal_return=?,wallet_net_sign=1,wallet_net_mag=?
WHERE session_id=? AND seat_no=0`, one, one, terminalClearID)
	var terminalEnvelope, terminalGesturePhase any
	if err := db.QueryRow(`SELECT current_gesture_envelope,current_gesture_phase_seq FROM game_rps_seats WHERE session_id=? AND seat_no=0`, terminalClearID).Scan(&terminalEnvelope, &terminalGesturePhase); err != nil {
		t.Fatalf("read terminal-cleared gesture pair: %v", err)
	}
	if terminalEnvelope != nil || terminalGesturePhase != nil {
		t.Fatal("terminal transition retained an RPS gesture pair")
	}
	hostileMustFailRPSSeatWithActions(t, db, terminalID, 1, nil, hostileEnvelope(33, 0x01), one, nil, nil, "deletion_pending")

	// Terminal return and wallet net are an all-or-nothing pair and can only
	// be frozen while the session is in terminal_processing.
	hostileMustFail(t, db, `UPDATE game_rps_seats SET terminal_return=? WHERE session_id=? AND seat_no=0`, one, gestureID)
	hostileInsertRPSSeat(t, db, terminalID, 0, nil, nil, nil, one, 1, one, "deletion_pending")
	hostileMustFail(t, db, `UPDATE game_rps_seats SET wallet_net_sign=0 WHERE session_id=? AND seat_no=0`, terminalID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET wallet_net_mag=? WHERE session_id=? AND seat_no=0`, hostileHigh128(), terminalID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET follower_action='call',last_action_phase_seq=? WHERE session_id=? AND seat_no=0`, one, terminalID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET terminal_return=?,wallet_net_sign=1,wallet_net_mag=? WHERE session_id=? AND seat_no=0`, hostileBlob16(0), one, terminalID)

	// A started session cannot be silently changed into a terminal row without
	// terminal operation/reason metadata, and its phase deadline remains in the
	// UTC range.
	hostileMustFail(t, db, `UPDATE game_rps_sessions SET state='terminal_processing',phase='terminal_processing' WHERE id=?`, gestureID)
	hostileMustFail(t, db, `UPDATE game_rps_sessions SET phase_deadline=? WHERE id=?`, hostileTimeMax+1, gestureID)

	leaseID := hostileOID("gle_")
	hostileMustExec(t, db, `
INSERT INTO game_online_leases(session_id,user_id,lease_id,health_epoch,expires_at,last_renewed_at)
VALUES(?,?,?,0,?,0)`, gestureID, uid, leaseID, hostileTimeMax)
	hostileMustFail(t, db, `UPDATE game_online_leases SET expires_at=? WHERE session_id=? AND user_id=? AND lease_id=?`, hostileTimeMax+1, gestureID, uid, leaseID)
	hostileMustFail(t, db, `UPDATE game_online_leases SET expires_at=-1 WHERE session_id=? AND user_id=? AND lease_id=?`, gestureID, uid, leaseID)
}

func TestGenerationTwoHostileGameParentAndProjectionMatrices(t *testing.T) {
	db := openGenerationTwoConstraintFixture(t)
	zero := hostileBlob16(0)
	one := hostileBlob16(1)

	// Fishing best keeps a denormalized safe snapshot.  Deleting the outcome
	// parent must clear both composite-FK columns together and leave that
	// snapshot intact; a writer cannot manually clear either half or rewrite
	// the linked snapshot while the outcome still exists.
	fishingUser := hostileInsertUser(t, db, "game-fishing-best", 0, 0)
	fishingBatchID := hostileOID("fb_")
	fishingOperationID := hostileOID("op_")
	hostileInsertFishingBatch(t, db, fishingBatchID, fishingUser, fishingOperationID)
	hostileMustExec(t, db, `
INSERT INTO game_fishing_outcomes(batch_id,ordinal,species_key,tier,size_cm,payout_milli)
VALUES(?,0,'whitebait','small',5,1)`, fishingBatchID)
	secondFishingUser := hostileInsertUser(t, db, "game-fishing-cross-batch", 0, 0)
	secondFishingBatchID := hostileOIDVariant("fb_", 'X', 'Q')
	hostileInsertFishingBatch(t, db, secondFishingBatchID, secondFishingUser, hostileOIDVariant("op_", 'X', 'Q'))
	// Outcome identity is bound to its original reserved batch.  A valid
	// ordinal in another batch must not turn an UPDATE into a cross-batch move.
	hostileMustFail(t, db, `UPDATE game_fishing_outcomes SET batch_id=? WHERE batch_id=? AND ordinal=0`, secondFishingBatchID, fishingBatchID)
	hostileMustExec(t, db, `
INSERT INTO game_fishing_best(user_id,batch_id,ordinal,species_key,tier,size_cm,caught_at,public_tie_key)
VALUES(?,?,0,'whitebait','small',5,0,?)`, fishingUser, fishingBatchID, hostileBlob32(1))
	hostileMustFail(t, db, `UPDATE game_fishing_best SET species_key='boot' WHERE user_id=?`, fishingUser)
	hostileMustFail(t, db, `UPDATE game_fishing_best SET batch_id=NULL WHERE user_id=?`, fishingUser)
	hostileMustFail(t, db, `UPDATE game_fishing_best SET ordinal=NULL WHERE user_id=?`, fishingUser)
	hostileMustFail(t, db, `UPDATE game_fishing_best SET batch_id=NULL,ordinal=NULL WHERE user_id=?`, fishingUser)
	hostileMustExec(t, db, `DELETE FROM game_fishing_outcomes WHERE batch_id=? AND ordinal=0`, fishingBatchID)
	var bestBatch, bestOrdinal, bestSpecies, bestTier, bestSize any
	if err := db.QueryRow(`SELECT batch_id,ordinal,species_key,tier,size_cm FROM game_fishing_best WHERE user_id=?`, fishingUser).Scan(&bestBatch, &bestOrdinal, &bestSpecies, &bestTier, &bestSize); err != nil {
		t.Fatal(err)
	}
	if bestBatch != nil || bestOrdinal != nil || bestSpecies != "whitebait" || bestTier != "small" || bestSize != int64(5) {
		t.Fatalf("fishing best parent delete did not preserve composite/null snapshot: batch=%v ordinal=%v species=%v tier=%v size=%v", bestBatch, bestOrdinal, bestSpecies, bestTier, bestSize)
	}
	// The composite link may be gone, but the denormalized snapshot remains a
	// closed, independently validated tier/species/size fact.  It must not be
	// possible to smuggle an illegal combination through the now-unlinked row.
	hostileMustFail(t, db, `UPDATE game_fishing_best SET species_key='boot' WHERE user_id=?`, fishingUser)
	hostileMustFail(t, db, `UPDATE game_fishing_best SET tier='junk' WHERE user_id=?`, fishingUser)
	hostileMustFail(t, db, `UPDATE game_fishing_best SET size_cm=0 WHERE user_id=?`, fishingUser)
	hostileMustExec(t, db, `
INSERT INTO game_fishing_outcomes(batch_id,ordinal,species_key,tier,size_cm,payout_milli)
VALUES(?,0,'whitebait','small',5,1)`, fishingBatchID)
	hostileMustExec(t, db, `UPDATE game_fishing_best SET batch_id=?,ordinal=0 WHERE user_id=?`, fishingBatchID, fishingUser)
	hostileMustExec(t, db, `UPDATE game_fishing_batches SET state='committed',ledger_rows_remaining=?,next_attempt_at=NULL,settled_at=1,revealed_at=1 WHERE id=?`, zero, fishingBatchID)
	hostileMustFail(t, db, `UPDATE game_fishing_best SET batch_id=NULL WHERE user_id=?`, fishingUser)
	hostileMustFail(t, db, `UPDATE game_fishing_best SET ordinal=NULL WHERE user_id=?`, fishingUser)

	// A fishing batch is born reserved.  Terminal rows are reachable only by
	// one atomic reserved -> committed/released transition after the outcome
	// facts have been materialized; direct terminal INSERTs and terminal fact
	// rewrites must not create or alter ledger obligations.
	committedUser := hostileInsertUser(t, db, "fishing-committed", 0, 0)
	committedBatchID := hostileOIDVariant("fb_", 'C', 'Q')
	committedOperationID := hostileOIDVariant("op_", 'C', 'Q')
	hostileMustFail(t, db, `
INSERT INTO game_fishing_batches(
 id,user_id,bait,count,unit_price_milli,entry_total_milli,payout_total_milli,
 operation_id,request_hash,state,ledger_rows_remaining,attempt_count,next_attempt_at,
 retry_exhausted,created_at,settled_at,revealed_at
) VALUES(?,?, 'worm',1,1,1,1,?,?, 'committed',?,0,NULL,0,0,1,1)`,
		committedBatchID, committedUser, committedOperationID, hostileBlob32(201), hostileBlob16(0))
	hostileInsertFishingBatch(t, db, committedBatchID, committedUser, committedOperationID)
	hostileMustExec(t, db, `
INSERT INTO game_fishing_outcomes(batch_id,ordinal,species_key,tier,size_cm,payout_milli)
VALUES(?,0,'whitebait','small',5,1)`, committedBatchID)
	hostileMustExec(t, db, `UPDATE game_fishing_batches SET state='committed',ledger_rows_remaining=?,next_attempt_at=NULL,settled_at=1,revealed_at=1 WHERE id=?`, hostileBlob16(0), committedBatchID)
	hostileMustFail(t, db, `UPDATE game_fishing_batches SET payout_total_milli=2 WHERE id=?`, committedBatchID)
	hostileMustFail(t, db, `UPDATE game_fishing_batches SET settled_at=2 WHERE id=?`, committedBatchID)
	hostileMustFail(t, db, `INSERT INTO game_fishing_outcomes(batch_id,ordinal,species_key,tier,size_cm,payout_milli) VALUES(?,0,'whitebait','small',5,1)`, committedBatchID)
	hostileMustFail(t, db, `UPDATE game_fishing_outcomes SET payout_milli=2 WHERE batch_id=? AND ordinal=0`, committedBatchID)
	hostileMustFail(t, db, `DELETE FROM game_fishing_outcomes WHERE batch_id=? AND ordinal=0`, committedBatchID)

	releasedUser := hostileInsertUser(t, db, "fishing-released", 0, 0)
	releasedBatchID := hostileOIDVariant("fb_", 'R', 'Q')
	releasedOperationID := hostileOIDVariant("op_", 'R', 'Q')
	hostileMustFail(t, db, `
INSERT INTO game_fishing_batches(
 id,user_id,bait,count,unit_price_milli,entry_total_milli,payout_total_milli,
 operation_id,request_hash,state,ledger_rows_remaining,attempt_count,next_attempt_at,
 retry_exhausted,created_at,settled_at
) VALUES(?,?, 'worm',1,1,1,0,?,?, 'released',?,0,NULL,0,0,1)`,
		releasedBatchID, releasedUser, releasedOperationID, hostileBlob32(202), hostileBlob16(0))
	hostileInsertFishingBatch(t, db, releasedBatchID, releasedUser, releasedOperationID)
	hostileMustExec(t, db, `UPDATE game_fishing_batches SET state='released',ledger_rows_remaining=?,next_attempt_at=NULL,settled_at=1 WHERE id=?`, zero, releasedBatchID)
	hostileMustFail(t, db, `UPDATE game_fishing_batches SET state='reserved' WHERE id=?`, releasedBatchID)
	hostileMustFail(t, db, `INSERT INTO game_fishing_outcomes(batch_id,ordinal,species_key,tier,size_cm,payout_milli) VALUES(?,0,'whitebait','small',5,1)`, releasedBatchID)

	// A live RPS queue reserves a positive amount and exactly one terminal
	// release/start row.  Both zero-headroom and zero-reserve rows are hostile.
	queueID, _ := hostileInsertRPSQueue(t, db, 'A', "quick", one, one)
	var queueReserved, queueRemaining []byte
	if err := db.QueryRow(`SELECT reserved,ledger_rows_remaining FROM game_rps_queue WHERE id=?`, queueID).Scan(&queueReserved, &queueRemaining); err != nil {
		t.Fatal(err)
	}
	if string(queueReserved) == string(zero) || string(queueRemaining) != string(one) {
		t.Fatalf("live queue headroom mismatch: reserved=%x remaining=%x", queueReserved, queueRemaining)
	}
	insertQueue := func(marker byte, mode string, reserved, remaining []byte) {
		t.Helper()
		id := hostileOIDVariant("rpsq_", marker, 'Q')
		accountID := hostileInsertAccount(t, db, "platform", nil, "rps-queue:"+id, 0, zero, 0)
		userID := hostileInsertUser(t, db, "queue-invalid-"+string(marker), 0, 0)
		hostileMustFail(t, db, `
INSERT INTO game_rps_queue(
 id,user_id,account_id,mode,revision,reservation_operation_id,reserved,
 ledger_rows_remaining,device_token_hash,source_ip_hash,deadline,created_at
) VALUES(?,?,? ,?, ?,? ,?, ?, ?, ?,0,0)`, id, userID, accountID, mode, one,
			hostileOIDVariant("op_", marker, 'Q'), reserved, remaining, hostileBlob32(marker), hostileBlob32(marker+1))
	}
	insertQueue('B', "quick", zero, one)
	insertQueue('C', "quick", one, zero)

	// Mode-specific base boundaries and the three-way cut sum are checked on
	// sessions, independently of the phase economy matrix.
	quickID := hostileOIDVariant("rps_", 'b', 'Q')
	quickAccount := hostileInsertRPSAccount(t, db, quickID)
	hostileInsertRPSSession(t, db, quickID, "gesture", "started", quickAccount)
	hostileMustExec(t, db, `UPDATE game_rps_sessions SET base_milli=? WHERE id=?`, int64(9000000000000000), quickID)
	hostileMustFail(t, db, `UPDATE game_rps_sessions SET base_milli=? WHERE id=?`, int64(9000000000000001), quickID)
	hostileMustFail(t, db, `UPDATE game_rps_sessions SET base_milli=0 WHERE id=?`, quickID)
	standardID := hostileOIDVariant("rps_", 'c', 'Q')
	standardAccount := hostileInsertRPSAccount(t, db, standardID)
	hostileInsertRPSSession(t, db, standardID, "gesture", "started", standardAccount)
	hostileMustExec(t, db, `UPDATE game_rps_sessions SET mode='standard' WHERE id=?`, standardID)
	hostileMustExec(t, db, `UPDATE game_rps_sessions SET base_milli=? WHERE id=?`, int64(1800000000000000), standardID)
	hostileMustFail(t, db, `UPDATE game_rps_sessions SET base_milli=? WHERE id=?`, int64(1800000000000001), standardID)
	deathmatchID := hostileOIDVariant("rps_", 'd', 'Q')
	deathmatchAccount := hostileInsertRPSAccount(t, db, deathmatchID)
	hostileInsertRPSSession(t, db, deathmatchID, "gesture", "started", deathmatchAccount)
	hostileMustExec(t, db, `UPDATE game_rps_sessions SET mode='deathmatch' WHERE id=?`, deathmatchID)
	hostileMustExec(t, db, `UPDATE game_rps_sessions SET base_milli=? WHERE id=?`, int64(9000000000000000), deathmatchID)
	hostileMustFail(t, db, `UPDATE game_rps_sessions SET base_milli=? WHERE id=?`, int64(9000000000000001), deathmatchID)
	hostileMustExec(t, db, `UPDATE game_rps_sessions SET platform_bp=9999,welfare_bp=0,thursday_bp=0 WHERE id=?`, quickID)
	hostileMustFail(t, db, `UPDATE game_rps_sessions SET platform_bp=9999,welfare_bp=1,thursday_bp=0 WHERE id=?`, quickID)
	hostileMustFail(t, db, `UPDATE game_rps_sessions SET platform_bp=5000,welfare_bp=4999,thursday_bp=1 WHERE id=?`, quickID)
	hostileMustFail(t, db, `UPDATE game_rps_sessions SET dealer_raise=? WHERE id=?`, one, quickID)

	// The phase/sequence action matrix is closed: gesture seats cannot carry a
	// follower action, followers cannot carry a gesture envelope, and an
	// action's sequence must equal the session phase sequence.
	gestureUser := hostileInsertUser(t, db, "game-gesture-seat", 0, 0)
	hostileInsertRPSSeat(t, db, quickID, 0, gestureUser, hostileEnvelope(33, 0x01), one, nil, nil, nil, "active")
	hostileMustFail(t, db, `UPDATE game_rps_seats SET follower_action='call',last_action_phase_seq=? WHERE session_id=? AND seat_no=0`, one, quickID)
	followerID := hostileOIDVariant("rps_", 'f', 'Q')
	followerAccount := hostileInsertRPSAccount(t, db, followerID)
	hostileInsertRPSSession(t, db, followerID, "followers", "started", followerAccount)
	two := hostileBlob16(2)
	hostileMustExec(t, db, `UPDATE game_rps_sessions SET revision=?,phase_seq=? WHERE id=?`, two, two, followerID)
	followerUser0 := hostileInsertUser(t, db, "game-follower-zero", 0, 0)
	followerUser1 := hostileInsertUser(t, db, "game-follower-one", 0, 0)
	hostileInsertRPSSeatWithActions(t, db, followerID, 0, followerUser0, hostileEnvelope(33, 0x01), one, nil, nil, nil, nil, nil, "active")
	hostileInsertRPSSeatWithActions(t, db, followerID, 1, followerUser1, hostileEnvelope(33, 0x01), one, nil, nil, nil, nil, nil, "active")
	hostileMustExec(t, db, `UPDATE game_rps_seats SET follower_action='surrender',last_action_phase_seq=? WHERE session_id=? AND seat_no=1`, two, followerID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=? WHERE session_id=? AND seat_no=1`, hostileEnvelope(34, 0x01), followerID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET last_action_phase_seq=? WHERE session_id=? AND seat_no=1`, hostileBlob16(3), followerID)

	identityID := hostileOIDVariant("rps_", 'i', 'Q')
	identityAccount := hostileInsertRPSAccount(t, db, identityID)
	hostileInsertRPSSession(t, db, identityID, "gesture", "started", identityAccount)
	hostileMustFail(t, db, `
INSERT INTO game_rps_seats(
 session_id,seat_no,user_id,deletion_state,starting_balance,current_balance,current_round_input,
 current_all_in,current_gesture_envelope,follower_action,last_action_phase_seq,total_input,
 total_returned,terminal_return,wallet_net_sign,wallet_net_mag,rock_count,scissors_count,
 paper_count,timeout_count,stats_applied
) VALUES(?,0,NULL,'active',?,?,?,0,NULL,NULL,NULL,?,?,?,?,?,?,?,?,?,0)`, identityID, zero, zero, zero, hostileBlob32(0), hostileBlob32(0), zero, zero, zero, zero, zero)

	// Pending-result own outcome is derived from the signed wallet net.  The
	// result projection cannot claim a win/loss/tie inconsistent with sign.
	insertPending := func(marker byte, userID int64, sign int, mag []byte, ownResult string, wantFail bool) {
		t.Helper()
		query := `
INSERT INTO game_rps_pending_results(
 user_id,session_id_text,mode,terminal_reason,own_seat_no,own_input,own_returned,
 own_wallet_net_sign,own_wallet_net_mag,seat0_result,seat1_result,seat2_result,created_at
) VALUES(?,?, 'quick','quick_resolved',0,?,?,?,? ,?,?,?,0)`
		if wantFail {
			hostileMustFail(t, db, query, userID, hostileOIDVariant("rps_", marker, 'Q'), hostileBlob32(marker), hostileBlob32(marker+1), sign, mag, ownResult, "loss", "tie")
			return
		}
		hostileMustExec(t, db, query, userID, hostileOIDVariant("rps_", marker, 'Q'), hostileBlob32(marker), hostileBlob32(marker+1), sign, mag, ownResult, "loss", "tie")
	}
	positivePendingUser := hostileInsertUser(t, db, "pending-positive", 0, 0)
	negativePendingUser := hostileInsertUser(t, db, "pending-negative", 0, 0)
	zeroPendingUser := hostileInsertUser(t, db, "pending-zero", 0, 0)
	insertPending('p', positivePendingUser, 1, one, "win", false)
	insertPending('n', negativePendingUser, -1, one, "loss", false)
	insertPending('z', zeroPendingUser, 0, zero, "tie", false)
	insertPending('P', hostileInsertUser(t, db, "pending-bad-positive", 0, 0), 1, one, "loss", true)
	insertPending('N', hostileInsertUser(t, db, "pending-bad-negative", 0, 0), -1, one, "win", true)
	insertPending('Z', hostileInsertUser(t, db, "pending-bad-zero", 0, 0), 0, zero, "win", true)

	// Rank facts use the same sign predicate, while aggregate rows encode the
	// exact rolling eligibility/count/rate relation.
	insertRankFact := func(marker byte, userID int64, sign int, mag []byte, profitable int, wantFail bool) {
		t.Helper()
		query := `
INSERT INTO game_rps_rank_facts(
 session_id_text,user_id,mode,terminal_at,expires_at,wallet_net_sign,wallet_net_mag,
 profitable,aggregate_applied
) VALUES(? ,?,'quick',0,2592000,?,?,?,0)`
		if wantFail {
			hostileMustFail(t, db, query, hostileOIDVariant("rps_", marker, 'Q'), userID, sign, mag, profitable)
			return
		}
		hostileMustExec(t, db, query, hostileOIDVariant("rps_", marker, 'Q'), userID, sign, mag, profitable)
	}
	insertRankFact('u', hostileInsertUser(t, db, "rank-positive", 0, 0), 1, one, 1, false)
	insertRankFact('v', hostileInsertUser(t, db, "rank-negative", 0, 0), -1, one, 0, false)
	insertRankFact('w', hostileInsertUser(t, db, "rank-zero", 0, 0), 0, zero, 0, false)
	insertRankFact('U', hostileInsertUser(t, db, "rank-bad-positive", 0, 0), 1, one, 0, true)
	insertRankFact('V', hostileInsertUser(t, db, "rank-bad-negative", 0, 0), -1, one, 1, true)
	insertRankFact('W', hostileInsertUser(t, db, "rank-bad-zero", 0, 0), 0, zero, 1, true)

	insertAggregate := func(marker byte, userID int64, sessionCount, profitableCount []byte, eligible, rate int, wantFail bool) {
		t.Helper()
		query := `
INSERT INTO game_rps_rank_aggregates(
 user_id,mode,session_count,profitable_count,net_profit_sign,net_profit_mag,eligible,
 profit_rate_bp,profit_rate_achieved_at,net_profit_achieved_at,profit_public_tie_key,
 net_public_tie_key,revision,updated_at
 ) VALUES(?,'quick',?,?,0,?,?,?,0,0,?,?,?,0)`
		args := []any{userID, sessionCount, profitableCount, zero, eligible, rate, hostileBlob32(marker), hostileBlob32(marker + 1), one}
		if wantFail {
			hostileMustFail(t, db, query, args...)
			return
		}
		hostileMustExec(t, db, query, args...)
	}
	insertAggregate('a', hostileInsertUser(t, db, "aggregate-valid", 0, 0), hostileBlob16(10), hostileBlob16(5), 1, 5000, false)
	insertAggregate('b', hostileInsertUser(t, db, "aggregate-ineligible", 0, 0), hostileBlob16(10), hostileBlob16(5), 0, 5000, true)
	insertAggregate('c', hostileInsertUser(t, db, "aggregate-under-ten", 0, 0), hostileBlob16(9), hostileBlob16(5), 1, 5555, true)
	// The rate formula is a service-owned projection invariant; the schema only
	// constrains its persisted scalar domain and the eligibility/count matrix.
	insertAggregate('d', hostileInsertUser(t, db, "aggregate-rate", 0, 0), hostileBlob16(10), hostileBlob16(5), 1, 4999, false)
	insertAggregate('e', hostileInsertUser(t, db, "aggregate-profitable-over", 0, 0), hostileBlob16(10), hostileBlob16(11), 1, 5000, true)
	insertAggregate('f', hostileInsertUser(t, db, "aggregate-zero", 0, 0), zero, zero, 0, 0, true)
}

func TestLimitedActivityRejectsFractionalAccountingAndScheduleFacts(t *testing.T) {
	database := openGenerationTwoConstraintFixture(t)
	hostileMustExec(t, database, `INSERT INTO limited_activity_configs VALUES('strict-fixture',0,NULL,NULL,0,'{}',1,0)`)
	hostileMustExec(t, database, `INSERT INTO limited_activity_revisions VALUES('strict-fixture',1,0,NULL,NULL,0,'{}',NULL,0)`)
	hostileMustExec(t, database, `INSERT INTO activity_exchange_state VALUES('strict-fixture','sketch_paper',zeroblob(16),NULL,1)`)
	op := hostileOID("op_")
	hostileInsertOperation(t, database, op, 1, "activity_exchange", "operation", op)
	for name, statement := range map[string]string{
		"schedule": `UPDATE limited_activity_configs SET starts_at=1.5,ends_at=10 WHERE activity_key='strict-fixture'`,
		"revision": `INSERT INTO limited_activity_revisions VALUES('strict-fixture',1.5,0,NULL,NULL,0,'{}',NULL,0)`,
		"supply":   `UPDATE activity_exchange_state SET revision=1.5 WHERE activity_key='strict-fixture'`,
		"receipt":  `INSERT INTO activity_exchange_receipts VALUES('` + op + `','strict-fixture',1,NULL,'sketch_paper',X'00000000000000000000000000000001',1.5,X'00000000000000000000000000000001',1,0)`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := database.Exec(statement)
			if err == nil || !strings.Contains(err.Error(), "cannot store REAL value in INTEGER column") {
				t.Fatalf("fractional fact was not rejected by integer storage: %v", err)
			}
		})
	}
}
