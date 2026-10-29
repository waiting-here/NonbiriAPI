import { normalizeFailure, type FailureDiagnostics } from '@shared/observability/failure';
import { apiFetch } from '@shared/query/http';
import { queryPath } from '@shared/operations/api';
import { array, decimal, integer, invalidResponse, record } from '@shared/operations/wire';
import type { PageMetadata, PageSize } from '@shared/operations/pageNumbers';

export type AgentRole = 'admin' | 'steward';
export type MemoryKind = 'auto_review' | 'chat' | 'personal';
export interface AgentMessage {
  id: string;
  role: string;
  content: string;
  tool_calls?: unknown;
  tool_call_id?: string;
  created_at: number;
  active: boolean;
  generation: string;
}
export interface QueueStatus {
  length: number;
  active: number;
  limit: number;
  position: number;
}
export interface AgentJob {
  id: string;
  kind: string;
  actor_id: string;
  actor_role: string;
  actor_label: string;
  conversation_id: string;
  generation: string;
  donation_id: string;
  state: string;
  request_id: string;
  error: string;
  created_at: number;
  updated_at: number;
  queue: QueueStatus;
}
export interface PlanOperation {
  kind: string;
  target_id: string;
  input: unknown;
}
export interface OperationPreview {
  title: string;
  description: string;
  before?: string;
  after?: string;
  dangerous: boolean;
  requires_elevation: boolean;
}
export interface AgentPlan {
  id: string;
  job_id: string;
  tool_call_ids?: string[];
  state: string;
  operations: PlanOperation[];
  preview: OperationPreview[];
  result?: unknown;
  created_at: number;
  decided_at?: number;
}
export interface Conversation {
  enabled: boolean;
  id: string;
  generation: string;
  messages: AgentMessage[];
  summary: string;
  job?: AgentJob;
  plans: AgentPlan[];
  has_older: boolean;
}
export interface SendMessage {
  content: string;
  expected_generation: string;
  request_key: string;
}
export interface PlanDecision {
  approve: boolean;
  feedback: string;
  elevation_token?: string;
}
export interface AgentMemory {
  kind: MemoryKind;
  content: string;
  revision: string;
  updated_at: number;
  updated_by: string;
  owner_id?: string;
  writable?: boolean;
}
export interface MemoryOwner {
  id: string;
  role: string;
  label: string;
}
export interface TemplateMessage {
  role: 'system' | 'user' | 'assistant';
  content: string;
}
export const AGENT_TOOLS = [
  'donations',
  'donation_models',
  'review_history',
  'charity_models',
  'charity_binding_presets',
  'users',
  'site_status',
  'web_search',
  'propose_operations',
  'edit_memory',
] as const;
export interface AgentSettings {
  chat_enabled: boolean;
  revision: string;
  mode: 'off' | 'preview' | 'auto';
  auto_template: TemplateMessage[];
  chat_template: TemplateMessage[];
  auto_prefill: string;
  chat_prefill: string;
  tools: Record<string, boolean>;
  tool_descriptions?: Record<string, string>;
  search_url: string;
  search_has_key: boolean;
}
export interface DriverSettings {
  revision: string;
  output_token_parameter: string;
  base_url: string;
  has_key: boolean;
  model: string;
  context_window: number;
  max_output_tokens: number;
  temperature?: number;
  top_p?: number;
  extra_parameters: Record<string, unknown> | null;
  concurrency: number;
  timeout_seconds: number;
}
export type DriverUpdate = Omit<DriverSettings, 'revision' | 'has_key'> & {
  expected_revision: string;
  api_key?: string;
};
export type AgentSettingsUpdate = Omit<AgentSettings, 'revision' | 'search_has_key'> & {
  expected_revision: string;
  search_api_key?: string;
};
export interface AgentReview {
  job: AgentJob;
  result?: { decision: string; internal_reason: string; user_note: string; bindings: unknown[] };
  execution?: unknown;
  intelligence: unknown;
  turns?: ModelResult[];
}
export interface ModelResult {
  request_id: string;
  kind: string;
  message: unknown;
  error: string;
  created_at: number;
}
export interface AgentPage<T> {
  items: T[];
  total: string;
  page: number;
  page_size: PageSize;
  cutoff_id?: string;
}
export interface TokenUsage {
  input_tokens: string;
  cached_input_tokens: string;
  cache_write_input_tokens: string;
  output_tokens: string;
  reasoning_tokens: string;
  unknown: boolean;
}
export interface AgentRequest {
  id: string;
  client_request_id: string;
  source: string;
  kind: string;
  actor_id: string;
  actor_role: string;
  actor_label: string;
  state: string;
  base_url: string;
  model: string;
  created_at: number;
  started_at?: number;
  completed_at?: number;
  status_code: number;
  error_code: string;
  content_redacted: boolean;
  response_complete: boolean;
  usage: TokenUsage;
}
export interface AgentRequestDetail extends AgentRequest {
  error_diagnostics?: FailureDiagnostics | null;
  queued_input: unknown;
  request_body: string;
  response_body: string;
  response_body_base64?: string;
}
export interface UsageSummary {
  actor_id: string;
  actor_role: string;
  actor_label: string;
  source: string;
  kind: string;
  requests: string;
  usage: TokenUsage;
}
export interface UsageReport {
  total: UsageSummary[];
  last_24_hours: UsageSummary[];
  as_of: number;
}
export interface ArchiveOwner {
  actor_id: string;
  role: string;
  label: string;
  updated_at: number;
}
export interface RequestFilters {
  source: string;
  kind: string;
  state: string;
  actor_id: string;
}
export interface CleanupInput {
  through_id?: string;
  ids?: string[];
  source?: string;
  kind?: string;
  state?: string;
  actor_id?: string;
}
export interface CleanupResult {
  deleted_count: string;
  skipped_count: string;
}

