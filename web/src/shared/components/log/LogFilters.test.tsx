import { fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { installJsonFetchFixtures, renderWithProviders } from '../../../../test/unit/support';
import { LogFilters, type LogFilterField } from './LogFilters';

const nativeOptions = Intl.DateTimeFormat.prototype.resolvedOptions;
const fields: readonly LogFilterField[] = [
  { name: 'model', label: 'Model', ariaLabel: 'Model', maxLength: 133 },
];

function installTimeZoneFixture(station: 'user' | 'admin') {
  const prefix = station === 'admin' ? '/admin/api' : '/api';
  return installJsonFetchFixtures([
    {
      method: 'GET',
      path: `${prefix}/time-zones`,
      body: { version: 'go1.26.6-zoneinfo', zones: ['UTC'] },
    },
  ]);
}

beforeEach(() => {
  vi.spyOn(Intl.DateTimeFormat.prototype, 'resolvedOptions').mockImplementation(function (
    this: Intl.DateTimeFormat,
  ) {
    return { ...nativeOptions.call(this), timeZone: 'UTC' };
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('shared log time filters', () => {
  it('keeps a reversed range visible and does not apply it', async () => {
    installTimeZoneFixture('user');
    const onApply = vi.fn();
    await renderWithProviders(
      <LogFilters
        station="user"
        fields={fields}
        state={{ filters: {}, fromUnix: 75, toUnix: 0 }}
        onApply={onApply}
      />,
      { station: 'user' },
    );
    fireEvent.submit(screen.getByTestId('log-filters'));
    expect(onApply).not.toHaveBeenCalled();
    expect(screen.getByRole('alert')).toBeVisible();
    expect(screen.getByLabelText('Filter by start time')).toHaveValue('1970-01-01T00:01');
  });

  it('keeps epoch zero and original seconds when applying a seeded range', async () => {
    installTimeZoneFixture('user');
    const onApply = vi.fn();
    const end = 75;
    await renderWithProviders(
      <LogFilters
        station="user"
        fields={fields}
        state={{ filters: { model: 'gpt-test' }, fromUnix: 0, toUnix: end }}
        onApply={onApply}
      />,
      { station: 'user' },
    );

    expect(screen.getByLabelText('Filter by start time')).toHaveValue('1970-01-01T00:00');
    fireEvent.submit(screen.getByTestId('log-filters'));
    expect(onApply).toHaveBeenCalledWith({
      filters: { model: 'gpt-test' },
      fromUnix: 0,
      toUnix: end,
    });
  });

  it('rebuilds drafts when the applied URL seed changes', async () => {
    installTimeZoneFixture('user');
    const onApply = vi.fn();
    const view = await renderWithProviders(
      <LogFilters station="user" fields={fields} state={{ filters: {} }} onApply={onApply} />,
      { station: 'user' },
    );
    const from = screen.getByLabelText('Filter by start time');
    expect(from).toHaveValue('');

    const epoch = Date.parse('2026-09-08T12:00:45Z') / 1_000;
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

  it('maps an explicitly cleared field to an unset wire value', async () => {
    installTimeZoneFixture('user');
    const onApply = vi.fn();
    await renderWithProviders(
      <LogFilters
        station="user"
        fields={fields}
        state={{ filters: {}, fromUnix: 0, toUnix: 75 }}
        onApply={onApply}
      />,
      { station: 'user' },
    );

    fireEvent.change(screen.getByLabelText('Filter by start time'), { target: { value: '' } });
    fireEvent.submit(screen.getByTestId('log-filters'));
    expect(onApply).toHaveBeenCalledWith({
      filters: {},
      fromUnix: undefined,
      toUnix: 75,
    });
  });

  it('uses the explicit station path and keeps quick ranges exact', async () => {
    const fetchMock = installTimeZoneFixture('admin');
    const onApply = vi.fn();
    const now = 1_800_000_000;
    vi.spyOn(Date, 'now').mockReturnValue(now * 1_000);
    const view = await renderWithProviders(
      <LogFilters
        station="admin"
        fields={fields}
        state={{ filters: { model: 'admin-model' } }}
        onApply={onApply}
      />,
      { station: 'admin' },
    );

    await waitFor(() =>
      expect(fetchMock.mock.calls.map(([path]) => String(path))).toContain('/admin/api/time-zones'),
    );
    expect(fetchMock.mock.calls.map(([path]) => String(path))).not.toContain('/api/time-zones');
    await view.user.click(screen.getByRole('button', { name: 'Last hour' }));
    expect(onApply).toHaveBeenCalledWith({
      filters: { model: 'admin-model' },
      fromUnix: now - 3_600,
      toUnix: now,
    });
  });

  it('disables and rechecks submit while an edited time is unresolved', async () => {
    installTimeZoneFixture('user');
    const onApply = vi.fn();
    await renderWithProviders(
      <LogFilters station="user" fields={fields} state={{ filters: {} }} onApply={onApply} />,
      { station: 'user' },
    );
    const from = screen.getByLabelText('Filter by start time');
    fireEvent.change(from, { target: { value: '2026-09-08T12:34' } });
    expect(screen.getByRole('button', { name: 'Apply filter' })).toBeDisabled();
    fireEvent.submit(screen.getByTestId('log-filters'));
    expect(onApply).not.toHaveBeenCalled();
    expect(from).toHaveValue('2026-09-08T12:34');
  });
});
