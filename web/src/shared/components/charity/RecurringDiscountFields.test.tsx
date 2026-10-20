import { fireEvent, screen, within } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { defaultRecurringDiscount } from '@shared/operations/recurringDiscount';
import { installJsonFetchFixtures, renderWithProviders } from '../../../../test/unit/support';
import { RecurringDiscountFields } from './RecurringDiscountFields';

afterEach(() => vi.unstubAllGlobals());

function Editor() {
  const [value, setValue] = useState(defaultRecurringDiscount());
  return (
    <>
      <RecurringDiscountFields value={value} onChange={setValue} role="admin" fixedEnabled />
      <output aria-label="Draft">{JSON.stringify(value)}</output>
    </>
  );
}

it('edits a paused schedule, preserves zero and makes all-day and 24:00 reversible', async () => {
  installJsonFetchFixtures([
    {
      method: 'GET',
      path: '/admin/api/time-zones',
      body: {
        version: 'go1.26.6-zoneinfo',
        zones: ['Asia/Tokyo', 'UTC'],
      },
    },
  ]);
  const view = await renderWithProviders(<Editor />, { station: 'admin', role: 'admin' });
  await view.user.click(screen.getByRole('switch', { name: 'Enable recurring discount' }));
  expect(screen.getByText(/Recurring discounts are paused/)).toBeVisible();
  fireEvent.change(screen.getByLabelText('Schedule time zone'), {
    target: { value: 'Asia/Tokyo' },
  });
  fireEvent.change(screen.getByLabelText('Default payable percentage'), { target: { value: '0' } });
  await view.user.click(screen.getByRole('button', { name: 'Add period' }));
  const period = within(screen.getByRole('group', { name: 'Period 1' }));
  fireEvent.change(period.getByLabelText('Start time'), { target: { value: '22:30' } });
  fireEvent.change(period.getByLabelText('End time', { exact: true }), {
    target: { value: '02:00' },
  });
  await view.user.click(period.getByRole('checkbox', { name: 'Sun' }));
  await view.user.click(period.getByRole('checkbox', { name: 'All day (00:00–24:00)' }));
  expect(period.getByLabelText('Start time')).toHaveValue('00:00');
  expect(period.getByLabelText('End time', { exact: true })).toBeDisabled();
  expect(period.getByRole('checkbox', { name: 'End at 24:00' })).toBeChecked();
  await view.user.click(period.getByRole('checkbox', { name: 'All day (00:00–24:00)' }));
  expect(period.getByLabelText('Start time')).toHaveValue('22:30');
  expect(period.getByLabelText('End time', { exact: true })).toHaveValue('02:00');
  expect(period.getByText(/Ends the next day/)).toBeVisible();
  await view.user.click(period.getByRole('checkbox', { name: 'End at 24:00' }));
  expect(JSON.parse(screen.getByLabelText('Draft').textContent!)).toMatchObject({
    enabled: true,
    time_zone: 'Asia/Tokyo',
    default_percent: 0,
    periods: [
      { weekdays: [1, 2, 3, 4, 5, 7], start_time: '22:30', end_time: '24:00', percent: 100 },
    ],
  });
  await view.user.click(period.getByRole('button', { name: 'Remove period 1' }));
  expect(JSON.parse(screen.getByLabelText('Draft').textContent!).periods).toEqual([]);
});

it('bounds normal editing to 16 periods while keeping a removal route', async () => {
  installJsonFetchFixtures([
    {
      method: 'GET',
      path: '/admin/api/time-zones',
      body: { version: 'go1.26.6-zoneinfo', zones: ['UTC'] },
    },
  ]);
  const view = await renderWithProviders(<Editor />, { station: 'admin', role: 'admin' });
  const add = screen.getByRole('button', { name: 'Add period' });
  for (let count = 0; count < 16; count++) await view.user.click(add);
  expect(add).toBeDisabled();
  await view.user.click(screen.getByRole('button', { name: 'Remove period 16' }));
  expect(add).toBeEnabled();
});
