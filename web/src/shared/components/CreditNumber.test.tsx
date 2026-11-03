import { fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { ExactCredits } from '../../user/features/core/components';
import { CreditChange } from '../../user/features/credits/CreditChange';
import { GameMoney } from '../../user/games/common/GameMoney';
import { Amount } from './Amount';
import { CopyValue } from './CopyValue';
import { CreditNumber } from './CreditNumber';
import { CreditText } from './CreditText';
import { ToastViewport } from './Toast';

afterEach(() => vi.restoreAllMocks());

describe('Credit number display', () => {
  it.each(['0.001', '-0.001', '+0.001', '9,007,199,254,740,993.001'])(
    'preserves the exact signed text %s and includes the point in the fraction',
    (value) => {
      const view = render(<CreditNumber value={value} />);
      expect(view.container.textContent).toBe(value);
      expect(view.container.querySelector('.credit-number__fraction')?.textContent).toBe('.001');
    },
  );

  it('leaves an integer without a fractional element', () => {
    const view = render(<CreditNumber value="1,234" />);
    expect(view.container.textContent).toBe('1,234');
    expect(view.container.querySelector('.credit-number__fraction')).toBeNull();
  });

  it('keeps the three existing money entry points exact and keeps expense/income signs', async () => {
    const view = await renderWithProviders(
      <>
        <Amount value="1" unit="" />
        <ExactCredits value="0.001" />
        <GameMoney value="0.001" />
        <CreditChange value="-0.001" />
        <CreditChange value="0.001" />
      </>,
      { station: 'user' },
    );
    expect(view.container.querySelectorAll('.credit-number__fraction')).toHaveLength(5);
    expect(view.container.querySelector('.credit-change.is-expense')?.textContent).toBe('−0.001');
    expect(view.container.querySelector('.credit-change.is-income')?.textContent).toBe('+0.001');
  });

  it('copies the original canonical value independently of its grouped display', async () => {
    const value = '9007199254740993.001';
    await renderWithProviders(
      <>
        <CreditNumber value="9,007,199,254,740,993.001" />
        <CopyValue value={value} label="credits" showValue={false} />
      </>,
      { station: 'user' },
    );
    const write = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue(undefined);
    fireEvent.click(screen.getByRole('button', { name: 'Copy credits' }));
    expect(write).toHaveBeenCalledWith(value);
  });

  it.each(['en', 'zh'] as const)(
    'renders controlled amount slots in %s without changing their text',
    async (locale) => {
      const view = await renderWithProviders(
        <CreditText i18nKey="user.games.copy.fishing.reward" amounts={{ amount: '1,234.005' }} />,
        { station: 'user', locale },
      );
      expect(view.container.querySelector('.credit-number')?.textContent).toBe('1,234.005');
      expect(view.container.querySelector('.credit-number__fraction')?.textContent).toBe('.005');
      expect(view.container).toHaveTextContent(
        locale === 'zh' ? '净到账 1,234.005 通用积分' : 'Net: 1,234.005 general credits',
      );
      expect(view.container.textContent).not.toContain('<amount>');
    },
  );

  it('announces rich credit title/message and retains the ordinary string toast and close action', async () => {
    const dismiss = vi.fn();
    const view = await renderWithProviders(
      <ToastViewport
        toasts={[
          {
            id: 'rich',
            tone: 'success',
            durationMs: null,
            title: (
              <CreditText
                i18nKey="user.games.copy.onboarding.awarded"
                amounts={{ reward: '0.001' }}
              />
            ),
            message: (
              <CreditText
                i18nKey="user.games.copy.fishing.netReceived"
                amounts={{ amount: '0.001' }}
              />
            ),
          },
          { id: 'plain', title: 'Saved', message: 'Still a string', durationMs: null },
        ]}
        onDismiss={dismiss}
      />,
      { station: 'user' },
    );
    // Each item announces once through status/alert; the viewport must not duplicate it.
    expect(view.container.querySelector('.nb-toast-viewport')).not.toHaveAttribute('aria-live');
    const toasts = screen.getAllByRole('status');
    expect(toasts[0]).toHaveTextContent('Net catch: +0.001 general credits');
    expect(toasts[0].querySelectorAll('.credit-number__fraction')).toHaveLength(2);
    expect(toasts[1]).toHaveTextContent('SavedStill a string');
    fireEvent.click(within(toasts[0]).getByRole('button', { name: 'Close' }));
    expect(dismiss).toHaveBeenCalledWith('rich');
  });
});

it.each(['en', 'zh'] as const)(
  'keeps two exchange amounts and their units in %s order',
  async (locale) => {
    const view = await renderWithProviders(
      <CreditText
        i18nKey="user.lakeNotes.quoteAmounts"
        amounts={{ sourceAmount: '0.001', targetAmount: '2.005' }}
        values={{ sourceUnit: 'general', targetUnit: 'game' }}
      />,
      { station: 'user', locale },
    );
    expect(
      [...view.container.querySelectorAll('.credit-number')].map((node) => node.textContent),
    ).toEqual(['0.001', '2.005']);
    expect(view.container.textContent).not.toMatch(/<[^>]+>|\{\{/);
    expect(view.container).toHaveTextContent('general');
    expect(view.container).toHaveTextContent('game');
  },
);
