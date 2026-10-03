import { screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { OutcomeNote } from './OutcomeNote';

function primaryNotices() {
  return [...screen.queryAllByRole('alert'), ...screen.queryAllByRole('status')];
}

describe('mutation outcome presentation', () => {
  it.each(['zh', 'en'] as const)(
    'shows one primary outcome and only runs the selected recovery action in %s',
    async (locale) => {
      const recheck = vi.fn();
      const reload = vi.fn();
      const retry = vi.fn();
      const view = await renderWithProviders(
        <OutcomeNote outcome={{ kind: 'unknown', recheck }} />,
        { station: 'user', locale },
      );
      expect(primaryNotices()).toHaveLength(1);
      expect(screen.getByRole('alert')).toHaveTextContent(
        view.i18n.t('common.outcome.unknownTitle'),
      );
      expect(recheck).not.toHaveBeenCalled();
      await view.user.click(
        screen.getByRole('button', { name: view.i18n.t('common.outcome.recheck') }),
      );
      expect(recheck).toHaveBeenCalledTimes(1);
      view.rerender(<OutcomeNote outcome={{ kind: 'conflict', reload }} />);
      expect(primaryNotices()).toHaveLength(1);
      expect(screen.getByRole('alert')).toHaveTextContent(
        view.i18n.t('common.outcome.conflictBody'),
      );
      expect(screen.getByRole('alert')).not.toHaveTextContent(
        view.i18n.t('common.outcome.unknownTitle'),
      );
      await view.user.click(
        screen.getByRole('button', { name: view.i18n.t('common.outcome.reload') }),
      );
      expect(reload).toHaveBeenCalledTimes(1);
      expect(recheck).toHaveBeenCalledTimes(1);
      view.rerender(
        <OutcomeNote outcome={{ kind: 'failed', message: 'Save failed', retry }} busy />,
      );
      expect(primaryNotices()).toHaveLength(1);
      expect(screen.getByRole('button', { name: view.i18n.t('common.retry') })).toBeDisabled();
      expect(retry).not.toHaveBeenCalled();
      view.rerender(<OutcomeNote outcome={{ kind: 'savedRefreshFailed', recheck }} />);
      expect(primaryNotices()).toHaveLength(1);
      expect(screen.getByRole('status')).toHaveTextContent(
        view.i18n.t('common.outcome.savedRefreshFailed'),
      );
      await view.user.click(within(screen.getByRole('status')).getByRole('button'));
      expect(recheck).toHaveBeenCalledTimes(2);
      view.rerender(<OutcomeNote outcome={{ kind: 'saved' }} />);
      expect(primaryNotices()).toHaveLength(1);
      expect(screen.getByRole('status')).toHaveTextContent(view.i18n.t('common.outcome.saved'));
      view.rerender(<OutcomeNote outcome={{ kind: 'idle' }} />);
      expect(primaryNotices()).toHaveLength(0);
    },
  );

  it.each(['zh', 'en'] as const)(
    'keeps one-time-key loss consequences inside the single unknown outcome in %s',
    async (locale) => {
      const view = await renderWithProviders(
        <OutcomeNote outcome={{ kind: 'unknown', oneTimeSecret: true, recheck: vi.fn() }} />,
        { station: 'user', locale },
      );
      expect(primaryNotices()).toHaveLength(1);
      expect(screen.getByRole('alert')).toHaveTextContent(
        view.i18n.t('common.outcome.oneTimeMissedBody'),
      );
      view.rerender(<OutcomeNote outcome={{ kind: 'oneTimeMissed' }} />);
      expect(primaryNotices()).toHaveLength(1);
      expect(screen.getByRole('alert')).toHaveTextContent(
        view.i18n.t('common.outcome.oneTimeMissedTitle'),
      );
      expect(screen.queryByRole('button')).not.toBeInTheDocument();
    },
  );

  it('does not promise preserved input when the language selection has already been refreshed', async () => {
    const view = await renderWithProviders(
      <OutcomeNote outcome={{ kind: 'conflict', inputPreserved: false }} />,
      { station: 'user' },
    );
    expect(screen.getByRole('alert')).toHaveTextContent(
      view.i18n.t('common.outcome.languageConflictBody'),
    );
    expect(screen.getByRole('alert')).not.toHaveTextContent(
      view.i18n.t('common.outcome.conflictBody'),
    );
  });
});
