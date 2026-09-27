package fatfish

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

type SubmitInput struct {
	TabCapability string `json:"tab_capability"`
	Inputs        []byte `json:"-"`
	TerminalTick  int    `json:"terminal_tick"`
}

func (s *Service) inputDigest(id string, tick int, raw []byte) ([32]byte, error) {
	var digest [32]byte
	secret, err := s.keys.DeriveGenerationTwoSubkey([]byte("NonbiriAPI/fatfish-input-digest/v1"))
	if err != nil || len(secret) != 32 {
		clear(secret)
		return digest, ErrInvariant
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("fatfish-input-v1\x00"))
	_, _ = mac.Write([]byte(id))
	var tickBytes [8]byte
	binary.BigEndian.PutUint64(tickBytes[:], uint64(tick))
	_, _ = mac.Write(tickBytes[:])
	var lengthBytes [8]byte
	binary.BigEndian.PutUint64(lengthBytes[:], uint64(len(raw)))
	_, _ = mac.Write(lengthBytes[:])
	_, _ = mac.Write(raw)
	copy(digest[:], mac.Sum(nil))
	clear(secret)
	return digest, nil
}

func (s *Service) Submit(ctx context.Context, userID int64, id string, input SubmitInput, key string) (ChallengeView, error) {
	return s.submit(ctx, userID, id, input, key, false)
}
func (s *Service) SubmitPlaytest(ctx context.Context, actorID int64, id string, input SubmitInput, key string) (ChallengeView, error) {
	return s.submit(ctx, actorID, id, input, key, true)
}

