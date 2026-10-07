import { deckStorage } from './storage.js';
/* global window, document, localStorage, ResizeObserver, game, GameState */
import { boot } from './src/main.js';
import { suspendSound, closeSound } from './src/ui/animation.js';
import { PlatformBattle } from './battle.js';

const channel = 'nonbiri.gwent',
  origin = window.location.origin;
let runtime = null,
  latest = null,
  booting = false,
  platformActive = false,
  paused = false,
  lastCurrent = null,
  lastResult = null;
const send = (message) => window.parent.postMessage({ channel, ...message }, origin);
const pending = [];
const host = {
  send,
  inspectDeck(deck) {
    if (!deck) return;
    const cards = [deck.leader, ...deck.cards.flatMap((entry) => Array(entry.count).fill(entry.id))]
      .map((id) => runtime.catalog.find((card) => card.id === id) ?? runtime.battle.catalog.get(id))
      .filter(Boolean);
    runtime.battle.inspect(cards, '挑战冻结卡组 · 整场沿用');
  },
  startDemo() {
    platformActive = false;
    runtime.battle.stopReplay();
    runtime.battle.replaying = false;
    runtime.battle.view = null;
    runtime.battle.resultActive = false;
    document.getElementById('result-proof').hidden = true;
    runtime.battle.cache.clear();
  },
  returnLobby() {
    platformActive = false;
    runtime.setup.update(latest);
    if (latest?.home?.current) apply(latest);
  },
};
function resize() {
  const height = Math.ceil(document.body.getBoundingClientRect().height);
  send({ type: 'height', height });
}
function viewport(value) {
  document.documentElement.style.setProperty(
    '--frame-viewport-height',
    `${value.screenHeight ?? value.height}px`,
  );
  document.documentElement.style.setProperty('--visible-top', `${value.top}px`);
  document.documentElement.style.setProperty('--visible-height', `${value.height}px`);
}
async function initialize(snapshot) {
  if (booting || runtime) return;
  booting = true;
  try {
    const result = await boot(deckStorage(snapshot.accountID, localStorage), host);
    const battle = new PlatformBattle(result.arena, result.catalog, host);
    runtime = { ...result, battle };
    const arena = result.arena,
      render = arena.view.render.bind(arena.view),
      label = arena.label.bind(arena),
      delay = arena.delay.bind(arena),
      fail = arena.fail.bind(arena);
    arena.view.render = () =>
      platformActive && latest?.home?.current ? battle.render(latest) : render();
    arena.label = (player) =>
      player.platformSide
        ? `${player.deck.faction}（${player.platformSide === 'self' ? '己方' : '对手'}）`
        : label(player);
    arena.delay = async (ms) => {
      while (paused && !platformActive) await new Promise((resolve) => pending.push(resolve));
      if (platformActive) throw new Error('Local demonstration stopped');
      await delay(ms);
    };
    arena.fail = (error) => {
      if (!platformActive) fail(error);
    };
    document.getElementById('platform-history').onclick = () => send({ type: 'history' });
    document.getElementById('platform-rankings').onclick = () => send({ type: 'rankings' });
    document.getElementById('platform-back').onclick = (event) => {
      if (window.parent !== window) {
        event.preventDefault();
        send({ type: 'back' });
      }
    };
    const originalCards = document.getElementById('view-cards').onclick;
    document.getElementById('view-cards').onclick = () =>
      platformActive
        ? battle.inspect(
            result.catalog.filter((card) =>
              [battle.view.self.faction, 'neutral'].includes(card.faction),
            ),
            '卡牌图鉴',
          )
        : originalCards();
    const refresh = document.createElement('button');
    refresh.type = 'button';
    refresh.className = 'ghost-button';
    refresh.textContent = '刷新／重试';
    refresh.onclick = () => send({ type: 'retry' });
    result.setup.panel.append(refresh);
    const observer = new ResizeObserver(resize);
    observer.observe(document.body);
    runtime.observer = observer;
    apply(latest ?? snapshot);
    send({ type: 'mounted' });
    resize();
  } catch {
    document.getElementById('loading-label').hidden = false;
    document.getElementById('loading-label').textContent = '卡牌界面未能加载，请刷新重试。';
    send({ type: 'failed' });
    resize();
  } finally {
    booting = false;
  }
}
function apply(snapshot) {
  latest = snapshot;
  if (snapshot.viewport) viewport(snapshot.viewport);
  if (!runtime) {
    void initialize(snapshot);
    return;
  }
  runtime.setup.update(snapshot);
  const current = snapshot.home?.current,
    result = snapshot.home?.latestResult;
  if (current) {
    lastCurrent = current.id;
    if (runtime.battle.replaying) return;
    if (!platformActive) {
      runtime.arena.clock.stop();
      runtime.arena.matchAbort.abort();
      runtime.arena.finished = true;
      game.state = GameState.END_SCREEN;
      platformActive = true;
    }
    runtime.battle.replaying = false;
    runtime.battle.stopReplay();
    runtime.battle.render(snapshot);
    lastCurrent = current.id;
  } else if (
    result &&
    !snapshot.home?.queue &&
    (lastCurrent === result.id || lastResult === null) &&
    lastResult !== result.id
  ) {
    runtime.battle.snapshot = snapshot;
    runtime.battle.showResult(result);
    lastResult = result.id;
    lastCurrent = null;
  }
}
window.addEventListener('message', (event) => {
  if (event.origin !== origin || event.source !== window.parent || event.data?.channel !== channel)
    return;
  const message = event.data;
  if (message.type === 'snapshot') apply(message.snapshot);
  else if (message.type === 'viewport') viewport(message.viewport);
  else if (message.type === 'visibility') {
    paused = !message.visible;
    if (!paused) while (pending.length) pending.shift()();
    if (paused) {
      runtime?.battle.stopReplayTimer();
      suspendSound();
    }
  } else if (message.type === 'history') runtime?.battle.history(message.page);
  else if (message.type === 'replay') runtime?.battle.replay(message.replay);
  else if (message.type === 'read-error') {
    document.getElementById('loading-label').hidden = false;
    document.getElementById('loading-label').textContent = message.message;
  }
});
window.addEventListener('pagehide', () => {
  closeSound();
  paused = false;
  while (pending.length) pending.shift()();
  runtime?.observer.disconnect();
  runtime?.arena.clock.stop();
  runtime?.arena.matchAbort.abort();
  runtime?.battle.stopReplay();
  document.getAnimations().forEach((animation) => animation.cancel());
});
window.addEventListener('resize', resize);
send({ type: 'ready' });
if (window.parent === window) {
  void initialize({
    accountID: null,
    config: { enabled: false, modes: {} },
    home: null,
    ai: null,
    blocked: true,
    accepting: false,
    availableCredits: '0',
    viewport: { top: 0, height: window.innerHeight },
  });
}
