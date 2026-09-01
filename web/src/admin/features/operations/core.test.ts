import { describe, expect, it } from 'vitest';
import { normalizeLegalHoldSummary, normalizeSiteConfigCatalogEntry } from './core';

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
});
