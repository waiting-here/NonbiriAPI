import { describe, expect, it } from 'vitest';
import { normalizeEndpointCreateOptions } from '../normalizers';
import en from './matching-copy/en.json';
import zh from './matching-copy/zh.json';

describe('mainstream matching projection', () => {
  const channelID = `mch_${'A'.repeat(22)}`;
  const match = { channel_id: channelID, entry_id: '2' };
  const wire = {
    base_connector_types: ['openai-compatible'],
    mainstream_channels: [
      {
        id: channelID,
        name: 'Channel',
        connector_type: 'openai-compatible',
        base_url: 'https://example.com/v1',
        entries: [
          {
            id: '2',
            name: 'Entry',
            connector_type: 'openai-compatible',
            base_url: 'https://example.com/v1',
            native_services: ['chat_completions'],
            rerank_format: '',
            enabled: true,
          },
        ],
      },
    ],
  };
  it('accepts current entry identities and preserves an explicit empty result', () => {
    expect(
      normalizeEndpointCreateOptions({ ...wire, matching_mainstream_entries: [match] })
        .matching_mainstream_entries,
    ).toEqual([match]);
    expect(
      normalizeEndpointCreateOptions({ ...wire, matching_mainstream_entries: [] })
        .matching_mainstream_entries,
    ).toEqual([]);
    expect(normalizeEndpointCreateOptions(wire)).not.toHaveProperty('matching_mainstream_entries');
  });
  it('rejects missing, disabled, duplicate, and malformed matches', () => {
    for (const matching_mainstream_entries of [
      [{ ...match, entry_id: '3' }],
      [{ ...match, channel_id: `mch_${'B'.repeat(21)}A` }],
      [match, match],
      [{ ...match, entry_id: '02' }],
    ]) {
      expect(() =>
        normalizeEndpointCreateOptions({ ...wire, matching_mainstream_entries }),
      ).toThrow();
    }
    const disabled = {
      ...wire,
      mainstream_channels: wire.mainstream_channels.map((channel) => ({
        ...channel,
        entries: channel.entries.map((entry) => ({ ...entry, enabled: false })),
      })),
    };
    expect(() =>
      normalizeEndpointCreateOptions({ ...disabled, matching_mainstream_entries: [match] }),
    ).toThrow();
  });
  it('keeps suggestion copy paired in both interface languages', () => {
    expect(Object.keys(en).sort()).toEqual(Object.keys(zh).sort());
    expect(Object.values(en).every((value) => Boolean(value.trim()))).toBe(true);
    expect(Object.values(zh).every((value) => Boolean(value.trim()))).toBe(true);
  });
});
