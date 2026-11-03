import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { SnapshotChanges } from './SnapshotChanges';
import type { Item } from './api';
import { DisplayTimeContext } from '@shared/components/timeContextValue';

const item: Item = {
  ordinal: '1',
  action: 'update',
  target_kind: 'user',
  target_id: '2',
  target_name: 'Saved user',
  outcome: 'succeeded',
  code: '',
  changes: [],
};

describe('transaction snapshot details', () => {
  it('uses site time for retained cooldown values in management history', async () => {
    await renderWithProviders(
      <DisplayTimeContext.Provider value={{ mode: 'site', offset_minutes: 480 }}>
        <SnapshotChanges
          item={{
            ...item,
            changes: [
              {
                field: 'retry_after_until',
                operation: 'retained',
                unit: 'unix_seconds',
                after: String(Date.parse('2026-11-04T06:41:00Z') / 1000),
              },
            ],
          }}
        />
      </DisplayTimeContext.Provider>,
      { station: 'admin', locale: 'en' },
    );
    expect(screen.getByText('11/04/2026, 02:41 PM')).toBeVisible();
    expect(screen.queryByText('11/04/2026, 01:41 AM')).not.toBeInTheDocument();
  });

  it('distinguishes legacy missing details from retained deletion and permission values', async () => {
    await renderWithProviders(
      <>
        <SnapshotChanges item={item} />
        <SnapshotChanges
          item={{
            ...item,
            changes: [
              { field: 'permissions.users.ban', operation: 'updated', before: '0', after: '1' },
            ],
            details: {
              version: 1,
              state: 'deleted',
              previous_name: 'Original name',
              truncated: true,
            },
          }}
        />
      </>,
      { station: 'admin', role: 'admin' },
    );
    expect(screen.getByText(/were not recorded at the time/)).toBeVisible();
    expect(screen.getByText('Previous name: Original name')).toBeVisible();
    expect(
      screen.getByText('Deleted in this operation; name retained before deletion'),
    ).toBeVisible();
    expect(screen.getByText('Ban users')).toBeVisible();
    expect(screen.getByText('Not granted')).toBeVisible();
    expect(screen.getByText('Allowed')).toBeVisible();
    expect(screen.getByText(/Only a sample/)).toBeVisible();
  });
});
