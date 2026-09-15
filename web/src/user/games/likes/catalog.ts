import {
  booleanValue,
  enumValue,
  exactRecord,
  invalidResponse,
  safeInteger,
} from '../common/strict';
import { hashValue, list, nullable } from '../common/duel/normalize';
import { amount, dictionary, label, prose, unique } from './value';

export const ROLES = ['ChatGPT', 'Claude', 'Gemini', 'GLM', 'DeepSeek'] as const;
export type RoleID = (typeof ROLES)[number];
export const roleID = (v: unknown) => enumValue(v, ROLES, 'role');
export const SKILL_KINDS = ['basic', 'normal', 'special', 'ultimate'] as const;
export interface Effect {
  kind: string;
  likes: number;
  p: number;
  q: number;
  n: number;
  buffId?: string;
  extraBuffId?: string;
  cacheTarget?: string;
  meme?: string;
  combo?: number;
  randomTargets?: boolean;
  auditTarget?: string;
}
export interface Skill {
  id: string;
  owner: string;
  kind: (typeof SKILL_KINDS)[number];
  name: string;
  energy: number;
  token: number;
  payment: 'mix' | 'api' | 'sub';
  image: number;
  maxUses: number | null;
  learn: number;
  copyable: boolean;
  stable: boolean;
  meme: string;
  note: string;
  effects: { base: Effect; I: Effect; II: Effect };
  resourceCosts: Record<string, number>;
  gold?: number;
}
export interface Buff {
  id: string;
  name: string;
  kind: string;
  category?: string;
  source?: string;
  p: number;
  q: number;
  n: number;
  cap: number;
  trigger: string;
  expiry: string;
  overwrite: string;
  target: string;
  description?: string;
  meme?: string;
  stackGroup?: string;
  reapply?: string;
  cacheScope?: string;
  refreshOnCombo?: boolean;
}
export interface Role {
  id: RoleID;
  name: string;
  focus: string;
  difficulty: string;
  note: string;
  weakness: string;
  overrides: Record<string, number>;
  resources: Record<string, { initial: number; cap: number }>;
}
export interface Harness {
  id: string;
  name: string;
  activeSlots: number;
  passives: string[];
  description: string;
  meme: string;
}
export interface Passive {
  id: string;
  name: string;
  kind: string;
  p: number;
  q: number;
  buffId?: string;
  description: string;
  meme: string;
}
export interface Preset {
  id: string;
  role: string;
  name: string;
  skills: string[];
  note: string;
}
export interface Resource {
  id: string;
  name: string;
  unit: string;
  description: string;
  meme: string;
  subscription?: boolean;
  upgrade?: number;
}
export interface Parameter {
  id: string;
  name: string;
  unit: string;
  note: string;
}
export interface ModeCatalog {
  contentHash: string;
  mode: 'quick' | 'standard';
  name: string;
  parameters: Record<string, number>;
  paramMeta: Parameter[];
  roles: Role[];
  skills: Skill[];
  buffs: Buff[];
  harnesses: Harness[];
  passives: Passive[];
  loadouts: Preset[];
  resources: Resource[];
}
export interface LikesCatalog {
  contentHash: string;
  modes: { quick: ModeCatalog; standard: ModeCatalog };
}
const optionalText = (r: Record<string, unknown>, key: string) =>
  r[key] === undefined ? undefined : prose(r[key]);
