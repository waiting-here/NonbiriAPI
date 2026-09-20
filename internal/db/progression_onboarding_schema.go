package db

const previousOnboardingTasks = `(game_key='fishing' AND task_key IN ('worm','lure','premium')) OR (game_key='linklink' AND task_key IN ('6x8','8x8','10x10')) OR (game_key='rps' AND task_key IN ('quick','standard','deathmatch'))`

const progressionOnboardingTasks = previousOnboardingTasks + ` OR
 (game_key='bidding' AND task_key IN ('complete_tier_1','complete_tier_2','complete_tier_3','first_win')) OR
 (game_key='likes' AND task_key IN ('quick_complete','quick_win','standard_complete','standard_win')) OR
 (game_key='blackjack' AND task_key IN ('complete','first_win','first_bust','first_21','first_natural_21'))`

const progressionOnboardingHolds = `CREATE TABLE game_onboarding_holds (
 id TEXT NOT NULL PRIMARY KEY CHECK(typeof(id)='text' AND length(id)=26 AND substr(id,1,4)='goh_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT CHECK(typeof(user_id)='integer'),
 game_key TEXT NOT NULL,
 task_key TEXT NOT NULL,
 ledger_rows_remaining BLOB NOT NULL CHECK(typeof(ledger_rows_remaining)='blob' AND ledger_rows_remaining=X'00000000000000000000000000000001'),
 created_at INTEGER NOT NULL CHECK(typeof(created_at)='integer' AND created_at BETWEEN 0 AND 253402300799),
 fishing_batch_id TEXT REFERENCES game_fishing_batches(id) ON DELETE RESTRICT,
 linklink_session_id TEXT REFERENCES game_linklink_sessions(id) ON DELETE RESTRICT,
 rps_queue_id TEXT REFERENCES game_rps_queue(id) ON DELETE RESTRICT,
 rps_session_id TEXT,
 seat_no INTEGER CHECK(seat_no IS NULL OR (typeof(seat_no)='integer' AND seat_no BETWEEN 0 AND 2)),
 duel_queue_id TEXT REFERENCES game_duel_queue(id) ON DELETE RESTRICT,
 duel_session_id TEXT,
 blackjack_entry_id TEXT REFERENCES game_blackjack_entries(id) ON DELETE RESTRICT,
 FOREIGN KEY(rps_session_id,seat_no) REFERENCES game_rps_seats(session_id,seat_no) ON DELETE RESTRICT,
 FOREIGN KEY(duel_session_id,seat_no) REFERENCES game_duel_seats(session_id,seat_no) ON DELETE RESTRICT,
 CHECK(` + progressionOnboardingTasks + `),
 CHECK(
  (game_key='fishing' AND fishing_batch_id IS NOT NULL AND linklink_session_id IS NULL AND rps_queue_id IS NULL AND rps_session_id IS NULL AND seat_no IS NULL AND duel_queue_id IS NULL AND duel_session_id IS NULL AND blackjack_entry_id IS NULL) OR
  (game_key='linklink' AND fishing_batch_id IS NULL AND linklink_session_id IS NOT NULL AND rps_queue_id IS NULL AND rps_session_id IS NULL AND seat_no IS NULL AND duel_queue_id IS NULL AND duel_session_id IS NULL AND blackjack_entry_id IS NULL) OR
  (game_key='rps' AND fishing_batch_id IS NULL AND linklink_session_id IS NULL AND duel_queue_id IS NULL AND duel_session_id IS NULL AND blackjack_entry_id IS NULL AND
   ((rps_queue_id IS NOT NULL AND rps_session_id IS NULL AND seat_no IS NULL) OR
    (rps_queue_id IS NULL AND rps_session_id IS NOT NULL AND seat_no IS NOT NULL))) OR
  (game_key IN ('bidding','likes') AND fishing_batch_id IS NULL AND linklink_session_id IS NULL AND rps_queue_id IS NULL AND rps_session_id IS NULL AND blackjack_entry_id IS NULL AND
   ((duel_queue_id IS NOT NULL AND duel_session_id IS NULL AND seat_no IS NULL) OR
    (duel_queue_id IS NULL AND duel_session_id IS NOT NULL AND seat_no IN (0,1)))) OR
  (game_key='blackjack' AND fishing_batch_id IS NULL AND linklink_session_id IS NULL AND rps_queue_id IS NULL AND rps_session_id IS NULL AND seat_no IS NULL AND duel_queue_id IS NULL AND duel_session_id IS NULL AND blackjack_entry_id IS NOT NULL)
 )
)`

