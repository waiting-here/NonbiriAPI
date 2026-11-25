/* eslint-disable */
import { ArenaView } from './ui/battlefield.js';
import { DeckStore } from './cards/decks.js';
import { installDecisionTimer } from './ui/decision-timer.js';
import { loadPreferences } from './ui/settings.js';
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
    preferences = loadPreferences(localStorage),
    arena = {
      catalog,
      decks: new DeckStore(catalog, storage),
      preferences,
      speed: preferences.speed,
      label: (player) => models[player.deck.faction].short,
    };
  applyMotion(preferences.motion);
  installDecisionTimer();
  arena.view = new ArenaView(arena);
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
  document
    .getElementById('arena-dialog')
    .addEventListener('cancel', (event) => event.preventDefault());
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
  document.getElementById('view-rules').onclick = () => showRulesGuide(arena);
  document.getElementById('loading-label').hidden = true;
  document.getElementById('launch-play').disabled = false;
  const setup = new PlatformSetup(arena, deckEditor, host);
  return { arena, setup, catalog };
}
