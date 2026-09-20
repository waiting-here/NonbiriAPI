import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { testCatalog } from './testCatalog';
import { guideLevels, knowledge, relatedEntries } from './knowledge';
import { Glossary } from './Glossary';

vi.mock('../common/duel/copy', () => ({ useDuelText: () => (zh: string) => zh }));
HTMLDialogElement.prototype.showModal = function () {
  this.open = true;
};
HTMLDialogElement.prototype.close = function () {
  this.open = false;
};
const zh = (text: string) => text;
describe.each(['quick', 'standard'] as const)('%s player explanations', (mode) => {
  const catalog = testCatalog.modes[mode];
  it('describes every entry and version with resolved, deduplicated links and separate flavor', () => {
    const entries = [
      ...catalog.skills,
      ...catalog.buffs,
      ...catalog.harnesses,
      ...catalog.passives,
      ...catalog.roles.flatMap((role) => (role.passive ? [role.passive] : [])),
    ];
    for (const item of entries)
      for (const level of guideLevels) {
        const guide = knowledge(catalog, item.id, level, zh);
        expect(guide.summary, `${item.id}/${level}`).not.toBe('');
        expect(guide.paragraphs.length, `${item.id}/${level}`).toBeGreaterThan(0);
        for (const ref of guide.refs)
          expect(
            entries.some((e) => e.id === ref),
            ref,
          ).toBe(true);
        const plain = guide.paragraphs.join(' ').replace(/\[\[[^\]]+\]\]/g, '术语');
        expect(plain).not.toMatch(/Buff ID|buffId|stackGroup|按 ID|后端|NaN|undefined/);
        if (guide.meme) expect(plain).not.toContain(guide.meme);
        const related = relatedEntries(catalog, guide, zh);
        expect(new Set(related.map((e) => e.id)).size).toBe(related.length);
        expect(related.some((e) => e.id === item.id)).toBe(false);
      }
  });
  it('explains payment, persistence, reset and distillation exceptions', () => {
    const text = (id: string) => knowledge(catalog, id, 'base', zh).paragraphs.join(' ');
    expect(text('PUB42')).toContain('不能挽救当轮付款失败');
    expect(text('PUB41')).toContain('不继承原技能的金币、图像费用或成功次数限制');
    const discount = catalog.buffs.find((b) => b.kind === 'API_DISCOUNT')!;
    expect(text(discount.id)).toContain('不是百分比');
    expect(text(discount.id)).toContain('封禁');
    const cache = catalog.skills.find((s) => s.effects.base.kind === 'PERSIST_CACHE')!;
    expect(text(cache.id)).toContain('本轮新获得的缓存不包括在内');
    const distill = catalog.skills.find((s) => s.id === 'PUB41')!;
    const conversion = catalog.skills.find((s) => s.effects.base.kind === 'CACHE_CONVERT')!;
    const amount = Math.floor((distill.token * conversion.effects.II.p) / 100);
    expect(knowledge(catalog, conversion.id, 'II', zh).paragraphs.join(' ')).toContain(
      `每层 ${amount} K`,
    );
    const ban = catalog.buffs.find((b) => b.kind === 'SUBSCRIPTION_BAN')!;
    expect(text(ban.id)).toContain('试用额度足以支付全部 Token');
  });
});

it('navigates related terms and returns to the chosen version with flavor outside mechanics', () => {
  render(<Glossary catalog={testCatalog.modes.quick} initial="GPT01" onClose={() => undefined} />);
  const article = document.querySelector('.likes-reader-main') as HTMLElement;
  fireEvent.click(screen.getByRole('button', { name: '蒸馏 II' }));
  const linked = within(article)
    .getAllByRole('button')
    .find((button) => button.classList.contains('likes-term'))!;
  fireEvent.click(linked);
  fireEvent.click(screen.getByRole('button', { name: '← 返回上一词条' }));
  expect(screen.getByRole('button', { name: '蒸馏 II' })).toHaveAttribute('aria-pressed', 'true');
  expect(article.querySelector('.likes-flavor blockquote')).not.toBeNull();
});
