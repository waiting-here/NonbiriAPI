/* eslint-disable */
import { deployUnit } from '../cards/mechanics.js';
import { hasThirdRoundRecovery, thirdRoundRule } from '../cards/leaders.js';

export const factionRules = {
  openai: {
    name: '推演积累',
    description: '赢得一小局后，在下一小局开始时抽一张牌；牌库为空时不抽。',
  },
  deepseek: {
    name: '持续推理',
    description:
      '小局结束后随机保留己方一张普通单位；英雄和诱饵除外。保留单位的临时基础强化在新小局清除。',
  },
  claude: { name: '共识裁定', description: '小局战力相同时获胜；双方都是 Claude 时仍按平局处理。' },
  gemini: {
    name: '感知先机',
    description: '选择整场第一小局的先手，限时 15 秒；超时随机。双方都是 Gemini 时随机先手。',
  },
};

export function effectiveFactionRule(player) {
  return hasThirdRoundRecovery(player) ? thirdRoundRule : factionRules[player.deck.faction];
}

export function roundDifference(meFaction, opFaction, mePower, opPower) {
  const difference = mePower - opPower;
  if (difference !== 0 || (meFaction === 'claude') === (opFaction === 'claude')) return difference;
  return meFaction === 'claude' ? 1 : -1;
}

export function playerDifference(
  player,
  ownPower = player.total,
  enemyPower = player.opponent().total,
) {
  return roundDifference(player.deck.faction, player.opponent().deck.faction, ownPower, enemyPower);
}

// Passing can secure a won round, or a match when a normal tie eliminates only the opponent.
export function canFinishByPassing(player) {
  const opponent = player.opponent(),
    difference = playerDifference(player);
  return (
    opponent.passed &&
    (difference > 0 || (difference === 0 && opponent.health === 1 && player.health > 1))
  );
}

export function registerFaction(arena, player) {
  const faction = player.deck.faction;
  player.arenaPassive = effectiveFactionRule(player);
  const log = (text) => arena.view.log(`${factions[faction].name} · ${text}`);
  if (faction === 'openai')
    game.roundStart.push(async () => {
      if (
        game.roundCount > 1 &&
        game.roundHistory.at(-1)?.winner === player &&
        player.deck.cards.length
      ) {
        await player.deck.draw(player.hand);
        log('推演积累 · 胜局后抽一张牌。');
      }
      return false;
    });
  if (hasThirdRoundRecovery(player))
    game.roundStart.push(async () => {
      if (game.roundCount !== 3) return false;
      const current = game.currPlayer;
      // Sample before deploying: a recovered medic may change the remaining discard pile.
      const targets = player.grave.findCardsRandom((card) => card.isUnit(), 2);
      game.currPlayer = player;
      try {
        for (const card of targets)
          if (player.grave.cards.includes(card)) await deployUnit(arena, card, player.grave);
        log(`第三局重启 · 随机恢复最多两张普通单位。`);
      } finally {
        game.currPlayer = current;
      }
      return true;
    });
  if (faction === 'deepseek' && !hasThirdRoundRecovery(player))
    game.roundEnd.push(() => {
      const units = arena
        .rows(player)
        .flatMap((row) => row.cards)
        .filter((card) => card.isUnit());
      if (!units.length) return false;
      const card = units[Math.floor(Math.random() * units.length)];
      card.noRemove = true;
      log(`持续推理 · 保留 ${card.name}。`);
      game.roundStart.push(() => {
        delete card.noRemove;
        card.resetPower();
        arena
          .rows(player)
          .find((row) => row.cards.includes(card))
          ?.updateScore();
        return true;
      });
      return false;
    });
  if (faction === 'gemini' && player.opponent().deck.faction !== 'gemini')
    game.gameStart.push(async () => {
      // Preserve the same random fallback regardless of whether a human chooses in time.
      game.firstPlayer = Math.random() < 0.5 ? player_me : player_op;
      if (!(player.controller instanceof ControllerAI)) {
        const previous = game.currPlayer;
        game.currPlayer = player;
        try {
          await arena.view.chooseCards(
            { cards: [player.leader, player.opponent().leader], choicePhase: 'initiative' },
            1,
            (container, index) => {
              game.firstPlayer = container.cards[index].holder;
            },
            () => true,
            true,
            '感知先机 · 选择先行动的阵营；继续则随机先手',
          );
        } finally {
          game.currPlayer = previous;
        }
      }
      log(`感知先机 · ${factions[game.firstPlayer.deck.faction].name} 先手。`);
      return true;
    });
}
