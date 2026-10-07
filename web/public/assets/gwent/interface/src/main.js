/* eslint-disable */
import { ArenaEngine } from './game/engine.js';
import { models } from './ai/models.js';
import { cardElement } from './ui/card.js';
import { tone } from './ui/animation.js';
import { DeckEditor } from './ui/deck-editor.js';
import { CardCatalog } from './ui/catalog.js';
import { applyMotion, savePreference } from './ui/settings.js';
import { setFactionMark } from './ui/faction-mark.js';
import { PlatformSetup } from '../setup.js';
import { showRulesGuide } from './ui/rules-guide.js';

export async function boot(storage, host) {
  const response = await fetch('src/cards/cards.json');
  if (!response.ok) throw Error('卡牌数据加载失败');
  const catalog = await response.json(),
    arena = new ArenaEngine(catalog, storage);
  const playerSelect = document.getElementById('player-faction'),
    opponentSelect = document.getElementById('opponent-faction');
  for (const select of [playerSelect, opponentSelect])
    for (const [id, model] of Object.entries(models)) {
      const option = document.createElement('option');
      option.value = id;
      option.textContent = model.short;
      select.append(option);
    }
  playerSelect.value =
    localStorage.getItem('arena-faction') in models
      ? localStorage.getItem('arena-faction')
      : 'openai';
  opponentSelect.value = 'deepseek';
  const deckEditor = new DeckEditor(arena, playerSelect);
  new CardCatalog(arena);
  function updateFaction() {
    const selected = playerSelect.value,
      model = models[selected];
    localStorage.setItem('arena-faction', selected);
    document
      .querySelectorAll('.faction-tile')
      .forEach((tile) => tile.classList.toggle('chosen', tile.dataset.faction === selected));
    document.documentElement.style.setProperty('--selected-color', model.color);
    document.documentElement.dataset.faction = selected;
    const hero = catalog.find((card) => card.id === model.heroId);
    document
      .querySelector('.spotlight-art')
      .style.setProperty(
        '--spotlight-image',
        `url("${new URL(hero.thumbnail || hero.image, document.baseURI).href}")`,
      );
    document.getElementById('selected-motto').textContent = model.motto;
    document.getElementById('selected-description').textContent = model.description;
    document
      .getElementById('hero-preview')
      .replaceChildren(cardElement(catalog.find((card) => card.id === model.heroId)));
    document.getElementById('selected-model').textContent = `HERO UNIT · ${model.hero}`;
    deckEditor.updateSummary();
  }
  for (const [id, model] of Object.entries(models)) {
    const button = document.createElement('button');
    button.className = 'faction-tile';
    button.dataset.faction = id;
    button.style.setProperty('--faction-color', model.color);
    const hero = catalog.find((card) => card.id === model.heroId);
    if (hero) {
      const art = document.createElement('img');
      art.className = 'tile-art';
      art.src = hero.thumbnail || hero.image;
      art.alt = '';
      art.loading = 'lazy';
      art.onerror = () => {
        art.onerror = null;
        art.src = hero.image;
      };
      button.append(art);
    }
    for (const [className, text] of [
      ['tile-symbol', model.symbol],
      ['tile-name', model.short],
      ['tile-trait', model.trait],
      ['tile-hero', model.hero],
    ]) {
      const span = document.createElement('span');
      span.className = className;
      span.textContent = text;
      if (className === 'tile-symbol') setFactionMark(span, id);
      button.append(span);
    }
    button.onclick = () => {
      playerSelect.value = id;
      updateFaction();
      tone();
    };
    document.getElementById('faction-tiles').append(button);
  }
  playerSelect.onchange = updateFaction;
  opponentSelect.onchange = () => {
    document.getElementById('opponent-deck').value = 'random-preset';
    deckEditor.updateSummary();
  };
  updateFaction();
  document.getElementById('launch-play').onclick = () => arena.start('play');
  document.getElementById('launch-watch').onclick = () => arena.start('watch');
  document.getElementById('arena-pass').onclick = () => arena.command('pass');
  document.getElementById('arena-leader').onclick = () => arena.command('leader');
  document.getElementById('arena-concede').onclick = () => arena.command('concede');
  document.getElementById('inspect-hand').onclick = () =>
    arena.view.chooseCards(
      player_me.hand,
      0,
      () => {},
      () => true,
      true,
      '手牌详情 · 查看效果与触发时机',
    );
  document.querySelectorAll('.arena-row').forEach((element) =>
    element.addEventListener('click', (event) => {
      if (event.target.closest('button') || !arena.view.selection) return;
      const card = arena.view.selection,
        row = board.getRow(
          { abilities: [] },
          element.dataset.row,
          element.dataset.side === 'me' ? player_me : player_op,
        );
      arena.act(card, row);
    }),
  );
  document
    .getElementById('arena-dialog')
    .addEventListener('cancel', (event) => event.preventDefault());
  document.getElementById('rematch').onclick = () => arena.start(arena.mode);
  document.getElementById('return-lobby').onclick = () => arena.lobby();
  const speed = document.getElementById('arena-speed');
  speed.value = String(arena.speed);
  speed.onchange = (event) => {
    arena.speed = Number(event.target.value);
    savePreference('speed', arena.speed);
  };
  const motion = document.getElementById('arena-motion');
  motion.value = arena.preferences.motion;
  motion.onchange = (event) => {
    applyMotion(event.target.value);
    savePreference('motion', event.target.value);
  };
  matchMedia('(prefers-reduced-motion: reduce)').addEventListener('change', (event) => {
    if (event.matches) document.getAnimations().forEach((animation) => animation.cancel());
  });
  const sound = document.getElementById('arena-sound');
  const updateSound = () => {
    sound.textContent = localStorage.getItem('arena-sound') === 'on' ? '声音 开' : '声音 关';
  };
  sound.onclick = () => {
    localStorage.setItem(
      'arena-sound',
      localStorage.getItem('arena-sound') === 'on' ? 'off' : 'on',
    );
    updateSound();
    tone();
  };
  updateSound();
  document.getElementById('view-cards').onclick = () =>
    arena.view.chooseCards(
      {
        cards: catalog
          .filter((card) => [playerSelect.value, 'neutral'].includes(card.faction))
          .map((data) => new Card(card_dict[arena.index.get(data.id)], player_me)),
      },
      0,
      () => {},
      () => true,
      true,
      '卡牌图鉴 · ' + models[playerSelect.value].short,
    );
  document.getElementById('view-rules').onclick = () => showRulesGuide(arena);
  document.getElementById('loading-label').hidden = true;
  document.getElementById('launch-play').disabled = false;
  document.getElementById('launch-watch').disabled = false;
  const setup = new PlatformSetup(arena, deckEditor, host);
  return { arena, setup, catalog };
}
