import { beforeEach, expect, it, vi } from 'vitest';
import frameHTML from '../../../../public/assets/gwent/interface/index.html?raw';
import cards from '../../../../public/assets/gwent/interface/src/cards/cards.json';
import { installNativeDialog } from '../../../../test/unit/nativeDialog';
const decksPath = '../../../../public/assets/gwent/interface/src/cards/decks.js';
const editorPath = '../../../../public/assets/gwent/interface/src/ui/deck-editor.js';
const setupPath = '../../../../public/assets/gwent/interface/setup.js';
const { DeckStore } = await import(decksPath);
const { DeckEditor } = await import(editorPath);
const { PlatformSetup } = await import(setupPath);
installNativeDialog();
beforeEach(() => {
  document.body.innerHTML = new DOMParser().parseFromString(frameHTML, 'text/html').body.innerHTML;
  for (const id of ['player-faction', 'opponent-faction']) {
    const select = document.getElementById(id) as HTMLSelectElement;
    for (const faction of ['openai', 'deepseek', 'claude', 'gemini'])
      select.append(new Option(faction, faction));
    select.value = id === 'player-faction' ? 'openai' : 'deepseek';
  }
});
it('uses the selected deck for player matching and free companion starts', () => {
  const decks = new DeckStore(cards, localStorage);
  const arena = { catalog: cards, decks, view: { modalOpen: false } };
  const editor = new DeckEditor(arena, document.getElementById('player-faction'));
  const host = { send: vi.fn() };
  const setup = new PlatformSetup(arena, editor, host);
  const snapshot = {
    config: {
      available: true,
      modes: {
        standard: {
          enabled: true,
          available: true,
          ticket: '3',
          termsHash: 'pvp',
          rates: { platform: 100, welfare: 200, thursday: 0 },
        },
      },
    },
    accepting: true,
    blocked: false,
    availableCredits: '30',
    home: { current: null, queue: null },
  };
  setup.update(snapshot);
  document.getElementById('launch-play')!.click();
  expect(host.send).toHaveBeenLastCalledWith({
    type: 'queue',
    mode: 'standard',
    termsHash: 'pvp',
    deck: decks.profile('openai').deck,
  });
  document.getElementById('companion-entry')!.click();
  expect(host.send).toHaveBeenLastCalledWith({ type: 'companion-entry' });
  setup.update({ ...snapshot, companion: true });
  document.getElementById('launch-play')!.click();
  expect(host.send).toHaveBeenLastCalledWith({
    type: 'companion-start',
    deck: decks.profile('openai').deck,
  });
  expect(document.getElementById('platform-terms')!.textContent).toContain('免费');
  setup.update({ ...snapshot, home: { queue: { id: 'queue' }, current: null } });
  expect((document.getElementById('player-faction') as HTMLSelectElement).disabled).toBe(true);
  document.querySelector<HTMLButtonElement>('.match-panel .ghost-button')!.click();
  expect(host.send).toHaveBeenLastCalledWith({ type: 'cancel' });
});

it('boots the native deck editor and card rules without the retired browser engine', async () => {
  // The native HTML loads a standalone ES module; exercise its normal boot with JSON card data.
  const bootPath = '../../../../public/assets/gwent/interface/src/main.js';
  const { boot } = await import(bootPath);
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => Response.json(cards)),
  );
  vi.stubGlobal('matchMedia', () => ({ matches: false, addEventListener: vi.fn() }));
  const { setup, arena } = await boot(localStorage, { send: vi.fn() });
  setup.update({ companion: true, config: { modes: {} }, home: { current: null, queue: null } });
  expect(document.getElementById('launch-play')).toBeEnabled();
  const hero = document.querySelector('#hero-preview .arena-card');
  expect(hero).toBeTruthy();
  document.getElementById('view-rules')!.click();
  expect(document.querySelector('.rules-guide')).toHaveTextContent('检查点恢复');
  arena.view.closeDialog();
});
