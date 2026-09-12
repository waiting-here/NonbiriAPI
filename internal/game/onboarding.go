package game

import (
	"context"
	"database/sql"
)

// OnboardingTask is immutable compiled module metadata, not site configuration.
type OnboardingTask struct {
	Key         string
	RewardMilli int64
}

func (module ModuleDescriptor) OnboardingReward(taskKey string) (int64, error) {
	for _, task := range module.Onboarding {
		if task.Key == taskKey {
			return task.RewardMilli, nil
		}
	}
	return 0, ErrInvalidContract
}

type OnboardingItem struct {
	Key       string `json:"key"`
	Reward    string `json:"reward"`
	AssetType string `json:"asset_type"`
	Completed bool   `json:"completed"`
}

type OnboardingProgress struct {
	Items        []OnboardingItem `json:"items"`
	AllCompleted bool             `json:"all_completed"`
}

// OnboardingProgress reads the user's lifetime facts once for all modules.
// Ordinary game-history retention never determines eligibility.
func (registry *Registry) OnboardingProgress(ctx context.Context, tx *sql.Tx, userID int64) (map[string]OnboardingProgress, error) {
	rows, err := tx.QueryContext(ctx, "SELECT game_key,task_key FROM game_onboarding_completions WHERE user_id=?", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	completed := map[string]bool{}
	for rows.Next() {
		var gameKey, taskKey string
		if err := rows.Scan(&gameKey, &taskKey); err != nil {
			return nil, err
		}
		completed[gameKey+"\x00"+taskKey] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make(map[string]OnboardingProgress, len(registry.modules))
	for _, module := range registry.modules {
		progress := OnboardingProgress{Items: make([]OnboardingItem, 0, len(module.Onboarding)), AllCompleted: true}
		for _, task := range module.Onboarding {
			done := completed[module.ID+"\x00"+task.Key]
			progress.Items = append(progress.Items, OnboardingItem{Key: task.Key, Reward: FormatAmount(task.RewardMilli), AssetType: "general", Completed: done})
			progress.AllCompleted = progress.AllCompleted && done
		}
		result[module.ID] = progress
	}
	return result, nil
}
