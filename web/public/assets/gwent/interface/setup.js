/* global document */
import { validateDeck } from './src/game/rules.js';

export class PlatformSetup {
  constructor(arena, editor, host) {
    this.arena = arena;
    this.editor = editor;
    this.host = host;
    this.panel = document.createElement('div');
    this.panel.className = 'match-panel';
    this.status = document.createElement('span');
    this.status.setAttribute('role', 'status');
    this.cancel = document.createElement('button');
    this.cancel.type = 'button';
    this.cancel.className = 'ghost-button';
    this.cancel.textContent = '取消匹配';
    this.cancel.onclick = () => host.send({ type: 'cancel' });
    this.panel.append(this.status, this.cancel);
    document.querySelector('.setup').before(this.panel);
    document.querySelector('.setup-op').hidden = true;
    document.querySelector('.setup-vs').hidden = true;
    document.querySelector('.setup').dataset.matchMode = 'pvp';
    document.querySelector('.setup-actions').append(document.getElementById('view-all-cards'));
    document.getElementById('launch-play').onclick = () => this.launch();
    document.getElementById('companion-entry').onclick = () =>
      host.send({ type: 'companion-entry' });
    document.addEventListener('arena-deck-change', () => this.render());
  }
  update(snapshot) {
    this.snapshot = snapshot;
    this.render();
  }
  render() {
    const snapshot = this.snapshot;
    if (!snapshot) return;
    const queue = snapshot.home?.queue;
    const busy = snapshot.blocked || !!queue || !!snapshot.home?.current;
    for (const control of document.querySelectorAll(
      '#player-faction, #player-deck, #edit-deck, .faction-tile',
    ))
      control.disabled = busy;
    const mode = snapshot.config?.modes.standard;
    const companion = snapshot.companion;
    this.cancel.hidden = !queue;
    this.cancel.disabled = snapshot.blocked;
    this.panel.hidden = !queue && !snapshot.error && !snapshot.uncertain;
    this.status.textContent =
      snapshot.error ||
      (queue
        ? `正在匹配 · ${snapshot.remaining ?? '—'}s`
        : snapshot.uncertain
          ? '结果尚未确认，请重试同一操作。'
          : '');
    const play = document.getElementById('launch-play');
    let invalid = false;
    try {
      validateDeck(
        this.arena.decks.profile(document.getElementById('player-faction').value).deck,
        this.arena.catalog,
      );
    } catch (e) {
      invalid = true;
      document.getElementById('deck-summary').textContent = e.message;
    }
    play.disabled =
      busy ||
      invalid ||
      (!companion &&
        !(snapshot.accepting && snapshot.config?.available && mode?.enabled && mode.available));
    play.textContent = companion ? '使用这副牌开始陪玩 ↗' : '开始匹配 ↗';
    document.getElementById('platform-terms').textContent = companion
      ? '免费陪玩 · 无积分结算'
      : mode
        ? `门票 ${mode.ticket} 积分 · 双方门票组成奖池 · 平台 ${mode.rates.platform / 100}% / 公益 ${mode.rates.welfare / 100}% / 星期四 ${mode.rates.thursday / 100}%。可用 ${snapshot.availableCredits} 积分。`
        : '玩家对战暂未开放';
    document.querySelector('.rule-offline').textContent = companion
      ? 'AI 陪玩 · 服务器保存对局'
      : '玩家对战 · 平台匹配';
    const entry = document.getElementById('companion-entry');
    entry.hidden = !!companion || !!queue || !!snapshot.home?.current;
    entry.disabled = false;
    for (const id of ['platform-history', 'platform-rankings'])
      document.getElementById(id).hidden = !!companion;
    document.getElementById('loading-label').hidden = !snapshot.error;
    if (snapshot.error) document.getElementById('loading-label').textContent = snapshot.error;
  }
  launch() {
    const deck = this.arena.decks.profile(document.getElementById('player-faction').value).deck;
    if (this.snapshot.companion) this.host.send({ type: 'companion-start', deck });
    else
      this.host.send({
        type: 'queue',
        mode: 'standard',
        termsHash: this.snapshot.config.modes.standard.termsHash,
        deck,
      });
  }
}
