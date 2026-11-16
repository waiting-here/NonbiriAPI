import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import {
  copyKey,
  emptyDraft,
  mergePackage,
  packageDraft,
  parsePackage,
  reconcileDraft,
  resolvePending,
  serializeDraft,
  updateDraft,
} from './drafts';
import { collectCopyReviewSources, collectScreenCopy } from './source-plugin';
import type { CopyChange, CopyEntry } from './types';

const entry: CopyEntry = {
  source: { file: 'example.json', key: 'pool.summary' },
  language: 'en',
  original: '{{name}} received {{count}} tickets.',
  category: 'screen',
  area: ['抽卡', 'Draw'],
  field: ['结果', 'Results'],
};
const key = copyKey(entry);
const sources = new Map([[key, entry]]);

function withRecords(changes: CopyChange[]) {
  return { ...packageDraft(emptyDraft()), changes };
}

describe('copy review drafts', () => {
  it('round-trips multiline copy and preserves interpolation tokens', () => {
    const value = '{{count}} tickets\nfor {{name}}.';
    const draft = updateDraft(emptyDraft(), entry, value);
    const document = parsePackage(serializeDraft(draft));
    expect(document.changes).toEqual([
      { source: entry.source, language: entry.language, original: entry.original, value },
    ]);
    expect(mergePackage(emptyDraft(), document, sources).draft.edits.get(key)?.value).toBe(value);
  });

  it('keeps edits with missing or duplicated placeholders as editable drafts', () => {
    const value = '{{name}} received {{name}} tickets.';
    const draft = updateDraft(emptyDraft(), entry, value);
    const document = parsePackage(serializeDraft(draft));
    expect(document.changes).toEqual([]);
    expect(document.drafts).toHaveLength(1);
    expect(mergePackage(emptyDraft(), document, sources).draft.edits.get(key)?.value).toBe(value);
  });

  it('retains the local edit, a competing imported edit, source drift, and removed sources', () => {
    const local = updateDraft(emptyDraft(), entry, '{{name}} won {{count}} tickets.');
    const imported = '{{count}} tickets have gone to {{name}}.';
    const sourceDrift = { ...entry, original: '{{name}} had {{count}} tickets.', value: imported };
    const competing = { ...entry, value: imported };
    const removed = { ...competing, source: { file: entry.source.file, key: 'removed' } };
    const result = mergePackage(local, withRecords([competing, sourceDrift, removed]), sources);
    expect(result.conflicted).toBe(3);
    expect(result.draft.edits.get(key)).toEqual(local.edits.get(key));
    expect(packageDraft(result.draft).changes).toEqual([]);
    const restored = mergePackage(
      emptyDraft(),
      parsePackage(serializeDraft(result.draft)),
      sources,
    ).draft;
    expect(restored.edits).toEqual(local.edits);
    expect(restored.pending).toHaveLength(3);
    const confirmed = resolvePending(restored, restored.pending[1], entry);
    expect(confirmed.edits.get(key)?.original).toBe(entry.original);
    expect(confirmed.edits.get(key)?.value).toBe(imported);
    expect(confirmed.pending).toHaveLength(2);
  });

  it('rejects a malformed record without mutating the current draft', () => {
    const draft = updateDraft(emptyDraft(), entry, '{{name}} won {{count}} tickets.');
    const before = serializeDraft(draft);
    expect(() =>
      parsePackage(
        JSON.stringify(withRecords([{ ...entry, value: undefined } as unknown as CopyChange])),
      ),
    ).toThrow();
    expect(serializeDraft(draft)).toBe(before);
  });
});

