import { afterEach, expect, it, vi } from 'vitest';
import { customPresetList, presetName, renameCustomPreset, saveCustomPreset } from './presetApi';

const stored = (name = '') => ({
  slot: 1,
  name,
  revision: '2',
  mode: 'quick',
  loadout: { role: 'ChatGPT', harness: null, skills: ['PUB01'] },
  updated_at: 100,
});
afterEach(() => vi.unstubAllGlobals());

it('validates twenty Unicode code points, single-line names and plain text', () => {
  expect(presetName('🐟'.repeat(20))).toBe('🐟'.repeat(20));
  expect(presetName('<b>fish</b>')).toBe('<b>fish</b>');
  for (const name of [
    '🐟'.repeat(21),
    'a\nb',
    'a\rb',
    'a\tb',
    '\0',
    '\u007f',
    '\u0085',
    '\u2028',
    '\ud800',
  ])
    expect(() => presetName(name)).toThrow();
  expect(
    customPresetList({
      capacity: 10,
      slots: [stored('same'), { ...stored('same'), slot: 2 }],
    }).slots.map((v) => v.name),
  ).toEqual(['same', 'same']);
});

it('sends only name and revision on PATCH while PUT omission preserves old clients', async () => {
  const fetcher = vi.fn(
    async () =>
      new Response(JSON.stringify(stored('renamed')), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
  );
  vi.stubGlobal('fetch', fetcher);
  const renamed = await renameCustomPreset({
    slot: 1,
    name: 'renamed',
    expectedRevision: '1',
    key: 'rename-key',
  });
  expect(renamed.loadout.skills).toEqual(['PUB01']);
  const patch = fetcher.mock.calls[0] as unknown as [string, RequestInit];
  expect(patch[1].method).toBe('PATCH');
  expect(JSON.parse(patch[1].body as string)).toEqual({ expected_revision: '1', name: 'renamed' });
  await saveCustomPreset({
    slot: 1,
    expectedRevision: '1',
    mode: 'quick',
    loadout: {
      role: 'ChatGPT',
      harness: renamed.loadout.harness,
      skills: [...renamed.loadout.skills],
    },
    key: 'save-key',
  });
  const put = fetcher.mock.calls[1] as unknown as [string, RequestInit];
  expect(JSON.parse(put[1].body as string)).not.toHaveProperty('name');
  await saveCustomPreset({
    slot: 1,
    expectedRevision: '1',
    mode: 'quick',
    loadout: {
      role: 'ChatGPT',
      harness: renamed.loadout.harness,
      skills: [...renamed.loadout.skills],
    },
    key: 'clear-key',
    name: '',
  });
  const clear = fetcher.mock.calls[2] as unknown as [string, RequestInit];
  expect(JSON.parse(clear[1].body as string).name).toBe('');
});
