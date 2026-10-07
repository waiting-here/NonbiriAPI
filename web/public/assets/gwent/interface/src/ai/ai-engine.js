/* eslint-disable */
import { legalActions, executeAction } from '../game/battle.js';
import { strategyAdjustment } from './strategies.js';
import { models } from './models.js';
import { balanceRules, growthGain } from '../cards/balance.js';
import { decoyTargets } from '../cards/mechanics.js';
import { createForm } from '../cards/forms.js';
import { playerDifference, canFinishByPassing } from '../game/factions.js';

const leaderHornRows = {
  leader_openai_assault: 'close',
  leader_openai_compute: 'siege',
  leader_gemini_sensors: 'ranged',
};

// Extends the upstream controller, retaining its board evaluation and utility methods.
export class ArenaAI extends ControllerAI {
  constructor(player, arena) {
    super(player);
    this.arena = arena;
  }
  getMaximums() {
    const rmax = board.row.map((row) => ({ row, cards: row.maxUnits() }));
    const maximum = (player) => {
      const pairs = rmax
        .filter(({ row }) => this.arena.rows(player).includes(row))
        .flatMap(({ row, cards }) => cards.map((card) => ({ row: { row }, card })));
      const max = Math.max(0, ...pairs.map(({ card }) => card.power));
      return pairs.filter(({ card }) => card.power === max);
    };
    return {
      rmax,
      me: maximum(this.player),
      op: maximum(this.player.opponent()),
    };
  }
  getBoardData() {
    const data = this.countCards(new CardContainer());
    this.arena.rows(this.player).forEach((row) => this.countCards(row, data));
    data.grave_me = this.countCards(this.player.grave);
    data.grave_op = this.countCards(this.player.opponent().grave);
    return data;
  }
  weightScorchRow(card, max, rowName) {
    const row = board.getRow({ abilities: [] }, rowName, this.player.opponent());
    return row.total >= 10 && !this.player.opponent().arenaShield
      ? this.removalLoss(row, row.maxUnits())
      : 0;
  }
  removalLoss(row, targets) {
    if (!targets.length) return 0;
    const removed = new Set(targets),
      virtual = row.getVirtualCopy((card) => !removed.has(card));
    for (const card of targets)
      if (card.abilities.includes('avenger')) {
        const form = createForm(this.arena, card, 'avengerForm');
        virtual.cards.push(form);
        virtual.updateState(form, true);
      }
    return row.calcScore() - virtual.calcScore();
  }
  weightMedic(data, score, owner) {
    const target = this.medic(null, owner.grave);
    return score + (target ? Math.max(0, this.recoveryValue(target)) : 0);
  }
  withRecoveryForecast(evaluate) {
    const root = !this.recoveryForecast;
    if (root) this.recoveryForecast = { ids: new Map(), values: new Map() };
    try {
      return evaluate(this.recoveryForecast);
    } finally {
      if (root) this.recoveryForecast = null;
    }
  }
  recoveryValue(card, seen = new Set(), immediate = false) {
    return this.withRecoveryForecast(({ ids, values }) => {
      const identity = (target) => {
        if (!ids.has(target)) ids.set(target, ids.size);
        return ids.get(target);
      };
      const key = `${identity(card)}/${immediate}/${[...seen]
        .map(identity)
        .sort((a, b) => a - b)
        .join(',')}`;
      if (values.has(key)) return values.get(key);
      // Forecasting must not consume the deal RNG for a tied agile placement.
      // Actual deployment still uses the existing agile-row choice.
      const rows = card.row === 'agile' ? ['close', 'ranged'] : [card.row];
      const gain = Math.max(
        ...rows.map((name) =>
          this.immediateGain(
            { type: 'play_card', card, row: board.getRow(card, name, this.player) },
            { fromHand: false, includeGrowth: false, seen },
          ),
        ),
      );
      const value =
        gain +
        (!immediate && card.abilities.includes('spy')
          ? 9 * Math.min(2, this.player.deck.cards.length)
          : 0);
      values.set(key, value);
      return value;
    });
  }
  medic(card, grave, seen = new Set()) {
    return this.withRecoveryForecast(() => {
      let best,
        bestValue = -Infinity;
      for (const target of grave.cards) {
        if (!target.isUnit() || seen.has(target)) continue;
        const value = this.recoveryValue(target, seen);
        if (!best || value > bestValue) {
          best = target;
          bestValue = value;
        }
      }
      return best;
    });
  }
  copyAbilityValue(target, card, row, immediate = false) {
    const id = target.abilities.find((id) =>
      ['medic', 'morale', 'scorch_c', 'scorch_r', 'scorch_s'].includes(id),
    );
    if (id === 'medic') {
      const recovered = this.medic(null, this.player.grave);
      return recovered ? this.recoveryValue(recovered, new Set(), immediate) : 0;
    }
    if (id === 'morale') {
      const virtual = row.getVirtualCopy();
      // The hero may not yet be on the board when evaluating an action.
      if (!virtual.cards.includes(card)) virtual.cards.push(card);
      const before = virtual.calcScore();
      virtual.effects.morale++;
      return virtual.calcScore() - before;
    }
    return this.weightScorchRow(
      card,
      null,
      { scorch_c: 'close', scorch_r: 'ranged', scorch_s: 'siege' }[id],
    );
  }
  chooseCopyTarget(card, row, targets) {
    return [...targets].sort(
      (a, b) => this.copyAbilityValue(b, card, row) - this.copyAbilityValue(a, card, row),
    )[0];
  }
  setupValue(action) {
    const { card, row } = action;
    if (!row || !card.isUnit()) return 0;
    if (card.abilities.includes('bond')) {
      const companions = this.player.hand.cards.filter(
        (target) => target !== card && target.isUnit() && target.name === card.name,
      ).length;
      const base = row.effects.weather ? Math.min(1, card.basePower) : card.basePower;
      return Math.min(12, companions * base);
    }
    if (row.effects.mardroeme) return 0;
    if (
      card.abilities.includes('berserker') &&
      this.player.hand.cards.some(
        (target) => target.abilities.includes('mardroeme') && target.row === row.type,
      )
    ) {
      const form = createForm(this.arena, card, 'transformForm');
      return Math.max(0, row.calcCardScore(form) - row.calcCardScore(card)) * 0.75;
    }
    if (card.abilities.includes('mardroeme')) {
      const pending = this.player.hand.cards.filter(
        (target) => target.abilities.includes('berserker') && target.row === row.type,
      );
      return Math.min(
        12,
        pending.reduce(
          (sum, target) =>
            sum +
            Math.max(
              0,
              row.calcCardScore(createForm(this.arena, target, 'transformForm')) -
                row.calcCardScore(target),
            ),
          0,
        ) * 0.75,
      );
    }
    return 0;
  }
  weightPass() {
    const player = this.player,
      opponent = player.opponent();
    if (player.health === 1) return 0;
    const deficit = opponent.total - player.total;
    // The upstream `handsize` field stays at 10. Use live hand counts instead.
    if (deficit < -30 && opponent.hand.cards.length - player.hand.cards.length > 2) return 100;
    return super.weightPass() * (1.6 - models[player.deck.faction].risk);
  }
  weatherDelta(clear, key) {
    let total = 0;
    for (const row of board.row) {
      const kind = { close: 'frost', ranged: 'fog', siege: 'rain' }[row.type];
      const affected = key === 'storm' ? ['fog', 'rain'].includes(kind) : kind === key;
      if (clear ? !row.effects.weather : !affected || row.effects.weather) continue;
      const virtual = row.getVirtualCopy();
      virtual.effects.weather = !clear;
      const delta = virtual.calcScore() - row.calcScore();
      total += this.arena.rows(this.player).includes(row) ? delta : -delta;
    }
    return total;
  }
  scorchValue() {
    const units = board.row.flatMap((row) =>
      row.cards
        .filter((card) => card.isUnit())
        .map((card) => ({
          card,
          row,
          own: this.arena.rows(this.player).includes(row),
        })),
    );
    const max = Math.max(0, ...units.map(({ card }) => card.power));
    return board.row.reduce((sum, row) => {
      const own = this.arena.rows(this.player).includes(row);
      if (!own && this.player.opponent().arenaShield) return sum;
      const targets = units
        .filter((unit) => unit.row === row && unit.card.power === max)
        .map((unit) => unit.card);
      return sum + (own ? -1 : 1) * this.removalLoss(row, targets);
    }, 0);
  }
  immediateGain(action, { fromHand = true, includeGrowth = true, seen = new Set() } = {}) {
    const growth = includeGrowth
      ? this.arena
          .rows(this.player)
          .flatMap((row) => row.cards)
          .filter((card) => card.abilities.includes('deep_think'))
          .reduce((sum, card) => sum + growthGain(card), 0)
      : 0;
    if (action.type === 'pass') return 0;
    if (action.type === 'leader') {
      const ability = this.player.leader.abilities[0],
        rowName = leaderHornRows[ability];
      if (rowName) {
        const row = board.getRow({ abilities: [] }, rowName, this.player),
          virtual = row.getVirtualCopy();
        if (row.special) return growth;
        virtual.effects.horn++;
        return virtual.calcScore() - row.calcScore() + growth;
      }
      if (ability === 'leader_gemini_clear') return this.clearWeatherValue() + growth;
      if (ability === 'leader_deepseek_recover') {
        const card = this.medic(null, this.player.grave);
        return (card ? this.recoveryValue(card, new Set(), true) : 0) + growth;
      }
      if (['leader_claude_deny', 'leader_claude_retrieve'].includes(ability)) return growth;
      if (this.player.deck.faction === 'gemini')
        return Math.max(0, this.clearWeatherValue()) + growth;
      if (this.player.deck.faction !== 'deepseek') return growth;
      const unit = this.arena
        .rows(this.player)
        .flatMap((row) => row.cards.map((card) => ({ card, row })))
        .filter(({ card }) => card.isUnit())
        .sort((a, b) => b.card.power - a.card.power)[0];
      if (!unit) return growth;
      const copy = Object.create(unit.card);
      copy.arenaBonus = (copy.arenaBonus || 0) + balanceRules.leaderOptimization;
      return unit.row.calcCardScore(copy) - unit.card.power + growth;
    }
    const card = action.card,
      ability = card.abilities[0];
    if (ability === 'decoy') {
      const target = this.chooseDecoyTarget();
      if (!target) return growth;
      const row = this.arena.rows(this.player).find((row) => row.cards.includes(target));
      const virtual = row.getVirtualCopy();
      virtual.cards.splice(virtual.cards.indexOf(target), 1);
      virtual.updateState(target, false);
      if (target.abilities.includes('avenger')) {
        const form = createForm(this.arena, target, 'avengerForm');
        virtual.cards.push(form);
        virtual.updateState(form, true);
      }
      return virtual.calcScore() - row.calcScore() + growth;
    }
    if (card.row === 'weather') return this.weightWeather(card) + growth;
    if (ability === 'scorch') return this.scorchValue() + growth;
    if (!action.row) return growth;
    const virtual = action.row.getVirtualCopy(),
      copy = Object.create(card);
    if (copy.isUnit() && fromHand)
      copy.arenaBonus = (copy.arenaBonus || 0) + (this.player.arenaBoost || 0);
    if (ability === 'deep_think') copy.arenaGrowth = (copy.arenaGrowth || 0) + growthGain(copy);
    if (card.isSpecial()) virtual.special = copy;
    else virtual.cards.push(copy);
    virtual.updateState(copy, true);
    if (virtual.effects.mardroeme)
      for (const target of [...virtual.cards]) {
        if (!target.abilities.includes('berserker')) continue;
        const form = createForm(this.arena, target, 'transformForm');
        virtual.cards[virtual.cards.indexOf(target)] = form;
        virtual.updateState(target, false);
        virtual.updateState(form, true);
      }
    let gain = virtual.calcScore() - action.row.calcScore();
    if (!this.arena.rows(this.player).includes(action.row)) gain = -gain;
    if (ability === 'visual_analysis') gain += Math.max(0, this.clearWeatherValue());
    if (ability?.startsWith('scorch_'))
      gain += this.weightScorchRow(
        card,
        null,
        { scorch_c: 'close', scorch_r: 'ranged', scorch_s: 'siege' }[ability],
      );
    // Bound forecast work on long medic chains; execution still resolves the entire chain.
    if (ability === 'medic' && !seen.has(card) && seen.size < 3) {
      const next = new Set(seen);
      next.add(card);
      const target = this.medic(card, this.player.grave, next);
      if (target) gain += this.recoveryValue(target, next, true);
    }
    if (ability === 'long_context') {
      const targets = this.arena
        .rows(this.player.opponent())
        .flatMap((row) => row.cards)
        .filter(
          (target) =>
            target.isUnit() &&
            target.abilities.some((id) =>
              ['medic', 'morale', 'scorch_c', 'scorch_r', 'scorch_s'].includes(id),
            ),
        );
      const target = this.chooseCopyTarget(card, action.row, targets);
      if (target) gain += this.copyAbilityValue(target, card, action.row, true);
    }
    if (ability === 'muster') {
      const members = [...this.player.hand.cards, ...this.player.deck.cards].filter(
        (target) =>
          target !== card &&
          target.isUnit() &&
          target.arenaData.musterGroup === card.arenaData.musterGroup,
      );
      for (const member of members) {
        const row =
          member.row === 'agile'
            ? this.determineAgileRow(member)
            : board.getRow(member, member.row, this.player);
        gain += row.calcCardScore(member);
      }
    }
    return gain + growth;
  }
  score(action, max, data) {
    const player = this.player,
      opponent = player.opponent();
    if (action.type === 'pass') {
      if (!player.hand.cards.length && !player.leaderAvailable) return 100;
      if (opponent.passed) return playerDifference(player) > 0 ? 100 : 0;
      return this.weightPass();
    }
    if (action.type === 'leader') {
      const ability = player.leader.abilities[0];
      if (leaderHornRows[ability]) return Math.max(0, this.immediateGain(action));
      if (ability === 'leader_gemini_clear')
        return Math.max(
          0,
          this.immediateGain(action) +
            9 * Math.min(balanceRules.clearLeaderDraw, player.deck.cards.length),
        );
      if (ability === 'leader_deepseek_recover') return this.weightMedic(null, 0, player);
      if (ability === 'leader_claude_deny') return opponent.leaderAvailable ? 8 : 0;
      if (ability === 'leader_claude_retrieve')
        return opponent.grave.cards.some((card) => card.isUnit()) ? 12 : 0;
      switch (player.deck.faction) {
        case 'deepseek':
          return (player.deck.cards.length ? 9 : 0) + Math.max(0, this.immediateGain(action) * 2);
        case 'claude':
          return (player.deck.cards.length ? 9 : 0) + this.shieldValue();
        case 'gemini':
          return Math.max(0, this.analysisValue() + (player.deck.cards.length ? 9 : 0));
        default:
          return (
            (player.deck.cards.length ? 9 : 0) +
            (player.hand.cards.some((card) => card.isUnit()) ? balanceRules.leaderBoost : 0)
          );
      }
    }
    const card = action.card,
      ability = card.abilities[0];
    const usesImmediate =
      [
        'muster',
        'mardroeme',
        'berserker',
        'morale',
        'bond',
        'horn',
        'long_context',
        'spy',
      ].includes(ability) ||
      ability?.startsWith('scorch_') ||
      card.row === 'agile';
    let score;
    if (card.isSpecial()) score = Math.max(0, this.weightHornRow(card, action.row));
    else if (ability === 'decoy') score = this.decoyValue();
    else if (ability === 'spy')
      score = this.immediateGain(action) + 9 * Math.min(2, player.deck.cards.length);
    else if (usesImmediate) score = this.immediateGain(action);
    else if (ability === 'avenger')
      score = player.health > 1 ? card.arenaData.avengerForm.power : 1;
    else if (ability === 'scorch') score = Math.max(0, this.scorchValue());
    else if (ability === 'chain_of_thought')
      score = player.hand.cards.some((card) => card.isUnit()) ? 6 : 0;
    else if (ability === 'safety_layer') score = this.shieldValue();
    else if (ability === 'context_window') score = player.hand.cards.length >= 3 ? 4 : 0;
    else if (card.row === 'weather') score = Math.max(0, this.weightWeather(card));
    else score = super.weightCard(card, max, data);
    score += this.setupValue(action);
    if (action.row && card.isUnit() && player.arenaBoost && !usesImmediate)
      score += this.boostGain(action);
    if (ability === 'deep_think')
      score += Math.min(
        balanceRules.growthLimit,
        player.hand.cards.length * balanceRules.growthPerAction,
      );
    if (ability === 'future_predict')
      score += player.hand.cards.some((card) => card.isUnit()) ? 4 : 0;
    if (ability === 'visual_analysis') score += this.analysisValue();
    return Math.max(
      0,
      strategyAdjustment(player, action, Number.isFinite(score) ? score : 0, this.arena),
    );
  }
  boostGain(action) {
    const copy = Object.create(action.card),
      row = action.row.getVirtualCopy();
    row.cards.push(copy);
    row.updateState(copy, true);
    const before = row.calcScore();
    copy.arenaBonus = (copy.arenaBonus || 0) + this.player.arenaBoost;
    const gain = row.calcScore() - before;
    return this.arena.rows(this.player).includes(action.row) ? gain : -gain;
  }
  shieldValue() {
    const player = this.player;
    if (player.arenaShield || player.opponent().passed || !player.opponent().hand.cards.length)
      return 0;
    const rows = this.arena.rows(player);
    // Discount visible exposure: the enemy may not actually hold a pruning card.
    const losses = rows
      .filter((row) => row.total >= 10)
      .map((row) => this.removalLoss(row, row.maxUnits()));
    const max = Math.max(
      0,
      ...board.row.flatMap((row) =>
        row.cards.filter((card) => card.isUnit()).map((card) => card.power),
      ),
    );
    const globalLoss = rows.reduce(
      (sum, row) =>
        sum +
        this.removalLoss(
          row,
          row.cards.filter((card) => card.isUnit() && card.power === max),
        ),
      0,
    );
    return Math.max(0, globalLoss, ...losses) * 0.5;
  }
  clearWeatherValue() {
    return this.weatherDelta(true);
  }
  analysisValue() {
    const clear = this.clearWeatherValue();
    return clear > 0
      ? clear
      : this.player.hand.cards.some((card) => card.isUnit())
        ? balanceRules.adaptationBoost
        : 0;
  }
  decoyTargetValue(card) {
    if (card.abilities.includes('avenger')) return card.arenaData.avengerForm.power - card.power;
    if (card.abilities.includes('spy') && this.player.deck.cards.length)
      return 9 * Math.min(2, this.player.deck.cards.length) - card.power - card.basePower;
    if (
      card.abilities.includes('medic') &&
      this.player.grave.cards.some((target) => target.isUnit())
    )
      return 12 - card.power;
    if (card.abilities.some((id) => id.startsWith('scorch_'))) {
      const row = { scorch_c: 'close', scorch_r: 'ranged', scorch_s: 'siege' }[
        card.abilities.find((id) => id.startsWith('scorch_'))
      ];
      return this.weightScorchRow(card, null, row) - card.power;
    }
    return this.player.health > 1 && this.player.total - card.power > this.player.opponent().total
      ? 1
      : 0;
  }
  chooseDecoyTarget() {
    return decoyTargets(this.arena, this.player).sort(
      (a, b) => this.decoyTargetValue(b) - this.decoyTargetValue(a),
    )[0];
  }
  decoyValue() {
    const target = this.chooseDecoyTarget();
    return target ? Math.max(0, this.decoyTargetValue(target)) : 0;
  }
  weightWeather(card) {
    const key = card.abilities[0];
    return this.weatherDelta(key === 'clear', key);
  }
  weightedOptions(actions, max, data) {
    const choices = new Map();
    for (const action of actions) {
      const candidate = { action, weight: this.score(action, max, data) };
      // One draw weight per physical card, irrespective of the number of legal rows.
      const key = action.card || action.type;
      const previous = choices.get(key);
      if (!previous || candidate.weight > previous.weight) choices.set(key, candidate);
    }
    return [...choices.values()];
  }
  chooseAction(player = this.player) {
    const actions = legalActions(player, this.arena),
      max = this.getMaximums(),
      data = this.getBoardData();
    const opponent = player.opponent();
    let chosen, reason;
    if (canFinishByPassing(player)) {
      chosen = actions.find((action) => action.type === 'pass');
      reason =
        player.total > opponent.total
          ? '对手已放弃，保留手牌收下本局'
          : playerDifference(player) > 0
            ? '阵营裁定可赢得本局，保留手牌'
            : '平局即可赢得整场，保留手牌';
    } else if (opponent.passed) {
      const finishers = actions.filter(
        (action) =>
          action.type !== 'pass' &&
          playerDifference(player, player.total + this.immediateGain(action), opponent.total) > 0,
      );
      finishers.sort(
        (a, b) =>
          this.resourceCost(a) - this.resourceCost(b) ||
          this.immediateGain(a) - this.immediateGain(b),
      );
      if (finishers.length) {
        chosen = finishers[0];
        reason = '对手已放弃，用较小资源赢得本局';
      }
    }
    if (!chosen) {
      const scored = this.weightedOptions(actions, max, data);
      const total = scored.reduce((sum, candidate) => sum + candidate.weight, 0);
      if (!total) {
        chosen = actions.find((action) => action.type === 'pass');
        reason = '没有有益行动，保留剩余资源';
      } else {
        let roll = Math.random() * total;
        chosen =
          scored.find((candidate) => (roll -= candidate.weight) < 0)?.action ||
          scored.at(-1).action;
        if (chosen.type === 'pass') reason = '参照原版分差与手牌优势，保留资源进入下一小局';
      }
    }
    this.lastDecision = {
      action: chosen.type,
      card: chosen.card?.arenaData.id,
      reason: reason || '按原版权重与阵营偏好选择',
      round: game.roundCount,
    };
    return chosen;
  }
  resourceCost(action) {
    if (action.type === 'leader') return 10;
    const card = action.card;
    return (
      (card.hero ? 20 : 0) +
      (card.abilities.includes('medic') ? 8 : 0) +
      Math.max(1, card.basePower)
    );
  }
  async startTurn(player) {
    this.arena.busy = true;
    this.arena.view.render();
    await this.arena.delay(350);
    const chosen = this.chooseAction(player);
    if (this.lastDecision.reason !== '按原版权重与阵营偏好选择')
      this.arena.view.log(`${this.arena.label(player)} · ${this.lastDecision.reason}。`);
    try {
      await executeAction(player, chosen, this.arena);
    } catch (error) {
      this.arena.fail(error);
    }
  }
}
