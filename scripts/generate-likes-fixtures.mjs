import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { gzipSync } from 'node:zlib';

// Regenerate only with the fixed reference rules and the committed presets.
// The reference adapter records random draws and state immediately before begin.
const [referencePath, catalogDirectory, outputPath] = process.argv.slice(2);
if (!referencePath || !catalogDirectory || !outputPath) {
  throw new Error('Usage: generate-likes-fixtures.mjs reference.cjs catalog-directory output.json.gz');
}
const require = createRequire(import.meta.url);
const reference = require(path.resolve(referencePath));
const clone = (value) => structuredClone(value);
const empty = () => ({ purchases: [], main: null, extra: [] });
const plan = (id, purchases = [], extra = [], options = {}) => ({
  purchases: purchases.map((item) => typeof item === 'string' ? { item } : item),
  main: id ? { skillId: id, ...options } : null,
  extra: extra.map((item) => typeof item === 'string' ? { skillId: item } : item),
});
const cleanPlan = (value) => ({ purchases: value.purchases, main: value.main, extra: value.extra ?? [] });
function stateView(state, mode, resolved = false) {
  return {
    version: 1, mode,
    round: state.sync.round - (resolved && !state.result ? 1 : 0),
    players: state.players.map(({ label: _label, ...player }) => player),
    records: state.records, energy: state.energy, cast_seq: state.castSeq,
    blocked: state.sync.blocked, likes_at_start: state.sync.likesAtStart,
    grants: state.sync.grants, result: state.result,
    awaiting_next_round: resolved && !state.result,
  };
}
const excluded = new Set(['start', 'commit', 'round', 'reset', 'debug']);
function eventsView(events) {
  return events.filter((event) => !excluded.has(event.kind)).map((event) => {
    const result = { kind: event.kind, seat: event.actor, data: event.data ?? null };
    if (event.kind === 'reveal') result.data = { plans: event.data.plans.map(cleanPlan) };
    return result;
  });
}
const cases = [];
function scenario(name, config, selections, rounds, mutate = () => {}) {
  globalThis.__draws = [];
  const setup = {
    roles: selections.map((selection) => selection.role),
    loadouts: selections.map((selection) => selection.skills),
    harnesses: selections.map((selection) => selection.harness ?? null),
    starter: 0, seed: 101, labels: ['A', 'B'],
  };
  let state = reference.createGame(config, setup);
  state = mutate(state) ?? state;
  state.sync.likesAtStart = state.players.map((player) => player.likes);
  const initial = stateView(clone(state), config.mode);
  const steps = [];
  for (const round of rounds) {
    if (state.result) break;
    const plans = typeof round === 'function' ? round(state) : round;
    const start = state.events.length;
    const draws = globalThis.__draws.length;
    globalThis.__beforeBegin = null;
    for (const seat of [0, 1]) {
      if (!state.sync.plans[seat]) state = reference.transition(state, config, { type: 'submit', seat, plan: plans[seat] });
    }
    const settled = state.result ? state : globalThis.__beforeBegin;
    if (!settled) throw new Error(`Missing round boundary: ${name}`);
    steps.push({
      plans,
      resolved: stateView(clone(settled), config.mode, true),
      ready: state.result ? null : stateView(clone(state), config.mode),
      events: eventsView(settled.events.slice(start)),
      draws: globalThis.__draws.slice(draws),
    });
  }
  cases.push({ name, mode: config.mode, seed: 101, initial, steps });
}

