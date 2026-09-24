import { decoded, idempotentOptions, queryPath } from '@shared/operations/api';
import {
  array,
  boolean,
  integer,
  invalidResponse,
  nullableUnixSecond,
  oneOf,
  opaqueID,
  record,
  string,
  unixSecond,
} from '@shared/operations/wire';
import {
  decodeCombinations,
  decodeParameters,
  decodePrice,
  imagePage,
  revision,
} from '@shared/picturebook/publicApi';
import { parameterKeys, type ParameterKey } from '@shared/picturebook/publicTypes';

const base = '/admin/api/limited-activities/picture-book';
const pointer = (v: unknown) => string(v, 'JSON pointer', { max: 512, bytes: 512 });
const jsonSize = (v: unknown, max: number, label: string) => {
  if (new TextEncoder().encode(JSON.stringify(v)).byteLength > max) invalidResponse(label);
};
export function decodeMapping(value: unknown) {
  const v = record(value, ['model_pointer', 'parameters', 'constants'], 'parameter mapping');
  const parameters = record(v.parameters, parameterKeys, 'parameter destinations', []);
  const result: {
    model_pointer: string;
    parameters: Partial<Record<ParameterKey, string>>;
    constants: { pointer: string; value: string | number | boolean | null }[];
  } = {
    model_pointer: pointer(v.model_pointer),
    parameters: Object.fromEntries(
      Object.entries(parameters).map(([key, value]) => [key, pointer(value)]),
    ),
    constants: array(v.constants, 'constant fields', 64).map((entry) => {
      const c = record(entry, ['pointer', 'value'], 'constant field');
      if (c.value !== null && !['string', 'boolean', 'number'].includes(typeof c.value))
        invalidResponse('constant scalar');
      if (typeof c.value === 'number' && !Number.isFinite(c.value))
        invalidResponse('constant number');
      jsonSize(c.value, 4096, 'constant size');
      return { pointer: pointer(c.pointer), value: c.value as string | number | boolean | null };
    }),
  };
  jsonSize(result, 65536, 'mapping size');
  return result;
}
export function decodeAdapter(value: unknown) {
  const v = record(value, ['discovery', 'submit', 'poll', 'response'], 'image adapter', [
    'discovery',
    'submit',
    'response',
  ]);
  const d = record(
    v.discovery,
    ['method', 'path', 'items_pointer', 'id_pointer', 'metadata_pointer'],
    'discovery adapter',
    ['method', 'path', 'items_pointer', 'id_pointer'],
  );
  const s = record(v.submit, ['method', 'path', 'mapping', 'receipt'], 'submit adapter', [
    'method',
    'path',
    'mapping',
  ]);
  const r = record(
    v.response,
    [
      'task_id_pointer',
      'state_pointer',
      'working_states',
      'success_states',
      'failure_states',
      'images_pointer',
      'base64_pointer',
      'url_pointer',
    ],
    'response adapter',
    ['working_states', 'success_states', 'failure_states', 'images_pointer'],
  );
  const path = (entry: unknown) => string(entry, 'relative path', { min: 1, max: 512, bytes: 512 });
  const optionalPointer = (entry: Record<string, unknown>, key: string) =>
    Object.hasOwn(entry, key) ? { [key]: pointer(entry[key]) } : {};
  const states = (entry: unknown) =>
    array(entry, 'response states', 32).map((item) =>
      string(item, 'response state', { min: 1, max: 128, bytes: 128 }),
    );
  const poll = Object.hasOwn(v, 'poll')
    ? record(v.poll, ['method', 'path'], 'poll adapter')
    : undefined;
  const receipt = Object.hasOwn(s, 'receipt')
    ? (() => {
        const value = record(
          s.receipt,
          ['indicator_pointer', 'indicator_value'],
          'submission receipt',
        );
        return {
          indicator_pointer: pointer(value.indicator_pointer),
          indicator_value:
            typeof value.indicator_value === 'boolean'
              ? value.indicator_value
              : string(value.indicator_value, 'receipt indicator', {
                  min: 1,
                  max: 128,
                  bytes: 128,
                }),
        };
      })()
    : undefined;
  if (receipt && (!poll || !Object.hasOwn(r, 'task_id_pointer')))
    invalidResponse('submission receipt requires polling and task identity');
  const result = {
    discovery: {
      method: oneOf(d.method, ['GET'], 'discovery method'),
      path: path(d.path),
      items_pointer: pointer(d.items_pointer),
      id_pointer: pointer(d.id_pointer),
      ...optionalPointer(d, 'metadata_pointer'),
    },
    submit: {
      method: oneOf(s.method, ['POST'], 'submit method'),
      path: path(s.path),
      mapping: decodeMapping(s.mapping),
      ...(receipt ? { receipt } : {}),
    },
    ...(poll
      ? { poll: { method: oneOf(poll.method, ['GET'], 'poll method'), path: path(poll.path) } }
      : {}),
    response: {
      images_pointer: pointer(r.images_pointer),
      working_states: states(r.working_states),
      success_states: states(r.success_states),
      failure_states: states(r.failure_states),
      ...optionalPointer(r, 'task_id_pointer'),
      ...optionalPointer(r, 'state_pointer'),
      ...optionalPointer(r, 'base64_pointer'),
      ...optionalPointer(r, 'url_pointer'),
    },
  };
  jsonSize(result, 262144, 'adapter size');
  return result;
}
export type Adapter = ReturnType<typeof decodeAdapter>;
export function decodeControl(value: unknown) {
  const v = record(
    value,
    ['id', 'paused', 'reason', 'revision', 'uncertain_slots'],
    'upstream control',
  );
  return {
    id: opaqueID(v.id, 'iup_', 'upstream control'),
    paused: boolean(v.paused, 'protection pause'),
    reason: oneOf(
      v.reason,
      ['', 'receipt_unknown', 'execution_timeout', 'recovery_uncertain'],
      'protection reason',
    ),
    revision: revision(v.revision),
    uncertain_slots: integer(v.uncertain_slots, 'uncertain slots', 0, 1000000),
  };
}
export function decodeControlRow(value: unknown) {
  const v = record(
    value,
    ['id', 'paused', 'reason', 'revision', 'uncertain_slots', 'current', 'queued', 'running'],
    'control row',
  );
  const { current, queued, running, ...control } = v;
  return {
    ...decodeControl(control),
    current: boolean(current, 'current upstream'),
    queued: integer(queued, 'queued tasks', 0, 1000000),
    running: integer(running, 'running tasks', 0, 1000000),
  };
}
export type ControlRow = ReturnType<typeof decodeControlRow>;
export function decodeUpstream(value: unknown) {
  const v = record(
    value,
    [
      'revision',
      'configured',
      'base_url',
      'secret_set',
      'rpm',
      'concurrency',
      'per_user_limit',
      'global_limit',
      'queue_timeout_seconds',
      'execution_timeout_seconds',
      'memory_budget_mib',
      'image_origins',
      'adapter',
      'control',
    ],
    'image upstream',
  );
  return {
    revision: revision(v.revision),
    configured: boolean(v.configured, 'upstream configuration'),
    base_url: string(v.base_url, 'upstream address', { max: 4096, bytes: 4096 }),
    secret_set: boolean(v.secret_set, 'stored key'),
    rpm: v.rpm === null ? null : integer(v.rpm, 'RPM', 1, 10000),
    concurrency: v.concurrency === null ? null : integer(v.concurrency, 'concurrency', 1, 32),
    per_user_limit: integer(v.per_user_limit, 'user queue limit', 1, 100),
    global_limit: integer(v.global_limit, 'global queue limit', 1, 10000),
    queue_timeout_seconds: integer(v.queue_timeout_seconds, 'queue timeout', 60, 86400),
    execution_timeout_seconds: integer(v.execution_timeout_seconds, 'execution timeout', 60, 86400),
    memory_budget_mib: integer(v.memory_budget_mib, 'image memory', 512, 4096),
    image_origins: array(v.image_origins, 'image origins', 8).map((origin) =>
      string(origin, 'image origin', { min: 1, max: 4096, bytes: 4096 }),
    ),
    adapter: v.adapter === null ? null : decodeAdapter(v.adapter),
    control: v.control === null ? null : decodeControl(v.control),
  };
}
export type Upstream = ReturnType<typeof decodeUpstream>;
export type UpstreamInput = Omit<
  Upstream,
  'revision' | 'configured' | 'secret_set' | 'control' | 'rpm' | 'concurrency' | 'adapter'
