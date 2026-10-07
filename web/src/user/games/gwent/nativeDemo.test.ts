import { waitFor } from '@testing-library/react';
import { runInThisContext } from 'node:vm';
import { expect, it, vi } from 'vitest';
import frameHTML from '../../../../public/assets/gwent/interface/index.html?raw';
import globals from '../../../../public/assets/gwent/interface/globals.js?raw';
import common from '../../../../public/assets/gwent/interface/common.js?raw';
import abilities from '../../../../public/assets/gwent/interface/abilities.js?raw';
import runtime from '../../../../public/assets/gwent/interface/runtime.js?raw';
import cards from '../../../../public/assets/gwent/interface/src/cards/cards.json';
import { installNativeDialog } from '../../../../test/unit/nativeDialog';
const enginePath = '../../../../public/assets/gwent/interface/src/game/engine.js';
const storagePath = '../../../../public/assets/gwent/interface/storage.js';
const { deckStorage } = await import(storagePath);
installNativeDialog();
it('runs the actual local demonstration through multiple rounds to results without a global arena', async () => {
  document.body.innerHTML = new DOMParser().parseFromString(frameHTML, 'text/html').body.innerHTML;
  document.documentElement.dataset.motion = 'reduced';
  vi.stubGlobal('matchMedia', () => ({
    matches: true,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  }));
  document.getAnimations = () => [];
  localStorage.setItem('arena-motion', 'reduced');
  vi.stubGlobal('getComputedStyle', window.getComputedStyle.bind(window));
  for (const id of ['player-faction', 'opponent-faction']) {
    const select = document.getElementById(id) as HTMLSelectElement;
    for (const faction of ['openai', 'deepseek', 'claude', 'gemini'])
      select.append(new Option(faction, faction));
    select.value = id === 'player-faction' ? 'openai' : 'deepseek';
  }
  const opponent = document.getElementById('opponent-deck') as HTMLSelectElement;
  opponent.append(new Option('标准', 'standard-balanced'));
  opponent.value = 'standard-balanced';
  for (const code of [globals, common, abilities, runtime]) runInThisContext(code);
  const { ArenaEngine } = await import(enginePath);
  const arena = new ArenaEngine(cards, deckStorage(null, localStorage));
  const battlePath = '../../../../public/assets/gwent/interface/battle.js';
  const catalogPath = '../../../../public/assets/gwent/interface/src/ui/catalog.js';
  const editorPath = '../../../../public/assets/gwent/interface/src/ui/deck-editor.js';
  const rulesPath = '../../../../public/assets/gwent/interface/src/ui/rules-guide.js';
  const { PlatformBattle } = await import(battlePath);
  const { CardCatalog } = await import(catalogPath);
  const { DeckEditor } = await import(editorPath);
  const { showRulesGuide } = await import(rulesPath);
  new PlatformBattle(arena, cards, { send: vi.fn(), returnLobby: vi.fn() });
  const catalog = new CardCatalog(arena);
  const editor = new DeckEditor(arena, document.getElementById('player-faction'));
  showRulesGuide(arena);
  expect((document.getElementById('arena-dialog') as HTMLDialogElement).open).toBe(true);
  document.querySelector<HTMLButtonElement>('#dialog-actions button')!.click();
  document.getElementById('view-all-cards')!.click();
  expect((document.getElementById('card-catalog') as HTMLDialogElement).open).toBe(true);
  catalog.close();
  editor.open();
  expect((document.getElementById('deck-editor') as HTMLDialogElement).open).toBe(true);
  editor.close();
  arena.delay = async () => {};
  arena.speed = 0.12;
  const failed = vi.fn();
  arena.fail = failed;
  expect('arena' in window).toBe(false);
  vi.spyOn(Math, 'random').mockReturnValue(0.314159);
  await arena.start('watch', 'local');
  await waitFor(
    () => {
      if (failed.mock.calls.length) throw failed.mock.calls[0]![0];
      expect(document.getElementById('result-overlay')!.hidden).toBe(false);
    },
    { timeout: 10000 },
  );
  expect(failed).not.toHaveBeenCalled();
  expect(document.getElementById('result-overlay')!.hidden).toBe(false);
  expect(arena.roundSummaries.length).toBeGreaterThanOrEqual(2);
  expect(document.getElementById('result-history')!.children.length).toBeGreaterThanOrEqual(2);
  arena.clock.stop();
  arena.matchAbort.abort();
}, 10000);
