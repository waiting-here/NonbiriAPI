/* eslint-disable */
import { registerForms } from './forms.js';

export function decoyTargets(arena, player) {
  return arena
    .rows(player)
    .flatMap((row) => row.cards)
    .filter((card) => card.isUnit());
}

export async function deployUnit(arena, card, source) {
  if (card.row !== 'agile') return board.toRow(card, source);
  const row =
    card.holder.controller instanceof ControllerAI
      ? card.holder.controller.determineAgileRow(card)
      : await arena.view.chooseRow(card);
  if (row) await board.moveTo(card, row, source);
}

export function registerMechanics(arena) {
  registerForms(arena);
  ability_dict.decoy.description =
    '选择己方战线的一张普通单位，用零战力替身交换回手牌；可回收对手送来的侦察单位，英雄和替身不能回收。回收清除临时强化，替身不受增益且占用一次出牌行动。';
  ability_dict.decoy.name = '诱饵回收';
  ability_dict.decoy.activated = async (card) => {
    const owner = card.holder,
      cards = decoyTargets(arena, owner);
    if (!cards.length) return;
    let target;
    if (owner.controller instanceof ControllerAI)
      target = owner.controller.chooseDecoyTarget?.() || cards[0];
    else
      await arena.view.chooseCards(
        { cards },
        1,
        (container, index) => {
          target = container.cards[index];
        },
        () => true,
        false,
        '诱饵回收 · 选择己方普通单位',
      );
    const row = arena.rows(owner).find((row) => row.cards.includes(target));
    if (!row || !target?.isUnit()) throw Error('诱饵目标已失效');
    await board.toHand(target, row);
    await board.moveTo(card, row, owner.hand);
    arena.view.log(`诱饵回收 · ${target.name} 返回手牌。`);
  };
  ability_dict.muster.name = '集群召集';
  ability_dict.muster.description =
    '从自己的手牌与牌库立即部署全部同召集组单位，不从弃牌堆召集、不凭空生成副本。联结是乘倍，召集是自动铺场；召集整组仅占一次行动。';
  ability_dict.muster.placed = async (card) => {
    const owner = card.holder,
      group = card.arenaData.musterGroup;
    if (!group) throw Error('召集组缺失');
    const active = (owner.arenaMustering ||= new Set());
    if (active.has(group)) return;
    active.add(group);
    try {
      const members = [owner.hand, owner.deck].flatMap((source) =>
        source.cards
          .filter((target) => target.isUnit() && target.arenaData.musterGroup === group)
          .map((target) => ({ target, source })),
      );
      for (const { target, source } of members) {
        if (source.cards.includes(target)) await deployUnit(arena, target, source);
      }
      if (members.length) arena.view.log(`集群召集 · ${card.name} 追加部署 ${members.length} 张。`);
    } finally {
      active.delete(group);
    }
  };
  ability_dict.agile.name = '双排部署';
  ability_dict.agile.description =
    '可部署在推理前线或感知矩阵；部署后不能随意移动。恢复或重新出牌时可重新选战线。';
  // Replace the upstream hard-coded player_op recovery row choice with the actual holder.
  ability_dict.medic.placed = async (card) => {
    const owner = card.holder,
      grave = owner.grave;
    if (!grave.cards.some((target) => target.isUnit())) return;
    let target;
    if (game.randomRespawn) target = grave.findCardsRandom((candidate) => candidate.isUnit())[0];
    else if (owner.controller instanceof ControllerAI) target = owner.controller.medic(card, grave);
    else
      await arena.view.chooseCards(
        grave,
        1,
        (container, index) => {
          target = container.cards[index];
        },
        (candidate) => candidate.isUnit(),
        false,
        '检查点恢复 · 选择普通单位',
      );
    if (target) {
      await target.animate('medic');
      await deployUnit(arena, target, grave);
    }
  };
}
