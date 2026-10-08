/* global document, setInterval, clearInterval */
import { models } from './src/ai/models.js';
import { cardElement, updateCardElement, cardDescription, abilityNames } from './src/ui/card.js';
import { setFactionMark } from './src/ui/faction-mark.js';
import { setTooltip } from './src/ui/tooltip.js';
import { renderLeaderHelp } from './src/ui/leader-help.js';
import { leaderDefinitions } from './src/cards/leaders.js';
import { runtimeForms } from './src/cards/forms.js';
import { rowNames } from './src/game/rules.js';
import { pulse, tone } from './src/ui/animation.js';

const choices = {
  mulligan: '开局调度 · 最多重抽两张',
  initiative: '选择第一小局先手',
  medic: '检查点恢复 · 选择普通单位',
  decoy: '诱饵回收 · 选择普通单位',
  context_window: '上下文窗口 · 选择替换的手牌',
  analysis: '视觉分析 · 清除天气或获得强化',
  reveal: '未来推演 · 已知牌库顶牌',
  row: '选择部署战线',
  retrieve: '上下文回收 · 选择敌方普通单位',
  copy: '选择复制目标',
};
export class PlatformBattle {
  constructor(arena, catalog, host) {
    this.arena = arena;
    this.host = host;
    this.catalog = new Map(
      [...catalog, ...leaderDefinitions(catalog), ...runtimeForms(catalog)].map((c) => [c.id, c]),
    );
    this.cache = new Map();
    this.view = null;
    this.selected = null;
    this.decision = null;
    this.replaying = false;
    const showDialog = arena.view.showDialog.bind(arena.view);
    arena.view.showDialog = (...args) => {
      this.openInfo();
      return showDialog(...args);
    };
    arena.view.closeDialog = () => this.closeInfo();
    document.getElementById('arena-dialog').addEventListener('close', () => {
      if (this.infoOpen) this.closeInfo();
    });
    const inspect = arena.view.inspect.bind(arena.view);
    arena.view.inspect = (card) => (this.view ? this.inspect([card], '卡牌详情') : inspect(card));
    document.getElementById('arena-pass').onclick = () =>
      this.view
        ? this.perform(this.view.legal_actions.find((a) => a.kind === 'pass'))
        : arena.command('pass');
    document.getElementById('arena-leader').onclick = () =>
      this.view
        ? this.perform(this.view.legal_actions.find((a) => a.kind === 'leader'))
        : arena.command('leader');
    document.getElementById('arena-concede').onclick = () =>
      this.view ? this.confirmSurrender() : arena.command('concede');
    const handInspect = document.getElementById('inspect-hand').onclick;
    document.getElementById('inspect-hand').onclick = () =>
      this.view ? this.inspect(this.view.hand, '手牌详情 · 查看效果与触发时机') : handInspect();
    for (const row of document.querySelectorAll('.arena-row')) {
      row.onclick = (event) => {
        if (!this.view || event.target.closest('button')) return;
        const side = row.dataset.side === 'me' ? 'self' : 'enemy';
        const legal = this.view.legal_actions.find(
          (a) => a.card === this.selected && a.row === row.dataset.row,
        );
        const card = this.view.hand.find((c) => c.instance_id === this.selected),
          target = card?.abilities.includes('spy') ? 'enemy' : 'self';
        if (side === target && legal) this.perform(legal);
      };
    }
    document.getElementById('return-lobby').onclick = () =>
      this.view ? this.lobby() : arena.lobby();
    document.getElementById('rematch').onclick = () => {
      if (this.view || this.resultActive) {
        const rematch = this.snapshot?.canRematch;
        this.lobby();
        if (rematch) host.send({ type: 'rematch' });
      } else arena.start('watch', 'local');
    };
  }
  enrich(card) {
    const data = this.catalog.get(card.id) ?? card;
    return {
      ...data,
      ...card,
      arenaData: data,
      image: data.image ? `/assets/gwent/cards/${data.image.split('/').at(-1)}` : undefined,
      thumbnail: data.thumbnail
        ? `/assets/gwent/cards/${data.thumbnail.split('/').at(-1)}`
        : undefined,
    };
  }
  tile(card, scope, side) {
    const enriched = this.enrich(card),
      key = `${scope}:${card.instance_id ?? card.id}`;
    let entry = this.cache.get(key);
    if (!entry) {
      entry = { card: enriched };
      entry.element = cardElement(enriched, {
        small: scope === 'board',
        onClick: () =>
          scope === 'hand' ? this.select(entry.card) : this.inspect([entry.card], '卡牌详情'),
      });
      this.cache.set(key, entry);
      pulse(entry.element, { kind: 'play', speed: this.arena.speed });
    }
    const before = entry.card.power;
    entry.card = enriched;
    entry.card.holder = {
      deck: { faction: side === 'enemy' ? this.view.enemy.faction : this.view.self.faction },
      platformSide: side,
    };
    updateCardElement(entry.element, enriched, this.selected === card.instance_id);
    if (before !== enriched.power) pulse(entry.element, { speed: this.arena.speed });
    entry.element.classList.toggle(
      'playable',
      scope === 'hand' && this.view.legal_actions.some((a) => a.card === card.instance_id),
    );
    return entry.element;
  }
  select(card) {
    if (this.replaying || this.blocked) {
      this.inspect([card], '卡牌详情');
      return;
    }
    const actions = this.view.legal_actions.filter((a) => a.card === card.instance_id);
    if (!actions.length) {
      this.inspect([card], '卡牌详情');
      return;
    }
    if (actions.length === 1 && !actions[0].row) {
      this.perform(actions[0]);
      return;
    }
    this.selected = this.selected === card.instance_id ? null : card.instance_id;
    this.render(this.snapshot, this.view);
  }
  sameWindow(expected) {
    const current = this.snapshot.home?.current;
    return (
      !!expected &&
      current?.id === expected.id &&
      current?.phaseSeq === expected.phaseSeq &&
      current?.decisionID === expected.decisionID
    );
  }
  sameDecision(expected) {
    const current = this.snapshot.home?.current;
    return (
      !!expected &&
      current?.id === expected.id &&
      (current.decisionID
        ? current.decisionID === expected.decisionID
        : !expected.decisionID && current.phaseSeq === expected.phaseSeq)
    );
  }
  perform(action, expected = this.snapshot.home?.current) {
    if (
      action &&
      !this.replaying &&
      !this.blocked &&
      this.sameDecision(expected) &&
      this.view.legal_actions.some(
        (a) => a.kind === action.kind && a.card === action.card && a.row === action.row,
      )
    ) {
      const current = expected;
      this.host.send({
        type: 'action',
        id: current?.id,
        phaseSeq: current?.phaseSeq,
        decisionID: current?.decisionID,
        action,
      });
    }
  }
  render(snapshot, override) {
    this.snapshot = snapshot;
    this.blocked = snapshot.blocked;
    const current = snapshot.home?.current;
    const previous = this.view;
    this.view = override ?? current?.view;
    if (!this.view) return;
    const view = this.view,
      you = current?.you ?? snapshot.home?.latestResult?.you ?? 0;
    if (!view.hand.some((card) => card.instance_id === this.selected)) this.selected = null;
    document.getElementById('lobby').hidden = true;
    document.getElementById('battle').hidden = false;
    document.getElementById('result-overlay').hidden = true;
    document.querySelector('.shell').inert = false;
    document
      .getElementById('battle')
      .classList.toggle('my-turn', view.legal_actions.length > 0 && !this.replaying);
    document.getElementById('battle').classList.toggle('has-selection', this.selected !== null);
    const selectedCard = view.hand.find((card) => card.instance_id === this.selected),
      targetSide = selectedCard?.abilities.includes('spy') ? 'enemy' : 'self';
    for (const side of ['enemy', 'self']) {
      const player = view[side],
        label = side === 'self' ? 'me' : 'op',
        stats = document.getElementById(`arena-stats-${label}`),
        model = models[player.faction];
      stats.style.setProperty('--faction-color', model.color);
      document.getElementById('battle').style.setProperty(`--${label}-color`, model.color);
      setFactionMark(stats.querySelector('.faction-mark'), player.faction);
      stats.querySelector('.faction-name').textContent = model.short;
      const profile =
        current?.profiles[side === 'self' ? you : 1 - you] ??
        snapshot.home?.latestResult?.profiles[side === 'self' ? you : 1 - you];
      stats.querySelector('.player-label').textContent =
        side === 'self'
          ? '你 / HUMAN'
          : current?.ai
            ? `对手 / ${current.ai.terms.bot_name}`
            : `对手 / ${profile?.displayName ?? (profile?.kind === 'deleted' ? '已删除账号' : '匿名玩家')}`;
      this.arena.view.score(
        stats.querySelector('.total-power'),
        view.board.filter((r) => r.side === side).reduce((n, r) => n + r.total, 0),
      );
      stats.querySelector('.lives').textContent =
        '◆'.repeat(player.lives) + '◇'.repeat(2 - player.lives);
      setTooltip(stats.querySelector('.lives'), `剩余生命 ${player.lives} / 2`);
      stats.classList.toggle('passed', player.passed);
      stats.classList.toggle('active-player', side === 'self' && view.legal_actions.length > 0);
      stats.querySelector('.pile-count').textContent =
        `手牌 ${player.hand_count} · 牌库 ${player.deck_count} · 弃牌 ${player.grave.length}`;
      stats.querySelector('.player-effects').textContent = [
        player.passed ? '已放弃本小局' : '',
        player.shield ? '◇ 防护就绪' : '',
        player.boost ? `↑ 待强化 +${player.boost}` : '',
        !player.leader_available ? '领袖已使用或被封锁' : '',
      ]
        .filter(Boolean)
        .join(' · ');
      stats.querySelector('.pile-count').onclick = () =>
        this.inspect(player.grave, `${model.short} · 公开弃牌堆`);
      for (const row of view.board.filter((r) => r.side === side)) {
        const element = document.getElementById(`arena-${label}-${row.row}`);
        element.classList.toggle('weather-active', row.weather);
        element.classList.toggle(
          'target-row',
          side === targetSide &&
            view.legal_actions.some((a) => a.card === this.selected && a.row === row.row),
        );
        this.arena.view.score(element.querySelector('.row-number'), row.total);
        element
          .querySelector('.row-units')
          .replaceChildren(
            ...[...row.cards, ...(row.special ? [row.special] : [])].map((card) =>
              this.tile(card, 'board', side),
            ),
          );
        const morale = row.cards.filter((card) => card.abilities.includes('morale')).length,
          horn =
            row.special?.abilities.includes('horn') ||
            row.cards.some((card) => card.abilities.includes('horn'));
        element.querySelector('.row-modifier').textContent = [
          row.weather ? '基础 ≤1 · 英雄免疫' : '',
          horn ? '普通单位 ×2' : '',
          morale ? `对齐 +${morale}` : '',
        ]
          .filter(Boolean)
          .join(' · ');
        setTooltip(element, `${rowNames[row.row]}：分数为本战线卡牌当前战力之和。`);
      }
    }
    document
      .getElementById('arena-hand')
      .replaceChildren(...view.hand.map((card) => this.tile(card, 'hand', 'self')));
    document.getElementById('hand-label').textContent = `你的手牌 · ${view.hand.length}`;
    document.getElementById('round-label').textContent =
      `ROUND ${String(view.round).padStart(2, '0')}`;
    document.getElementById('round-record').textContent =
      view.rounds.map((r) => `${r.scores[you]} : ${r.scores[1 - you]}`).join(' / ') ||
      '三局两胜 · 手牌跨小局保留';
    const record = view.rounds.at(-1);
    if (record && (!previous || previous.rounds.length !== view.rounds.length))
      this.roundSummary(record, view, you);
    else if (!record) document.getElementById('round-summary').hidden = true;
    document.getElementById('weather-label').textContent =
      view.weather.map((card) => abilityNames[card.abilities[0]] ?? card.name).join(' / ') ||
      '所有战线信号正常';
    document.getElementById('weather-label').onclick = () => this.inspect(view.weather, '当前天气');
    const leader = this.enrich(view.self.leader);
    renderLeaderHelp(leader, view.self.leader_available);
    for (const [id, kind] of [
      ['arena-pass', 'pass'],
      ['arena-leader', 'leader'],
    ])
      document.getElementById(id).disabled =
        this.replaying || this.blocked || !view.legal_actions.some((a) => a.kind === kind);
    document.getElementById('arena-concede').disabled = this.replaying || this.blocked;
    document.getElementById('inspect-hand').disabled = !view.hand.length;
    document.getElementById('arena-leader').textContent = view.self.leader_available
      ? `领袖 · ${abilityNames[leader.abilities[0]] ?? leader.name}`
      : '领袖技能已使用或被封锁';
    document.getElementById('selection-info').hidden = !selectedCard;
    if (selectedCard) {
      document.getElementById('selection-name').textContent = selectedCard.name;
      document.getElementById('selection-description').textContent = cardDescription(
        this.enrich(selectedCard),
      );
    }
    document.getElementById('battle-status').textContent = this.replaying
      ? '回放 · 仅展示当时可见的记录'
      : snapshot.error
        ? snapshot.error
        : this.blocked
          ? '正在确认操作，请稍候…'
          : view.choice
            ? (choices[view.choice.kind] ?? '请完成选择')
            : view.legal_actions.length
              ? '选择一张手牌，再点击高亮战线出牌。'
              : '等待对手行动…';
    const timer = document.getElementById('decision-timer');
    timer.hidden = this.replaying || !current;
    timer.textContent = `${current?.phase === 'turn' ? '决策回合' : '决策'} · ${snapshot.remaining ?? '—'}s`;
    timer.classList.toggle('urgent', (snapshot.remaining ?? 30) <= 5);
    if (previous && !this.replaying) {
      const prior = new Set(previous.board.flatMap((r) => r.cards.map((c) => c.instance_id)));
      const newest = view.board
        .flatMap((r) => r.cards.map((c) => ({ card: c, side: r.side })))
        .filter((entry) => !prior.has(entry.card.instance_id))
        .at(-1);
      if (newest) {
        const card = this.enrich(newest.card);
        card.holder = { deck: { faction: view[newest.side].faction }, platformSide: newest.side };
        this.arena.view.preview(card);
        this.arena.view.log(`${newest.side === 'self' ? '你' : '对手'} · ${card.name}`);
        tone();
      }
    }
    this.choice(
      current?.decisionID ?? `${current?.id}:${current?.phaseSeq}`,
      view.choice,
      view.legal_actions,
    );
  }
  roundSummary(record, view, you) {
    const me = models[view.self.faction].short,
      op = models[view.enemy.faction].short;
    document.getElementById('round-summary-title').textContent =
      `第 ${record.round} 小局 · ${record.winner === null ? '平局' : `${record.winner === you ? me : op} 胜出`}`;
    document.getElementById('round-summary-score').textContent =
      `${me} ${record.scores[you]} : ${record.scores[1 - you]} ${op}`;
    document.getElementById('round-summary-rows').replaceChildren(
      ...(record.rows ?? []).map(({ row, scores }) => {
        const item = document.createElement('span');
        item.textContent = `${rowNames[row]}  ${scores[you]} : ${scores[1 - you]}`;
        return item;
      }),
    );
    document.getElementById('round-summary-lives').textContent =
      record.lives_before && record.lives_after
        ? `${me} 生命 ${record.lives_before[you]} → ${record.lives_after[you]} · ${op} 生命 ${record.lives_before[1 - you]} → ${record.lives_after[1 - you]}；手牌跨小局保留。`
        : '该记录未保存生命变动；手牌跨小局保留。';
    const summary = document.getElementById('round-summary');
    summary.hidden = false;
    pulse(summary, { speed: this.arena.speed });
  }
  choice(identity, choice, actions) {
    if (this.replaying || this.infoOpen) return;
    const dialog = document.getElementById('arena-dialog');
    if (!choice || !actions.length) {
      if (this.decision) {
        dialog.close();
        this.decision = null;
        this.choiceKey = null;
      }
      return;
    }
    const key = JSON.stringify({ identity, choice, actions, blocked: this.blocked });
    if (key !== this.choiceKey) {
      const expected = this.snapshot.home?.current;
      document.getElementById('dialog-title').textContent = choices[choice.kind] ?? '完成卡牌选择';
      document.getElementById('dialog-description').textContent =
        `还可选择 ${choice.remaining} 次 · 按屏幕倒计时完成选择`;
      document.getElementById('dialog-cards').replaceChildren(
        ...choice.cards.map((card) => {
          const action = actions.find((a) => a.card === card.instance_id),
            element = cardElement(this.enrich(card), {
              ...(action ? { onClick: () => this.perform(action, expected) } : {}),
            });
          if (action) element.disabled = this.blocked;
          return element;
        }),
      );
      document.getElementById('dialog-actions').replaceChildren(
        ...actions
          .filter((a) => a.card === undefined || a.card === 0)
          .map((action) => {
            const button = this.button(
              action.row
                ? (rowNames[action.row] ?? action.row)
                : action.kind === 'continue'
                  ? '继续'
                  : '完成选择',
              () => this.perform(action, expected),
            );
            button.disabled = this.blocked;
            return button;
          }),
      );
      this.choiceKey = key;
    }
    this.decision = identity;
    if (!dialog.open) dialog.showModal();
    document.getElementById('decision-dialog-timer').hidden = false;
    document.getElementById('decision-dialog-timer').textContent =
      `剩余 ${this.snapshot.remaining ?? '—'}s`;
  }
  button(label, action) {
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'primary-button';
    button.textContent = label;
    button.onclick = action;
    return button;
  }
  openInfo() {
    this.host.send({ type: 'cancel-read' });
    this.infoOpen = true;
    this.decision = null;
    this.choiceKey = null;
  }
  closeInfo() {
    this.host.send({ type: 'cancel-read' });
    this.infoOpen = false;
    this.choiceKey = null;
    this.arena.view.modalOpen = false;
    document.getElementById('arena-dialog').close();
    if (!this.view) this.arena.view.render();
    const current = this.snapshot?.home?.current;
    if (this.view && current && !this.replaying)
      this.choice(
        current.decisionID ?? `${current.id}:${current.phaseSeq}`,
        this.view.choice,
        this.view.legal_actions,
      );
  }
  inspect(cards, title) {
    this.openInfo();
    const dialog = document.getElementById('arena-dialog');
    document.getElementById('dialog-title').textContent = title;
    document.getElementById('dialog-description').textContent = cards.length
      ? '查看卡牌效果与触发时机。'
      : '这里尚无卡牌。';
    document.getElementById('dialog-cards').replaceChildren(
      ...cards.map((card) => {
        const section = document.createElement('div'),
          description = document.createElement('p');
        section.className = 'dialog-card';
        description.textContent = cardDescription(this.enrich(card));
        section.append(cardElement(this.enrich(card)), description);
        return section;
      }),
    );
    document
      .getElementById('dialog-actions')
      .replaceChildren(this.button('关闭', () => this.closeInfo()));
    if (!dialog.open) dialog.showModal();
    document.getElementById('decision-dialog-timer').hidden = true;
  }
  confirmSurrender() {
    if (this.blocked) return;
    const expected = this.snapshot.home?.current;
    this.openInfo();
    const dialog = document.getElementById('arena-dialog');
    document.getElementById('dialog-title').textContent = '认输并结束对局？';
    document.getElementById('dialog-description').textContent = '本场按对手获胜结算。';
    document.getElementById('dialog-cards').replaceChildren();
    document.getElementById('dialog-actions').replaceChildren(
      this.button('继续对局', () => this.closeInfo()),
      this.button('认输', () => {
        this.closeInfo();
        if (this.blocked || !this.sameWindow(expected)) return;
        const current = expected;
        this.host.send({ type: 'surrender', id: current?.id, phaseSeq: current?.phaseSeq });
      }),
    );
    if (!dialog.open) dialog.showModal();
  }
  showResult(result) {
    this.infoOpen = false;
    document.getElementById('arena-dialog').close();
    this.decision = null;
    const proof = document.getElementById('result-proof');
    proof.hidden = false;
    proof.onclick = () => this.host.send({ type: 'proof', id: result.id, terminal: true });
    this.view = result.view;
    this.resultActive = true;
    this.replaying = false;
    document.getElementById('result-title').textContent = {
      win: '推演成功',
      loss: '算力不敌',
      draw: '势均力敌',
      system_cancelled: '对局已取消',
    }[result.outcome];
    document.getElementById('result-subtitle').textContent =
      `本场奖金 ${result.prize} 积分 · 退票 ${result.refund.balance} + ${result.refund.gameBalance} 积分${result.ai ? (result.ai.first_clear ? ` · 首次胜利奖励 ${result.ai.reward} 积分` : ' · 没有逐局奖励') : ''}`;
    document.getElementById('result-history').replaceChildren(
      ...(result.view?.rounds ?? []).map((r) => {
        const section = document.createElement('section');
        section.className = 'result-round';
        const heading = document.createElement('strong');
        heading.textContent = `第 ${r.round} 局 · ${r.scores[result.you]} : ${r.scores[1 - result.you]}`;
        section.append(heading);
        if (r.rows)
          for (const row of r.rows) {
            const detail = document.createElement('p');
            detail.textContent = `${rowNames[row.row]} ${row.scores[result.you]} : ${row.scores[1 - result.you]}`;
            section.append(detail);
          }
        if (r.lives_before && r.lives_after) {
          const lives = document.createElement('p');
          lives.textContent = `生命 你 ${r.lives_before[result.you]} → ${r.lives_after[result.you]} · 对手 ${r.lives_before[1 - result.you]} → ${r.lives_after[1 - result.you]}`;
          section.append(lives);
        }
        return section;
      }),
    );
    document.getElementById('result-overlay').hidden = false;
    document.querySelector('.shell').inert = true;
    document.getElementById('rematch').textContent = this.snapshot?.canRematch
      ? '再战一局'
      : '返回大厅选择新局';
    document.getElementById('rematch').focus();
    tone('win');
  }
  lobby() {
    this.stopReplay();
    this.replaying = false;
    this.view = null;
    this.infoOpen = false;
    this.resultActive = false;
    this.cache.clear();
    this.selected = null;
    this.decision = null;
    this.choiceKey = null;
    document.getElementById('arena-dialog').close();
    document.getElementById('result-overlay').hidden = true;
    document.getElementById('battle').hidden = true;
    document.getElementById('lobby').hidden = false;
    document.querySelector('.shell').inert = false;
    this.host.returnLobby();
  }
  history(page) {
    this.openInfo();
    const dialog = document.getElementById('arena-dialog');
    this.decision = null;
    document.getElementById('dialog-title').textContent = '对局记录';
    document.getElementById('dialog-description').textContent = '查看对局结果及当时可见的回放。';
    document.getElementById('decision-dialog-timer').hidden = true;
    const list = document.createElement('div');
    list.className = 'platform-ledger';
    for (const result of page.items) {
      const row = document.createElement('section');
      row.className = 'platform-history-row';
      const text = document.createElement('span');
      text.textContent = `${{ win: '胜利', loss: '失败', draw: '平局', system_cancelled: '系统取消' }[result.outcome]} · ${result.scores[result.you]} : ${result.scores[1 - result.you]} · ${new Date(result.terminalAt * 1000).toLocaleString()}`;
      row.append(
        text,
        this.button('回放', () => this.host.send({ type: 'replay', id: result.id })),
        this.button('随机验证', () =>
          this.host.send({ type: 'proof', id: result.id, terminal: true }),
        ),
      );
      list.append(row);
    }
    if (!page.items.length) list.textContent = '暂无对局记录。';
    document.getElementById('dialog-cards').replaceChildren(list);
    document.getElementById('dialog-actions').replaceChildren(
      ...(this.snapshot.home?.current
        ? [
            this.button('验证当前对局随机性', () =>
              this.host.send({ type: 'proof', id: this.snapshot.home.current.id, terminal: false }),
            ),
          ]
        : []),
      ...(page.nextCursor
        ? [
            this.button('下一页', () =>
              this.host.send({ type: 'history', cursor: page.nextCursor }),
            ),
          ]
        : []),
      this.button('关闭', () => this.closeInfo()),
    );
    if (!dialog.open) dialog.showModal();
  }
  replay(replay) {
    this.stopReplay();
    this.replaying = true;
    this.infoOpen = false;
    document.getElementById('arena-dialog').close();
    const steps = [replay.initial, ...replay.rounds.flatMap((r) => [r.before, r.after])];
    this.replayIndex = 0;
    const controls = document.createElement('div');
    controls.className = 'platform-replay-controls';
    const seek = document.createElement('input');
    seek.type = 'range';
    seek.min = '0';
    seek.max = String(steps.length - 1);
    seek.value = '0';
    seek.setAttribute('aria-label', '回放进度');
    const show = () => {
      seek.value = String(this.replayIndex);
      const snapshot = {
        ...this.snapshot,
        blocked: true,
        home: { current: null, latestResult: replay.result },
      };
      this.render(snapshot, { ...steps[this.replayIndex], legal_actions: [], choice: undefined });
      document.getElementById('battle-log').replaceChildren(
        ...replay.rounds.slice(0, Math.ceil(this.replayIndex / 2)).flatMap((r) =>
          (r.facts.actions ?? []).map((step) => {
            const li = document.createElement('li');
            li.textContent = `${step.seat === replay.result.you ? '你' : '对手'} · ${{ play: '出牌', leader: '领袖', pass: '放弃', swap: '换牌', choose: '选择', continue: '继续' }[step.action.kind] ?? step.action.kind}${step.automatic ? ' · 超时处理' : ''}`;
            return li;
          }),
        ),
      );
    };
    seek.oninput = () => {
      this.replayIndex = Number(seek.value);
      show();
    };
    const play = this.button('自动播放', () => {
      if (this.replayTimer) {
        this.stopReplayTimer();
        play.textContent = '自动播放';
        return;
      }
      play.textContent = '暂停回放';
      this.replayTimer = setInterval(
        () => {
          if (this.replayIndex >= steps.length - 1) {
            this.stopReplayTimer();
            play.textContent = '自动播放';
            return;
          }
          this.replayIndex++;
          show();
        },
        Math.max(400, 2500 * this.arena.speed),
      );
    });
    controls.append(
      this.button('上一步', () => {
        this.replayIndex = Math.max(0, this.replayIndex - 1);
        show();
      }),
      play,
      seek,
      this.button('下一步', () => {
        this.replayIndex = Math.min(steps.length - 1, this.replayIndex + 1);
        show();
      }),
      this.button('返回大厅', () => this.lobby()),
    );
    document.querySelector('.hand-header').before(controls);
    this.replayControls = controls;
    show();
  }
  stopReplayTimer() {
    if (this.replayTimer) clearInterval(this.replayTimer);
    this.replayTimer = null;
  }
  stopReplay() {
    this.stopReplayTimer();
    this.replayControls?.remove();
    this.replayControls = null;
  }
}
