/* eslint-disable */
import { models } from '../ai/models.js';
import { ROWS, rowNames } from '../game/rules.js';
import { cardElement, abilityNames, cardDescription, updateCardElement } from './card.js';
import { legalActions } from '../game/battle.js';
import { pulse } from './animation.js';
import { setTooltip } from './tooltip.js';
import { setFactionMark } from './faction-mark.js';
import { chooseCardsTimed, chooseRowTimed, expireDialog } from './timed-choice.js';
import { roundDifference } from '../game/factions.js';
import { renderLeaderHelp } from './leader-help.js';

function syncChildren(parent, nodes) {
  nodes.forEach((node, index) => {
    if (parent.children[index] !== node) parent.insertBefore(node, parent.children[index] || null);
  });
  while (parent.children.length > nodes.length) parent.lastElementChild.remove();
}

export class ArenaView {
  constructor(arena) {
    this.arena = arena;
    this.selection = null;
    this.modalOpen = false;
    this.logItems = [];
    this.cards = new WeakMap();
    this.scores = new WeakMap();
    this.suppressFeedback = false;
  }
  log(text) {
    this.logItems.unshift(text);
    this.logItems = this.logItems.slice(0, 18);
    const list = document.getElementById('battle-log');
    list.replaceChildren(
      ...this.logItems.map((message) => {
        const item = document.createElement('li');
        item.textContent = message;
        return item;
      }),
    );
  }
  announce(text) {
    document.getElementById('battle-status').textContent = text;
  }
  reset() {
    this.cards = new WeakMap();
    this.scores = new WeakMap();
    this.suppressFeedback = false;
    document
      .querySelectorAll('[data-change]')
      .forEach((element) => element.removeAttribute('data-change'));
    document.getElementById('round-summary').hidden = true;
    document.getElementById('action-card').replaceChildren();
    document.getElementById('action-title').textContent = '等待第一步部署';
    document.getElementById('action-description').textContent = '最近行动与技能说明将显示在这里。';
    document.getElementById('selection-info').hidden = true;
  }
  score(element, value) {
    const before = this.scores.get(element);
    element.textContent = value;
    this.scores.set(element, value);
    if (!this.suppressFeedback && before !== undefined && before !== value) {
      const delta = value - before;
      element.dataset.change = `${delta > 0 ? '+' : ''}${delta}`;
      element.classList.toggle('score-down', delta < 0);
      setTooltip(element, `最近一次变化 ${element.dataset.change}`);
      pulse(element, { speed: this.arena.speed });
    }
  }
  renderCard(card, scope) {
    let cached = this.cards.get(card);
    if (!cached) {
      cached = {};
      this.cards.set(card, cached);
    }
    let entry = cached[scope];
    const fresh = !entry;
    if (!entry) {
      entry = cached[scope] = {
        element: cardElement(card, {
          small: scope === 'board',
          onClick: () =>
            scope === 'board'
              ? this.inspect(card)
              : this.arena.canAct()
                ? this.select(card)
                : this.inspect(card),
        }),
        power: card.power,
      };
    }
    updateCardElement(entry.element, card, scope === 'hand' && this.selection === card);
    if (!this.suppressFeedback && (fresh || entry.power !== card.power))
      pulse(entry.element, {
        kind: fresh ? 'play' : 'effect',
        speed: this.arena.speed,
      });
    entry.power = card.power;
    return entry.element;
  }
  effect(card, kind) {
    const element = this.cards.get(card)?.board?.element;
    pulse(element, { kind, speed: this.arena.speed });
  }
  preview(card) {
    const name = this.arena.label(card.holder);
    document.getElementById('action-title').textContent =
      `${name} ${card.row === 'leader' ? '激活领袖' : '打出'} · ${card.name}`;
    document.getElementById('action-description').textContent = cardDescription(card);
    const element = cardElement(card, { small: true });
    document.getElementById('action-card').replaceChildren(element);
    pulse(element, { kind: 'play', speed: this.arena.speed });
  }
  showRound(record) {
    const me = models[player_me.deck.faction].short,
      op = models[player_op.deck.faction].short;
    const diff = roundDifference(
      player_me.deck.faction,
      player_op.deck.faction,
      record.total.me,
      record.total.op,
    );
    document.getElementById('round-summary-title').textContent =
      `第 ${record.round} 小局 · ${diff === 0 ? '平局，双方失去一颗生命' : `${diff > 0 ? me : op} 胜出`}`;
    document.getElementById('round-summary-score').textContent =
      `${me} ${record.total.me} : ${record.total.op} ${op}`;
    const grid = document.getElementById('round-summary-rows');
    grid.replaceChildren(
      ...record.rows.map(({ row, me, op }) => {
        const item = document.createElement('span');
        item.textContent = `${rowNames[row]}  ${me} : ${op}`;
        return item;
      }),
    );
    document.getElementById('round-summary-lives').textContent =
      `${me} 生命 ${record.before.me} → ${record.after.me} · ${op} 生命 ${record.before.op} → ${record.after.op}；手牌跨小局保留。`;
    document.getElementById('round-summary').hidden = false;
    pulse(document.getElementById('round-summary'), {
      speed: this.arena.speed,
    });
  }
  resultHistory(records) {
    const container = document.getElementById('result-history');
    container.replaceChildren(
      ...records.map((record) => {
        const item = document.createElement('section');
        item.className = 'result-round';
        const heading = document.createElement('strong');
        const me = models[player_me.deck.faction].short,
          op = models[player_op.deck.faction].short;
        const difference = roundDifference(
          player_me.deck.faction,
          player_op.deck.faction,
          record.total.me,
          record.total.op,
        );
        const winner = difference === 0 ? '平局' : `${difference > 0 ? me : op} 胜出`;
        heading.textContent = `第 ${record.round} 局 · ${record.total.me} : ${record.total.op} · ${winner}`;
        const details = document.createElement('p');
        details.textContent = record.rows
          .map(({ row, me, op }) => `${rowNames[row]} ${me}:${op}`)
          .join(' · ');
        const lives = document.createElement('p');
        lives.textContent = `生命 ${me} ${record.before.me}→${record.after.me} / ${op} ${record.before.op}→${record.after.op}`;
        item.append(heading, details, lives);
        return item;
      }),
    );
  }
  render() {
    if (!player_me || !player_op) return;
    const arena = this.arena,
      playing = game.state === GameState.PLAYING;
    for (const player of [player_op, player_me]) {
      const side = player === player_me ? 'me' : 'op',
        model = models[player.deck.faction];
      const stats = document.getElementById(`arena-stats-${side}`);
      stats.style.setProperty('--faction-color', model.color);
      document.getElementById('battle').style.setProperty(`--${side}-color`, model.color);
      setFactionMark(stats.querySelector('.faction-mark'), player.deck.faction);
      stats.querySelector('.faction-name').textContent = model.short;
      stats.querySelector('.player-label').textContent =
        side === 'op'
          ? arena.matchMode === 'llm'
            ? '对手 / LLM'
            : '对手 / LOCAL AI'
          : arena.mode === 'watch'
            ? '观战 / LOCAL AI'
            : '你 / HUMAN';
      this.score(stats.querySelector('.total-power'), player.total);
      stats.querySelector('.lives').textContent =
        '◆'.repeat(player.health) + '◇'.repeat(2 - player.health);
      setTooltip(stats.querySelector('.lives'), `剩余生命 ${player.health} / 2`);
      stats.classList.toggle('passed', !!player.passed);
      stats.querySelector('.pile-count').textContent =
        `手牌 ${player.hand.cards.length} · 牌库 ${player.deck.cards.length} · 弃牌 ${player.grave.cards.length}`;
      stats.querySelector('.player-effects').textContent = [
        player.passed ? '已放弃本小局' : '',
        player.arenaShield ? '◇ 防护就绪' : '',
        player.arenaBoost ? `↑ 待强化 +${player.arenaBoost}` : '',
        player.leader.abilities.includes('leader_deepseek_rebirth')
          ? '第三局重启 · 被动领袖'
          : player.leaderAvailable
            ? ''
            : '领袖已使用',
      ]
        .filter(Boolean)
        .join(' · ');
      stats.classList.toggle('active-player', playing && game.currPlayer === player);
      for (const rowName of ROWS) {
        const row = board.getRow({ abilities: [] }, rowName, player),
          element = document.getElementById(`arena-${side}-${rowName}`);
        element.classList.toggle('weather-active', row.effects.weather);
        element.classList.toggle(
          'target-row',
          !!(
            arena.canAct() &&
            this.selection &&
            legalActions(player_me, this.arena).some(
              (action) => action.card === this.selection && action.row === row,
            )
          ),
        );
        this.score(element.querySelector('.row-number'), row.total);
        const slots = element.querySelector('.row-units');
        syncChildren(
          slots,
          [...row.cards, ...(row.special ? [row.special] : [])].map((card) =>
            this.renderCard(card, 'board'),
          ),
        );
        element.querySelector('.row-modifier').textContent =
          `${row.effects.weather ? '基础 ≤1 · 英雄免疫 ' : ''}${row.effects.horn ? '普通单位 ×2 ' : ''}${row.effects.morale ? `对齐 +${row.effects.morale}` : ''}`;
        setTooltip(
          element,
          row.effects.weather
            ? `${rowNames[rowName]}：双方普通单位基础战力压低至最多 1；随后仍计算集群、对齐和翻倍。英雄免疫天气。`
            : `${rowNames[rowName]}：分数为本战线卡牌当前战力之和。`,
        );
      }
    }
    document
      .getElementById('battle')
      .classList.toggle(
        'my-turn',
        playing && game.currPlayer === player_me && arena.mode !== 'watch',
      );
    document.getElementById('battle').classList.toggle('has-selection', !!this.selection);
    document.getElementById('round-label').textContent =
      `ROUND ${String(game.roundCount || 1).padStart(2, '0')}`;
    document.getElementById('round-record').textContent =
      game.roundHistory.map((round) => `${round.score_me} : ${round.score_op}`).join('  /  ') ||
      '三局两胜 · 手牌跨小局保留';
    const canAct = arena.canAct();
    for (const id of ['view-cards', 'view-rules'])
      document.getElementById(id).disabled = playing && !canAct;
    const hand = document.getElementById('arena-hand');
    syncChildren(
      hand,
      player_me.hand.cards.map((card) => this.renderCard(card, 'hand')),
    );
    const selectionInfo = document.getElementById('selection-info');
    selectionInfo.hidden = !this.selection;
    if (this.selection) {
      document.getElementById('selection-name').textContent = this.selection.name;
      document.getElementById('selection-description').textContent = cardDescription(
        this.selection,
      );
    }
    document.getElementById('hand-label').textContent =
      arena.mode === 'watch' ? '观察席 · 手牌公开' : `你的手牌 · ${player_me.hand.cards.length}`;
    document.getElementById('arena-pass').disabled = !canAct;
    document.getElementById('arena-leader').disabled = !canAct || !player_me.leaderAvailable;
    document.getElementById('arena-leader').textContent = player_me.leaderAvailable
      ? `领袖 · ${abilityNames[player_me.leader.abilities[0]]}`
      : player_me.leader.abilities.includes('leader_deepseek_rebirth')
        ? '第三局重启 · 被动'
        : '领袖技能已使用';
    document.getElementById('arena-concede').disabled = !canAct;
    renderLeaderHelp(player_me.leader, player_me.leaderAvailable);
    document.getElementById('inspect-hand').disabled = !canAct || !player_me.hand.cards.length;
    document.getElementById('weather-label').textContent = weather.cards.length
      ? weather.cards
          .map(
            (card) =>
              `${abilityNames[card.abilities[0]]} · ${card.abilities[0] === 'storm' ? '感知矩阵 / 算力集群' : rowNames[{ frost: 'close', fog: 'ranged', rain: 'siege' }[card.abilities[0]]]}`,
          )
          .join(' / ')
      : '所有战线信号正常';
    if (playing && !this.selection && !this.modalOpen)
      this.announce(
        !game.currPlayer
          ? '正在抽取开局手牌…'
          : arena.mode === 'watch'
            ? `${models[game.currPlayer.deck.faction].short} 正在决策…`
            : game.currPlayer === player_me && !arena.busy
              ? '选择一张手牌，再点击高亮战线出牌。'
              : arena.llmThinking
                ? 'LLM 正在思考下一步…'
                : '对手正在推演下一步…',
      );
  }
  select(card) {
    if (!this.arena.canAct()) return;
    if (card.row === 'weather' || (card.row === 'special' && !card.isSpecial())) {
      this.arena.act(card);
      return;
    }
    this.selection = this.selection === card ? null : card;
    this.render();
    if (this.selection) this.announce(`${card.name} · 点击高亮战线部署；再次点击取消。`);
  }
  inspect(card) {
    if (this.modalOpen || (game.state === GameState.PLAYING && !this.arena.canAct())) return;
    this.chooseCards(
      { cards: [card] },
      0,
      () => {},
      () => true,
      true,
      '卡牌详情',
    );
  }
  chooseRow(card) {
    return chooseRowTimed(this, card);
  }
  chooseCards(container, count, action, predicate, canQuit, title = '选择卡牌') {
    return chooseCardsTimed(this, container, count, action, predicate, canQuit, title);
  }
  expireDialog() {
    expireDialog(this);
  }
  showDialog(title, description, actions) {
    this.modalOpen = true;
    document.getElementById('dialog-title').textContent = title;
    document.getElementById('dialog-description').textContent = description;
    document.getElementById('dialog-cards').replaceChildren();
    document.getElementById('dialog-actions').replaceChildren(
      ...actions.map(({ label, action }) => {
        const button = document.createElement('button');
        button.className = 'primary-button';
        button.textContent = label;
        button.onclick = action;
        return button;
      }),
    );
    const dialog = document.getElementById('arena-dialog');
    if (!dialog.open) dialog.showModal();
  }
  closeDialog() {
    document.getElementById('arena-dialog').close();
    this.modalOpen = false;
    this.render();
  }
}
