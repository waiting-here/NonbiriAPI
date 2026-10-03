import { useTranslation } from 'react-i18next';
import { act, screen, within, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { LegalLayout } from './LegalLayout';
import { LegalSections } from './LegalSections';

describe('legal navigation', () => {
  it('uses real override headings, retains safe text blocks and stable anchors on rerender', async () => {
    const document = (
      <LegalLayout documentKey="privacy">
        <LegalSections override={'## Data\nSafe <script> text\n### Retention\n- One\n- Two'} />
      </LegalLayout>
    );
    const view = await renderWithProviders(document, {
      station: 'user',
      role: 'user',
      locale: 'en',
    });
    await waitFor(() => expect(screen.getAllByRole('link', { name: 'Data' })).toHaveLength(3));
    expect(screen.getByText('Safe <script> text')).toBeVisible();
    expect(view.container.querySelector('script')).toBeNull();
    const body = view.container.querySelector('.legal-layout__body')!;
    expect(
      within(body as HTMLElement)
        .getAllByRole('heading')
        .map((heading) => heading.id),
    ).toEqual(['privacy-section-1', 'privacy-section-2']);
    view.rerender(document);
    expect(body.querySelectorAll('.legal-heading-anchor')).toHaveLength(2);
    await view.i18n.changeLanguage('zh');
    await waitFor(() => expect(screen.getByRole('heading', { name: '目录' })).toBeInTheDocument());
    expect(body.querySelectorAll('.legal-heading-anchor')).toHaveLength(2);
  });
});

function TranslatedLegal() {
  const { t } = useTranslation();
  return (
    <LegalLayout documentKey="privacy">
      <h2>{t('user.legal.privacy.operatorTitle')}</h2>
      <p>{t('user.legal.privacy.operatorBody')}</p>
    </LegalLayout>
  );
}

it('updates the directory when translated body headings change without duplicating anchors', async () => {
  const view = await renderWithProviders(<TranslatedLegal />, {
    station: 'user',
    role: 'user',
    locale: 'en',
  });
  const english = view.i18n.t('user.legal.privacy.operatorTitle');
  await waitFor(() => expect(screen.getAllByRole('link', { name: english })).toHaveLength(3));
  await act(async () => {
    await view.i18n.changeLanguage('zh');
  });
  const chinese = view.i18n.t('user.legal.privacy.operatorTitle');
  expect(chinese).not.toBe(english);
  await waitFor(() => expect(screen.getAllByRole('link', { name: chinese })).toHaveLength(3));
  expect(screen.queryByRole('link', { name: english })).not.toBeInTheDocument();
  expect(view.container.querySelectorAll('.legal-heading-anchor')).toHaveLength(1);
  expect(view.container.querySelector('.legal-layout__body h2')).toHaveAttribute(
    'id',
    'privacy-section-1',
  );
});