function effect(value: unknown): Effect {
  const r = exactRecord(
    value,
    ['kind', 'likes', 'p', 'q', 'n'],
    ['buffId', 'extraBuffId', 'cacheTarget', 'meme', 'combo', 'randomTargets', 'auditTarget'],
  );
  return {
    kind: label(r.kind),
    likes: amount(r.likes),
    p: amount(r.p),
    q: amount(r.q),
    n: amount(r.n),
    buffId: optionalText(r, 'buffId'),
    extraBuffId: optionalText(r, 'extraBuffId'),
    cacheTarget: optionalText(r, 'cacheTarget'),
    meme: optionalText(r, 'meme'),
    combo: r.combo === undefined ? undefined : amount(r.combo),
    randomTargets:
      r.randomTargets === undefined ? undefined : booleanValue(r.randomTargets, 'random targets'),
    auditTarget: optionalText(r, 'auditTarget'),
  };
}
function skill(value: unknown): Skill {
  const r = exactRecord(
    value,
    [
      'id',
      'owner',
      'kind',
      'name',
      'energy',
      'token',
      'payment',
      'image',
      'maxUses',
      'learn',
      'copyable',
      'stable',
      'meme',
      'note',
      'effects',
      'resourceCosts',
    ],
    ['gold'],
  );
  const fx = exactRecord(r.effects, ['base', 'I', 'II']);
  return {
    id: label(r.id),
    owner: enumValue(r.owner, [...ROLES, '全局公共'], 'owner'),
    kind: enumValue(r.kind, SKILL_KINDS, 'skill kind'),
    name: label(r.name),
    energy: amount(r.energy),
    token: amount(r.token),
    payment: enumValue(r.payment, ['mix', 'api', 'sub'], 'payment'),
    image: amount(r.image),
    maxUses: nullable(r.maxUses, amount),
    learn: amount(r.learn),
    copyable: booleanValue(r.copyable, 'copyable'),
    stable: booleanValue(r.stable, 'stable'),
    meme: prose(r.meme),
    note: prose(r.note),
    effects: { base: effect(fx.base), I: effect(fx.I), II: effect(fx.II) },
    resourceCosts: dictionary(r.resourceCosts, amount, 16),
    gold: r.gold === undefined ? undefined : amount(r.gold),
  };
}
function buff(value: unknown): Buff {
  const r = exactRecord(
    value,
    ['id', 'name', 'kind', 'p', 'q', 'n', 'cap', 'trigger', 'expiry', 'overwrite', 'target'],
    [
      'category',
      'source',
      'description',
      'meme',
      'stackGroup',
      'reapply',
      'cacheScope',
      'refreshOnCombo',
    ],
  );
  return {
    id: label(r.id),
    name: label(r.name),
    kind: label(r.kind),
    p: amount(r.p),
    q: amount(r.q),
    n: amount(r.n),
    cap: amount(r.cap),
    trigger: prose(r.trigger),
    expiry: prose(r.expiry),
    overwrite: prose(r.overwrite),
    target: prose(r.target),
    category: optionalText(r, 'category'),
    source: optionalText(r, 'source'),
    description: optionalText(r, 'description'),
    meme: optionalText(r, 'meme'),
    stackGroup: optionalText(r, 'stackGroup'),
    reapply: optionalText(r, 'reapply'),
    cacheScope: optionalText(r, 'cacheScope'),
    refreshOnCombo:
      r.refreshOnCombo === undefined ? undefined : booleanValue(r.refreshOnCombo, 'refresh combo'),
  };
}
const byID = (v: { id: string }) => v.id;
function modeCatalog(value: unknown, mode: 'quick' | 'standard'): ModeCatalog {
  const outer = exactRecord(value, [
    'rules_version',
    'design_version',
    'schema_version',
    'content_hash',
    'config',
  ]);
  safeInteger(outer.rules_version, 1, 1, 'rules');
  enumValue(outer.design_version, ['0.17.0'], 'design');
  safeInteger(outer.schema_version, 15, 15, 'schema');
  const r = exactRecord(outer.config, [
    'schemaVersion',
    'mode',
    'name',
    'parameters',
    'paramMeta',
    'rules',
    'roles',
    'skills',
    'buffs',
    'loadouts',
    'resources',
    'harnesses',
    'passives',
  ]);
  enumValue(r.mode, [mode], 'mode');
  safeInteger(r.schemaVersion, 15, 15, 'config schema');
  const rules = exactRecord(r.rules, [
    'cacheWindow',
    'uniqueSamples',
    'strictSamples',
    'imageShortage',
  ]);
  enumValue(rules.cacheWindow, ['round'], 'cache window');
  enumValue(rules.imageShortage, ['illegal'], 'image shortage');
  if (
    !booleanValue(rules.uniqueSamples, 'samples') ||
    !booleanValue(rules.strictSamples, 'samples')
  )
    invalidResponse('sample policy');
  const parameters = dictionary(r.parameters, amount);
  const parameterKeys = [
    'API_PACK',
    'API_PRICE',
    'API_START',
    'BURST_CAP',
    'BURST_PERIOD',
    'CHARGE_PACK',
    'CHARGE_PRICE',
    'CLEANSE_PRICE',
    'DS_API_PACK',
    'DS_API_START',
    'ENERGY_CAP',
    'ENERGY_FLOOR',
    'ENERGY_START',
    'INITIAL_GOLD',
    'INSERT_CAP',
    'MAX_ROUNDS',
    'POWER_GENERATION',
    'PREP_MAX',
    'REGULATOR_PRICE',
    'REGULATOR_SAVE',
    'SUB_BURST_UPGRADE',
    'SUB_PERIOD',
    'SUB_PRICE',
    'SUB_START',
    'SUB_TOTAL_UPGRADE',
    'TARGET_LIKES',
    'TOKEN_FLOOR',
    'TURN_SECONDS',
  ];
  exactRecord(parameters, parameterKeys);
  if (
    !parameters.MAX_ROUNDS ||
    !parameters.TARGET_LIKES ||
    !parameters.ENERGY_CAP ||
    parameters.TURN_SECONDS !== 20 ||
    parameters.PREP_MAX !== 2 ||
    parameters.INSERT_CAP !== 1
  )
    invalidResponse('rule parameters');
  const roles = unique(
    r.roles,
    5,
    (v) => {
      const q = exactRecord(v, [
        'id',
        'name',
        'focus',
        'difficulty',
        'note',
        'weakness',
        'overrides',
        'resources',
      ]);
      return {
        id: roleID(q.id),
        name: label(q.name),
        focus: prose(q.focus),
        difficulty: label(q.difficulty),
        note: prose(q.note),
        weakness: prose(q.weakness),
        overrides: dictionary(q.overrides, amount),
        resources: dictionary(q.resources, (v) => {
          const a = exactRecord(v, ['initial', 'cap']);
          return { initial: amount(a.initial), cap: amount(a.cap) };
        }),
      };
    },
    byID,
  );
  const skills = unique(r.skills, 48, skill, byID),
    buffs = unique(r.buffs, 46, buff, byID);
  const harnesses = unique(
    r.harnesses,
    8,
    (v) => {
      const q = exactRecord(v, ['id', 'name', 'activeSlots', 'passives', 'description', 'meme']);
      return {
        id: label(q.id),
        name: label(q.name),
        activeSlots: safeInteger(q.activeSlots, 0, 2, 'harness slots'),
        passives: list(q.passives, 2, label),
        description: prose(q.description),
        meme: prose(q.meme),
      };
    },
    byID,
  );
  const passives = unique(
    r.passives,
    8,
    (v) => {
      const q = exactRecord(v, ['id', 'name', 'kind', 'p', 'q', 'description', 'meme'], ['buffId']);
      return {
        id: label(q.id),
        name: label(q.name),
        kind: label(q.kind),
        p: amount(q.p),
        q: amount(q.q),
        description: prose(q.description),
        meme: prose(q.meme),
        buffId: optionalText(q, 'buffId'),
      };
    },
    byID,
  );
  const loadouts = unique(
    r.loadouts,
    32,
    (v) => {
      const q = exactRecord(v, ['id', 'role', 'name', 'skills', 'note']);
      return {
        id: label(q.id),
        role: label(q.role),
        name: label(q.name),
        skills: unique(q.skills, 6, label, (v) => v),
        note: prose(q.note),
      };
    },
    byID,
  );
  const resources = unique(
    r.resources,
    16,
    (v) => {
      const q = exactRecord(
        v,
        ['id', 'name', 'unit', 'description', 'meme'],
        ['subscription', 'upgrade'],
      );
      return {
        id: label(q.id),
        name: label(q.name),
        unit: label(q.unit),
        description: prose(q.description),
        meme: prose(q.meme),
        subscription:
          q.subscription === undefined ? undefined : booleanValue(q.subscription, 'subscription'),
        upgrade: q.upgrade === undefined ? undefined : amount(q.upgrade),
      };
    },
    byID,
  );
  const paramMeta = unique(
    r.paramMeta,
    128,
    (v) => {
      const q = exactRecord(v, ['id', 'name', 'unit', 'note']);
      return { id: label(q.id), name: label(q.name), unit: prose(q.unit), note: prose(q.note) };
    },
    byID,
  );
  if (
    roles.length !== 5 ||
    skills.length !== 48 ||
    buffs.length !== 46 ||
    harnesses.length !== 8 ||
    passives.length !== 8 ||
    resources.length !== 1
  )
    invalidResponse('catalog completeness');
  for (const sk of skills)
    for (const fx of [sk.effects.base, sk.effects.I, sk.effects.II])
      for (const id of [fx.buffId, fx.extraBuffId])
        if (id && !buffs.some((b) => b.id === id)) invalidResponse('buff reference');
  for (const h of harnesses)
    if (h.passives.some((id) => !passives.some((p) => p.id === id)))
      invalidResponse('passive reference');
  for (const p of loadouts)
    if (
      p.skills.length < 1 ||
      p.skills.length > 4 ||
      p.skills.some(
        (id) => !skills.some((s) => s.id === id && (s.owner === p.role || s.owner === '全局公共')),
      )
    )
      invalidResponse('preset reference');
  for (const role of roles)
    if (
      !loadouts.some((p) => p.role === role.id) ||
      skills.filter((s) => s.owner === role.id).length !== 8
    )
      invalidResponse('role completeness');
  if (
    skills.filter((s) => s.owner === '全局公共').length !== 8 ||
    paramMeta.length !== parameterKeys.length ||
    paramMeta.some((p) => !(p.id in parameters))
  )
    invalidResponse('catalog completeness');
  return {
    contentHash: hashValue(outer.content_hash),
    mode,
    name: label(r.name),
    parameters,
    paramMeta,
    roles,
    skills,
    buffs,
    harnesses,
    passives,
    loadouts,
    resources,
  };
}
export function likesCatalog(value: unknown): LikesCatalog {
  const r = exactRecord(value, [
    'rules_version',
    'design_version',
    'schema_version',
    'content_hash',
    'modes',
  ]);
  safeInteger(r.rules_version, 1, 1, 'catalog rules');
  enumValue(r.design_version, ['0.17.0'], 'catalog design');
  safeInteger(r.schema_version, 15, 15, 'catalog schema');
  const modes = exactRecord(r.modes, ['quick', 'standard']);
  return {
    contentHash: hashValue(r.content_hash),
    modes: {
      quick: modeCatalog(modes.quick, 'quick'),
      standard: modeCatalog(modes.standard, 'standard'),
    },
  };
}
