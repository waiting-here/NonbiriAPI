package duel

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/finance"
	"github.com/waiting-here/NonbiriAPI/internal/game/randomness"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/useractivity"
)

type aiQueueRecord struct {
	ID                string
	User              int64
	Created, Deadline int64
	TermsHash         string
	Snapshot          AISnapshot
}

func (s *Service) aiConfiguration(ctx context.Context, tx *sql.Tx, id string) (AISnapshot, bool, error) {
	snapshot := AISnapshot{}
	terms := AITerms{}
	var enabled bool
	var ticket, reward int64
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT b.id,b.name,b.description,b.revision,b.challenge_id,c.rules_key,b.policy_id,b.policy_version,v.source_id,v.schema_id,b.ticket_milli,b.reward_milli,b.memory_days,b.memory_games,v.definition_json,(b.enabled=1 AND p.enabled=1) FROM game_ai_bots b JOIN game_ai_challenges c ON c.id=b.challenge_id JOIN game_ai_policies p ON p.id=b.policy_id JOIN game_ai_policy_versions v ON v.policy_id=b.policy_id AND v.version=b.policy_version WHERE b.id=? AND b.game_key=?`, id, s.rules.ID()).Scan(&terms.BotID, &terms.BotName, &terms.Description, &terms.Revision, &terms.ChallengeID, &terms.RulesKey, &terms.PolicyID, &terms.PolicyVersion, &terms.SourceID, &terms.PolicySchema, &ticket, &reward, &terms.MemoryDays, &terms.MemoryGames, &raw, &enabled)
	if err != nil {
		return snapshot, false, notFound(err)
	}
	catalog, err := s.rules.Catalog("ai")
	if err != nil {
		return snapshot, false, err
	}
	terms.FirstReward = game.FormatAmount(reward)
	snapshot.Terms = Terms{Economy: AIEconomy, AI: &terms, Game: s.rules.ID(), Mode: "ai", Ticket: game.FormatAmount(ticket), RulesVersion: 1, ContentHash: catalog.Hash}
	snapshot.Policy = json.RawMessage(raw)
	if provider, ok := s.aiAdapter.(interface {
		BotLoadout(json.RawMessage) (json.RawMessage, error)
	}); ok {
		terms.BotLoadout, err = provider.BotLoadout(snapshot.Policy)
		if err != nil {
			return snapshot, false, err
		}
		snapshot.Terms.AI = &terms
	}
	if terms.SourceID != s.aiAdapter.Source().ID() || terms.PolicySchema != s.aiAdapter.PolicySchema() || terms.RulesKey != s.aiAdapter.RulesKey() {
		return snapshot, false, ErrInvariant
	}
	return snapshot, enabled, nil
}

func (s *Service) enqueueAI(ctx context.Context, in EnqueueInput) (MutationResult, error) {
	if s.aiAdapter == nil || !db.ValidateOpaqueID(in.BotID, "bot_") || len(in.ExpectedTermsHash) != 64 {
		return MutationResult{}, ErrInvalidRequest
	}
	var loadout json.RawMessage
	if _, ok := s.rules.(SequentialRules); ok {
		var err error
		loadout, err = s.rules.Loadout("ai", in.Loadout)
		if err != nil {
			return MutationResult{}, err
		}
	} else if len(in.Loadout) != 0 {
		return MutationResult{}, ErrInvalidRequest
	}
	tx, now, err := s.begin(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	defer tx.Rollback()
	if err := s.authorize(ctx, tx, in.Identity); err != nil {
		return MutationResult{}, err
	}
	body := enqueueBody{Mode: "ai", BotID: in.BotID, ExpectedTermsHash: in.ExpectedTermsHash, Loadout: loadout}
	route := "/api/games/" + s.rules.ID() + "/queue"
	if replay, err := s.probeReplay(ctx, tx, in.Identity, in.IdempotencyKey, "POST", route, "", body, now); err != nil {
		return MutationResult{}, err
	} else if replay != nil {
		return *replay, nil
	}
	settings, err := s.aiSettings(ctx, tx)
	if err != nil {
		return MutationResult{}, err
	}
	cfg, err := s.config(ctx, tx)
	if err != nil {
		return MutationResult{}, err
	}
	maintenance, err := maintenanceOn(ctx, tx)
	if err != nil {
		return MutationResult{}, err
	}
	if maintenance {
		return MutationResult{}, ErrMaintenance
	}
	if !settings.Enabled || !cfg.Enabled {
		return MutationResult{}, ErrConflict
	}
	allowed, err := eligible(ctx, tx, in.UserID, now)
	if err != nil {
		return MutationResult{}, err
	}
	if !allowed {
		return MutationResult{}, ErrForbidden
	}
	snapshot, enabled, err := s.aiConfiguration(ctx, tx, in.BotID)
	if err != nil {
		return MutationResult{}, err
	}
	if !enabled {
		return MutationResult{}, ErrConflict
	}
	terms, err := Encode(snapshot.Terms)
	if err != nil {
		return MutationResult{}, err
	}
	if digest(terms) != in.ExpectedTermsHash {
		return MutationResult{}, ErrConflict
	}
	var occupied, count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM game_duel_user_slots WHERE user_id=? AND game_key=?`, in.UserID, s.rules.ID()).Scan(&occupied); err != nil {
		return MutationResult{}, err
	}
	if occupied > 0 {
		return MutationResult{}, ErrConflict
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM game_ai_queue WHERE game_key=? AND state='waiting'`, s.rules.ID()).Scan(&count); err != nil {
		return MutationResult{}, err
	}
	if count >= AIQueueCapacity {
		return MutationResult{}, ErrResourceLimit
	}
	start, _, err := s.limiter.ReserveTx(ctx, tx, in.UserID)
	if err != nil {
		if errors.Is(err, game.ErrStartRateLimited) {
			return MutationResult{}, ErrRateLimited
		}
		return MutationResult{}, ErrUnavailable
	}
	defer start.Release()
	id, err := s.generate(s.aiQueuePrefix)
	if err != nil {
		return MutationResult{}, err
	}
	snapshot.HumanLoadout = loadout
	raw, err := Encode(snapshot)
	if err != nil {
		return MutationResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM game_ai_queue WHERE user_id=? AND game_key=? AND state='failed'`, in.UserID, s.rules.ID()); err != nil {
		return MutationResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO game_ai_queue(id,game_key,user_id,bot_id,challenge_id,created_at,deadline,terms_hash,snapshot_json) VALUES(?,?,?,?,?,?,?,?,?)`, id, s.rules.ID(), in.UserID, in.BotID, snapshot.Terms.AI.ChallengeID, now, now+QueueSeconds, in.ExpectedTermsHash, string(raw)); err != nil {
		return MutationResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO game_duel_user_slots(user_id,game_key,ai_queue_id) VALUES(?,?,?)`, in.UserID, s.rules.ID(), id); err != nil {
		return MutationResult{}, err
	}
	d, err := s.replay(ctx, tx, in.Identity, in.IdempotencyKey, "POST", route, "", body, now)
	if err != nil {
		return MutationResult{}, err
	}
	if err := useractivity.RecordActiveTx(ctx, tx, useractivity.ActiveEvent{UserID: in.UserID, At: now, Kind: "game", Fresh: true}); err != nil {
		return MutationResult{}, err
	}
	result, err := finish(ctx, tx, d, 202, QueueReceipt{QueueID: id, Revision: "1", Deadline: now + QueueSeconds})
	if err == nil {
		start.Commit()
		s.wakeAI()
	}
	return result, err
}