describe('copy review from two tabs', () => {
  const oldValue = '{{name}} had {{count}} tickets.';
  const newValue = '{{name}} won {{count}} tickets.';
  const other = { ...entry, source: { ...entry.source, key: 'another.summary' } };

  it('keeps edits to different rows and propagates a reset without reviving the old edit', () => {
    const base = updateDraft(emptyDraft(), entry, oldValue);
    const local = updateDraft(base, entry, entry.original);
    const remote = updateDraft(base, other, newValue);
    const saved = reconcileDraft(base, local, remote);
    expect(saved.edits.get(key)).toBeUndefined();
    expect(saved.edits.get(copyKey(other))?.value).toBe(newValue);
    expect(saved.pending).toHaveLength(0);
  });

  it('preserves a competing reset through export and import until it is accepted', () => {
    const base = updateDraft(emptyDraft(), entry, oldValue);
    const local = updateDraft(base, entry, entry.original);
    const remote = updateDraft(base, entry, newValue);
    const saved = reconcileDraft(base, local, remote);
    const restored = mergePackage(emptyDraft(), parsePackage(serializeDraft(saved)), sources).draft;
    expect(restored.edits.get(key)?.value).toBe(newValue);
    expect(restored.pending.map((change) => change.value)).toEqual([entry.original]);
    expect(packageDraft(restored).changes).toHaveLength(0);
    const accepted = resolvePending(restored, restored.pending[0], entry);
    expect(accepted.edits.has(key)).toBe(false);
    expect(accepted.pending).toHaveLength(0);
  });

  it('keeps a competing text edit available when the other tab already saved its reset', () => {
    const base = updateDraft(emptyDraft(), entry, oldValue);
    const local = updateDraft(base, entry, newValue);
    const remote = updateDraft(base, entry, entry.original);
    const saved = reconcileDraft(base, local, remote);
    expect(saved.edits.has(key)).toBe(false);
    expect(saved.pending.map((change) => change.value)).toEqual([newValue]);
  });
});

it('keeps repeated JSX copy attached to the same control across sibling reorderings', () => {
  const standard = "<Button onClick={() => choose('permanent')}>{t('兑换', 'Get')}</Button>";
  const limited = "<Button onClick={() => choose('limited')}>{t('兑换', 'Get')}</Button>";
  const row = (first: string, second: string) =>
    collectScreenCopy(
      'web/src/user/activities/gacha/GachaApp.tsx',
      'function Sample() { return <div className="wallet">' + first + '\n' + second + '</div> }',
    ).filter((item) => item.language === 'en');
  const previous = row(standard, limited);
  const reordered = row(limited, standard);
  expect(previous.map(copyKey)).toEqual(reordered.map(copyKey).reverse());
  expect(new Set(previous.map(copyKey)).size).toBe(2);

  const game = '<dd><CreditNumber value={credits.game} />{t("积分", "credits")}</dd>';
  const general = '<dd><CreditNumber value={credits.general} />{t("积分", "credits")}</dd>';
  expect(row(game, general).map(copyKey)).toEqual(row(general, game).map(copyKey).reverse());
});

it('collects uniquely located bilingual card names and first encounter dialogue', () => {
  const data = collectCopyReviewSources(resolve(process.cwd(), '..'));
  expect(new Set(data.entries.map(copyKey)).size).toBe(data.entries.length);
  expect(data.rarities).toEqual(['N', 'R', 'SR', 'SSR', 'SP']);
  for (const row of data.entries.filter(
    (item) => item.category === 'card' && item.source.key.endsWith('.name'),
  )) {
    const card = row.source.key.split('.')[1];
    expect(data.entries.some((item) => item.source.key === 'cards.' + card + '.name_en')).toBe(
      true,
    );
    for (const offset of [0, 1]) {
      expect(data.entries.some((item) => item.source.key === 'VOICE.' + card + '.' + offset)).toBe(
        true,
      );
    }
  }
  expect(data.entries.some((item) => item.source.key === 'user.appearance.locked')).toBe(true);
  expect(data.entries.some((item) => item.source.key === 'heart-moment.description')).toBe(true);
  expect(
    data.entries.some(
      (item) => item.source.file.endsWith('/ArtworkImage.tsx') && item.original === 'R-18',
    ),
  ).toBe(true);
  expect(data.entries.some((item) => item.source.key === 'parameter/GachaDialog/closeLabel')).toBe(
    true,
  );
  const interpolation = '$' + '{name}';
  expect(data.entries.some((item) => item.original === 'R-18 封面不可用：' + interpolation)).toBe(
    true,
  );
});
