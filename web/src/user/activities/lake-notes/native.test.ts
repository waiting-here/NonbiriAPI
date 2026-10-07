import { afterEach, expect, test, vi } from 'vitest';
import { createElement, StrictMode } from 'react';
import { cleanup, render } from '@testing-library/react';
import { NativeLake } from './NativeLake';
import { mountLake, type LakeBridge } from './native.mjs';
import html from './native.html?raw';
import { initialProfile } from './rules';
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
