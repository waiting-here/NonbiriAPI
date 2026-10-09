import { afterEach, expect, test, vi } from 'vitest';
import { createElement, StrictMode } from 'react';
import { cleanup, render } from '@testing-library/react';
import { NativeLake } from './NativeLake';
import { mountLake, type LakeBridge } from './native.mjs';
import html from './native.html?raw';
import { initialProfile, RULES_ID, start, step } from './rules';
import type { ControllerSnapshot } from './controller';
import type { CastResult } from './api';
import { nativeEnglish } from './native-language';

function fixture(language: 'zh' | 'en' = 'zh') {
  vi.stubGlobal('matchMedia', () => ({ matches: false }));
  vi.stubGlobal(
    'requestAnimationFrame',
    vi.fn(() => 1),
  );
  vi.stubGlobal('cancelAnimationFrame', vi.fn());
  const root = document.createElement('div');
  root.innerHTML = html;
  document.body.append(root);
  const profile = initialProfile();
  const bridge: LakeBridge = {
    language,
    translate: nativeEnglish,
    profile: () => profile,
    revision: () => '1',
    snapshot: () => ({ result: null, status: 'readonly', error: null }),
    projection: () => null,
    held: vi.fn(),
    tick: vi.fn(),
    act: vi.fn(async () => undefined),
    start: vi.fn(),
    resume: vi.fn(),
    pause: vi.fn(),
    blocked: () => false,
    busy: () => false,
    readonly: () => false,
    text: (key) => String(key),
  };
  const game = mountLake(root, bridge);
  return { root, profile, bridge, game };
}
afterEach(() => {
  cleanup();
  document.body.replaceChildren();
  vi.unstubAllGlobals();
});
test('original journal exposes complete equipment presets and sends account actions', () => {
  const { root, bridge, game, profile } = fixture();
  (root.querySelector('#shopButton') as HTMLButtonElement).click();
  expect(root.querySelector('#shopModal')?.hasAttribute('hidden')).toBe(false);
  const presets = [...root.querySelectorAll('#shopItems strong')].filter((e) =>
    e.textContent?.startsWith('方案 '),
  );
  expect(presets).toHaveLength(3);
  const save = [...root.querySelectorAll('#shopItems button')].find(
    (e) => e.textContent === '保存当前',
  ) as HTMLButtonElement;
  save.click();
  expect(bridge.act).toHaveBeenCalledWith({ action: 'save_gear_loadout', index: 0 });
  expect(profile.savedLoadouts).toEqual([null, null, null]);
  game.dispose();
});
test('casting uses the server and original navigation opens every journal', () => {
  const { root, bridge, game } = fixture();
  (root.querySelector('#startButton') as HTMLButtonElement).click();
  expect(bridge.start).toHaveBeenCalledOnce();
  for (const [button, modal] of [
    ['basket', 'basket'],
    ['catalog', 'catalog'],
    ['skill', 'skill'],
    ['contracts', 'contracts'],
    ['location', 'location'],
    ['settings', 'settings'],
  ]) {
    (root.querySelector('#' + button + 'Button') as HTMLButtonElement).click();
    expect(root.querySelector('#' + modal + 'Modal')?.hasAttribute('hidden')).toBe(false);
    (root.querySelector('#' + modal + 'Close') as HTMLButtonElement).click();
  }
  game.dispose();
});

test('original shop, catalog, skills, locations and contracts have English output', () => {
  const { root, game } = fixture('en');
  for (const [button, body] of [
    ['shop', 'shopItems'],
    ['catalog', 'catalogItems'],
    ['skill', 'skillChoices'],
    ['location', 'locationItems'],
    ['contracts', 'contractsItems'],
  ]) {
    (root.querySelector('#' + button + 'Button') as HTMLButtonElement).click();
    const content = root.querySelector('#' + body)?.textContent ?? '';
    expect(content.match(/[一-鿿]+/g)).toBeNull();
  }
  game.dispose();
});

test('StrictMode mount, disposal and remount leave one server action per click', () => {
  const { root, bridge, game } = fixture();
  game.dispose();
  root.remove();
  const view = render(createElement(StrictMode, null, createElement(NativeLake, { bridge })));
  (view.container.querySelector('#startButton') as HTMLButtonElement).click();
  expect(bridge.start).toHaveBeenCalledOnce();
});

