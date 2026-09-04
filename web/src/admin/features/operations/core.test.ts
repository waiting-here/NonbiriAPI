import { describe, expect, it } from 'vitest';
import {
  normalizeActivityPage,
  normalizeLegalHoldSummary,
  normalizeSiteConfigCatalogEntry,
  normalizeSiteTimezoneOffset,
} from './core';

const text = { zh: '', en: '' };
const catalog = {
  key: 'maintenance_mode', group: 'access', type: 'boolean', title: text, description: text, unit: null,
  nullable: false, null_writable: false, raw_default: true, effective_fallback: false,
  minimum: null, maximum: null, step: null, allowed_values: [], zero_semantics: text,
  null_semantics: text, empty_semantics: text, independent_gates: [], write_endpoint: '',
};

describe('administrator core wire', () => {
  it('accepts the empty write endpoint used by dedicated and read-only settings', () => {
    expect(normalizeSiteConfigCatalogEntry(catalog).write_endpoint).toBe('');
    expect(() => normalizeSiteConfigCatalogEntry({ ...catalog, write_endpoint: '/api/site-config/maintenance_mode' })).toThrow(/write endpoint/i);
  });

  it('requires canonical opaque references for held maintenance and report roots', () => {
    const base = { id: `lgh_${'A'.repeat(22)}`, state: 'active', revision: '1', created_at: 1, expires_at: 2, ended_at: null };
    expect(normalizeLegalHoldSummary({ ...base, object_kind: 'maintenance_event', object_ref: `op_${'A'.repeat(22)}` }).object_kind).toBe('maintenance_event');
    expect(() => normalizeLegalHoldSummary({ ...base, object_kind: 'report_case', object_ref: `rpc_${'A'.repeat(21)}B` })).toThrow(/report case/i);
  });

  it('accepts the activity day shape emitted by the administrator runtime', () => {
    const row = {
      day: 1_788_451_200,
      product_active: true,
      api_requests: '3',
      uncached_input_tokens: '5',
      cache_write_input_tokens: '7',
      cache_read_input_tokens: '11',
      output_tokens: '13',
      checkins: '17',
      console_writes: '19',
      game_active: false,
      game_rounds: '0',
      distinct_product_users: null,
    };

    expect(normalizeActivityPage({ enabled: true, data: [row], next_cursor: null }).data[0])
      .toEqual(row);
    expect(() => normalizeActivityPage({
      enabled: true,
      data: [{ ...row, day: '2026-09-04' }],
      next_cursor: null,
    })).toThrow(/activity day key/i);
    expect(() => normalizeActivityPage({
      enabled: true,
      data: [{ ...row, product_active: '1' }],
      next_cursor: null,
    })).toThrow(/product active state/i);
  });

  it('reads the fixed site offset without depending on unrelated configuration keys', () => {
    expect(normalizeSiteTimezoneOffset({
      revision: '32',
      values: { site_timezone_offset_minutes: 480, unrelated_setting: true },
    })).toBe(480);
    expect(() => normalizeSiteTimezoneOffset({
      revision: '32',
      values: { site_timezone_offset_minutes: 345 },
    })).toThrow(/site timezone offset/i);
  });
});