export const agentRoot = (role: AgentRole, account: string) =>
  [role === 'admin' ? 'admin' : 'user', 'ai', account] as const;
const base = (role: AgentRole) => (role === 'admin' ? '/admin/api/ai' : '/api/steward/ai');
function object<T>(value: unknown, fields: readonly string[], optional: readonly string[] = []): T {
  return record(value, [...fields, ...optional], 'AI response', fields) as T;
}
const rawString = (value: unknown) => {
  if (typeof value !== 'string') invalidResponse('AI text');
  return value;
};
function message(value: unknown): AgentMessage {
  const row = object<AgentMessage>(
    value,
    ['id', 'role', 'content', 'created_at', 'active', 'generation'],
    ['tool_calls', 'tool_call_id'],
  );
  decimal(row.id, 'message id');
  decimal(row.generation, 'message generation');
  rawString(row.content);
  return row;
}
function job(value: unknown): AgentJob {
  const row = object<AgentJob>(value, [
    'id',
    'kind',
    'state',
    'actor_id',
    'actor_role',
    'actor_label',
    'conversation_id',
    'generation',
    'donation_id',
    'request_id',
    'error',
    'created_at',
    'updated_at',
    'queue',
  ]);
  for (const key of [
    'id',
    'actor_id',
    'generation',
    'conversation_id',
    'donation_id',
    'request_id',
  ] as const)
    decimal(row[key], 'job identity');
  return row;
}
function plan(value: unknown): AgentPlan {
  const row = object<AgentPlan>(
    value,
    ['id', 'job_id', 'state', 'operations', 'preview', 'created_at'],
    ['tool_call_ids', 'result', 'decided_at'],
  );
  decimal(row.id, 'plan id');
  decimal(row.job_id, 'plan job');
  array(row.operations, 'operations', 100);
  array(row.preview, 'preview', 100);
  return row;
}
function conversation(value: unknown): Conversation {
  const row = object<Conversation>(
    value,
    ['id', 'generation', 'messages', 'summary', 'plans', 'has_older', 'enabled'],
    ['job'],
  );
  decimal(row.id, 'conversation id');
  decimal(row.generation, 'generation');
  return {
    ...row,
    messages: array(row.messages, 'messages', 100).map(message),
    plans: array(row.plans, 'plans').map(plan),
    ...(row.job ? { job: job(row.job) } : {}),
  };
}
function page<T>(value: unknown, decode: (item: unknown) => T): AgentPage<T> {
  const row = object<AgentPage<unknown>>(
    value,
    ['items', 'total', 'page', 'page_size'],
    ['cutoff_id'],
  );
  decimal(row.total, 'total');
  integer(row.page, 'page', 1);
  integer(row.page_size, 'page size', 1, 100);
  return { ...row, items: array(row.items, 'items', 100).map(decode) };
}
function memory(value: unknown): AgentMemory {
  const row = object<AgentMemory>(
    value,
    ['kind', 'content', 'revision', 'updated_at', 'updated_by'],
    ['owner_id', 'writable'],
  );
  decimal(row.revision, 'memory revision');
  if (row.owner_id !== undefined) decimal(row.owner_id, 'memory owner');
  if (row.writable !== undefined && typeof row.writable !== 'boolean')
    invalidResponse('memory access');
  rawString(row.content);
  return row;
}
function review(value: unknown): AgentReview {
  const row = object<AgentReview>(value, ['job', 'intelligence'], ['result', 'execution', 'turns']);
  return { ...row, job: job(row.job) };
}
function settings(value: unknown): AgentSettings {
  const row = object<AgentSettings>(
    value,
    [
      'revision',
      'chat_enabled',
      'mode',
      'auto_template',
      'chat_template',
      'auto_prefill',
      'chat_prefill',
      'tools',
      'search_url',
      'search_has_key',
    ],
    ['tool_descriptions'],
  );
  decimal(row.revision, 'settings revision');
  return row;
}
function driver(value: unknown): DriverSettings {
  const row = object<DriverSettings>(
    value,
    [
      'revision',
      'base_url',
      'has_key',
      'model',
      'context_window',
      'max_output_tokens',
      'extra_parameters',
      'concurrency',
      'timeout_seconds',
      'output_token_parameter',
    ],
    ['temperature', 'top_p'],
  );
  decimal(row.revision, 'driver revision');
  return row;
}
function request(value: unknown): AgentRequest {
  const row = object<AgentRequest>(
    value,
    [
      'id',
      'client_request_id',
      'actor_id',
      'actor_role',
      'actor_label',
      'source',
      'kind',
      'state',
      'created_at',
      'base_url',
      'model',
      'status_code',
      'error_code',
      'content_redacted',
      'usage',
      'response_complete',
    ],
    ['started_at', 'completed_at'],
  );
  decimal(row.id, 'request id');
  decimal(row.actor_id, 'actor id');
  for (const key of [
    'input_tokens',
    'cached_input_tokens',
    'cache_write_input_tokens',
    'output_tokens',
    'reasoning_tokens',
  ] as const)
    decimal(row.usage[key], 'token total');
  return row;
}
async function read<T>(path: string, decode: (value: unknown) => T, signal?: AbortSignal) {
  return decode(await apiFetch<unknown>(path, { signal }));
}
async function write<T>(
  path: string,
  method: string,
  input: unknown,
  decode: (value: unknown) => T,
  signal?: AbortSignal,
) {
  return decode(await apiFetch<unknown>(path, { method, json: input, signal }));
}
export const getConversation = (role: AgentRole, signal?: AbortSignal) =>
  read(base(role) + '/chat', conversation, signal);
