import { fireEvent, render, screen } from '@testing-library/react';
import { beforeAll, describe, expect, it } from 'vitest';
import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import en from '@shared/i18n/common/en.json';
import zh from '@shared/i18n/common/zh.json';
import { MessagesView } from './MessagesView';

beforeAll(async () => {
  await i18n
    .use(initReactI18next)
    .init({ resources: { en: { translation: en }, zh: { translation: zh } }, lng: 'en' });
});
describe('structured messages', () => {
  it('keeps field order and safe text, folding long fields individually', () => {
    const long = 'x'.repeat(2001);
    const { container } = render(
      <MessagesView
        messages={[
          { role: 'user', content: '<img src=x onerror=alert(1)>', extra: long },
          { role: 'assistant', content: 'short reply' },
          ['nonstandard', { answer: 7 }],
        ]}
      />,
    );
    expect(screen.getByText('<img src=x onerror=alert(1)>')).toBeVisible();
    expect(container.querySelector('img')).toBeNull();
    expect(screen.getByText('short reply')).toBeVisible();
    expect(screen.getByText(long).closest('details')).not.toHaveAttribute('open');
    expect(Array.from(container.querySelectorAll('dt')).map((node) => node.textContent)).toEqual([
      'role',
      'content',
      'extra',
      'role',
      'content',
    ]);
    expect(screen.getByText('Read the original message')).toBeVisible();
  });
  it('renders a bounded page and makes every remaining message reachable', () => {
    render(
      <MessagesView
        messages={Array.from({ length: 21 }, (_, i) => ({
          role: 'user',
          content: `content ${i + 1}`,
        }))}
      />,
    );
    expect(screen.queryByText('content 21')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Next' }));
    expect(screen.getByText('content 21')).toBeVisible();
    expect(screen.queryByText('content 1')).toBeNull();
    expect(screen.getByRole('button', { name: 'Next' })).toBeDisabled();
  });
});
