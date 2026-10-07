/* eslint-disable */
import { validateDeck, validateStoredDeck } from '../game/rules.js';
import { competitivePresets } from './competitive-presets.js';

const factions = {
  openai: {
    front: 'astra',
    heroes: ['astra', 'sol61', 'sol6', 'sol56', 'pro55', 'pro54'],
    themes: ['burst', 'resource', 'bond'],
    names: ['均衡推理', '英雄爆发', '侦察与恢复', '普通单位联结'],
  },
  deepseek: {
    front: 'v4pro',
    heroes: ['v4pro', 'v41flash', 'v32exp', 'v31terminus', 'r1zero', 'coderv2'],
    themes: ['growth', 'siege', 'resource'],
    names: ['均衡计算', '成长英雄', '算力集群', '资源续航'],
  },
  claude: {
    front: 'fable51',
    heroes: ['fable51', 'opus55', 'sonnet55', 'opus5', 'fable5', 'opus48'],
    themes: ['control', 'resource', 'bond'],
    names: ['均衡控制', '剪枝控制', '恢复与防护', '普通单位增益'],
  },
  gemini: {
    front: 'flash38',
    heroes: ['flash38', 'pro31', 'live38', 'flash37', 'flash36', 'flash35'],
    themes: ['weather', 'balanced-lines', 'burst'],
    names: ['均衡感知', '天气控制', '多战线部署', '英雄与解天气'],
  },
};
const templates = {
  balanced: {
    difficulty: '入门',
    description: '三条战线都有支援，保留恢复牌应对后续小局。',
    units:
      'infra_planner:2 infra_solver:2 infra_verifier:1 infra_branch:3 infra_vector:2 infra_visionbridge:1 infra_rollback:2 infra_batch:2 infra_stream:3 infra_toolrouter:1 @retrieval_agent:1 @recovery_daemon:1',
    specials: 'clear_signal compute_surge chain_of_thought safety_layer context_window',
    core: 'infra_branch infra_stream infra_rollback',
  },
  burst: {
    difficulty: '入门',
    description: '用六张英雄集中争夺关键小局，普通单位负责增益与恢复。',
    units:
      'infra_solver:3 $reason:2 infra_verifier:2 infra_vector:2 infra_visionbridge:1 $tools:2 infra_rollback:2 infra_batch:2 infra_toolrouter:1 @recovery_daemon:1 @retrieval_agent:1',
    specials: 'clear_signal compute_surge chain_of_thought safety_layer model_pruning',
    core: '$reason infra_rollback',
    heroes: 6,
  },
  resource: {
    difficulty: '进阶',
    description: '侦察补充手牌，恢复普通单位；适时放弃一局，为后续保留资源。',
    units:
      'scout:2 @retrieval_agent:2 infra_retrieval:1 restore:2 infra_rollback:2 @recovery_daemon:1 infra_branch:3 infra_stream:3 infra_toolrouter:1 infra_solver:2 infra_visionbridge:2',
    specials: 'clear_signal compute_surge safety_layer context_window model_pruning',
    core: 'scout @retrieval_agent infra_branch infra_stream restore',
  },
  bond: {
    difficulty: '进阶',
    description: '成组部署联结单位，配合战线增益与算力翻倍；留意天气和剪枝。',
    units:
      'infra_branch:3 infra_embed:3 infra_stream:3 infra_verifier:2 infra_visionbridge:2 infra_kv:2 infra_toolrouter:2 @vision_relay:1 infra_solver:2 @tensor_accelerator:1',
    specials: 'clear_signal compute_surge chain_of_thought safety_layer context_window',
    core: 'infra_branch infra_embed infra_stream infra_toolrouter',
  },
  growth: {
    difficulty: '进阶',
    description: '尽早部署成长英雄，穿插侦察和普通单位行动积累战力。',
    units:
      'scout:2 @retrieval_agent:2 infra_retrieval:1 restore:2 infra_rollback:2 @recovery_daemon:1 infra_branch:3 infra_stream:3 infra_toolrouter:1 infra_solver:2 infra_visionbridge:2',
    specials: 'clear_signal compute_surge safety_layer context_window chain_of_thought',
    core: 'scout @retrieval_agent infra_branch infra_stream',
    heroIds: ['v4pro_hero', 'hero', 'r1zero_hero', 'v41flash_hero'],
  },
  siege: {
    difficulty: '进阶',
    description: '算力战线集中部署，联结、增益和翻倍共同发力；为网络风暴保留解法。',
    units:
      'infra_stream:3 server:3 infra_batch:3 infra_kv:2 infra_rollback:2 infra_planner:2 infra_verifier:1 infra_beam:1 infra_embed:3 infra_visionbridge:1',
    specials: 'cold_start clear_signal compute_surge safety_layer context_window',
    core: 'infra_stream infra_kv server infra_rollback',
    heroIds: ['v4pro_hero', 'v32exp_hero', 'coderv2_hero', 'v3_hero'],
  },
  control: {
    difficulty: '进阶',
    description: '等待敌方高分战线成形再剪枝，结合复制与恢复争取交换优势。',
    units:
      'infra_sandbox:2 infra_beam:2 infra_judge:2 infra_retrieval:1 restore:2 infra_rollback:2 infra_branch:3 infra_embed:3 infra_verifier:1 infra_visionbridge:1 infra_kv:1 infra_toolrouter:1',
    specials: 'cold_start network_storm clear_signal model_pruning safety_layer context_window',
    core: 'infra_sandbox infra_beam infra_judge infra_branch infra_embed',
    heroIds: ['fable51_hero', 'opus55_hero', 'sonnet55_hero', 'opus48_hero'],
  },
  weather: {
    difficulty: '进阶',
    description: '主力部署感知战线，用冷启动和网络风暴压制其他战线；注意解天气的时机。',
    units:
      'infra_embed:3 live38_team:3 cluster:3 infra_vector:2 infra_distiller:2 infra_visionbridge:2 infra_rollback:2 @tensor_accelerator:2 infra_planner:2',
    specials: 'cold_start network_storm compute_surge model_pruning context_window',
    core: 'infra_embed live38_team cluster infra_visionbridge',
    heroIds: ['flash38_hero', 'pro31_hero', 'banana2_hero', 'flash35_hero'],
  },
  'balanced-lines': {
    difficulty: '入门',
    description: '三条战线分散部署，观察天气后再补强；用恢复牌重新分配普通单位。',
    units:
      'infra_branch:3 infra_embed:3 infra_stream:3 infra_planner:2 infra_vector:2 infra_batch:2 infra_verifier:1 infra_visionbridge:1 infra_kv:1 infra_rollback:2 @retrieval_agent:1',
    specials: 'clear_signal compute_surge chain_of_thought safety_layer context_window',
    core: 'infra_branch infra_embed infra_stream infra_rollback',
  },
};

