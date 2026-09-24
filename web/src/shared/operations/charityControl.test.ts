import { QueryClient } from '@tanstack/react-query';
import { describe, expect, it } from 'vitest';
import {
  beginManagementSessionRequest,
  charityManagementKeys,
  noteManagementSessionSuccess,
} from '@shared/charityManagement';
import { charityScopePath, excludedFields, halfPrice } from './charityScope';
import { normalizeCharitySuccess } from './charitySuccess';

describe('charity control boundaries', () => {
  it.each([
    ['0', '0'],
    ['0.001', '0'],
    ['0.003', '0.001'],
    ['3.127', '1.563'],
    ['9000000000000', '4500000000000'],
  ])('halves %s in integer milli-credits', (input, output) => {
    expect(halfPrice(input)).toBe(output);
  });

  it('rejects invalid or oversized prices instead of truncating them', () => {
    for (const value of ['', '01', '-1', '1.0', '0.010', '1.0001', '1e3', '9000000000000.001'])
      expect(halfPrice(value)).toBeNull();
  });

  it('scopes management paths and rejects ambiguous model identities', () => {
    expect(charityScopePath('/api/steward/donation-sources?page=1', '12')).toBe(
      '/api/steward/donation-sources?page=1&charity_model_id=12',
    );
    expect(() => charityScopePath('/api/steward/donations', '01')).toThrow();
  });

  it('deduplicates exact top-level exclusions while retaining protected fields', () => {
    expect(excludedFields('temperature, top_p temperature')).toEqual(['temperature', 'top_p']);
    for (const value of [
      'model',
      'messages',
      'stream',
      'input',
      'tools',
      'response_format',
      'input.text',
      'input[0]',
      Array.from({ length: 33 }, (_, i) => `field_${i}`).join(','),
    ])
      expect(excludedFields(value)).toBeNull();
  });

  it('does not reuse a full-steward projection after the same account becomes a trainee', () => {
    const client = new QueryClient();
    const session = (level: number) => ({
      user: { id: '5', username: 'fixture-steward', effective_level: level },
    });
    const accept = (level: number) =>
      noteManagementSessionSuccess(
        client,
        'steward',
        session(level),
        beginManagementSessionRequest(client, 'steward'),
      );
    expect(accept(6)).toBe(true);
    const key = charityManagementKeys.donations('steward', 1, 'all');
    client.setQueryData(key, { privateDonation: true });
    expect(accept(5)).toBe(true);
    expect(client.getQueryData(key)).toBeUndefined();
    expect(client.getQueryData(charityManagementKeys.capability('steward'))).toBe(false);
    expect(accept(4)).toBe(true);
    expect(client.getQueryData(charityManagementKeys.capability('steward'))).toBe(true);
    client.clear();
  });

  it('keeps cancellations outside the success denominator and requires explicit sample quality', () => {
    const value = {
      window_start: 0,
      as_of: 86400,
      success: 18,
      failure: 2,
      cancelled: 5,
      sample_count: 20,
      rate: 0.9,
      insufficient_sample: false,
      capture_started_at: 0,
    };
    expect(normalizeCharitySuccess(value)).toEqual(value);
    expect(() => normalizeCharitySuccess({ ...value, sample_count: 25 })).toThrow();
    expect(() => normalizeCharitySuccess({ ...value, insufficient_sample: true })).toThrow();
    expect(
      normalizeCharitySuccess({
        ...value,
        success: 0,
        failure: 0,
        sample_count: 0,
        rate: null,
        insufficient_sample: true,
      }).rate,
    ).toBeNull();
  });
});
