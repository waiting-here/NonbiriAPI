package adapters

import (
	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

func mapAITerms(v *duel.AITerms) *lifecycle.DuelAITerms {
	if v == nil {
		return nil
	}
	out := lifecycle.DuelAITerms(*v)
	return &out
}
func mapAIView(v *duel.AIView) *lifecycle.DuelAIView {
	if v == nil {
		return nil
	}
	return &lifecycle.DuelAIView{Terms: lifecycle.DuelAITerms(v.Terms), MemoryEnabled: v.MemoryEnabled, MemorySamples: v.MemorySamples, FirstClear: v.FirstClear, Reward: v.Reward}
}
func mapAISources(values []duel.ActionSource) []lifecycle.DuelActionSource {
	if values == nil {
		return nil
	}
	out := make([]lifecycle.DuelActionSource, len(values))
	for i, v := range values {
		out[i] = lifecycle.DuelActionSource(v)
	}
	return out
}
func mapAIExport(v *duel.AIExport) *lifecycle.DuelAIExport {
	if v == nil {
		return nil
	}
	out := &lifecycle.DuelAIExport{Preferences: []lifecycle.DuelAIPreferenceExport{}, Memories: []lifecycle.DuelAIMemoryExport{}, Snapshots: []lifecycle.DuelAIMemorySnapshotExport{}, Clears: []lifecycle.DuelAIClearExport{}}
	for _, item := range v.Preferences {
		out.Preferences = append(out.Preferences, lifecycle.DuelAIPreferenceExport(item))
	}
	for _, item := range v.Memories {
		out.Memories = append(out.Memories, lifecycle.DuelAIMemoryExport(item))
	}
	for _, item := range v.Snapshots {
		out.Snapshots = append(out.Snapshots, lifecycle.DuelAIMemorySnapshotExport(item))
	}
	for _, item := range v.Clears {
		out.Clears = append(out.Clears, lifecycle.DuelAIClearExport(item))
	}
	return out
}
