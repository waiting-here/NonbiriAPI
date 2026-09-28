import { afterEach, describe, expect, it, vi } from 'vitest';
import deletionSnapshots from '../../../../test/fixtures/account-deletion-alerts.json';
import { installJsonFetchFixtures } from '../../../../test/unit/support';
import { getAdminAlertDetail, normalizeAdminAlertDetail } from './alertDetail';
import {
  normalizeActivityPage,
  normalizeAdminAlert,
  normalizeLegalHoldSummary,
  normalizeSiteConfigCatalogEntry,
  normalizeSiteTimezoneOffset,
  setAdminAlertResolved,
} from './core';

afterEach(() => vi.unstubAllGlobals());

function deletionAlert(snapshot: unknown, resolved = false) {
  return {
    id: '7', kind: 'account_deleted', message: 'Account deleted.', ref: null,
    subject_user_id: null, created_at: 1_700_000_000, resolved,
    resolved_at: resolved ? 1_700_000_001 : null, account_deletion: snapshot,
  };
}

function deletionDetail(snapshot: unknown) {
  return {
    alert: deletionAlert(snapshot), context_version: 0, occurred_facts: [],
    targets: [{ kind: 'deleted_account', id: '7', available: true, status: 'retained' }],
    current_state: [], related_logs: null, resolution_kind: 'legacy',
  };
}

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
      game_checkins: '340282366920938463463374607431768211455',
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

describe('administrator deletion alert wire', () => {
  it.each(Object.entries(deletionSnapshots))('preserves the complete %s snapshot in alerts and details', (_name, snapshot) => {
    expect(normalizeAdminAlert(deletionAlert(snapshot)).account_deletion).toEqual(snapshot);
    expect(normalizeAdminAlertDetail(deletionDetail(snapshot)).alert.account_deletion).toEqual(snapshot);
  });

  it('keeps the full legal multiline Unicode penalty reason', () => {
    const reason = `First\n${'🐟'.repeat(1018)}`;
    const snapshot = { ...deletionSnapshots.v2, ban: { ...deletionSnapshots.v2.ban, reason } };
    expect(Array.from(reason)).toHaveLength(1024);
    expect(normalizeAdminAlert(deletionAlert(snapshot)).account_deletion).toEqual(snapshot);
    expect(normalizeAdminAlert(deletionAlert({ ...snapshot, discord_id: '1'.repeat(128) })).account_deletion)
      .toMatchObject({ discord_id: '1'.repeat(128) });
  });

  it.each([
    ['unknown snapshot fields', { ...deletionSnapshots.v2, private_detail: 'unexpected' }],
    ['partial new metadata', { ...deletionSnapshots.legacy, snapshot_version: 1 }],
    ['metadata without its version', { ...deletionSnapshots.legacy, source: 'unknown' }],
    ['an unsupported version', { ...deletionSnapshots.v2, snapshot_version: 3 }],
    ['a string version', { ...deletionSnapshots.v2, snapshot_version: '2' }],
    ['an absent new field', { ...deletionSnapshots.v2, registered_at: undefined }],
    ['an invalid registration time', { ...deletionSnapshots.v2, registered_at: -1 }],
    ['an invalid deletion time', { ...deletionSnapshots.v2, deleted_at: '1700000000' }],
    ['an invalid level', { ...deletionSnapshots.v2, effective_level: 7 }],
    ['an invalid source', { ...deletionSnapshots.v2, source: 'other' }],
    ['a noncanonical actor', { ...deletionSnapshots.v2, actor_user_id: '01' }],
    ['an invalid blacklist action', { ...deletionSnapshots.v2, blacklist_action: 'removed' }],
    ['unknown reason codes', { ...deletionSnapshots.v2, blacklist_reason_codes: ['other'] }],
    ['too many reason codes', { ...deletionSnapshots.v2, blacklist_reason_codes: Array(3).fill('deletion_debt_evasion') }],
    ['a missing penalty field', { ...deletionSnapshots.v2, ban: { state: 'known', active_at_deletion: true, until: null } }],
    ['an unknown penalty field', { ...deletionSnapshots.v2, ban: { ...deletionSnapshots.v2.ban, extra: true } }],
    ['an invalid penalty state', { ...deletionSnapshots.v2, ban: { ...deletionSnapshots.v2.ban, state: 'expired' } }],
    ['a string penalty flag', { ...deletionSnapshots.v2, ban: { ...deletionSnapshots.v2.ban, active_at_deletion: 'true' } }],
    ['an invalid penalty time', { ...deletionSnapshots.v2, charity_pause: { ...deletionSnapshots.v2.charity_pause, until: 253402300800 } }],
    ['an oversized reason', { ...deletionSnapshots.v2, ban: { ...deletionSnapshots.v2.ban, reason: '🐟'.repeat(1025) } }],
    ['a control character in the reason', { ...deletionSnapshots.v2, ban: { ...deletionSnapshots.v2.ban, reason: 'bad\u0000reason' } }],
    ['a noncanonical amount', { ...deletionSnapshots.v2, general_balance: '-0' }],
    ['a negative donation credit', { ...deletionSnapshots.v2, donation_credit: '-1' }],
    ['a negative paper balance', { ...deletionSnapshots.v2, sketch_paper: '-1' }],
  ])('rejects %s in alerts and details', (_name, snapshot) => {
    expect(() => normalizeAdminAlert(deletionAlert(snapshot))).toThrow();
    expect(() => normalizeAdminAlertDetail(deletionDetail(snapshot))).toThrow();
  });

  it('rejects a deletion snapshot attached to another kind or missing from a deletion alert', () => {
    expect(() => normalizeAdminAlert({ ...deletionAlert(deletionSnapshots.v1), kind: 'fetch_failed' })).toThrow();
    const missing: Record<string, unknown> = deletionAlert(deletionSnapshots.v1);
    delete missing.account_deletion;
    expect(() => normalizeAdminAlert(missing)).toThrow();
    const partial = { ...deletionSnapshots.v2 } as Record<string, unknown>;
    delete partial.registered_at;
    expect(() => normalizeAdminAlert(deletionAlert(partial))).toThrow();
  });

  it.each(Object.entries(deletionSnapshots))('decodes %s snapshots from detail and resolution requests', async (_name, snapshot) => {
    installJsonFetchFixtures([
      { method: 'GET', path: '/admin/api/alerts/7', body: deletionDetail(snapshot) },
      { method: 'POST', path: '/admin/api/alerts/7/resolve', body: deletionAlert(snapshot, true) },
    ]);
    expect((await getAdminAlertDetail('7')).alert.account_deletion).toEqual(snapshot);
    expect((await setAdminAlertResolved('7', true)).account_deletion).toEqual(snapshot);
  });

  it('keeps malformed detail and resolution responses as errors', async () => {
    const partial = { ...deletionSnapshots.legacy, snapshot_version: 1 };
    installJsonFetchFixtures([
      { method: 'GET', path: '/admin/api/alerts/7', body: deletionDetail(partial) },
      { method: 'POST', path: '/admin/api/alerts/7/resolve', body: deletionAlert(partial, true) },
    ]);
    await expect(getAdminAlertDetail('7')).rejects.toMatchObject({ code: 'invalid_response' });
    await expect(setAdminAlertResolved('7', true)).rejects.toMatchObject({ code: 'invalid_response' });
    await expect(getAdminAlertDetail('01')).rejects.toMatchObject({ code: 'invalid_response' });
  });
});
