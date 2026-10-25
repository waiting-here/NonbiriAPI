import type { AgentJob, AgentMessage, AgentPlan } from './api';

interface ToolCall {
  id?: string;
  name: string;
  arguments: unknown;
  raw: unknown;
}

function object(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined;
}

function nonemptyString(value: unknown): value is string {
  return typeof value === 'string' && value.length > 0;
}

export function readToolCalls(message: AgentMessage): ToolCall[] {
  const raw = message.tool_calls;
  if (raw === undefined) return [];
  if (!Array.isArray(raw)) return [{ name: '', arguments: undefined, raw }];
  return raw.map((entry: unknown) => {
    const call = object(entry);
    const fn = object(call?.function);
    return {
      ...(nonemptyString(call?.id) ? { id: call.id } : {}),
      name: typeof fn?.name === 'string' ? fn.name : '',
      arguments: fn?.arguments,
      raw: entry,
    };
  });
}

interface PendingCall {
  messageID: string;
  index: number;
}

export function planMessageID(plan: AgentPlan, messages: AgentMessage[], job?: AgentJob) {
  if (!job || plan.job_id !== job.id) return undefined;
  const ids = new Set(plan.tool_call_ids);
  if (!ids.size) return undefined;
  const matches = messages.filter((message) => {
    if (
      message.role !== 'assistant' ||
      message.generation !== job.generation ||
      message.created_at < job.created_at ||
      message.created_at > plan.created_at
    )
      return false;
    const calls = new Set(readToolCalls(message).map((call) => call.id));
    return [...ids].every((id) => calls.has(id));
  });
  // Historical, ambiguous or paged-out calls keep a separate plan record.
  return matches.length === 1 ? matches[0]!.id : undefined;
}

export function indexToolResults(messages: readonly AgentMessage[]): {
  results: Map<string, Map<number, AgentMessage>>;
  consumed: Set<string>;
} {
  const results = new Map<string, Map<number, AgentMessage>>();
  const consumed = new Set<string>();
  const pending = new Map<string, Map<string, PendingCall[]>>();

  for (const message of messages) {
    if (!nonemptyString(message.generation)) continue;
    if (message.role === 'assistant') {
      readToolCalls(message).forEach((call, index) => {
        if (!call.id) return;
        let generation = pending.get(message.generation);
        if (!generation) {
          generation = new Map<string, PendingCall[]>();
          pending.set(message.generation, generation);
        }
        const calls = generation.get(call.id) ?? [];
        calls.push({ messageID: message.id, index });
        generation.set(call.id, calls);
      });
      continue;
    }
    if (message.role !== 'tool' || !nonemptyString(message.tool_call_id)) continue;
    const calls = pending.get(message.generation)?.get(message.tool_call_id);
    const call = calls?.pop();
    if (!call) continue;
    let messageResults = results.get(call.messageID);
    if (!messageResults) {
      messageResults = new Map<number, AgentMessage>();
      results.set(call.messageID, messageResults);
    }
    messageResults.set(call.index, message);
    consumed.add(message.id);
  }
  return { results, consumed };
}