> & {
  expected_revision: string;
  rpm: number;
  concurrency: number;
  adapter: Adapter;
  secret: { mode: 'keep' } | { mode: 'replace'; value: string };
};
export function decodeAdminModel(value: unknown) {
  const v = record(
    value,
    [
      'id',
      'upstream_model_id',
      'metadata',
      'configured',
      'revision',
      'display_name',
      'description',
      'enabled',
      'price',
      'parameters',
      'combinations',
      'mapping',
    ],
    'private image model',
  );
  jsonSize(v.metadata, 32768, 'model metadata');
  return {
    id: opaqueID(v.id, 'imdl_', 'image model'),
    upstream_model_id: string(v.upstream_model_id, 'upstream model', {
      min: 1,
      max: 512,
      bytes: 2048,
    }),
    metadata: v.metadata,
    configured: boolean(v.configured, 'configured model'),
    revision: revision(v.revision, true),
    display_name: string(v.display_name, 'display name', { max: 128, bytes: 512 }),
    description: string(v.description, 'model description', {
      max: 4096,
      bytes: 4096,
      multiline: true,
    }),
    enabled: boolean(v.enabled, 'model availability'),
    price: decodePrice(v.price),
    parameters: decodeParameters(v.parameters),
    combinations: decodeCombinations(v.combinations),
    mapping: decodeMapping(v.mapping),
  };
}
export type AdminModel = ReturnType<typeof decodeAdminModel>;
export type ModelInput = Pick<
  AdminModel,
  'display_name' | 'description' | 'enabled' | 'price' | 'parameters' | 'combinations' | 'mapping'
