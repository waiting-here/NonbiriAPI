import type { DebugTrace } from './types';

export type ParameterPresence =
  'absent' | 'null' | 'false' | 'zero' | 'empty_string' | 'empty_array' | 'empty_object' | 'value';

export interface DebugParameter {
  name: string;
  presence: ParameterPresence | string;
  value?: unknown;
  type: string;
  source: string;
  changed: boolean;
  truncated: boolean;
}

// Keep the vocabulary aligned with the server projection. Unknown caller
// fields are retained in their original order; these names only fill useful
// absent rows so that "missing" is never confused with JSON null/false/0.
export const KNOWN_DEBUG_PARAMETERS = [
  'model',
  'stream',
  'temperature',
  'top_p',
  'max_tokens',
  'max_completion_tokens',
  'n',
  'stop',
  'presence_penalty',
  'frequency_penalty',
  'logit_bias',
  'user',
  'tool_choice',
  'response_format',
  'seed',
  'store',
  'safety_identifier',
  'stream_options',
  'parallel_tool_calls',
  'reasoning_effort',
  'reasoning',
  'reasoning_tokens',
  'reasoning_summary',
] as const;

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

export function jsonType(value: unknown): string {
  if (value === null) return 'null';
  if (Array.isArray(value)) return 'array';
  switch (typeof value) {
    case 'string':
      return 'string';
    case 'number':
      return 'number';
    case 'boolean':
      return 'boolean';
    case 'object':
      return 'object';
    default:
      return 'unknown';
  }
}

/** A deterministic JSON-like key for comparing caller/effective values. */
export function stableValueKey(value: unknown): string {
  if (value === null) return 'null';
  if (value === undefined) return 'undefined';
  if (Array.isArray(value)) return `[${value.map(stableValueKey).join(',')}]`;
  if (isRecord(value)) {
    return `{${Object.keys(value)
      .sort()
      .map((key) => `${JSON.stringify(key)}:${stableValueKey(value[key])}`)
      .join(',')}}`;
  }
  if (typeof value === 'number') {
    if (Number.isNaN(value)) return 'number:NaN';
    if (Object.is(value, -0)) return 'number:-0';
  }
  return `${typeof value}:${String(value)}`;
}

export function projectionTruncated(value: unknown): boolean {
  if (!isRecord(value)) return false;
  return (
    value.truncated === true ||
    typeof value.parse_category === 'string' ||
    (typeof value.original_bytes === 'number' &&
      typeof value.captured_bytes === 'number' &&
      value.original_bytes > value.captured_bytes)
  );
}

export function parameterPresence(value: unknown): ParameterPresence {
  if (value === null) return 'null';
  if (value === false) return 'false';
  if (typeof value === 'number' && value === 0) return 'zero';
  if (typeof value === 'string' && value.length === 0) return 'empty_string';
  if (Array.isArray(value) && value.length === 0) return 'empty_array';
  if (isRecord(value) && Object.keys(value).length === 0) return 'empty_object';
  return 'value';
}

export function projectParameters(value: unknown): DebugParameter[] {
  if (!isRecord(value)) {
    return KNOWN_DEBUG_PARAMETERS.map((name) => ({
      name,
      presence: 'absent',
      type: 'absent',
      source: 'caller',
      changed: false,
      truncated: false,
    }));
  }
  const seen = new Set<string>();
  const result: DebugParameter[] = [];
  for (const [name, item] of Object.entries(value)) {
    if (name === 'messages' || name === 'tools') continue;
    seen.add(name);
    result.push({
      name,
      presence: parameterPresence(item),
      value: item,
      type: jsonType(item),
      source: 'caller',
      changed: false,
      truncated: projectionTruncated(item),
    });
  }
  for (const name of KNOWN_DEBUG_PARAMETERS) {
    if (!seen.has(name))
      result.push({
        name,
        presence: 'absent',
        type: 'absent',
        source: 'caller',
        changed: false,
        truncated: false,
      });
  }
  return result;
}

export function parseRawRequest(value: unknown): Record<string, unknown> | null {
  if (isRecord(value)) return value;
  if (typeof value !== 'string') return null;
  try {
    const parsed: unknown = JSON.parse(value);
    return isRecord(parsed) ? parsed : null;
  } catch {
    return null;
  }
}

export function parametersForTrace(trace: DebugTrace): DebugParameter[] {
  const projected = trace.parameters;
  if (Array.isArray(projected)) {
    const entries: DebugParameter[] = [];
    for (const item of projected) {
      if (!isRecord(item) || typeof item.name !== 'string' || typeof item.presence !== 'string')
        continue;
      const value = item.value;
      entries.push({
        name: item.name,
        presence: item.presence,
        ...(value !== undefined ? { value } : {}),
        type: typeof item.type === 'string' ? item.type : jsonType(value),
        source: typeof item.source === 'string' ? item.source : 'caller',
        changed: item.changed === true,
        truncated: item.truncated === true || projectionTruncated(value),
      });
    }
    if (entries.length > 0) return entries;
  }
  const raw = parseRawRequest(trace.raw_request);
  return projectParameters(raw);
}

export function effectiveParameter(trace: DebugTrace, name: string): unknown {
  const effective = trace.effective;
  if (!isRecord(effective)) return undefined;
  return effective[name];
}
