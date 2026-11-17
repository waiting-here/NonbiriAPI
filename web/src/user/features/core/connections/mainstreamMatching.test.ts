import { channelPreset } from '../../../../../test/fixtures/resourceScopes';
import { describe, expect, it } from 'vitest';
import { normalizeEndpointCreateOptions } from '../normalizers';
import en from './matching-copy/en.json';
import zh from './matching-copy/zh.json';

describe('mainstream matching projection', () => {
  const channelID = `mch_${'A'.repeat(22)}`;
  const match = { channel_id: channelID, entry_id: '2' };
  const wire = {
    native_service_options: [],
    base_connector_types: ['openai-compatible'],
    mainstream_channels: [
      {
        ...channelPreset,
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
  it('keeps usable channel options when another matched entry is absent from the list', () => {
    const matches = [match, { ...match, entry_id: '3' }];
    const options = normalizeEndpointCreateOptions({
      ...wire,
      matching_mainstream_entries: matches,
    });
    expect(options.matching_mainstream_entries).toEqual(matches);
    expect(options.mainstream_channels[0]?.entries[0]?.id).toBe('2');
  });

  it('rejects malformed channel and entry identities', () => {
    for (const invalid of [
      { ...match, entry_id: '02' },
      { ...match, channel_id: 'invalid' },
    ])
      expect(() =>
        normalizeEndpointCreateOptions({ ...wire, matching_mainstream_entries: [invalid] }),
      ).toThrow();
  });
  it('keeps suggestion copy paired in both interface languages', () => {
    expect(Object.keys(en).sort()).toEqual(Object.keys(zh).sort());
    expect(Object.values(en).every((value) => Boolean(value.trim()))).toBe(true);
    expect(Object.values(zh).every((value) => Boolean(value.trim()))).toBe(true);
  });
});