> & { expected_revision: string };
export function decodeRefresh(value: unknown) {
  const v = record(
    value,
    ['id', 'state', 'created_at', 'completed_at', 'model_count', 'error_code', 'http_status'],
    'model discovery',
    ['id', 'state', 'created_at', 'completed_at', 'model_count', 'error_code'],
  );
  return {
    id: opaqueID(v.id, 'op_', 'discovery operation'),
    ...(Object.hasOwn(v, 'http_status')
      ? { http_status: integer(v.http_status, 'discovery HTTP status', 100, 599) }
      : {}),
    state: oneOf(v.state, ['queued', 'running', 'succeeded', 'failed'], 'discovery state'),
    created_at: unixSecond(v.created_at, 'discovery acceptance'),
    completed_at: nullableUnixSecond(v.completed_at, 'discovery completion'),
    model_count: integer(v.model_count, 'discovered models', 0, 1000),
    error_code:
      v.error_code === null
        ? null
        : string(v.error_code, 'discovery error', { max: 96, bytes: 96, ascii: true }),
  };
}
export const getUpstream = (signal?: AbortSignal) =>
  decoded(base + '/upstream', decodeUpstream, { signal });
export const saveUpstream = (input: UpstreamInput, key: string) =>
  decoded(
    base + '/upstream',
    (v) => ({ revision: revision(record(v, ['revision'], 'upstream save receipt').revision) }),
    idempotentOptions(key, { method: 'PUT', json: input }),
  );
export const getAdminModels = (cursor?: string, signal?: AbortSignal) =>
  decoded(
    queryPath(base + '/models', { page_size: 100, cursor }),
    (v) => imagePage(v, decodeAdminModel),
    { signal },
  );
export const saveModel = (value: { id: string; input: ModelInput }, key: string) =>
  decoded(
    base + '/models/' + opaqueID(value.id, 'imdl_', 'model id'),
    (v) => {
      const receipt = record(v, ['id', 'revision'], 'model save receipt');
      return {
        id: opaqueID(receipt.id, 'imdl_', 'model id'),
        revision: revision(receipt.revision),
      };
    },
    idempotentOptions(key, { method: 'PUT', json: value.input }),
  );
export const refreshModels = (_: Record<string, never>, key: string) =>
  decoded(
    base + '/models/refresh',
    (value) => decodeRefresh(record(value, ['operation'], 'model discovery receipt').operation),
    idempotentOptions(key, { method: 'POST', json: {} }),
  );
export const getRefresh = (id: string, signal?: AbortSignal) =>
  decoded(base + '/models/refresh/' + opaqueID(id, 'op_', 'discovery id'), decodeRefresh, {
    signal,
  });
export const getControls = (cursor?: string, signal?: AbortSignal) =>
  decoded(
    queryPath(base + '/upstream/controls', { page_size: 100, cursor }),
    (v) => imagePage(v, decodeControlRow),
    { signal },
  );
export const resumeUpstream = (
  input: { control_id: string; expected_revision: string; reason: string },
  key: string,
) =>
  decoded(
    base + '/upstream/resume',
    (v) => decodeControl(record(v, ['control'], 'resume receipt').control),
    idempotentOptions(key, { method: 'POST', json: input }),
  );

export const getAdminModel = (id: string, signal?: AbortSignal) =>
  decoded(base + '/models/' + opaqueID(id, 'imdl_', 'model id'), decodeAdminModel, { signal });
