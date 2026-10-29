import { expect, it } from 'vitest';
import { normalizeUserDonationHistoryPage } from './userDonationHistory';

function item() {
  return {
    id: '21',
    status: 'pending',
    description: 'Retained donation',
    review_result: null,
    created_at: 1800000000,
    updated_at: 1800000000,
    key_count: '1',
    source_count: '1',
    owner_hidden_at: null,
    handling: {
      state: 'pending',
      revision: '1',
      processed_at: null,
      processed_by_role: null,
      closed_at: null,
      closed_reason: null,
    },
  };
}

function page(row: unknown) {
  return {
    data: [row],
    next_cursor: null,
    pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1' },
  };
}

it('reads history without requiring unused management metadata', () => {
  const expected = normalizeUserDonationHistoryPage(page(item()), '1', 20);
  const extended = {
    ...item(),
    sources: null,
    owner: { future_profile: true },
    reviewer: { role: 'future_role' },
    state_counts: null,
    revision: null,
    discord_public_thanks: 'unused',
  };
  expect(normalizeUserDonationHistoryPage(page(extended), '1', 20)).toEqual(expected);
});

it('renders retained review reasons and logical expiry without unrelated review fields', () => {
  const value = item();
  const result = normalizeUserDonationHistoryPage(
    page({
      ...value,
      status: 'expired',
      review_result: { reason: 'Retained reason\nwith a second line', future_detail: true },
      handling: { ...value.handling, state: 'closed', closed_reason: 'expired' },
    }),
    '1',
    20,
  );
  expect(result.data[0].review_result?.reason).toContain('second line');
  expect(result.data[0].handling).toMatchObject({ state: 'closed', closed_at: null });
});

it.each([
  { id: null },
  { created_at: 'not a timestamp' },
  { source_count: '-1' },
  { handling: null },
  { review_result: { reason: null } },
])('still rejects invalid displayed values: %j', (invalid) => {
  expect(() =>
    normalizeUserDonationHistoryPage(page({ ...item(), ...invalid }), '1', 20),
  ).toThrow();
});
