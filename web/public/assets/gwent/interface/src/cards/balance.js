// Game tuning only; hero model identity, base power and copy limits stay in cards.json.
export const balanceRules = {
  growthPerAction: 1,
  growthLimit: 6,
  predictionBoost: 3,
  leaderBoost: 3,
  leaderOptimization: 4,
  adaptationBoost: 3,
  clearLeaderDraw: 2,
};

export function growthGain(card) {
  return Math.max(
    0,
    Math.min(balanceRules.growthPerAction, balanceRules.growthLimit - (card.arenaGrowth || 0)),
  );
}