for (const mode of ['quick', 'standard']) {
  const config = JSON.parse(fs.readFileSync(path.join(catalogDirectory, `${mode}.json`), 'utf8'));
  for (const skill of config.skills) {
    for (const level of skill.copyable ? ['base', 'I', 'II'] : ['base']) {
      const role = level === 'base' && skill.owner !== '全局公共' ? skill.owner : 'ChatGPT';
      const id = level === 'base' ? skill.id : 'PUB41';
      const selections = [
        { role, skills: [...new Set(['PUB01', id])] },
        { role: 'Claude', skills: ['PUB01'] },
      ];
      scenario(`${mode}/${skill.id}/${level}`, config, selections,
        [[plan(id, role === 'DeepSeek' ? [] : ['sub']), plan('PUB01')],
          (state) => state.players.map((player, seat) => player.stunned || state.sync.blocked[seat] ? empty() : plan('PUB01'))],
        (state) => {
          for (const player of state.players) { player.gold = 1_000_000; player.api = 1_000_000; }
          if (level !== 'base') Object.assign(state.players[0].distill, { template: skill.id, level, learning: 0 });
        });
    }
  }
  const defaults = [{ role: 'ChatGPT', skills: ['PUB01'] }, { role: 'Claude', skills: ['PUB01'] }];
  scenario(`${mode}/cache-subscription`, config, defaults,
    Array.from({ length: 8 }, () => [plan('PUB01'), plan('PUB01')]));
  scenario(`${mode}/skip-to-limit`, config, defaults,
    Array.from({ length: config.parameters.MAX_ROUNDS }, () => [empty(), empty()]));

  const wealthy = (state) => {
    for (const player of state.players) { player.gold = 1_000_000; player.api = 1_000_000; }
    state.energy = config.parameters.ENERGY_CAP;
    return state;
  };
  const grant = (state, seat, buffId, count = 1) => {
    for (let i = 0; i < count; i++) state = reference.transition(state, config, { type: 'grantBuff', seat, buffId });
    return state;
  };
  for (const role of config.roles) for (const harness of config.harnesses) {
    scenario(`${mode}/harness/${role.id}/${harness.id}`, config,
      [{ role: role.id, harness: harness.id, skills: ['PUB01', 'PUB21', 'PUB41'] }, { role: 'Claude', skills: ['PUB01', 'CLA21', 'CLA22'] }],
      [[plan('PUB01'), plan('CLA21')], [plan('PUB41'), plan('CLA22')], [plan('PUB21'), plan('PUB01')]], wealthy);
  }
  scenario(`${mode}/sota-cap-overflow-and-decay`, config,
    [{ role: 'Claude', harness: 'H02', skills: ['PUB01', 'CLA21', 'CLA22'] }, { role: 'Claude', skills: ['PUB01'] }],
    [[plan('CLA21'), plan('PUB01')], [plan('CLA22'), plan('PUB01')], [empty(), plan('PUB01')], [empty(), plan('PUB01')]],
    (state) => grant(wealthy(state), 1, 'B35:原', 4));
  scenario(`${mode}/flash-batch-both-seats`, config,
    [{ role: 'Gemini', harness: 'H03', skills: ['PUB01', 'GEM61'] }, { role: 'Gemini', harness: 'H03', skills: ['PUB01', 'GEM61'] }],
    [[plan('GEM61', [], ['GEM61']), plan('GEM61', [], ['GEM61'])]],
    (state) => grant(grant(wealthy(state), 0, 'B28:通用', 2), 1, 'B28:通用', 2));
  scenario(`${mode}/pro-persistence-conversion`, config,
    [{ role: 'Gemini', skills: ['PUB01', 'GEM02', 'GEM62', 'PUB22'] }, { role: 'Claude', skills: ['PUB01'] }],
    [[plan('GEM02'), empty()], [plan('GEM02'), empty()], [plan('PUB22'), empty()], [plan('GEM62'), empty()], [empty(), empty()]], wealthy);
  scenario(`${mode}/extra-fails-main-remains-sample`, config,
    [{ role: 'Gemini', skills: ['PUB01', 'GEM41', 'PUB61'] }, { role: 'Claude', skills: ['PUB01', 'PUB41'] }],
    [[plan('GEM41', [], ['PUB61']), empty()], [empty(), plan('PUB41')]],
    (state) => { state.energy = config.parameters.ENERGY_CAP; state.players[0].burst = 250; state.players[0].sub = 250; state.players[0].api = 0; return state; });
  scenario(`${mode}/cleanse-keeps-frozen-token-tax`, config,
    [{ role: 'ChatGPT', skills: ['PUB01', 'GPT41'] }, { role: 'Claude', skills: ['PUB01'] }],
    [[plan('GPT41', [], [], { targets: ['B17:原版'] }), plan('PUB01')]],
    (state) => grant(grant(wealthy(state), 0, 'B17:原版'), 0, 'B18:原版'));
  scenario(`${mode}/shopping-cleanse-removes-token-tax`, config,
    [{ role: 'ChatGPT', skills: ['PUB01', 'GPT41'] }, { role: 'Claude', skills: ['PUB01'] }],
    [[plan('GPT41', [{ item: 'cleanse', target: 'B17:原版' }], [], { targets: ['B18:原版'] }), plan('PUB01')]],
    (state) => grant(grant(wealthy(state), 0, 'B17:原版'), 0, 'B18:原版'));
  for (const template of ['GPT41', 'CLA41', 'GLM41']) {
    scenario(`${mode}/random-removal/${template}`, config,
      [{ role: 'ChatGPT', skills: ['PUB01', 'PUB41'] }, { role: 'Claude', skills: ['PUB01'] }],
      [[plan('PUB41'), plan('PUB01')]],
      (state) => {
        state = wealthy(state);
        for (const [seat, id] of [[0, 'B17:原版'], [0, 'B18:原版'], [0, 'B33:状态'], [1, 'B13:原版'], [1, 'B14:原版'], [1, 'B34:状态']]) {
          if (id !== 'B33:状态') state = grant(state, seat, id);
        }
        Object.assign(state.players[0].distill, { template, level: 'I', learning: 0 });
        return state;
      });
  }
  scenario(`${mode}/counter-hits-failed-main`, config,
    [{ role: 'GLM', skills: ['PUB01', 'GLM21'] }, { role: 'Claude', skills: ['PUB01', 'PUB21'] }],
    [[plan('GLM21'), plan('PUB21')]],
    (state) => { state = wealthy(state); Object.assign(state.players[1], { burst: 0, sub: 0, api: 0 }); return state; });
  scenario(`${mode}/usage-reset-after-failure`, config,
    [{ role: 'ChatGPT', skills: ['PUB01', 'PUB42'] }, { role: 'Claude', skills: ['PUB01'] }],
    [[plan('PUB42'), plan('PUB01')]],
    (state) => { state = wealthy(state); Object.assign(state.players[1], { burst: 0, sub: 0, api: 0 }); return state; });
  scenario(`${mode}/resource-income-after-failure`, config,
    [{ role: 'DeepSeek', skills: ['PUB01', 'DS43'] }, { role: 'Claude', skills: ['PUB01'] }],
    [[plan('DS43'), plan('PUB01')]],
    (state) => { state = wealthy(state); state.players[0].api = 0; return state; });
  scenario(`${mode}/regulator-delayed-and-bounded`, config, defaults,
    [[plan('PUB01', ['regulator']), plan('PUB01')], [plan('PUB01'), plan('PUB01')], [plan('PUB01'), plan('PUB01')]], wealthy);
  scenario(`${mode}/double-charge-cap`, config, defaults,
    [[plan('PUB01', ['charge']), plan('PUB01', ['charge'])]],
    (state) => { state.energy = config.parameters.ENERGY_CAP - 10; return state; });
  scenario(`${mode}/double-overload-no-payment`, config, defaults,
    [[plan('PUB01'), plan('PUB01')]], (state) => { state.energy = 19; return state; });
  const comboSelections = [{ role: 'Gemini', skills: ['PUB01', 'GEM61'] }, { role: 'Gemini', skills: ['PUB01', 'GEM61'] }];
  scenario(`${mode}/flash-shared-energy-failure`, config, comboSelections,
    [[plan('GEM61', [], ['GEM61']), plan('GEM61', [], ['GEM61'])]],
    (state) => { state = wealthy(state); state.energy = config.skills.find((skill) => skill.id === 'GEM61').energy * 4; return grant(grant(state, 0, 'B28:通用', 2), 1, 'B28:通用', 2); });
  scenario(`${mode}/flash-personal-failure`, config, comboSelections,
    [[plan('GEM61', [], ['GEM61']), empty()]],
    (state) => { state = wealthy(state); Object.assign(state.players[0], { burst: 0, sub: 0, api: config.skills.find((skill) => skill.id === 'GEM61').token * 2 }); return state; });
  scenario(`${mode}/banned-subscription-fully-covered-by-trial`, config,
    [{ role: 'ChatGPT', harness: 'H04', skills: ['PUB01', 'PUB02'] }, { role: 'Claude', skills: ['PUB01'] }],
    [[plan('PUB02'), empty()]],
    (state) => { state = grant(wealthy(state), 0, 'B31:原版'); state.players[0].trial = 420; return state; });
  scenario(`${mode}/image-payment-starts-only-total-clock`, config,
    [{ role: 'ChatGPT', skills: ['PUB01', 'GPT22'] }, { role: 'Claude', skills: ['PUB01'] }],
    [[plan('GPT22', [], [], { pay: 'api' }), empty()]], wealthy);
  scenario(`${mode}/subscription-upgrade-preserves-deficit`, config, defaults,
    [[plan('PUB01', ['sub']), empty()]],
    (state) => { state = wealthy(state); Object.assign(state.players[0], { burst: 100, sub: 50 }); state.players[0].resources.R_IMAGE = 1; state.players[0].subscription.burstResetAt = 2; state.players[0].subscription.totalResetAt = 3; return state; });
  scenario(`${mode}/svg-successive-decay`, config,
    [{ role: 'ChatGPT', skills: ['PUB01', 'PUB02'] }, { role: 'Claude', skills: ['PUB01'] }],
    Array.from({ length: 4 }, () => [plan('PUB02', ['sub', 'charge']), empty()]),
    (state) => { state = wealthy(state); state.energy = config.parameters.ENERGY_CAP - 10; return state; });
}

fs.mkdirSync(path.dirname(path.resolve(outputPath)), { recursive: true });
fs.writeFileSync(outputPath, gzipSync(JSON.stringify({ format: 1, reference: '0.17.0', cases }) + '\n', { level: 9 }));
console.log(`Wrote ${cases.length} differential cases`);
