import { fireEvent, screen, waitFor } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { installJsonFetchFixtures, renderWithProviders } from '../../../../test/unit/support';
import { LogFilters, type LogFilterField } from './LogFilters';
import type { LogUrlState } from './useLogUrlState';

const nativeOptions = Intl.DateTimeFormat.prototype.resolvedOptions;
const fields: readonly LogFilterField[] = [
  { name: 'model', label: 'Model', ariaLabel: 'Model', maxLength: 133 },
  { name: 'status', label: 'Status', ariaLabel: 'Status', inputType: 'number', maxLength: 3 },
  {
    name: 'category',
    label: 'Category',
    ariaLabel: 'Category',
    options: [
      { value: '', label: 'All' },
      { value: 'network', label: 'Network' },
    ],
  },
];
beforeEach(() => {
  vi.spyOn(Intl.DateTimeFormat.prototype, 'resolvedOptions').mockImplementation(function (
    this: Intl.DateTimeFormat,
  ) {
    return { ...nativeOptions.call(this), timeZone: 'UTC' };
  });
  installJsonFetchFixtures([
    {
      method: 'GET',
      path: '/api/time-zones',
      body: { version: 'go1.26.6-zoneinfo', zones: ['UTC'] },
    },
  ]);
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('shared log filters', () => {
  it('keeps epoch zero and original seconds when applying a seeded range', async () => {
    const onApply = vi.fn();
    const view = await renderWithProviders(
      <LogFilters
        station="user"
        fields={fields}
        state={{ filters: { model: 'seeded' }, fromUnix: 0, toUnix: 75 }}
        onApply={onApply}
      />,
      { station: 'user' },
    );
    expect(screen.getByLabelText('Filter by start time')).toHaveValue('1970-01-01T00:00');
    await view.user.click(screen.getByRole('button', { name: 'Search' }));
    expect(onApply).toHaveBeenCalledWith({ filters: { model: 'seeded' }, fromUnix: 0, toUnix: 75 });
    fireEvent.change(screen.getByLabelText('Filter by start time'), { target: { value: '' } });
    await view.user.click(screen.getByRole('button', { name: 'Search' }));
    expect(onApply).toHaveBeenLastCalledWith({
      filters: { model: 'seeded' },
      fromUnix: undefined,
      toUnix: 75,
    });
  });

  it('preserves a reversed time draft, reports it, and ignores the invalid range', async () => {
    const onApply = vi.fn();
    const view = await renderWithProviders(
      <LogFilters
        station="user"
        fields={fields}
        state={{ filters: {}, fromUnix: 75, toUnix: 0 }}
        onApply={onApply}
      />,
      { station: 'user' },
    );
    await view.user.click(screen.getByRole('button', { name: 'Search' }));
    expect(onApply).toHaveBeenCalledWith({ filters: {}, fromUnix: 75, toUnix: 0 });
    expect(screen.getByRole('alert')).toBeVisible();
    expect(screen.getByLabelText('Filter by start time')).toHaveValue('1970-01-01T00:01');
  });

  it('does not disable search for an unresolved time or send an invalid status', async () => {
    const onApply = vi.fn();
    const view = await renderWithProviders(
      <LogFilters station="user" fields={fields} state={{ filters: {} }} onApply={onApply} />,
      { station: 'user' },
    );
    await view.user.click(screen.getByText('More filters', { exact: true }));
    fireEvent.change(screen.getByLabelText('Filter by start time'), {
      target: { value: '2026-09-08T12:34' },
    });
    fireEvent.change(screen.getByLabelText('Status'), { target: { value: '99' } });
    const search = screen.getByRole('button', { name: 'Search' });
    expect(search).toBeEnabled();
    await view.user.click(search);
    expect(onApply).toHaveBeenCalledWith({ filters: {}, fromUnix: undefined, toUnix: undefined });
    expect(screen.getByRole('alert')).toBeVisible();
  });

  it('applies selects immediately without submitting or discarding unfinished text', async () => {
    const onApply = vi.fn();
    function Harness() {
      const [state, setState] = useState<Pick<LogUrlState, 'filters' | 'fromUnix' | 'toUnix'>>({
        filters: {},
      });
      return (
        <LogFilters
          station="user"
          fields={fields}
          state={state}
          onApply={(value) => {
            onApply(value);
            setState(value);
          }}
        />
      );
    }
    const view = await renderWithProviders(<Harness />, { station: 'user' });
    await view.user.type(screen.getByLabelText('Model'), 'draft');
    expect(onApply).not.toHaveBeenCalled();
    await view.user.click(screen.getByText('More filters', { exact: true }));
    await view.user.selectOptions(screen.getByLabelText('Category'), 'network');
    expect(onApply).toHaveBeenLastCalledWith({ filters: { category: 'network' } });
    expect(screen.getByLabelText('Model')).toHaveValue('draft');
    await view.user.type(screen.getByLabelText('Model'), '{Enter}');
    expect(onApply).toHaveBeenLastCalledWith({
      filters: { category: 'network', model: 'draft' },
      fromUnix: undefined,
      toUnix: undefined,
    });
    await view.user.click(screen.getByRole('button', { name: 'Reset' }));
    expect(screen.getByLabelText('Model')).toHaveValue('');
    expect(onApply).toHaveBeenLastCalledWith({
      filters: {},
      fromUnix: undefined,
      toUnix: undefined,
    });
  });

  it('rebuilds drafts when navigation changes the applied URL', async () => {
    const onApply = vi.fn();
    const view = await renderWithProviders(
      <LogFilters station="user" fields={fields} state={{ filters: {} }} onApply={onApply} />,
      { station: 'user' },
    );
    const from = screen.getByLabelText('Filter by start time');
    const epoch = Date.parse('2026-09-08T12:00:45Z') / 1000;
    view.rerender(
      <LogFilters
        station="user"
        fields={fields}
        state={{ filters: { model: 'seeded' }, fromUnix: epoch, toUnix: epoch + 60 }}
        onApply={onApply}
      />,
    );
    await waitFor(() => expect(from).toHaveValue('2026-09-08T12:00'));
    expect(screen.getByLabelText('Model')).toHaveValue('seeded');
  });

  it('keeps quick ranges exact and leaves an unfinished text search alone', async () => {
    const now = 1800000000;
    vi.spyOn(Date, 'now').mockReturnValue(now * 1000);
    const onApply = vi.fn();
    function Harness() {
      const [state, setState] = useState<Pick<LogUrlState, 'filters' | 'fromUnix' | 'toUnix'>>({
        filters: { model: 'applied' },
      });
      return (
        <LogFilters
          station="user"
          fields={fields}
          state={state}
          onApply={(value) => {
            onApply(value);
            setState(value);
          }}
        />
      );
    }
    const view = await renderWithProviders(<Harness />, { station: 'user' });
    fireEvent.change(screen.getByLabelText('Model'), { target: { value: 'draft' } });
    await view.user.click(screen.getByRole('button', { name: 'Last hour' }));
    expect(onApply).toHaveBeenCalledWith({
      filters: { model: 'applied' },
      fromUnix: now - 3600,
      toUnix: now,
    });
    expect(screen.getByLabelText('Model')).toHaveValue('draft');
  });
});
