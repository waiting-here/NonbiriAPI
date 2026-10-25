import { describe, expect, it } from 'vitest';
import type { AgentMessage } from './api';
import { indexToolResults, readToolCalls } from './toolActivityData';

function message(id: string, fields: Partial<AgentMessage> = {}): AgentMessage {
  return {
    id,
    role: 'assistant',
    content: '',
    created_at: 1,
    active: true,
    generation: '1',
    ...fields,
  };
}

function call(id: string, name = 'donations', args: unknown = '{}') {
  return { id, type: 'function', function: { name, arguments: args } };
}

function result(id: string, callID: string, generation = '1'): AgentMessage {
  return message(id, {
    role: 'tool',
    content: '{"items":[]}',
    tool_call_id: callID,
    generation,
  });
}

describe('readToolCalls', () => {
  it('preserves raw arguments so presentation can parse JSON exactly once', () => {
    const args = { donation_id: '7' };
    const entries = [
      call('json', 'donations', '{"donation_id":"7"}'),
      call('scalar', 'site_status', '"123"'),
      call('invalid', 'web_search', '{unfinished'),
      call('object', 'donations', args),
      call('null', 'donations', null),
    ];
    const calls = readToolCalls(message('1', { tool_calls: entries }));

    expect(calls.map((item) => item.arguments)).toEqual([
      '{"donation_id":"7"}',
      '"123"',
      '{unfinished',
      args,
      null,
    ]);
    expect(calls[3].arguments).toBe(args);
    calls.forEach((item, index) => expect(item.raw).toBe(entries[index]));
    expect(calls[0]).toMatchObject({ id: 'json', name: 'donations' });
  });

  it('keeps malformed and unknown array entries at their original indices', () => {
    const entries = [
      null,
      'unexpected',
      0,
      false,
      ['nested'],
      { custom: 'visible detail' },
      { id: 'known-id', function: { name: 123, arguments: 'raw' } },
      { id: 123, function: { name: 'future_tool', arguments: false } },
    ];
    const calls = readToolCalls(message('1', { tool_calls: entries }));

    expect(calls).toHaveLength(entries.length);
    calls.forEach((item, index) => expect(item.raw).toBe(entries[index]));
    expect(calls.slice(0, 6).every((item) => item.id === undefined && item.name === '')).toBe(true);
    expect(calls[6]).toMatchObject({ id: 'known-id', name: '', arguments: 'raw' });
    expect(calls[7]).toMatchObject({ name: 'future_tool', arguments: false });
    expect(calls[7].id).toBeUndefined();
  });

  it.each([null, false, 0, 'raw', { id: 'not-a-call-array', future: true }])(
    'keeps a malformed container visible without guessing a call ID: %j',
    (raw) => {
      expect(readToolCalls(message('1', { tool_calls: raw }))).toEqual([
        { name: '', arguments: undefined, raw },
      ]);
    },
  );

  it('returns no calls when the field is absent or the array is empty', () => {
    expect(readToolCalls(message('1'))).toEqual([]);
    expect(readToolCalls(message('1', { tool_calls: [] }))).toEqual([]);
  });
});

describe('indexToolResults', () => {
  it('keeps repeated synthetic IDs attached to their own successive turns', () => {
    const first = result('2', 'agent_driver');
    const second = result('5', 'agent_driver');
    const indexed = indexToolResults([
      message('1', { tool_calls: [call('agent_driver')] }),
      first,
      message('3', { role: 'user', content: 'Continue' }),
      message('4', { tool_calls: [call('agent_driver')] }),
      second,
    ]);

    expect(indexed.results.get('1')?.get(0)).toBe(first);
    expect(indexed.results.get('4')?.get(0)).toBe(second);
    expect([...indexed.consumed]).toEqual(['2', '5']);
  });

  it('selects the latest unmatched predecessor without overwriting an earlier result', () => {
    const latest = result('3', 'repeat');
    const earlier = result('4', 'repeat');
    const surplus = result('5', 'repeat');
    const indexed = indexToolResults([
      message('1', { tool_calls: [call('repeat')] }),
      message('2', { tool_calls: [call('repeat')] }),
      latest,
      earlier,
      surplus,
    ]);

    expect(indexed.results.get('2')?.get(0)).toBe(latest);
    expect(indexed.results.get('1')?.get(0)).toBe(earlier);
    expect([...indexed.consumed]).toEqual(['3', '4']);
  });

  it('never associates a reused ID with a different generation', () => {
    const oldResult = result('4', 'repeat', '1');
    const newResult = result('5', 'repeat', '2');
    const indexed = indexToolResults([
      message('1', { generation: '1', active: false, tool_calls: [call('repeat')] }),
      message('2', { generation: '2', tool_calls: [call('repeat')] }),
      result('3', 'repeat', '3'),
      oldResult,
      newResult,
    ]);

    expect(indexed.results.get('1')?.get(0)).toBe(oldResult);
    expect(indexed.results.get('2')?.get(0)).toBe(newResult);
    expect([...indexed.consumed]).toEqual(['4', '5']);
  });

  it('leaves a partial-page orphan visible even if its ID appears later', () => {
    const orphan = result('1', 'repeat');
    const matched = result('3', 'repeat');
    const indexed = indexToolResults([
      orphan,
      message('2', { tool_calls: [call('repeat')] }),
      matched,
    ]);

    expect(indexed.results.get('2')?.get(0)).toBe(matched);
    expect([...indexed.consumed]).toEqual(['3']);
    expect(indexToolResults([orphan]).results.size).toBe(0);
  });

  it('associates parallel out-of-order results using original call indices', () => {
    const first = result('3', 'a');
    const second = result('2', 'b');
    const indexed = indexToolResults([
      message('1', { tool_calls: [call('a'), null, call('b')] }),
      second,
      first,
    ]);

    expect(indexed.results.get('1')?.get(0)).toBe(first);
    expect(indexed.results.get('1')?.get(2)).toBe(second);
    expect(indexed.results.get('1')?.has(1)).toBe(false);
    expect([...indexed.consumed]).toEqual(['2', '3']);
  });

  it('does not consume missing identifiers or treat non-assistant content as calls', () => {
    const indexed = indexToolResults([
      message('1', { role: 'user', tool_calls: [call('user-call')] }),
      message('2', { tool_calls: { id: 'malformed-container' } }),
      message('3', { tool_calls: [{ id: 12 }, call('')] }),
      result('4', 'user-call'),
      result('5', 'malformed-container'),
      result('6', '12'),
      result('7', ''),
      message('8', { role: 'tool', content: 'No call ID' }),
    ]);

    expect(indexed.results.size).toBe(0);
    expect(indexed.consumed.size).toBe(0);
  });
});
