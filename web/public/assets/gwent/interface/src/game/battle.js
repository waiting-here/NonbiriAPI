/* eslint-disable */
import { decoyTargets } from '../cards/mechanics.js';
import { effectiveFactionRule } from './factions.js';
import { knownDeckTop } from './deck.js';

export function legalActions(player, arena) {
  const actions = [];
  for (const card of player.hand.cards) {
    if (card.abilities.includes('decoy')) {
      if (decoyTargets(arena, player).length) actions.push({ type: 'play_card', card, row: null });
      continue;
    }
    if (card.isSpecial()) {
      for (const rowName of ['close', 'ranged', 'siege']) {
        const row = board.getRow(card, rowName, player);
        if (!row.special) actions.push({ type: 'play_card', card, row });
      }
    } else if (card.row === 'agile') {
      for (const rowName of ['close', 'ranged'])
        actions.push({
          type: 'play_card',
          card,
          row: board.getRow(card, rowName, player),
        });
    } else
      actions.push({
        type: 'play_card',
        card,
        row: card.row === 'special' ? null : board.getRow(card, card.row, player),
      });
  }
  if (player.leaderAvailable) actions.push({ type: 'leader' });
  actions.push({ type: 'pass' });
  return actions;
}
export async function executeAction(player, action, arena) {
  if (game.state !== GameState.PLAYING || game.currPlayer !== player || player.passed)
    throw Error('当前玩家不能行动');
  const valid = legalActions(player, arena).some(
    (candidate) =>
      candidate.type === action.type &&
      candidate.card === action.card &&
      candidate.row === action.row,
  );
  if (!valid) throw Error('非法出牌或目标战线');
  player.arenaAction = action.type;
  if (action.type === 'pass') return player.passRound();
  if (action.type === 'leader') return player.activateLeader();
  const card = action.card;
  if (card.abilities.includes('decoy'))
    return player.playCardAction(card, () => ability_dict.decoy.activated(card));
  if (card.row === 'special' && !card.isSpecial())
    return player.playCardAction(card, async () => {
      await board.toGrave(card, player.hand);
      await ability_dict[card.abilities[0]].placed(card);
    });
  if (action.row) return player.playCardToRow(card, action.row);
}
// Decisions use only the available actions.
export function decisionSnapshot(player, arena) {
  const opponent = player.opponent();
  const cardData = (card) => ({
    id: card.arenaData.id,
    name: card.name,
    power: card.power,
    basePower: card.basePower,
    hero: card.hero,
    row: card.row,
    abilities: card.abilities.map((id) => ({
      id,
      description: ability_dict[id]?.description || '',
    })),
    ...(card.arenaData.musterGroup ? { musterGroup: card.arenaData.musterGroup } : {}),
    ...(card.arenaData.transformForm ? { transformForm: card.arenaData.transformForm } : {}),
    ...(card.arenaData.avengerForm ? { avengerForm: card.arenaData.avengerForm } : {}),
    ...(card.arenaData.generated ? { generated: true, ephemeral: !!card.arenaData.ephemeral } : {}),
  });
  const playerData = (owner) => ({
    faction: owner.deck.faction,
    passive: effectiveFactionRule(owner),
    lives: owner.health,
    passed: !!owner.passed,
    handCount: owner.hand.cards.length,
    deckCount: owner.deck.cards.length,
    boost: owner.arenaBoost || 0,
    shield: owner.arenaShield || 0,
    leader: { ...cardData(owner.leader), available: !!owner.leaderAvailable },
    grave: owner.grave.cards.map(cardData),
  });
  return {
    round: game.roundCount,
    self: { ...playerData(player), knownDeckTop: knownDeckTop(player.deck).map(cardData) },
    enemy: playerData(opponent),
    playerPower: player.total,
    enemyPower: player.opponent().total,
    enemyHandCount: player.opponent().hand.cards.length,
    board: board.row.map((row) => ({
      row: row.type,
      side:
        row.elem_parent.parentElement.id === 'field-me'
          ? player === player_me
            ? 'self'
            : 'enemy'
          : player === player_op
            ? 'self'
            : 'enemy',
      weather: row.effects.weather,
      effects: {
        horn: row.effects.horn,
        morale: row.effects.morale,
        catalyst: row.effects.mardroeme,
      },
      cards: [...row.cards, ...(row.special ? [row.special] : [])].map(cardData),
    })),
    hand: player.hand.cards.map(cardData),
    legalActions: legalActions(player, arena).map((action, index) => ({
      index,
      type: action.type,
      cardId: action.card?.arenaData.id,
      row: action.row?.type,
    })),
  };
}