func (s *Service) submit(ctx context.Context, userID int64, id string, input SubmitInput, key string, playtest bool) (ChallengeView, error) {
	if !db.ValidateOpaqueID(id, "ffc_") || len(input.Inputs) == 0 || len(input.Inputs) > 4<<20 || input.TerminalTick < 0 {
		return ChallengeView{}, ErrInvalid
	}
	if _, err := idempotency.KeyHash(key); err != nil {
		return ChallengeView{}, ErrInvalid
	}
	nowMS, err := s.nowMS()
	if err != nil {
		return ChallengeView{}, err
	}
	digest, err := s.inputDigest(id, input.TerminalTick, input.Inputs)
	if err != nil {
		return ChallengeView{}, err
	}
	// This first read avoids taking RAM queue capacity for a completed receipt.
	readTx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ChallengeView{}, err
	}
	if playtest {
		err = s.authorizeAdminTx(ctx, readTx, userID)
	} else {
		err = s.authorizeUserTx(ctx, readTx, userID, nowMS, false)
	}
	if err != nil {
		readTx.Rollback()
		return ChallengeView{}, err
	}
	c, err := readChallengeTx(ctx, readTx, id)
	if err != nil {
		readTx.Rollback()
		return ChallengeView{}, err
	}
	if c.userID != userID || c.playtest != playtest {
		readTx.Rollback()
		return ChallengeView{}, ErrNotFound
	}
	if !sameCapability(c.capabilityHash, input.TabCapability) {
		readTx.Rollback()
		return ChallengeView{}, ErrForbidden
	}
	if c.inputDigest != nil && subtle.ConstantTimeCompare(c.inputDigest, digest[:]) != 1 {
		readTx.Rollback()
		return ChallengeView{}, ErrConflict
	}
	if c.state != "active" && c.state != "verifying" {
		if c.inputDigest == nil {
			readTx.Rollback()
			return ChallengeView{}, ErrClosed
		}
		readTx.Rollback()
		return s.terminalSubmitReceipt(ctx, userID, id, input, key, digest, nowMS, playtest)
	}
	if c.state == "active" && (!c.start.Valid || !c.submitUntil.Valid || nowMS < c.start.Int64 || nowMS > c.submitUntil.Int64) {
		readTx.Rollback()
		return ChallengeView{}, ErrClosed
	}
	if c.state == "verifying" && (!c.received.Valid || nowMS > c.received.Int64+600000) {
		readTx.Rollback()
		return ChallengeView{}, ErrClosed
	}
	if err = readTx.Commit(); err != nil {
		return ChallengeView{}, err
	}
	job, already, err := s.reserveJob(id, input.Inputs, input.TerminalTick)
	if err != nil {
		return ChallengeView{}, err
	}
	release := !already
	defer func() {
		if release {
			s.releaseJob(job)
		}
	}()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ChallengeView{}, err
	}
	defer tx.Rollback()
	if playtest {
		err = s.authorizeAdminTx(ctx, tx, userID)
	} else {
		err = s.authorizeUserTx(ctx, tx, userID, nowMS, false)
	}
	if err != nil {
		return ChallengeView{}, err
	}
	c, err = readChallengeTx(ctx, tx, id)
	if err != nil {
		return ChallengeView{}, err
	}
	if c.userID != userID || c.playtest != playtest {
		return ChallengeView{}, ErrNotFound
	}
	if !sameCapability(c.capabilityHash, input.TabCapability) {
		return ChallengeView{}, ErrForbidden
	}
	metadata := struct {
		Digest       string `json:"digest"`
		TerminalTick int    `json:"terminal_tick"`
	}{hex.EncodeToString(digest[:]), input.TerminalTick}
	actorKind, route := "user", "/challenges/{id}/submit"
	if playtest {
		actorKind, route = "admin", "/playtests/{id}/submit"
	}
	decision, err := s.beginMutationTx(ctx, tx, actorKind, userID, key, http.MethodPost, route, []string{id}, metadata, nowMS)
	if err != nil {
		return ChallengeView{}, err
	}
	if c.inputDigest != nil && subtle.ConstantTimeCompare(c.inputDigest, digest[:]) != 1 {
		return ChallengeView{}, ErrConflict
	}
	if c.state == "active" {
		if !c.start.Valid || !c.submitUntil.Valid || nowMS < c.start.Int64 || nowMS > c.submitUntil.Int64 {
			return ChallengeView{}, ErrClosed
		}
		res, err := tx.ExecContext(ctx, `UPDATE fatfish_challenges SET state='verifying',input_digest=?,received_at_ms=?,revision=revision+1
 WHERE id=? AND state='active' AND input_digest IS NULL AND revision=?`, digest[:], nowMS, id, c.revision)
		if err != nil {
			return ChallengeView{}, err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return ChallengeView{}, err
		}
		if affected != 1 {
			return ChallengeView{}, ErrConflict
		}
		c, err = readChallengeTx(ctx, tx, id)
		if err != nil {
			return ChallengeView{}, err
		}
	} else if c.state == "verifying" {
		if !c.received.Valid || nowMS > c.received.Int64+600000 {
			return ChallengeView{}, ErrClosed
		}
	} else {
		view, err := c.view(nowMS, "")
		if err != nil {
			return ChallengeView{}, err
		}
		if decision.Kind != idempotency.Replay {
			if err = completeMutationTx(ctx, tx, decision, view, http.StatusOK); err != nil {
				return ChallengeView{}, err
			}
		}
		return view, tx.Commit()
	}
	view, err := c.view(nowMS, "")
	if err != nil {
		return ChallengeView{}, err
	}
	if decision.Kind != idempotency.Replay {
		if err = completeMutationTx(ctx, tx, decision, view, http.StatusAccepted); err != nil {
			return ChallengeView{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return ChallengeView{}, err
	}
	if !already {
		release = false
		if err = s.startJob(job); err != nil {
			return ChallengeView{}, err
		}
	}
	return view, nil
}

func (s *Service) terminalSubmitReceipt(ctx context.Context, userID int64, id string, input SubmitInput, key string, digest [32]byte, nowMS int64, playtest bool) (ChallengeView, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ChallengeView{}, err
	}
	defer tx.Rollback()
	if playtest {
		err = s.authorizeAdminTx(ctx, tx, userID)
	} else {
		err = s.authorizeUserTx(ctx, tx, userID, nowMS, false)
	}
	if err != nil {
		return ChallengeView{}, err
	}
	c, err := readChallengeTx(ctx, tx, id)
	if err != nil {
		return ChallengeView{}, err
	}
	if c.userID != userID || c.playtest != playtest {
		return ChallengeView{}, ErrNotFound
	}
	if !sameCapability(c.capabilityHash, input.TabCapability) {
		return ChallengeView{}, ErrForbidden
	}
	if c.inputDigest == nil || subtle.ConstantTimeCompare(c.inputDigest, digest[:]) != 1 {
		return ChallengeView{}, ErrConflict
	}
	actorKind, route := "user", "/challenges/{id}/submit"
	if playtest {
		actorKind, route = "admin", "/playtests/{id}/submit"
	}
	metadata := struct {
		Digest       string `json:"digest"`
		TerminalTick int    `json:"terminal_tick"`
	}{hex.EncodeToString(digest[:]), input.TerminalTick}
	decision, err := s.beginMutationTx(ctx, tx, actorKind, userID, key, http.MethodPost, route, []string{id}, metadata, nowMS)
	if err != nil {
		return ChallengeView{}, err
	}
	view, err := c.view(nowMS, "")
	if err != nil {
		return ChallengeView{}, err
	}
	if decision.Kind != idempotency.Replay {
		if err = completeMutationTx(ctx, tx, decision, view, http.StatusOK); err != nil {
			return ChallengeView{}, err
		}
	}
	return view, tx.Commit()
}
