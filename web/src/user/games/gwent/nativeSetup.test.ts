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
it('keeps original selectors working through offer polling, demo and player matching', async () => {
  const decks = new DeckStore(cards, localStorage);
  const arena = { catalog: cards, decks, view: { modalOpen: false }, start: vi.fn() };
  const editor = new DeckEditor(arena, document.getElementById('player-faction'));
  const host = { send: vi.fn(), startDemo: vi.fn(), inspectDeck: vi.fn() };
  const setup = new PlatformSetup(arena, editor, host);
  const loadout = decks.profile('deepseek').deck;
  const snapshot = {
    config: {
      enabled: true,
      available: true,
      modes: {
        standard: {
          enabled: true,
          available: true,
          ticket: '3',
          termsHash: 'standard-hash',
          rates: { platform: 100, welfare: 200, thursday: 0 },
        },
      },
    },
    home: { current: null, queue: null },
    accepting: true,
    blocked: false,
    availableCredits: '30',
    ai: {
      enabled: true,
      bots: [
        {
          terms: {
            ticket: '1',
            ai: {
              bot_id: 'opaque-bot-id',
              bot_name: '冻结挑战',
              description: '挑战说明',
              first_reward: '5',
              bot_loadout: loadout,
            },
          },
          terms_hash: 'challenge-hash',
          completed: false,
        },
      ],
    },
  };
  setup.update(snapshot);
  setup.update(snapshot);
  expect((document.getElementById('opponent-faction') as HTMLSelectElement).value).toBe(
    'opaque-bot-id',
  );
  expect(document.getElementById('platform-terms')!.textContent).toContain('仅首次胜利奖励 5');
  document.getElementById('opponent-deck-help')!.click();
  expect(host.inspectDeck).toHaveBeenCalledWith(loadout);
  document.getElementById('launch-play')!.click();
  expect(host.send).toHaveBeenCalledWith({
    type: 'queue',
    mode: 'ai',
    termsHash: 'challenge-hash',
    botID: 'opaque-bot-id',
    deck: decks.profile('openai').deck,
  });
  document.querySelector<HTMLButtonElement>('[data-mode=demo]')!.click();
  expect((document.getElementById('opponent-faction') as HTMLSelectElement).value).toBe('deepseek');
  expect((document.getElementById('opponent-deck') as HTMLSelectElement).options.length).toBe(6);
  await setup.watch();
  expect(arena.start).toHaveBeenCalledWith('watch', 'local');
  expect(host.send).toHaveBeenCalledTimes(1);
  document.querySelector<HTMLButtonElement>('[data-mode=standard]')!.click();
  document.getElementById('launch-play')!.click();
  expect(host.send).toHaveBeenLastCalledWith({
    type: 'queue',
    mode: 'standard',
    termsHash: 'standard-hash',
    deck: decks.profile('openai').deck,
  });
  setup.update({
    ...snapshot,
    home: { current: null, queue: { mode: 'standard', id: 'queue' } },
    remaining: 10,
  });
  expect((document.getElementById('player-faction') as HTMLSelectElement).disabled).toBe(true);
  document.querySelector<HTMLButtonElement>('.match-panel .ghost-button')!.click();
  expect(host.send).toHaveBeenLastCalledWith({ type: 'cancel' });
});
