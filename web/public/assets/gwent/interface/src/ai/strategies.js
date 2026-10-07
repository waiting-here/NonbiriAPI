/* eslint-disable */
import { models } from './models.js';
export function strategyAdjustment(player, action, base, arena) {
  const profile = models[player.deck.faction];
  if (action.type !== 'play_card') return base;
  const card = action.card,
    ability = card.abilities[0],
    opponent = player.opponent();
  if (card.hero) {
    if (profile.style === 'calculation')
      return base + Math.max(0, 9 - arena.rows(player).flatMap((row) => row.cards).length);
    if (game.roundCount === 1 && opponent.total < 10) return base * 0.5;
  }
  if (profile.style === 'control' && ['scorch', 'frost', 'fog', 'rain', 'medic'].includes(ability))
    return base * 1.35;
  if (
    profile.style === 'adaptive' &&
    player.total < opponent.total &&
    card.basePower >= opponent.total - player.total
  )
    return base * 1.2;
  if (profile.style === 'multimodal' && ['clear', 'medic'].includes(ability)) return base * 1.25;
  return base;
}
