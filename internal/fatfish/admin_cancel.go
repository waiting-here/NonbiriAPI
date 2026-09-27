package fatfish

import (
	"context"
	"net/http"
)

type CancelInput struct {
	Reason string `json:"reason"`
}

func (s *Service) AdminCancelChallenge(ctx context.Context, actorID int64, id string, input CancelInput, key string) (ChallengeView, error) {
	if len(input.Reason) == 0 || len(input.Reason) > 128 || !validText(input.Reason, 128, true) {
		return ChallengeView{}, ErrInvalid
	}
	nowMS, err := s.nowMS()
	if err != nil {
		return ChallengeView{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ChallengeView{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeAdminTx(ctx, tx, actorID); err != nil {
		return ChallengeView{}, err
	}
	c, err := readChallengeTx(ctx, tx, id)
	if err != nil {
		return ChallengeView{}, err
	}
	if c.playtest {
		return ChallengeView{}, ErrInvalid
	}
	decision, err := s.beginMutationTx(ctx, tx, "admin", actorID, key, http.MethodPost, "/challenges/{id}/cancel", []string{id}, input, nowMS)
	if err != nil {
		return ChallengeView{}, err
	}
	if replay, ok, replayErr := replayMutation[ChallengeView](decision); ok || replayErr != nil {
		return replay, replayErr
	}
	if c.state == "prepared" || c.state == "active" || c.state == "verifying" {
		if err = s.cancelChallengeTx(ctx, tx, c, nowMS, input.Reason, true); err != nil {
			return ChallengeView{}, err
		}
		c, err = readChallengeTx(ctx, tx, id)
		if err != nil {
			return ChallengeView{}, err
		}
	} else if c.state != "cancelled_refunded" {
		return ChallengeView{}, ErrConflict
	}
	view, err := c.view(nowMS, "")
	if err != nil {
		return ChallengeView{}, err
	}
	if err = completeMutationTx(ctx, tx, decision, view, http.StatusOK); err != nil {
		return ChallengeView{}, err
	}
	if err = tx.Commit(); err != nil {
		return ChallengeView{}, err
	}
	s.cancelJobAndWait(id)
	return view, nil
}
