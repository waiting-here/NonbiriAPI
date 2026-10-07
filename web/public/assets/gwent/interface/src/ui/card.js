/* eslint-disable */
import { models } from '../ai/models.js';
import { setTooltip } from './tooltip.js';
import { rowNames } from '../game/rules.js';
export const abilityNames = {
  spy: '数据侦察',
  medic: '检查点恢复',
  bond: '集群联结',
  morale: '对齐增益',
  horn: '算力翻倍',
  scorch: '模型剪枝',
  frost: '冷启动',
  fog: '数据噪声',
  rain: '网络风暴',
  clear: '信号恢复',
  future_predict: '未来推演',
  deep_think: '深度思考',
  long_context: '长文本理解',
  visual_analysis: '视觉分析',
  chain_of_thought: '思维链',
  safety_layer: '安全层',
  context_window: '上下文窗口',
  decoy: '诱饵回收',
  muster: '集群召集',
  agile: '双排部署',
  mardroeme: '蒸馏催化',
  berserker: '形态编译',
  avenger: '离场召唤',
  storm: '双域风暴',
  leader_openai: '扩展推理',
  leader_deepseek: '深度优化',
  leader_claude: '防护协议',
  leader_gemini: '全域感知',
  leader_openai_assault: '前线统筹',
  leader_openai_compute: '算力统筹',
  leader_deepseek_recover: '检查点调度',
  leader_deepseek_rebirth: '第三局重启',
  leader_claude_deny: '协议封锁',
  leader_claude_retrieve: '上下文回收',
  leader_gemini_clear: '晴空解析',
  leader_gemini_sensors: '感知统筹',
  scorch_c: '前线剪枝',
  scorch_r: '矩阵剪枝',
  scorch_s: '集群剪枝',
};
export function definition(card) {
  return card.arenaData || card;
}
export function cardDescription(card, registry = ability_dict) {
  const data = definition(card),
    ids = card.abilities || data.abilities;
  const types = {
    hero: '英雄',
    unit: '普通单位',
    weather: '天气',
    skill: '技能',
    leader: '领袖',
  };
  const lines = [
    `${types[data.type]} · ${rowNames[data.row] || (data.type === 'leader' ? (ids.includes('leader_deepseek_rebirth') ? '被动技能' : '整场一次') : '特殊牌')}`,
  ];
  if (data.modelName) {
    const statuses = {
      current: '当前型号',
      previous: '前代型号',
      historical: '历史型号',
      preview: '预览型号',
    };
    lines.push(
      `${data.modelName} · ${statuses[data.modelStatus] || '模型角色'} · 核验 ${data.verifiedAt}`,
    );
    if (data.modelKind === 'distilled') lines.push(`R1 蒸馏模型 · 基座 ${data.baseModel}。`);
    if (data.modelKind === 'specialist') lines.push('专业模型 · 历史型号。');
    if (data.category === 'variant')
      lines.push(`${data.role}是本游戏的战术角色，不是独立 API 型号。`);
  }
  if (data.category === 'support') lines.push('基础设施单位 · 游戏原创角色。');
  if (data.category === 'troop')
    lines.push(`${data.faction === 'neutral' ? '中立支援单位' : '阵营普通单位'} · 游戏原创角色。`);
  if (data.category === 'prototype') lines.push('初始卡池 · 兼容旧卡组。');
  if (data.tier === 'flagship') lines.push('旗舰英雄 · 战力属于游戏设定，不代表真实模型测试分数。');
  if (['unit', 'hero'].includes(data.type))
    lines.push(
      `基础战力 ${data.power}${card.power !== undefined && card.power !== data.power ? ` · 当前战力 ${card.power}` : ''}`,
    );
  if (card.arenaGrowth) lines.push(`自身成长 +${card.arenaGrowth}（离场后清除）`);
  if (card.arenaBonus) lines.push(`基础强化 +${card.arenaBonus}（离场后清除，仍受天气影响）`);
  if (data.type === 'unit' && typeof board !== 'undefined') {
    const row = board.row.find((row) => row.cards.includes(card));
    if (row) {
      const effects = [];
      if (row.effects.weather) effects.push('天气：基础战力 ≤1');
      if (row.effects.bond[data.id] > 1) effects.push(`集群 ×${row.effects.bond[data.id]}`);
      const morale = row.effects.morale - (ids.includes('morale') ? 1 : 0);
      if (morale > 0) effects.push(`对齐 +${morale}`);
      if (row.effects.horn - (ids.includes('horn') ? 1 : 0) > 0) effects.push('算力 ×2');
      if (effects.length) lines.push(`当前影响：${effects.join(' · ')}`);
    }
  }
  const passive = new Set(['bond', 'morale', 'horn', 'deep_think', 'mardroeme', 'berserker']);
  for (const id of ids) {
    if (ids.includes('storm') && ['fog', 'rain'].includes(id)) continue;
    const timing =
      id === 'leader_deepseek_rebirth'
        ? '第三小局开始'
        : id.startsWith('leader_')
          ? '主动使用'
          : id === 'avenger'
            ? '离场时'
            : id === 'deep_think'
              ? '己方行动结束'
              : passive.has(id)
                ? '在场时'
                : '出牌时';
    lines.push(
      `${abilityNames[id] || registry[id]?.name || id}（${timing}）：${registry[id]?.description || '暂无技能说明。'}`,
    );
  }
  if (!ids.length && data.row !== 'agile')
    lines.push('部署到对应战线，以当前战力计入总分。没有额外技能。');
  if (data.row === 'agile')
    lines.push(
      `双排部署：${registry.agile?.description || '可部署在推理前线或感知矩阵，部署后不能随意移动。'}`,
    );
  if (data.type === 'hero')
    lines.push(
      registry.hero?.description || '英雄免疫天气、普通强化、恢复和剪枝。自身技能仍可触发。',
    );
  for (const key of ['transformForm', 'avengerForm'])
    if (data[key])
      lines.push(
        `衍生形态：${data[key].name} · 基础战力 ${data[key].power}${data[key].abilities.includes('bond') ? ' · 集群联结' : ''}`,
      );
  if (data.generated)
    lines.push(
      `运行时衍生${data.type === 'unit' ? '单位' : '牌'} · ${data.ephemeral ? '进入弃牌堆时消失' : '可恢复或回收'}，不能直接加入卡组。`,
    );
  return lines.join('\n');
}
export function updateCardElement(element, card, selected = false) {
  const data = definition(card),
    ids = card.abilities || data.abilities;
  element.classList.toggle('selected', selected);
  element.querySelector('.card-power').textContent = ['skill', 'weather', 'leader'].includes(
    data.type,
  )
    ? '✦'
    : String(card.power ?? data.power);
  element.querySelector('.card-skill').textContent =
    ids
      .filter((id) => !ids.includes('storm') || !['fog', 'rain'].includes(id))
      .map((id) => abilityNames[id] || id)
      .join(' · ') ||
    rowNames[data.row] ||
    '普通单位';
  setTooltip(element, `${data.name}\n${cardDescription(card)}`);
  element.setAttribute('aria-label', `${data.name}，${cardDescription(card)}`);
}
export function cardElement(card, { small = false, onClick, selected = false } = {}) {
  const data = definition(card),
    element = document.createElement(onClick ? 'button' : 'div');
  element.className = `arena-card ${small ? 'compact' : ''} ${data.type === 'hero' ? 'hero-card' : ''} ${selected ? 'selected' : ''}`;
  element.dataset.cardId = data.id;
  element.dataset.type = data.type;
  element.dataset.faction = data.faction;
  element.dataset.art = /\.svg$/i.test(data.image || '') ? 'vector' : 'painted';
  element.style.setProperty('--card-color', models[data.faction]?.color || '#d4d4be');
  if (onClick) {
    element.type = 'button';
    element.addEventListener('click', () => onClick(card));
  }
  const power = document.createElement('span');
  power.className = 'card-power';
  power.textContent = ['skill', 'weather', 'leader'].includes(data.type)
    ? '✦'
    : String(card.power ?? data.power);
  const image = document.createElement('img');
  image.src = data.thumbnail || data.image;
  if (data.thumbnail) {
    image.onerror = () => {
      image.onerror = null;
      image.src = data.image;
    };
  }
  image.alt = '';
  image.draggable = false;
  const label = document.createElement('span');
  label.className = 'card-label';
  label.textContent = data.name;
  const skill = document.createElement('span');
  skill.className = 'card-skill';
  skill.textContent =
    data.abilities.map((id) => abilityNames[id] || id).join(' · ') ||
    rowNames[data.row] ||
    '普通单位';
  element.append(image, power, label, skill);
  if (data.tier === 'flagship') {
    const badge = document.createElement('span');
    badge.className = 'card-tier';
    badge.textContent = '旗舰';
    element.append(badge);
  }
  updateCardElement(element, card, selected);
  return element;
}
