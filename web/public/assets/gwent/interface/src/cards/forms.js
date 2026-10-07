/* eslint-disable */
// Generated forms have stable IDs but are never selectable deck entries.
export function runtimeForms(catalog) {
  return catalog.flatMap((source) =>
    ['transformForm', 'avengerForm'].flatMap((key) => {
      const form = source[key];
      if (!form) return [];
      return [
        {
          ...source,
          transformForm: undefined,
          avengerForm: undefined,
          ...form,
          type: 'unit',
          maxCopies: 1,
          starterCopies: 0,
          generated: true,
          ephemeral: key === 'avengerForm',
          originId: source.id,
          category: 'generated',
        },
      ];
    }),
  );
}

export function createForm(arena, card, key) {
  const id = card.arenaData[key]?.id;
  if (!id || !arena.index.has(id)) throw Error('衍生形态映射缺失');
  return new Card(card_dict[arena.index.get(id)], card.holder);
}

export async function summonOnDeparture(arena, card, row) {
  if (!card.abilities.includes('avenger')) return;
  const form = createForm(arena, card, 'avengerForm');
  await row.addCard(form);
  arena.view.log(`离场召唤 · ${card.name} 留下 ${form.name}。`);
}

export function registerForms(arena) {
  ability_dict.mardroeme = {
    name: '蒸馏催化',
    description:
      '在场时触发同一战线所有可变身普通单位的强化形态；先部署催化器或先部署待变身单位都能触发。',
    placed: async (card, row) => {
      for (const target of [...row.cards])
        if (target.abilities.includes('berserker'))
          await ability_dict.berserker.placed(target, row);
    },
  };
  ability_dict.berserker = {
    name: '形态编译',
    description:
      '同战线存在蒸馏催化器时，替换为强化形态；原单位不进入弃牌堆，变身清除原单位的临时强化。',
    placed: async (card, row) => {
      if (!row.effects.mardroeme || !row.cards.includes(card)) return;
      const form = createForm(arena, card, 'transformForm');
      row.removeCard(card);
      await row.addCard(form);
      arena.view.log(`形态编译 · ${card.name} 变为 ${form.name}。`);
    },
  };
  // Resolve this through Board.moveTo, which awaits it; upstream removed callbacks are fire-and-forget.
  ability_dict.avenger = {
    name: '离场召唤',
    description:
      '从战线进入弃牌堆或回手时，在原战线生成一个后继单位；包括小局清场。后继离场进入弃牌堆时消失，不能反复恢复，不再继续召唤。',
  };
  ability_dict.storm.name = '双域风暴';
  ability_dict.storm.description =
    '双方感知矩阵和算力集群同时受到天气影响，普通单位基础战力压低至最多 1；英雄免疫，与单战线天气独立叠加，晴天全部解除。';
}