export const getMessages = (role: AgentRole, before: string, signal?: AbortSignal) =>
  read(
    queryPath(base(role) + '/chat/messages', { before_id: before }),
    (value) =>
      array(object<{ items: unknown[] }>(value, ['items']).items, 'messages', 100).map(message),
    signal,
  );
export const sendMessage = (role: AgentRole, input: SendMessage, signal?: AbortSignal) =>
  write(base(role) + '/chat/messages', 'POST', input, job, signal);
const planCursor = (before: string) => (before && before !== '0' ? before : undefined);
export const getPlans = (role: AgentRole, before: string, signal?: AbortSignal) =>
  read(
    queryPath(base(role) + '/chat/plans', { before_id: planCursor(before) }),
    (value) => {
      const row = object<{ items: unknown[]; has_older: boolean }>(value, ['items', 'has_older']);
      return { items: array(row.items, 'plans', 100).map(plan), has_older: row.has_older };
    },
    signal,
  );
export const getArchivePlans = (id: string, before: string, signal?: AbortSignal) =>
  read(
    queryPath(base('admin') + '/archives/' + encodeURIComponent(id) + '/plans', {
      before_id: planCursor(before),
    }),
    (value) => {
      const row = object<{ items: unknown[]; has_older: boolean }>(value, ['items', 'has_older']);
      return { items: array(row.items, 'plans', 100).map(plan), has_older: row.has_older };
    },
    signal,
  );
