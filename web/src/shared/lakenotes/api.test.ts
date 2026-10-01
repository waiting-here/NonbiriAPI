import { describe, expect, it } from 'vitest';
import { decodeDetail } from '@shared/limitedactivities/api';
import { decodeLakeDetail, naturalToUnits, unitsToNatural } from './api';

describe('Lake API amounts and directory', () => {
  it('keeps whole coins and three decimal credits exact beyond Number precision', () => {
    expect(naturalToUnits('9007199254740993', 'coins')).toBe('9007199254740993');
    expect(naturalToUnits('9007199254740993.001', 'general')).toBe('9007199254740993001');
    expect(unitsToNatural('9007199254740993001', 'game')).toBe('9007199254740993.001');
    expect(unitsToNatural('-1', 'general')).toBe('-0.001');
    expect(unitsToNatural('-1200', 'general')).toBe('-1.2');
    for (const value of ['1.0001', '1e3', ' 2', '01', '-1'])
      expect(() => naturalToUnits(value, 'general')).toThrow();
    expect(() => naturalToUnits('1.1', 'coins')).toThrow();
    expect(() => naturalToUnits('0', 'coins')).toThrow();
    expect(naturalToUnits('0', 'general', true)).toBe('0');
  });
  it('accepts closed default projections through the shared activity directory', () => {
    const raw = {
      key: 'lake-notes',
      name: 'Lake Notes',
      cover_key: 'lake-notes',
      visible: false,
      paused: true,
      starts_at: null,
      ends_at: null,
      revision: '1',
      status: 'unconfigured',
      module_config: { periods: [] },
    };
    expect(decodeDetail(raw)).toEqual(decodeLakeDetail(raw));
    expect(() => decodeDetail({ ...raw, key: 'unknown' })).toThrow();
  });
  it('uses only enabled public exchanges while defaulting absent directions off', () => {
    const raw = {
      key: 'lake-notes',
      name: 'Lake Notes',
      cover_key: 'lake-notes',
      visible: true,
      paused: false,
      starts_at: 1,
      ends_at: 100,
      revision: '1',
      status: 'open',
      module_config: {
        periods: [
          {
            id: 'lnp_AAAAAAAAAAAAAAAAAAAAAA',
            revision: '1',
            name: 'Summer',
            starts_at: 1,
            ends_at: 100,
            entry_fee_milli: '0',
            exchanges: {
              general_to_coins: { enabled: true, source_amount: '125', target_amount: '3' },
            },
          },
        ],
      },
    };
    const period = decodeLakeDetail(raw).module_config.periods[0];
    expect(period.status).toBe('published');
    expect(period.exchanges.coins_to_game.enabled).toBe(false);
    expect(period.exchanges.general_to_coins.source_amount).toBe('125');
  });
});
