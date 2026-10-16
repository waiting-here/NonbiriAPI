package runtime

import "github.com/waiting-here/NonbiriAPI/internal/game/fishing"

type RankExportProjection struct {
	Rank                string  `json:"rank"`
	SpeciesKey          *string `json:"species_key"`
	SizeCM              *int    `json:"size_cm"`
	TotalCredits        *string `json:"total_credits"`
	BlueFatFishLengthCM *string `json:"blue_fat_fish_length_cm"`
	MilkCrabLengthCM    *string `json:"milk_crab_length_cm"`
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
	if !validPresentation(value.SpeciesKey, value.SizeCM, value.BlueFatFishLengthCM, value.MilkCrabLengthCM) {
		return nil, ErrInvariant
	}
	if value.BlueFatFishLengthCM != nil {
		length := *value.BlueFatFishLengthCM
		out.BlueFatFishLengthCM = &length
	}
	if value.MilkCrabLengthCM != nil {
		length := *value.MilkCrabLengthCM
		out.MilkCrabLengthCM = &length
	}
	if value.TotalCredits != "" {
		total := value.TotalCredits
		out.TotalCredits = &total
	}
	return out, nil
}

func validPresentation(species string, size int, blue, milk *string) bool {
	if blue == nil && milk == nil {
		return true
	}
	if blue != nil && milk != nil || size < 100 || size > 200 || species != "koi" && species != "taimen" && species != "yellowcheek" {
		return false
	}
	length := blue
	if length == nil {
		length = milk
	}
	return fishing.ValidPresentationLength(*length)
}

func ValidExportOutcome(value FishingOutcome) bool {
	return validPresentation(value.SpeciesKey, value.SizeCM, value.BlueFatFishLengthCM, value.MilkCrabLengthCM) &&
		(value.BlueFatFishLengthCM == nil && value.MilkCrabLengthCM == nil || value.Tier == "legend")
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
