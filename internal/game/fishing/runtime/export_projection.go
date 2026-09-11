package runtime

import "github.com/waiting-here/NonbiriAPI/internal/game/fishing"

type RankExportProjection struct {
	Rank                string  `json:"rank"`
	SpeciesKey          *string `json:"species_key"`
	SizeCM              *int    `json:"size_cm"`
	TotalCredits        *string `json:"total_credits"`
	BlueFatFishLengthCM *string `json:"blue_fat_fish_length_cm"`
}

func ProjectExportRank(value *FishingLeaderboardRow) (*RankExportProjection, error) {
	if value == nil {
		return nil, nil
	}
	if value.Rank == "" || (value.SpeciesKey == "") == (value.TotalCredits == "") {
		return nil, ErrInvariant
	}
	out := &RankExportProjection{Rank: value.Rank}
	if value.SpeciesKey != "" {
		species, size := value.SpeciesKey, value.SizeCM
		out.SpeciesKey, out.SizeCM = &species, &size
	}
	if value.BlueFatFishLengthCM != nil {
		if !fishing.ValidBlueFatFishLength(*value.BlueFatFishLengthCM) || value.SpeciesKey != "koi" && value.SpeciesKey != "taimen" && value.SpeciesKey != "yellowcheek" {
			return nil, ErrInvariant
		}
		length := *value.BlueFatFishLengthCM
		out.BlueFatFishLengthCM = &length
	}
	if value.TotalCredits != "" {
		total := value.TotalCredits
		out.TotalCredits = &total
	}
	return out, nil
}

func ValidExportOutcome(value FishingOutcome) bool {
	return value.BlueFatFishLengthCM == nil || value.Tier == "legend" && fishing.ValidBlueFatFishLength(*value.BlueFatFishLengthCM)
}

func (value UserExport) ValidateProjection() error {
	for _, rank := range []*FishingLeaderboardRow{value.Single, value.Total, value.RollingBest} {
		if _, err := ProjectExportRank(rank); err != nil {
			return err
		}
	}
	for _, batch := range value.Terminal {
		for _, outcome := range batch.Outcomes {
			if !ValidExportOutcome(outcome) {
				return ErrInvariant
			}
		}
	}
	return nil
}