func (s *Service) aiQueue(ctx context.Context, tx *sql.Tx, id string) (aiQueueRecord, error) {
	var q aiQueueRecord
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT id,user_id,created_at,deadline,terms_hash,snapshot_json FROM game_ai_queue WHERE id=? AND game_key=? AND state='waiting'`, id, s.rules.ID()).Scan(&q.ID, &q.User, &q.Created, &q.Deadline, &q.TermsHash, &raw)
	if err != nil {
		return q, err
	}
	if Decode([]byte(raw), &q.Snapshot) != nil || q.Snapshot.Terms.AI == nil {
		return q, ErrInvariant
	}
	return q, nil
}
func (s *Service) releaseAIQueue(ctx context.Context, tx *sql.Tx, id, reason string, now int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM game_duel_user_slots WHERE ai_queue_id=? AND game_key=?`, id, s.rules.ID()); err != nil {
		return err
	}
	if reason == "" {
		_, err := tx.ExecContext(ctx, `DELETE FROM game_ai_queue WHERE id=? AND game_key=?`, id, s.rules.ID())
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE game_ai_queue SET state='failed',failure=?,resolved_at=? WHERE id=? AND game_key=? AND state='waiting'`, reason, now, id, s.rules.ID())
	return err
}
func (s *Service) cancelAIQueue(ctx context.Context, in CancelInput) (MutationResult, error) {
	if s.aiAdapter == nil || !db.ValidateOpaqueID(in.QueueID, s.aiQueuePrefix) || in.ExpectedRevision != "1" {
		return MutationResult{}, ErrInvalidRequest
	}
	tx, now, err := s.begin(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	defer tx.Rollback()
	if err := s.authorize(ctx, tx, in.Identity); err != nil {
		return MutationResult{}, err
	}
	route := "/api/games/" + s.rules.ID() + "/queue/{id}"
	body := cancelBody{ExpectedRevision: "1"}
	if replay, err := s.probeReplay(ctx, tx, in.Identity, in.IdempotencyKey, "DELETE", route, in.QueueID, body, now); err != nil {
		return MutationResult{}, err
	} else if replay != nil {
		return *replay, nil
	}
	var user int64
	if err := tx.QueryRowContext(ctx, `SELECT user_id FROM game_ai_queue WHERE id=? AND game_key=?`, in.QueueID, s.rules.ID()).Scan(&user); err != nil {
		return MutationResult{}, notFound(err)
	}
	if user != in.UserID {
		return MutationResult{}, ErrNotFound
	}
	if err := s.releaseAIQueue(ctx, tx, in.QueueID, "", now); err != nil {
		return MutationResult{}, err
	}
	d, err := s.replay(ctx, tx, in.Identity, in.IdempotencyKey, "DELETE", route, in.QueueID, body, now)
	if err != nil {
		return MutationResult{}, err
	}
	return finish(ctx, tx, d, 204, nil)
}

func (s *Service) workAIQueue(ctx context.Context, tx *sql.Tx, now int64, recovery bool) (bool, error) {
	if s.aiAdapter == nil {
		return false, nil
	}
	removed, err := tx.ExecContext(ctx, `DELETE FROM game_ai_queue WHERE id IN (SELECT id FROM game_ai_queue WHERE game_key=? AND state='failed' AND resolved_at<=? LIMIT 100)`, s.rules.ID(), now-QueueSeconds)
	if err != nil {
		return false, err
	}
	count, err := removed.RowsAffected()
	if err != nil {
		return false, err
	}
	removed, err = tx.ExecContext(ctx, `DELETE FROM game_ai_memories WHERE session_id IN (SELECT session_id FROM game_ai_memories WHERE expires_at<=? ORDER BY expires_at,session_id LIMIT 100)`, now)
	if err != nil {
		return false, err
	}
	memories, err := removed.RowsAffected()
	if err != nil {
		return false, err
	}
	cleaned := count+memories > 0
	var id string
	err = tx.QueryRowContext(ctx, `SELECT q.id FROM game_ai_queue q WHERE q.game_key=? AND q.state='waiting' ORDER BY (q.deadline>? AND EXISTS(SELECT 1 FROM game_ai_bots b JOIN game_ai_policies p ON p.id=b.policy_id WHERE b.id=q.bot_id AND b.enabled=1 AND p.enabled=1) AND EXISTS(SELECT 1 FROM game_ai_policies accepted WHERE accepted.id=json_extract(q.snapshot_json,'$.terms.ai.policy_id') AND accepted.enabled=1) AND EXISTS(SELECT 1 FROM users u WHERE u.id=q.user_id AND (u.is_banned=0 OR u.banned_until<=?))),q.ordinal LIMIT 1`, s.rules.ID(), now, now).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return cleaned, nil
	}
	if err != nil {
		return false, err
	}
	q, err := s.aiQueue(ctx, tx, id)
	if err != nil {
		return false, err
	}
	reason := ""
	if recovery {
		reason = "server_restart"
	} else if now >= q.Deadline {
		reason = "expired"
	} else {
		settings, err := s.aiSettings(ctx, tx)
		if err != nil {
			return false, err
		}
		cfg, err := s.config(ctx, tx)
		if err != nil {
			return false, err
		}
		maintenance, err := maintenanceOn(ctx, tx)
		if err != nil {
			return false, err
		}
		_, enabled, err := s.aiConfiguration(ctx, tx, q.Snapshot.Terms.AI.BotID)
		if err != nil {
			return false, err
		}
		var acceptedPolicyEnabled bool
		if err := tx.QueryRowContext(ctx, `SELECT enabled FROM game_ai_policies WHERE id=? AND game_key=?`, q.Snapshot.Terms.AI.PolicyID, s.rules.ID()).Scan(&acceptedPolicyEnabled); err != nil {
			return false, err
		}
		allowed, err := eligible(ctx, tx, q.User, now)
		if err != nil {
			return false, err
		}
		if !allowed {
			reason = "account_unavailable"
		} else if !settings.Enabled || !cfg.Enabled || maintenance || !enabled || !acceptedPolicyEnabled {
			reason = "closed"
		}
	}
	if reason != "" {
		return true, s.releaseAIQueue(ctx, tx, id, reason, now)
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM game_duel_sessions WHERE game_key=? AND state='active' AND economy='ai_challenge'`, s.rules.ID()).Scan(&active); err != nil {
		return false, err
	}
	if active >= AIActiveCapacity {
		return cleaned, nil
	}
	// A failed admission must roll back escrow creation and capacity changes
	// before the unpaid queue receives its user-visible failure receipt.
	if _, err := tx.ExecContext(ctx, `SAVEPOINT ai_admission`); err != nil {
		return false, err
	}
	err = s.startAI(ctx, tx, q, now)
	if errors.Is(err, ErrInsufficientCredits) || errors.Is(err, ledger.ErrInsufficientBalance) {
		if _, rollback := tx.ExecContext(ctx, `ROLLBACK TO ai_admission`); rollback != nil {
			return false, rollback
		}
		if _, release := tx.ExecContext(ctx, `RELEASE ai_admission`); release != nil {
			return false, release
		}
		return true, s.releaseAIQueue(ctx, tx, id, "insufficient_credits", now)
	}
	if err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `RELEASE ai_admission`)
	return true, err
}

