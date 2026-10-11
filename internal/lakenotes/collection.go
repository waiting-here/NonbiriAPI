package lakenotes

import (
	"context"
	"database/sql"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/lakenotes/rules"
)

type CollectionInput struct {
	Location string `json:"location"`
}

// ClaimCollection keeps the permanent receipt and donation reward in one transaction.
func (s *Service) ClaimCollection(ctx context.Context, user int64, key string, input CollectionInput) (MutationResult[ProfileView], error) {
	if _, _, ok := rules.CollectionProgress(rules.InitialProfile(), input.Location); !ok {
		return MutationResult[ProfileView]{}, ErrInvalid
	}
	return mutate(ctx, s, user, false, key, "POST", baseRoute+"/collection-claims", input, func(tx *sql.Tx, now time.Time) (ProfileView, error) {
		if _, err := s.qualifiedTx(ctx, tx, user, now.Unix()); err != nil {
			return ProfileView{}, err
		}
		row, err := profileTx(ctx, tx, user, true, now.Unix())
		if err != nil {
			return ProfileView{}, err
		}
		if _, claimed := row.profile.CollectionClaims[input.Location]; claimed {
			return ProfileView{}, ErrConflict
		}
		caught, total, _ := rules.CollectionProgress(row.profile, input.Location)
		if total == 0 || caught != total {
			return ProfileView{}, ErrConflict
		}
		if s.reward == nil {
			return ProfileView{}, ErrInvariant
		}
		if err = s.reward(ctx, tx, user, input.Location, now.Unix()); err != nil {
			return ProfileView{}, err
		}
		if row.profile.CollectionClaims == nil {
			row.profile.CollectionClaims = map[string]int64{}
		}
		row.profile.CollectionClaims[input.Location] = now.Unix()
		if err = saveProfileTx(ctx, tx, user, &row, now.Unix()); err != nil {
			return ProfileView{}, err
		}
		return s.profileViewTx(ctx, tx, user, now)
	})
}