const resolveId = (faction, config, token) =>
  token.startsWith('@') ? token.slice(1) : `${faction}_${token.replace('$', config.front + '_')}`;
export const PRESET_IDS = new Set([
  'preset',
  ...Object.keys(templates).map((id) => `theme-${id}`),
  ...['balanced', 'resource', 'bond', 'control'].map((id) => `standard-${id}`),
  'random-preset',
  'random-theme',
]);
export const isRandomSelection = (id) => ['random-preset', 'random-theme'].includes(id);
export const isBuiltInSelection = (id) => PRESET_IDS.has(id);

// Keep the old compositions and IDs available for saved selections and comparisons.
export function legacyThemePresets(faction, catalog) {
  const config = factions[faction];
  if (!config) throw Error('未知卡组阵营');
  return ['balanced', ...config.themes].map((themeId, index) => {
    const template = templates[themeId];
    const heroIds =
      template.heroIds?.map((id) => `${faction}_${id}`) ||
      config.heroes.slice(0, template.heroes || 4).map((id) => `${faction}_${id}_hero`);
    const cards = [
      ...heroIds.map((id) => ({ id, count: 1 })),
      ...template.units.split(' ').map((pair) => {
        const [token, count] = pair.split(':');
        return { id: resolveId(faction, config, token), count: Number(count) };
      }),
      ...template.specials.split(' ').map((id) => ({ id, count: 1 })),
    ];
    const deck = { faction, leader: `${faction}_leader`, cards };
    validateStoredDeck(deck, catalog);
    return {
      id: `theme-${themeId}`,
      name: config.names[index],
      themeId,
      kind: 'theme',
      difficulty: template.difficulty,
      description: template.description,
      coreIds: [
        heroIds[0],
        ...template.core.split(' ').map((id) => resolveId(faction, config, id)),
      ],
      deck,
    };
  });
}