test('an English to Chinese remount restores static original copy and replaces listeners', () => {
  const { root, bridge, game } = fixture('en');
  game.dispose();
  root.remove();
  const view = render(createElement(NativeLake, { bridge }));
  expect(view.container.querySelector('#settingsTitle')?.textContent).toBe(
    'Sound and saved progress',
  );
  const previousStart = view.container.querySelector('#startButton');
  const chinese = { ...bridge, language: 'zh' as const, start: vi.fn() };
  view.rerender(createElement(NativeLake, { bridge: chinese }));
  expect(view.container.querySelector('#settingsTitle')?.textContent).toBe('音效与存档');
  expect(previousStart?.isConnected).toBe(false);
  (view.container.querySelector('#startButton') as HTMLButtonElement).click();
  expect(chinese.start).toHaveBeenCalledOnce();
  expect(bridge.start).not.toHaveBeenCalled();
});

test.each(['fish', 'debris'] as const)(
  'a lost %s terminal save exposes retry instead of an endless saving overlay',
  (encounter) => {
    const { root, bridge, game, profile } = fixture();
    if (encounter === 'debris')
      profile.records.lake_carp = {
        caught: '1',
        maxLength: 30,
        bestQuality: 0,
        perfectCount: '0',
      };
    const prediction = start(profile, () => 0, 1);
    const waiting = structuredClone(prediction.cast);
    for (let tick = 0; tick < 240; tick++) step(prediction.profile, prediction.cast, false);
    const cast: CastResult['cast'] = {
      id: 'lnc_AAAAAAAAAAAAAAAAAAAAAA',
      rules_id: RULES_ID,
      generation: '1',
      revision: '1',
      ack_tick: encounter === 'debris' ? waiting.tick : prediction.cast.tick,
      phase: encounter === 'debris' ? 'waiting' : 'playing',
      paused: false,
      readonly: false,
      state: encounter === 'debris' ? waiting : structuredClone(prediction.cast),
      profile_revision: '1',
    };
    const result: CastResult = {
      cast,
      profile: {
        readonly: false,
        revision: '1',
        rules_id: RULES_ID,
        profile,
        cast,
        wallet: { general_milli: '0', game_milli: '0' },
        settings: {
          revision: '1',
          enabled: true,
          exchanges: {
            coins_to_general: { enabled: false, source_amount: '', target_amount: '' },
            general_to_coins: { enabled: false, source_amount: '', target_amount: '' },
            coins_to_game: { enabled: false, source_amount: '', target_amount: '' },
            game_to_coins: { enabled: false, source_amount: '', target_amount: '' },
          },
        },
      },
    };
    if (encounter === 'fish') prediction.cast.phase = 'failed';
    else {
      expect(prediction.cast.result?.debris).toBeTruthy();
      expect(prediction.cast.fish).toBeUndefined();
    }
    const drawFrame = () => {
      const schedule = vi.mocked(requestAnimationFrame);
      const next = schedule.mock.lastCall![0];
      schedule.mockClear();
      next(performance.now() + 20);
      expect(schedule).toHaveBeenCalledOnce();
    };
    let snapshot: ControllerSnapshot = { result, status: 'saving', error: null };
    bridge.snapshot = () => snapshot;
    bridge.projection = () => prediction;
    bridge.blocked = () => true;
    drawFrame();
    const button = root.querySelector<HTMLButtonElement>('#overlayButton')!;
    expect(root.querySelector('#overlayTitle')).toHaveTextContent('confirming');
    expect(button).toBeDisabled();

    snapshot = { result, status: 'unknown', error: new Error('response lost') };
    drawFrame();
    expect(root.querySelector('#overlayTitle')).not.toHaveTextContent('confirming');
    expect(root.querySelector('#overlayText')).toHaveTextContent('saveUnknown');
    expect(button).toHaveTextContent('retry');
    expect(button).toBeEnabled();
    button.click();
    expect(bridge.resume).toHaveBeenCalledOnce();
    expect(bridge.start).not.toHaveBeenCalled();

    if (encounter === 'debris') {
      // Once confirmed, the same mounted game must render the authoritative
      // debris result and allow another cast without refreshing the page.
      const confirmed: CastResult = {
        cast: {
          ...cast,
          phase: 'success',
          ack_tick: prediction.cast.tick,
          state: structuredClone(prediction.cast),
          profile_revision: '2',
        },
        profile: { ...result.profile, revision: '2', profile: prediction.profile },
      };
      confirmed.profile.cast = confirmed.cast;
      bridge.profile = () => confirmed.profile.profile;
      bridge.revision = () => '2';
      snapshot = { result: confirmed, status: 'terminal', error: null };
      bridge.blocked = () => false;
      drawFrame();
      expect(root.querySelector('#overlayTitle')).toHaveTextContent('捞起一份杂物');
      expect(button).toBeEnabled();
      button.click();
      expect(bridge.start).toHaveBeenCalledOnce();
    }
    game.dispose();
  },
);