const progressionOnboardingIndexes = `
DROP INDEX idx_game_onboarding_hold_fishing;
DROP INDEX idx_game_onboarding_hold_linklink;
DROP INDEX idx_game_onboarding_hold_queue;
DROP INDEX idx_game_onboarding_hold_seat;
CREATE UNIQUE INDEX idx_game_onboarding_hold_fishing ON game_onboarding_holds(fishing_batch_id,task_key) WHERE fishing_batch_id IS NOT NULL;
CREATE UNIQUE INDEX idx_game_onboarding_hold_linklink ON game_onboarding_holds(linklink_session_id,task_key) WHERE linklink_session_id IS NOT NULL;
CREATE UNIQUE INDEX idx_game_onboarding_hold_queue ON game_onboarding_holds(rps_queue_id,task_key) WHERE rps_queue_id IS NOT NULL;
CREATE UNIQUE INDEX idx_game_onboarding_hold_seat ON game_onboarding_holds(rps_session_id,seat_no,task_key) WHERE rps_session_id IS NOT NULL;
CREATE UNIQUE INDEX idx_game_onboarding_hold_duel_queue ON game_onboarding_holds(duel_queue_id,task_key) WHERE duel_queue_id IS NOT NULL;
CREATE UNIQUE INDEX idx_game_onboarding_hold_duel_seat ON game_onboarding_holds(duel_session_id,seat_no,task_key) WHERE duel_session_id IS NOT NULL;
CREATE UNIQUE INDEX idx_game_onboarding_hold_blackjack ON game_onboarding_holds(blackjack_entry_id,task_key) WHERE blackjack_entry_id IS NOT NULL;
`

const onboardingParentCondition = `
 (NEW.game_key='fishing' AND EXISTS(SELECT 1 FROM game_fishing_batches b WHERE b.id=NEW.fishing_batch_id AND b.user_id=NEW.user_id AND b.bait=NEW.task_key AND b.rules_version=2 AND b.state='reserved')) OR
 (NEW.game_key='linklink' AND EXISTS(SELECT 1 FROM game_linklink_sessions s WHERE s.id=NEW.linklink_session_id AND s.user_id=NEW.user_id AND s.spec=NEW.task_key AND s.rules_version=2)) OR
 (NEW.game_key='rps' AND EXISTS(SELECT 1 FROM game_rps_queue q WHERE q.id=NEW.rps_queue_id AND q.user_id=NEW.user_id AND q.mode=NEW.task_key AND q.rules_version=2)) OR
 (NEW.game_key='rps' AND EXISTS(SELECT 1 FROM game_rps_seats p JOIN game_rps_sessions s ON s.id=p.session_id WHERE p.session_id=NEW.rps_session_id AND p.seat_no=NEW.seat_no AND p.user_id=NEW.user_id AND p.deletion_state='active' AND s.mode=NEW.task_key AND s.rules_version=2)) OR
 (NEW.game_key IN ('bidding','likes') AND EXISTS(SELECT 1 FROM game_duel_queue q WHERE q.id=NEW.duel_queue_id AND q.user_id=NEW.user_id AND q.game_key=NEW.game_key AND
  ((q.game_key='bidding' AND (NEW.task_key='first_win' OR NEW.task_key='complete_tier_'||substr(q.mode,5,1))) OR
   (q.game_key='likes' AND NEW.task_key IN (q.mode||'_complete',q.mode||'_win'))))) OR
 (NEW.game_key IN ('bidding','likes') AND EXISTS(SELECT 1 FROM game_duel_seats p JOIN game_duel_sessions s ON s.id=p.session_id WHERE p.session_id=NEW.duel_session_id AND p.seat_no=NEW.seat_no AND p.user_id=NEW.user_id AND s.game_key=NEW.game_key AND
  ((s.game_key='bidding' AND (NEW.task_key='first_win' OR NEW.task_key='complete_tier_'||substr(s.mode,5,1))) OR
   (s.game_key='likes' AND NEW.task_key IN (s.mode||'_complete',s.mode||'_win'))))) OR
 (NEW.game_key='blackjack' AND EXISTS(SELECT 1 FROM game_blackjack_entries e WHERE e.id=NEW.blackjack_entry_id AND e.user_id=NEW.user_id AND e.state IN ('waiting','seated','playing')))
`

const progressionOnboardingGuards = `
DROP TRIGGER onboarding_hold_parent_insert;
DROP TRIGGER onboarding_hold_parent_update;
CREATE TRIGGER onboarding_hold_parent_insert BEFORE INSERT ON game_onboarding_holds WHEN NOT (` + onboardingParentCondition + `)
BEGIN SELECT RAISE(ABORT,'onboarding hold parent mismatch'); END;
CREATE TRIGGER onboarding_hold_parent_update BEFORE UPDATE ON game_onboarding_holds WHEN NOT (` + onboardingParentCondition + `)
BEGIN SELECT RAISE(ABORT,'onboarding hold parent mismatch'); END;
`