func (s *Service) startAI(ctx context.Context, tx *sql.Tx, q aiQueueRecord, now int64) error {
	snapshot := q.Snapshot
	terms := snapshot.Terms
	if err := s.ensureCatalog(ctx, tx, "ai"); err != nil {
		return err
	}
	id, err := s.generate(s.sessionPrefix)
	if err != nil {
		return err
	}
	secret, err := randomness.New(s.rules.ID(), id, "ai/"+terms.ContentHash, nil)
	if err != nil {
		return err
	}
	seatStream, err := secret.Stream("seat-order")
	if err != nil {
		return err
	}
	humanSeat, err := seatStream.Uint64n(2)
	if err != nil {
		return err
	}
	human := int(humanSeat)
	bot := 1 - human
	initialStream, err := secret.Stream("initial")
	if err != nil {
		return err
	}
	rules, err := s.rulesFor("ai", terms.ContentHash)
	if err != nil {
		return err
	}
	creator, ok := rules.(interface {
		CreateWithRandom(string, [2]json.RawMessage, io.Reader) (json.RawMessage, error)
	})
	if !ok {
		return ErrInvariant
	}
	var loadouts [2]json.RawMessage
	loadouts[human], loadouts[bot] = snapshot.HumanLoadout, terms.AI.BotLoadout
	state, err := creator.CreateWithRandom("ai", loadouts, initialStream)
	if err != nil {
		return err
	}
	snapshot.MemoryEnabled, snapshot.Memory, snapshot.MemorySamples, err = s.readAIMemory(ctx, tx, q.User, *terms.AI, now)
	if err != nil {
		return err
	}
	var seed [32]byte
	if _, err := rand.Read(seed[:]); err != nil {
		return ErrUnavailable
	}
	snapshot.RandomSeed = hex.EncodeToString(seed[:])
	ticket, err := game.ParseAmount(terms.Ticket)
	if err != nil {
		return ErrInvariant
	}
	reward, err := game.ParseAmount(terms.AI.FirstReward)
	if err != nil {
		return ErrInvariant
	}
	v := sessionRecord{rules: rules, ID: id, Mode: "ai", Economy: AIEconomy, State: "active", Terms: terms, TermsHash: q.TermsHash, Ticket: ticket, Revision: one(), PhaseSeq: one(), Started: now, Initial: state, Payload: storedPayload{Rules: state}, AI: &aiSession{BotSeat: bot, Snapshot: snapshot}}
	v.Seats[human] = seatRecord{Kind: "human", User: &q.User}
	v.Seats[bot] = seatRecord{Kind: "bot", BotID: terms.AI.BotID}
	if err := s.enterPhase(&v, now, false); err != nil {
		return err
	}
	meta, err := s.meta(q.User, now)
	if err != nil {
		return err
	}
	port := s.finance.(finance.AIDuel)
	return classify(port.AIStart(ctx, tx, finance.AIStart{Meta: meta, SessionID: id, UserID: q.User, Ticket: ledger.AmountFromMilli(ticket), MayReward: reward > 0}, func(ctx context.Context, tx *sql.Tx, accounts ledger.AccountPair, payment ledger.Payment, hold db.U128) error {
		v.GeneralAccount, v.GameAccount, v.Remaining = accounts.General, accounts.Game, hold
		v.Seats[human].GeneralPaid, v.Seats[human].GamePaid = payment.General.Big().Int64(), payment.Game.Big().Int64()
		if err := s.insertSession(ctx, tx, v); err != nil {
			return err
		}
		if err := randomness.Insert(ctx, tx, secret); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE game_duel_user_slots SET ai_queue_id=NULL,session_id=? WHERE user_id=? AND game_key=? AND ai_queue_id=?`, id, q.User, s.rules.ID(), q.ID)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return ErrInvariant
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM game_ai_queue WHERE id=?`, q.ID)
		return err
	}))
}
