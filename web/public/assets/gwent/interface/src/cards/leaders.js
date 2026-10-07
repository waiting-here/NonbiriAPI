/* eslint-disable */
import { deployUnit } from './mechanics.js';
import { balanceRules } from './balance.js';

// Alternate leader configurations reuse the four existing illustrations and base catalog entries.
const options = {
  openai: [
    ['assault', '前线统筹', 'leader_openai_assault'],
    ['compute', '算力统筹', 'leader_openai_compute'],
  ],
  deepseek: [
    ['recover', '检查点调度', 'leader_deepseek_recover'],
    ['rebirth', '第三局重启', 'leader_deepseek_rebirth'],
  ],
  claude: [
    ['deny', '协议封锁', 'leader_claude_deny'],
    ['retrieve', '上下文回收', 'leader_claude_retrieve'],
  ],
  gemini: [
    ['clear', '晴空解析', 'leader_gemini_clear'],
    ['sensors', '感知统筹', 'leader_gemini_sensors'],
  ],
};

export function leaderDefinitions(catalog) {
  return catalog
    .filter((card) => card.type === 'leader')
    .flatMap((source) => [
      source,
      ...(options[source.faction] || []).map(([suffix, title, ability]) => ({
        ...source,
        id: `${source.faction}_leader_${suffix}`,
        name: `${source.name} · ${title}`,
        abilities: [ability],
        choiceOf: source.id,
      })),
    ]);
}

export const thirdRoundRule = {
  name: '第三局重启',
  description:
    '替换持续推理：不再保留小局单位；第三小局开始时，从己方弃牌堆随机恢复最多两张普通单位，英雄和特殊牌除外。恢复正常触发部署技能。',
};

export function hasThirdRoundRecovery(player) {
  return (
    player.deck.faction === 'deepseek' &&
    player.leader.abilities.includes('leader_deepseek_rebirth')
  );
}

export function registerExtraLeaders(arena) {
  const define = (id, name, description, activated) => {
    ability_dict[id] = {
      name,
      description: `${activated ? '整场限一次，占用一次行动。' : '被动领袖，无主动技能。'}${description}`,
      ...(activated ? { activated } : {}),
    };
  };
  const horn = (rowName) => async (card) => {
    const row = board.getRow({ abilities: [] }, rowName, card.holder);
    if (row.special) return;
    const token = new Card(card_dict[arena.index.get('compute_surge')], card.holder);
    token.arenaData = { ...token.arenaData, generated: true, ephemeral: true };
    await row.addCard(token);
    arena.view.log(`${card.name} · 本战线普通单位翻倍，不与其他翻倍叠加。`);
  };
  define(
    'leader_openai_assault',
    '前线统筹',
    '本小局己方推理前线普通单位战力翻倍；英雄除外，翻倍不叠加。占用该战线翻倍牌槽，已有翻倍牌时无效；清场时消失。',
    horn('close'),
  );
  define(
    'leader_openai_compute',
    '算力统筹',
    '本小局己方算力集群普通单位战力翻倍；英雄除外，翻倍不叠加。占用该战线翻倍牌槽，已有翻倍牌时无效；清场时消失。',
    horn('siege'),
  );
  define(
    'leader_gemini_sensors',
    '感知统筹',
    '本小局己方感知矩阵普通单位战力翻倍；英雄除外，翻倍不叠加。占用该战线翻倍牌槽，已有翻倍牌时无效；清场时消失。',
    horn('ranged'),
  );
  define(
    'leader_gemini_clear',
    '晴空解析',
    `解除双方全部天气，再抽取最多 ${balanceRules.clearLeaderDraw} 张牌；无天气也可以使用，牌库不足时只抽剩余牌。`,
    async (card) => {
      await weather.clearWeather();
      for (
        let count = 0;
        count < balanceRules.clearLeaderDraw && card.holder.deck.cards.length;
        count++
      )
        await card.holder.deck.draw(card.holder.hand);
    },
  );
  define('leader_deepseek_rebirth', '第三局重启', thirdRoundRule.description);
  define(
    'leader_deepseek_recover',
    '检查点调度',
    '从己方弃牌堆选择一张普通单位恢复到战线，正常触发部署技能；英雄和特殊牌除外。',
    async (card) => {
      const player = card.holder,
        grave = player.grave;
      let target;
      if (player.controller instanceof ControllerAI && grave.cards.some((unit) => unit.isUnit()))
        target = player.controller.medic(card, grave);
      else if (grave.cards.some((unit) => unit.isUnit()))
        await arena.view.chooseCards(
          grave,
          1,
          (container, index) => {
            target = container.cards[index];
          },
          (unit) => unit.isUnit(),
          false,
          '检查点调度 · 恢复普通单位',
        );
      if (target) await deployUnit(arena, target, grave);
    },
  );
  define(
    'leader_claude_deny',
    '协议封锁',
    '封锁对手尚未使用的主动领袖技能；不影响阵营被动与被动领袖。',
    async (card) => {
      const enemy = card.holder.opponent();
      if (enemy.leaderAvailable) enemy.disableLeader();
    },
  );
  define(
    'leader_claude_retrieve',
    '上下文回收',
    '从敌方公开弃牌堆选择一张普通单位加入己方手牌；英雄和特殊牌除外，不立即部署。',
    async (card) => {
      const player = card.holder,
        grave = player.opponent().grave;
      let target;
      if (player.controller instanceof ControllerAI) {
        target = grave.cards
          .filter((unit) => unit.isUnit())
          .sort(
            (a, b) =>
              (b.abilities.includes('spy') ? 20 : b.basePower) -
              (a.abilities.includes('spy') ? 20 : a.basePower),
          )[0];
      } else if (grave.cards.some((unit) => unit.isUnit()))
        await arena.view.chooseCards(
          grave,
          1,
          (container, index) => {
            target = container.cards[index];
          },
          (unit) => unit.isUnit(),
          false,
          '上下文回收 · 选择敌方普通单位',
        );
      if (target) {
        grave.removeCard(target);
        target.holder = player;
        target.resetPower();
        await player.hand.addCard(target);
      }
    },
  );
}