const standardDescriptions = {
  balanced: {
    name: '均衡部署',
    difficulty: '入门',
    description: '侦察寻找主力组合，恢复补充后续输出；按局势分配战线，为后续小局留牌。',
  },
  resource: {
    name: '资源续航',
    difficulty: '进阶',
    description: '侦察补牌，恢复与诱饵回收普通单位；适时放弃一局。侦察会送给敌方战力。',
  },
  bond: {
    name: '联结增益',
    difficulty: '进阶',
    description:
      '联结配合同线增益与翻倍，侦察寻找组合，恢复同名单位重建联结；独立输出支撑后续小局。留意天气与剪枝。',
  },
  control: {
    name: '天气剪枝',
    difficulty: '进阶',
    description:
      '前线输出配合剪枝，冷启动压低双方前线；恢复与双诱饵重复利用普通单位，留意英雄免疫。',
  },
};

// Each faction has independently selected units and leaders. Card powers stay separate.
export function themePresets(faction, catalog) {
  if (!factions[faction]) throw Error('未知卡组阵营');
  return Object.entries(standardDescriptions).map(([themeId, template]) => {
    const definition = competitivePresets[`${faction}/${themeId}`];
    const deck = {
      faction,
      leader: definition.leader,
      cards: definition.cards.map((entry) => ({ ...entry })),
    };
    validateDeck(deck, catalog);
    return {
      id: `standard-${themeId}`,
      name: template.name,
      themeId,
      kind: 'theme',
      difficulty: template.difficulty,
      description: template.description,
      coreIds: [...definition.coreIds],
      deck,
    };
  });
}

export function seededRandom(seed) {
  if (!Number.isInteger(seed) || seed < 0 || seed > 0xffffffff)
    throw Error('随机种子必须是 uint32');
  let state = seed >>> 0;
  return () => {
    state = (Math.imul(state, 1664525) + 1013904223) >>> 0;
    return state / 0x100000000;
  };
}
export function nextDeckSeed() {
  return globalThis.crypto?.getRandomValues
    ? crypto.getRandomValues(new Uint32Array(1))[0]
    : Math.floor(Math.random() * 0x100000000);
}

export function randomDeckProfile(
  faction,
  catalog,
  { mode = 'random-theme', seed = nextDeckSeed(), themeId } = {},
) {
  if (!isRandomSelection(mode)) throw Error('未知随机卡组模式');
  const rng = seededRandom(seed),
    profiles = themePresets(faction, catalog);
  const base = themeId
    ? profiles.find((p) => p.themeId === themeId)
    : profiles[Math.floor(rng() * profiles.length)];
  if (!base) throw Error('本阵营没有这个卡组主题');
  const profile = structuredClone(base);
  if (mode === 'random-theme') {
    const map = new Map(catalog.map((card) => [card.id, card]));
    const protectedIds = new Set(base.coreIds);
    // Swap whole entries within the same row, role and power range. Bond sets stay intact.
    for (const entry of profile.deck.cards) {
      const original = map.get(entry.id);
      if (
        protectedIds.has(entry.id) ||
        !['unit', 'hero'].includes(original.type) ||
        original.abilities.includes('bond')
      )
        continue;
      const candidates = catalog
        .filter(
          (card) =>
            card.type === original.type &&
            card.row === original.row &&
            [faction, 'neutral'].includes(card.faction) &&
            card.abilities.join('|') === original.abilities.join('|') &&
            Math.abs(card.power - original.power) <= 2 &&
            card.maxCopies >= entry.count &&
            !profile.deck.cards.some((other) => other !== entry && other.id === card.id) &&
            // Keep the number of neutral units bounded by retaining each slot's faction.
            (card.faction === 'neutral') === (original.faction === 'neutral'),
        )
        .sort((a, b) => a.id.localeCompare(b.id));
      if (candidates.length) entry.id = candidates[Math.floor(rng() * candidates.length)].id;
    }
  }
  validateDeck(profile.deck, catalog);
  Object.assign(profile, {
    id: mode,
    kind: 'random',
    sourceId: base.id,
    seed,
    name: `${mode === 'random-preset' ? '随机预设' : '随机组牌'} · ${base.name}`,
  });
  return profile;
}
