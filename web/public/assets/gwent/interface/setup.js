/* global document, Option */
import { models } from './src/ai/models.js';
import { validateDeck } from './src/game/rules.js';
import { setTooltip } from './src/ui/tooltip.js';

export class PlatformSetup {
  constructor(arena, editor, host) {
    this.arena = arena;
    this.editor = editor;
    this.host = host;
    this.mode = 'ai';
    this.offerID = null;
    this.snapshot = null;
    this.bar = document.createElement('div');
    this.bar.className = 'match-modes';
    this.bar.setAttribute('role', 'group');
    this.bar.setAttribute('aria-label', '对战模式');
    for (const [id, label] of [
      ['ai', '人机挑战'],
      ['demo', 'AI 观战'],
      ['standard', '玩家对战'],
    ]) {
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'match-mode';
      button.dataset.mode = id;
      button.textContent = label;
      button.onclick = () => {
        this.mode = id;
        this.editor.updateSummary();
      };
      this.bar.append(button);
    }
    document.querySelector('.setup').before(this.bar);
    this.panel = document.createElement('div');
    this.panel.className = 'match-panel';
    this.status = document.createElement('span');
    this.status.id = 'match-connection';
    this.status.setAttribute('role', 'status');
    this.cancel = document.createElement('button');
    this.cancel.type = 'button';
    this.cancel.className = 'ghost-button';
    this.cancel.textContent = '取消匹配';
    this.cancel.onclick = () => host.send({ type: 'cancel' });
    this.panel.append(this.status, this.cancel);
    this.bar.after(this.panel);
    const help = document.getElementById('opponent-deck-help');
    const explain = help.onclick;
    help.onclick = () =>
      this.mode === 'ai' ? this.host.inspectDeck?.(this.offer()?.terms.ai.bot_loadout) : explain();
    const update = editor.updateSummary.bind(editor);
    editor.updateSummary = () => {
      const faction = document.getElementById('opponent-faction'),
        deck = document.getElementById('opponent-deck');
      if (!models[faction.value]) {
        faction.replaceChildren(
          ...Object.entries(models).map(([id, m]) => new Option(m.short, id)),
        );
        faction.value = this.offer()?.terms.ai.bot_loadout?.faction ?? 'deepseek';
        deck.value = 'random-preset';
      }
      update();
      this.render();
    };
    document.getElementById('opponent-faction').onchange = () => {
      if (this.mode === 'ai') {
        this.offerID = document.getElementById('opponent-faction').value;
        this.render();
      } else editor.updateSummary();
    };
    document.getElementById('opponent-deck').onchange = () => editor.updateSummary();
    document.getElementById('launch-play').onclick = () => this.launch();
    document.getElementById('launch-watch').onclick = () => this.watch();
    document.addEventListener('arena-deck-change', () => this.render());
  }
  update(snapshot) {
    this.snapshot = snapshot;
    if (snapshot.home?.current?.mode) this.mode = snapshot.home.current.mode;
    if (snapshot.home?.queue?.mode) this.mode = snapshot.home.queue.mode;
    this.editor.updateSummary();
  }
  offer() {
    return this.snapshot?.ai?.bots.find((offer) => offer.terms.ai.bot_id === this.offerID);
  }
  render() {
    const snapshot = this.snapshot;
    if (!snapshot) return;
    const current = snapshot.home?.current,
      queue = snapshot.home?.queue,
      blocked = snapshot.blocked;
    for (const button of this.bar.children) {
      button.setAttribute('aria-pressed', String(button.dataset.mode === this.mode));
      button.disabled = !!current || !!queue;
    }
    for (const id of ['player-faction', 'player-deck', 'edit-deck'])
      document.getElementById(id).disabled = !!current || !!queue || blocked;
    for (const tile of document.querySelectorAll('.faction-tile'))
      tile.disabled = !!current || !!queue || blocked;
    const demo = this.mode === 'demo',
      pvp = this.mode === 'standard';
    const opponent = document.querySelector('.setup-op');
    opponent.hidden = pvp;
    document.querySelector('.setup-vs').hidden = pvp;
    document.querySelector('.setup').dataset.matchMode = pvp ? 'pvp' : 'local';
    opponent.querySelector('.setup-role').textContent = demo
      ? '对手 · LOCAL AI'
      : '对手 · 平台挑战';
    document.getElementById('opponent-deck-help').hidden = pvp;
    document.getElementById('opponent-deck-help').textContent = demo ? 'ⓘ 卡组说明' : 'ⓘ 冻结卡组';
    document.getElementById('opponent-deck-reroll').hidden = !demo;
    const faction = document.getElementById('opponent-faction'),
      deck = document.getElementById('opponent-deck');
    if (demo) {
      if (!models[faction.value]) {
        faction.replaceChildren(
          ...Object.entries(models).map(([id, m]) => new Option(m.short, id)),
        );
        faction.value = 'deepseek';
        this.editor.updateSummary();
        return;
      }
      faction.disabled = false;
      deck.disabled = false;
    } else if (!pvp) {
      const offers = snapshot.ai?.bots ?? [];
      if (!offers.some((offer) => offer.terms.ai.bot_id === this.offerID))
        this.offerID = offers[0]?.terms.ai.bot_id ?? null;
      faction.replaceChildren(
        ...offers.map((offer) => new Option(offer.terms.ai.bot_name, offer.terms.ai.bot_id)),
      );
      if (!offers.length) faction.append(new Option('暂无开放挑战', ''));
      faction.value = this.offerID ?? '';
      faction.disabled = !!current || !!queue || blocked;
      const offer = this.offer(),
        loadout = offer?.terms.ai.bot_loadout;
      deck.replaceChildren(
        new Option(
          loadout
            ? `${models[loadout.faction]?.short ?? loadout.faction} · 固定挑战卡组`
            : '等待挑战开放',
          'frozen',
        ),
      );
      deck.disabled = true;
      document.getElementById('opponent-deck-info').hidden = false;
      setTooltip(
        document.getElementById('opponent-deck-help'),
        offer
          ? `${offer.terms.ai.description}
点击查看本场冻结的完整卡组。`
          : '暂无开放挑战',
      );
      faction.closest('label').firstChild.textContent = '挑战';
      setTooltip(
        deck,
        loadout
          ? `${offer.terms.ai.description}
${loadout.cards.reduce((n, c) => n + c.count, 0)} 张卡牌 · ${loadout.leader}`
          : '管理员开放后可选择挑战',
      );
    }
    if (demo) {
      faction.closest('label').firstChild.textContent = '阵营';
      document.getElementById('opponent-deck-info').hidden = false;
    }
    this.cancel.hidden = !queue;
    this.cancel.disabled = blocked;
    this.panel.hidden = demo;
    this.status.textContent =
      snapshot.error ||
      (queue
        ? `正在匹配 · ${snapshot.remaining ?? '—'}s`
        : snapshot.uncertain
          ? '结果尚未确认，请重试同一操作。'
          : pvp
            ? '玩家对战 · 平台匹配'
            : snapshot.ai?.enabled
              ? '人机挑战 · 服务端对局'
              : '人机挑战暂未开放');
    const play = document.getElementById('launch-play'),
      watch = document.getElementById('launch-watch');
    let deckError = '';
    try {
      validateDeck(
        this.arena.decks.profile(document.getElementById('player-faction').value).deck,
        this.arena.catalog,
      );
    } catch (error) {
      deckError = error.message;
    }
    const offer = this.offer(),
      enabled = pvp ? snapshot.config?.modes.standard?.enabled : snapshot.ai?.enabled && !!offer;
    play.disabled =
      demo ||
      blocked ||
      !!queue ||
      !!current ||
      !!deckError ||
      !snapshot.accepting ||
      !snapshot.config?.enabled ||
      !snapshot.config?.available ||
      (pvp && !snapshot.config?.modes.standard?.available) ||
      !enabled;
    play.hidden = demo;
    play.textContent = pvp ? '开始匹配 ↗' : '进入竞技场 ↗';
    watch.hidden = pvp;
    watch.disabled = !!queue || !!current || !!deckError;
    watch.textContent = demo ? 'AI vs AI · 观战模式' : 'AI vs AI · 配置观战';
    this.termsHash = pvp ? snapshot.config?.modes.standard?.termsHash : offer?.terms_hash;
    const terms = document.getElementById('platform-terms');
    if (demo) terms.textContent = '免费本机演示，不计账号成绩或奖励。';
    else if (pvp) {
      const mode = snapshot.config?.modes.standard;
      terms.textContent = mode
        ? `门票 ${mode.ticket} 积分 · 双方门票组成奖池 · 平台 ${mode.rates.platform / 100}% / 公益 ${mode.rates.welfare / 100}% / 星期四 ${mode.rates.thursday / 100}%。可用 ${snapshot.availableCredits} 积分。`
        : '玩家对战暂未开放';
    } else
      terms.textContent = offer
        ? `门票 ${offer.terms.ticket} 积分 · 没有逐局积分奖励。${offer.completed ? '此挑战已首通，本次不再领奖。' : `仅首次胜利奖励 ${offer.terms.ai.first_reward} 积分。`}可用 ${snapshot.availableCredits} 积分。`
        : '没有逐局积分奖励；开放挑战后可查看门票和首次胜利奖励。';
    document.querySelector('.rule-offline').textContent = demo
      ? '本机演示 · 不计成绩和奖励'
      : pvp
        ? '玩家对战 · 平台匹配'
        : '人机挑战 · 平台保存对局';
    document.getElementById('loading-label').hidden = !snapshot.error;
    if (snapshot.error) document.getElementById('loading-label').textContent = snapshot.error;
  }
  launch() {
    const deck = this.arena.decks.profile(document.getElementById('player-faction').value).deck;
    if (this.mode === 'standard')
      this.host.send({ type: 'queue', mode: 'standard', termsHash: this.termsHash, deck });
    else if (this.mode === 'ai') {
      const offer = this.offer();
      if (offer)
        this.host.send({
          type: 'queue',
          mode: 'ai',
          botID: offer.terms.ai.bot_id,
          termsHash: this.termsHash,
          deck,
        });
    }
  }
  async watch() {
    if (this.mode !== 'demo') {
      this.mode = 'demo';
      this.editor.updateSummary();
      return;
    }
    try {
      this.host.startDemo();
      await this.arena.start('watch', 'local');
    } catch (error) {
      document.getElementById('deck-summary').textContent = error.message;
    }
  }
}