export const editMessage = (
  role: AgentRole,
  id: string,
  input: SendMessage,
  signal?: AbortSignal,
) => write(base(role) + '/chat/messages/' + encodeURIComponent(id), 'PUT', input, job, signal);
export const clearConversation = (role: AgentRole, generation: string, signal?: AbortSignal) =>
  write(
    base(role) + '/chat/clear',
    'POST',
    { expected_generation: generation },
    () => undefined,
    signal,
  );
export const cancelJob = (role: AgentRole, id: string, signal?: AbortSignal) =>
  write(
    base(role) + '/chat/jobs/' + encodeURIComponent(id) + '/cancel',
    'POST',
    {},
    () => undefined,
    signal,
  );
export const decidePlan = (
  role: AgentRole,
  id: string,
  input: PlanDecision,
  signal?: AbortSignal,
) =>
  write(
    base(role) + '/chat/plans/' + encodeURIComponent(id) + '/decision',
    'POST',
    input,
    plan,
    signal,
  );
export const getReviews = (role: AgentRole, p: string, n: PageSize, signal?: AbortSignal) =>
  read(
    queryPath(base(role) + '/reviews', { page: p, page_size: n }),
    (value) => page(value, review),
    signal,
  );
export const getReview = (role: AgentRole, id: string, signal?: AbortSignal) =>
  read(base(role) + '/reviews/' + encodeURIComponent(id), review, signal);
const memoryPath = (role: AgentRole, kind: MemoryKind, owner = '') =>
  base(role) +
  (kind !== 'personal'
    ? '/memories/' + kind
    : owner
      ? '/personal-memories/' + encodeURIComponent(owner)
      : '/personal-memory');
export const getMemory = (role: AgentRole, kind: MemoryKind, signal?: AbortSignal, owner = '') =>
  read(memoryPath(role, kind, owner), memory, signal);
export const putMemory = (
  role: AgentRole,
  kind: MemoryKind,
  input: { content: string; expected_revision: string },
  signal?: AbortSignal,
  owner = '',
) => write(memoryPath(role, kind, owner), 'PUT', input, memory, signal);
export const getMemoryOwners = (query: string, p: string, n: PageSize, signal?: AbortSignal) =>
  read(
    queryPath(base('admin') + '/personal-memories', {
      q: query || undefined,
      page: p,
      page_size: n,
    }),
    (value) => ({
      ...page(value, (item) => {
        const row = object<MemoryOwner>(item, ['id', 'role', 'label']);
        decimal(row.id, 'memory owner');
        rawString(row.label);
        rawString(row.role);
        return row;
      }),
      current_owner_id: decimal(
        object<{ current_owner_id: string }>(value, ['current_owner_id']).current_owner_id,
        'current memory owner',
      ),
    }),
    signal,
  );
