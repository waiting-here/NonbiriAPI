/* eslint-disable */
import { models } from '../ai/models.js';
import { revealDeckTop } from '../game/deck.js';
import { balanceRules, growthGain } from './balance.js';
import { registerMechanics } from './mechanics.js';
import { factionRules } from '../game/factions.js';
import { registerExtraLeaders } from './leaders.js';

export function registerAbilities(arena) {
  registerMechanics(arena);
  const define = (id, name, description, placed) =>
    (ability_dict[id] = { name, description, placed });
  const log = (text) => arena.view.log(text);
  const refresh = () => {
    board.row.forEach((row) => row.updateScore());
    arena.view.render();
  };
  define(
    'future_predict',
    '未来推演',
    '查看自己牌库顶部至多两张牌；下一张普通单位基础战力 +3，英雄不消耗强化。未使用的强化在本小局结束时清除。',
    async (card) => {
      const player = card.holder;
      const revealed = revealDeckTop(player.deck);
      player.arenaBoost = (player.arenaBoost || 0) + balanceRules.predictionBoost;
      log(`${card.name} 预读牌库，下一张普通单位 +${balanceRules.predictionBoost}。`);
      if (!(player.controller instanceof ControllerAI))
        await arena.view.chooseCards(
          { cards: revealed },
          0,
          () => {},
          () => true,
          true,
          '未来推演 · 下一次抽牌顺序从左至右',
        );
    },
  );
  define(
    'deep_think',
    '深度思考',
    `在场时，己方每次出牌或使用领袖后成长 +${balanceRules.growthPerAction}，本小局最多成长 +${balanceRules.growthLimit}。放弃不触发，离场后清除。`,
    async (card) => {
      const owner = card.holder;
      game.turnEnd.push(() => {
        const row = board.row.find((row) => row.cards.includes(card));
        if (!row) return true;
        if (game.currPlayer === owner && owner.arenaAction !== 'pass') {
          const gain = growthGain(card);
          if (!gain) return false;
          card.arenaGrowth = (card.arenaGrowth || 0) + gain;
          row.updateScore();
          log(
            `${card.name} 深度思考 · 战力 +${gain}（成长 ${card.arenaGrowth}/${balanceRules.growthLimit}）。`,
          );
        }
        return false;
      });
    },
  );
  define(
    'long_context',
    '长文本理解',
    '复制敌方一张普通单位的恢复、对齐增益或战线剪枝技能。恢复和剪枝立即结算一次，对齐增益在此英雄在场时持续。没有合法目标时不触发。',
    async (card, row) => {
      const allowed = ['medic', 'morale', 'scorch_c', 'scorch_r', 'scorch_s'];
      const enemies = arena
        .rows(card.holder.opponent())
        .flatMap((row) => row.cards)
        .filter((target) => target.isUnit() && target.abilities.some((id) => allowed.includes(id)));
      if (!enemies.length) {
        log('长文本理解 · 当前没有可复制的普通单位技能。');
        return;
      }
      let target = enemies[0];
      if (card.holder.controller instanceof ControllerAI)
        target = card.holder.controller.chooseCopyTarget?.(card, row, enemies) || target;
      else
        await arena.view.chooseCards(
          { cards: enemies },
          1,
          (container, index) => {
            target = container.cards[index];
          },
          () => true,
          false,
          '长文本理解 · 选择复制目标',
        );
      const id = target.abilities.find((id) => allowed.includes(id));
      log(`${card.name} 复制 ${target.name} 的 ${ability_dict[id].name}。`);
      if (id === 'morale') {
        card.abilities.push('morale');
        row.effects.morale++;
      } else await ability_dict[id].placed(card, row);
      refresh();
    },
  );
  const analyze = async (card) => {
    const player = card.holder;
    let clear = false;
    if (weather.cards.length) {
      if (player.controller instanceof ControllerAI)
        clear = (player.controller.clearWeatherValue?.() || 0) > 0;
      else
        await arena.view.chooseCards(
          weather,
          1,
          () => {
            clear = true;
          },
          () => true,
          true,
          `视觉分析 · 选择任一天气以全部清除，或继续获得 +${balanceRules.adaptationBoost} 强化`,
        );
    }
    if (clear) {
      await weather.clearWeather();
      log('视觉分析 · 全部天气已解除。');
    } else {
      player.arenaBoost = (player.arenaBoost || 0) + balanceRules.adaptationBoost;
      log(`视觉分析 · 保留当前天气，下一张普通单位 +${balanceRules.adaptationBoost}。`);
    }
  };
  define(
    'visual_analysis',
    '视觉分析',
    `选择清除双方所有天气，或保留天气并使下一张普通单位基础战力 +${balanceRules.adaptationBoost}。没有天气时直接获得强化，本小局有效，英雄不消耗。`,
    analyze,
  );
  define(
    'chain_of_thought',
    '思维链',
    '下一张打出的普通单位基础战力 +5，可与未来推演叠加。英雄不消耗强化，天气仍会压低基础战力。未使用的强化在本小局结束时清除。',
    async (card) => {
      card.holder.arenaBoost = (card.holder.arenaBoost || 0) + 5;
      log('思维链 · 下一张普通单位 +5。');
    },
  );
  define(
    'safety_layer',
    '安全层',
    '本小局抵消下一次敌方模型剪枝或战线剪枝对己方单位的破坏。最多储存一层。',
    async (card) => {
      card.holder.arenaShield = 1;
      log('安全层 · 防护协议已就绪。');
    },
  );
  define(
    'context_window',
    '上下文窗口',
    '将至多三张手牌逐张放回牌库末尾，并逐张抽取顶部的牌。手牌数量不变；可以提前结束重抽。牌库为空时会取回刚放入的牌，同名副本仍可能被抽到。',
    async (card) => {
      const player = card.holder;
      if (player.controller instanceof ControllerAI) {
        const choices = [...player.hand.cards]
          .sort((a, b) => a.basePower - b.basePower)
          .slice(0, 3);
        choices.forEach((target) => player.deck.swapToBottom(player.hand, target));
      } else
        await arena.view.chooseCards(
          player.hand,
          3,
          (container, index) => player.deck.swapToBottom(container, container.cards[index]),
          () => true,
          true,
          '上下文窗口 · 最多重抽三张',
        );
      log('上下文窗口 · 手牌已重新组织。');
    },
  );
  for (const faction of Object.keys(models)) {
    const names = {
      openai: [
        '扩展推理',
        `抽取一张牌；下一张普通单位基础战力 +${balanceRules.leaderBoost}。强化本小局有效，可叠加，英雄不消耗。`,
      ],
      deepseek: [
        '深度优化',
        `抽取一张牌；己方最强普通单位基础战力 +${balanceRules.leaderOptimization}，离场后清除；没有单位时只抽牌。`,
      ],
      claude: [
        '防护协议',
        '抽取一张牌；本小局获得一层安全防护，抵消下一次敌方剪枝对己方单位的破坏；最多一层。',
      ],
      gemini: [
        '全域感知',
        `抽取一张牌；选择清除所有天气，或保留天气并使下一张普通单位基础战力 +${balanceRules.adaptationBoost}，本小局有效。`,
      ],
    };
    ability_dict[`leader_${faction}`] = {
      name: names[faction][0],
      description: `整场对局限一次：${names[faction][1]} 阵营被动 · ${factionRules[faction].name}：${factionRules[faction].description}`,
      weight: () => 12,
      activated: async (card) => {
        const player = card.holder;
        if (player.deck.cards.length) await player.deck.draw(player.hand);
        if (faction === 'gemini') await analyze(card);
        if (faction === 'openai')
          player.arenaBoost = (player.arenaBoost || 0) + balanceRules.leaderBoost;
        if (faction === 'deepseek') {
          const units = arena
            .rows(player)
            .flatMap((row) => row.cards)
            .filter((card) => card.isUnit())
            .sort((a, b) => b.power - a.power);
          if (units[0])
            units[0].arenaBonus = (units[0].arenaBonus || 0) + balanceRules.leaderOptimization;
        }
        if (faction === 'claude') player.arenaShield = 1;
        log(`${models[faction].short} 使用领袖技能 · ${names[faction][0]}。`);
        refresh();
      },
    };
  }
  registerExtraLeaders(arena);
  // Original weather/scoring/spy/medic/bond effects remain in use; descriptions are localized.
  const descriptions = {
    spy: '部署到敌方战线，计入敌方战力；抽两张牌。',
    medic: '从弃牌堆恢复一张普通单位（英雄和特殊牌除外）。',
    bond: '同一战线中，同名普通单位的战力乘以数量。',
    morale: '本战线其他普通单位战力 +1。',
    horn: '本战线其他普通单位战力翻倍，翻倍不叠加；携带此技能的单位不强化自身。每条战线最多部署一张翻倍技能牌，英雄不受影响。持续至本小局结束。',
    frost:
      '双方推理前线普通单位的基础战力压低至最多 1，随后仍计算集群、对齐和翻倍；英雄不受影响。持续至解除或本小局结束。',
    fog: '双方感知矩阵普通单位的基础战力压低至最多 1，随后仍计算集群、对齐和翻倍；英雄不受影响。持续至解除或本小局结束。',
    rain: '双方算力集群普通单位的基础战力压低至最多 1，随后仍计算集群、对齐和翻倍；英雄不受影响。持续至解除或本小局结束。',
    clear: '解除双方全部天气。',
    hero: '英雄免疫天气、普通强化、复活和剪枝；自身技能可以使自己成长。',
  };
  const localizedNames = {
    spy: '数据侦察',
    medic: '检查点恢复',
    bond: '集群联结',
    morale: '对齐增益',
    horn: '算力翻倍',
    frost: '冷启动',
    fog: '数据噪声',
    rain: '网络风暴',
    clear: '信号恢复',
    hero: '英雄',
  };
  for (const [id, description] of Object.entries(descriptions)) {
    ability_dict[id].description = description;
    ability_dict[id].name = localizedNames[id];
  }
  ability_dict.scorch.name = '模型剪枝';
  ability_dict.scorch.description =
    '摧毁战场上战力最高的全部普通单位（可能包括己方）。英雄不受影响。';
  for (const [id, label] of [
    ['scorch_c', '推理前线'],
    ['scorch_r', '感知矩阵'],
    ['scorch_s', '算力集群'],
  ]) {
    ability_dict[id].description =
      `敌方${label}总战力至少 10 时，摧毁该战线战力最高的全部普通单位；英雄不受影响。`;
  }
  const destroy = async (caster, targets) => {
    const protectedPlayers = new Set();
    for (const { card, row } of targets) {
      const owner = row.elem_parent.parentElement.id === 'field-me' ? player_me : player_op;
      if (owner !== caster.holder && owner.arenaShield) {
        owner.arenaShield = 0;
        protectedPlayers.add(owner);
        log(`${models[owner.deck.faction].short} 的安全层拦截剪枝。`);
      }
    }
    for (const { card, row } of targets) {
      const owner = row.elem_parent.parentElement.id === 'field-me' ? player_me : player_op;
      if (!protectedPlayers.has(owner)) {
        const power = card.power;
        await card.animate('scorch');
        await board.toGrave(card, row);
        log(`剪枝移除 ${card.name} · 战力 ${power}。`);
      }
    }
    refresh();
  };
  ability_dict.scorch.placed = async (caster) => {
    const units = board.row.flatMap((row) =>
      row.cards.filter((card) => card !== caster && card.isUnit()).map((card) => ({ card, row })),
    );
    const max = Math.max(0, ...units.map(({ card }) => card.power));
    if (!units.length) log('模型剪枝 · 战场没有可破坏的普通单位。');
    await destroy(
      caster,
      units.filter(({ card }) => card.power === max),
    );
  };
  for (const [id, rowName] of [
    ['scorch_c', 'close'],
    ['scorch_r', 'ranged'],
    ['scorch_s', 'siege'],
  ])
    ability_dict[id].placed = async (caster) => {
      const row = board.getRow({ abilities: [] }, rowName, caster.holder.opponent());
      if (row.total >= 10)
        await destroy(
          caster,
          row.maxUnits().map((card) => ({ card, row })),
        );
    };
}
