import type { ActivityDetail, ExchangeResult } from '../../src/shared/limitedactivities/api';
export function limitedActivity(overrides: Partial<ActivityDetail> = {}): ActivityDetail {
  return {
    key: 'picture-book',
    name: 'Picture book',
    visible: false,
    starts_at: 1_800_000_000,
    ends_at: 1_800_003_600,
    paused: false,
    revision: '1',
    status: 'open',
    module_config: {
      paper_price: '1000',
      brush_price: '10000',
      brush_cap: '10',
      brush_exchanged: '0',
      brush_remaining: '10',
    },
    ...overrides,
  };
}
export function activityExchange(): ExchangeResult {
  return {
    receipt: {
      operation_id: 'op_' + 'A'.repeat(22),
      activity_key: 'picture-book',
      config_revision: '1',
      asset: 'sketch_paper',
      quantity: '1',
      unit_price: '1000',
      cost: '1000',
      ledger_seq: '2',
      created_at: 1_800_000_010,
    },
    wallet: { general: '9000', sketch_paper: '1', sketch_brush: '0' },
    supply: limitedActivity().module_config,
  };
}