export const getSettings = (signal?: AbortSignal) =>
  read(base('admin') + '/config', settings, signal);
export const putSettings = (input: AgentSettingsUpdate, signal?: AbortSignal) =>
  write(base('admin') + '/config', 'PUT', input, settings, signal);
export const getDriver = (signal?: AbortSignal) => read(base('admin') + '/driver', driver, signal);
export const putDriver = (input: DriverUpdate, signal?: AbortSignal) =>
  write(base('admin') + '/driver', 'PUT', input, driver, signal);
export const discoverModels = (signal?: AbortSignal) =>
  write(
    base('admin') + '/driver/models',
    'POST',
    {},
    (value) =>
      array(object<{ models: unknown[] }>(value, ['models']).models, 'models').map(rawString),
    signal,
  );
export const getRequests = (
  filters: RequestFilters,
  p: string,
  n: PageSize,
  signal?: AbortSignal,
) =>
  read(
    queryPath(base('admin') + '/requests', {
      ...filters,
      actor_id: filters.actor_id || undefined,
      page: p,
      page_size: n,
    }),
    (value) => page(value, request),
    signal,
  );
export const getRequest = (id: string, signal?: AbortSignal) =>
  read(
    base('admin') + '/requests/' + encodeURIComponent(id),
    (value) => ({
      ...request(value),
      ...object<AgentRequestDetail>(
        value,
        ['queued_input', 'request_body', 'response_body'],
        ['response_body_base64', 'error_diagnostics'],
      ),
      error_diagnostics: normalizeFailure((value as Record<string, unknown>).error_diagnostics),
    }),
    signal,
  );
export const cleanupRequests = (input: CleanupInput, signal?: AbortSignal) =>
  write(
    base('admin') + '/requests/cleanup',
    'POST',
    input,
    (value) => object<CleanupResult>(value, ['deleted_count', 'skipped_count']),
    signal,
  );
export const getUsage = (signal?: AbortSignal) =>
  read(
    base('admin') + '/usage',
    (value) => object<UsageReport>(value, ['total', 'last_24_hours', 'as_of']),
    signal,
  );
export const getArchives = (p: string, n: PageSize, signal?: AbortSignal) =>
  read(
    queryPath(base('admin') + '/archives', { page: p, page_size: n }),
    (value) =>
      page(value, (item) =>
        object<ArchiveOwner>(item, ['actor_id', 'role', 'label', 'updated_at']),
      ),
    signal,
  );
export const getArchiveMessages = (id: string, before: string, signal?: AbortSignal) =>
  read(
    queryPath(base('admin') + '/archives/' + encodeURIComponent(id) + '/messages', {
      before_id: before,
    }),
    (value) =>
      array(object<{ items: unknown[] }>(value, ['items']).items, 'archive messages', 100).map(
        message,
      ),
    signal,
  );
export const getArchiveResults = (id: string, p: string, n: PageSize, signal?: AbortSignal) =>
  read(
    queryPath(base('admin') + '/archives/' + encodeURIComponent(id) + '/results', {
      page: p,
      page_size: n,
    }),
    (value) =>
      page(value, (item) =>
        object<ModelResult>(item, ['request_id', 'kind', 'message', 'error', 'created_at']),
      ),
    signal,
  );
export function pageMetadata(
  value: Pick<AgentPage<unknown>, 'total' | 'page' | 'page_size'>,
): PageMetadata {
  return {
    page: String(value.page),
    page_size: value.page_size,
    total_items: value.total,
    total_pages: String(
      (BigInt(value.total) + BigInt(value.page_size) - 1n) / BigInt(value.page_size) || 1n,
    ),
  };
}
export const activeJob = (value?: AgentJob) =>
  Boolean(value && ['pending', 'running'].includes(value.state));
