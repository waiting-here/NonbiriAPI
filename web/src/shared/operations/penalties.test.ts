import { describe, expect, it } from 'vitest';
import {
  evidencePage,
  penalty,
  penaltyAction,
  penaltyPage,
} from '../../../test/fixtures/penalties';
import {
  normalizePenaltyDetail,
  normalizePenaltyEvidence,
  normalizePenaltyList,
} from './penalties';
import { readLoginRestrictions } from './restrictions';

describe('penalty boundary', () => {
  it('validates a complete page and rejects truncation, mixed owners and hidden additions', () => {
    const value = { ...penaltyPage([penalty]), legacy_details_unavailable: false };
    expect(normalizePenaltyList(value, '1', 20).data[0].id).toBe(penalty.id);
    expect(() =>
      normalizePenaltyList(
        { ...value, pagination: { ...value.pagination, total_items: '2' } },
        '1',
        20,
      ),
    ).toThrow();
    expect(() =>
      normalizePenaltyList({ ...value, data: [{ ...penalty, rules: {} }] }, '1', 20),
    ).toThrow();
    expect(() =>
      normalizePenaltyList({ ...value, data: [{ ...penalty, state: 'active' }] }, '1', 20),
    ).toThrow();
    expect(
      normalizePenaltyDetail({ case: penalty, actions: penaltyPage([penaltyAction]) }, '1', 20)
        .actions.data[0].request_log_available,
    ).toBe(false);
  });
  it('requires complete frozen evidence and bounded unique members', () => {
    const value = evidencePage();
    expect(normalizePenaltyEvidence(value, '1', 20).members.data).toHaveLength(20);
    expect(normalizePenaltyEvidence(evidencePage('2'), '2', 20).members.data).toHaveLength(1);
    expect(() => normalizePenaltyEvidence({ ...value, rules: { version: 1 } }, '1', 20)).toThrow();
    expect(() =>
      normalizePenaltyEvidence(
        { ...value, statistics: { ...value.statistics, raw_body: 'secret' } },
        '1',
        20,
      ),
    ).toThrow();
    expect(() =>
      normalizePenaltyEvidence(
        { ...value, members: { ...value.members, data: Array(20).fill(value.members.data[0]) } },
        '1',
        20,
      ),
    ).toThrow();
  });
  it('accepts only a safe bounded verified-login display projection', () => {
    const safe = {
      kind: 'ban',
      reason_code: 'charity_rpm',
      reason: 'Access restricted.',
      started_at: 1,
      ends_at: 10,
    };
    const fragment = (value: unknown) =>
      '#restrictions=' +
      btoa(JSON.stringify(value)).replaceAll('+', '-').replaceAll('/', '_').replaceAll('=', '');
    expect(readLoginRestrictions(fragment([safe]))).toEqual([safe]);
    for (const value of [
      [{ ...safe, threshold: 10 }],
      [{ ...safe, user_id: '7' }],
      [safe, safe],
      [{ ...safe, ends_at: 0 }],
      [{ ...safe, kind: 'admin' }],
    ])
      expect(readLoginRestrictions(fragment(value))).toEqual([]);
    expect(readLoginRestrictions('#restrictions=' + 'A'.repeat(6001))).toEqual([]);
    expect(readLoginRestrictions('#user_id=7')).toEqual([]);
  });
});
