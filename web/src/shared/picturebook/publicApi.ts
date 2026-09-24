import { decoded, idempotentOptions, queryPath } from '@shared/operations/api';
import {
  array,
  boolean,
  decimal,
  integer,
  invalidResponse,
  nullableInteger,
  nullableUnixSecond,
  oneOf,
  opaqueID,
  record,
  string,
  unixSecond,
} from '@shared/operations/wire';
import { ApiError } from '@shared/query/http';
import { currencyUnits, validScalar } from './parameters';
import {
  parameterKeys,
  taskStatuses,
  type CombinationRule,
  type DimensionAxis,
  type SizeDimensions,
  type ImageInfo,
  type ImageModel,
  type ImagePage,
  type ImageQueue,
  type ImageTask,
  type ParameterRule,
  type Price,
  type Scalar,
  type SubmitInput,
} from './publicTypes';

export const pictureBookBase = '/api/limited-activities/picture-book';
export function revision(value: unknown, allowZero = false): string {
  const result = decimal(string(value, 'revision', { min: 1, max: 19 }), 'revision', {
    positive: !allowZero,
  });
  if (BigInt(result) > 9223372036854775807n) invalidResponse('revision');
  return result;
}
export function decodePrice(value: unknown): Price {
  const v = record(value, ['paper', 'brush'], 'image price');
  const paper = string(v.paper, 'paper quantity', { min: 1, max: 39 }),
    brush = string(v.brush, 'brush quantity', { min: 1, max: 39 });
  if (currencyUnits(paper) === null || currencyUnits(brush) === null)
    invalidResponse('image price');
  return { paper, brush };
}
function finite(value: unknown): number {
  if (typeof value !== 'number' || !Number.isFinite(value)) invalidResponse('numeric constraint');
  return value;
}
function scalar(value: unknown): Scalar {
  return typeof value === 'number'
    ? finite(value)
    : string(value, 'parameter value', { bytes: 512, max: 512 });
}
function decodeDimensions(value: unknown): SizeDimensions {
  const dimensions = record(value, ['format', 'width', 'height'], 'size dimensions');
  const axis = (value: unknown): DimensionAxis => {
    const v = record(value, ['minimum', 'maximum', 'step'], 'dimension range');
    const result = {
      minimum: integer(v.minimum, 'minimum dimension', 1, 65536),
      maximum: integer(v.maximum, 'maximum dimension', 1, 65536),
      step: integer(v.step, 'dimension step', 1, 65536),
    };
    if (result.minimum > result.maximum) invalidResponse('dimension range');
    return result;
  };
  return {
    format: oneOf(dimensions.format, ['width_height'], 'size format'),
    width: axis(dimensions.width),
    height: axis(dimensions.height),
  };
}
export function decodeParameters(value: unknown): ParameterRule[] {
  const result = array(value, 'parameter rules', 10).map((entry): ParameterRule => {
    const v = record(
      entry,
      [
        'key',
        'supported',
        'required',
        'type',
        'minimum',
        'maximum',
        'step',
        'enum',
        'default',
        'min_length',
        'max_length',
        'length_unit',
        'dimensions',
      ],
      'parameter rule',
      ['key', 'supported', 'required', 'type'],
    );
    const rule: ParameterRule = {
      key: oneOf(v.key, parameterKeys, 'parameter key'),
      supported: boolean(v.supported, 'parameter support'),
      required: boolean(v.required, 'required parameter'),
      type: oneOf(v.type, ['string', 'integer', 'number'], 'parameter type'),
    };
    for (const key of ['minimum', 'maximum', 'step'] as const)
      if (Object.hasOwn(v, key)) rule[key] = finite(v[key]);
    for (const key of ['min_length', 'max_length'] as const)
      if (Object.hasOwn(v, key)) rule[key] = integer(v[key], 'text limit', 0, 65536);
    if (Object.hasOwn(v, 'length_unit'))
      rule.length_unit = oneOf(
        v.length_unit,
        ['utf8_bytes', 'unicode_scalars', 'utf16_units'],
        'length unit',
      );
    if (Object.hasOwn(v, 'enum')) {
      rule.enum = array(v.enum, 'parameter choices', 128).map(scalar);
      if (rule.enum.length === 0 || new Set(rule.enum).size !== rule.enum.length)
        invalidResponse('parameter choices');
    }
    if (Object.hasOwn(v, 'default')) rule.default = scalar(v.default);
    if (Object.hasOwn(v, 'dimensions')) {
      if (!rule.supported || rule.key !== 'size' || rule.type !== 'string')
        invalidResponse('size dimensions');
      rule.dimensions = decodeDimensions(v.dimensions);
    }
    if (
      (!rule.supported && rule.required) ||
      (rule.step !== undefined && rule.step <= 0) ||
      (rule.minimum !== undefined && rule.maximum !== undefined && rule.minimum > rule.maximum) ||
      (rule.min_length !== undefined &&
        rule.max_length !== undefined &&
        rule.min_length > rule.max_length)
    )
      invalidResponse('parameter constraint');
    if (
      rule.type === 'integer' &&
      [rule.minimum, rule.maximum, rule.step].some(
        (n) => n !== undefined && !Number.isSafeInteger(n),
      )
    )
      invalidResponse('integer constraint');
    if (
      rule.type === 'string'
        ? [rule.minimum, rule.maximum, rule.step].some((n) => n !== undefined)
        : [rule.min_length, rule.max_length, rule.length_unit].some((n) => n !== undefined)
    )
      invalidResponse('parameter type constraints');
    if (rule.type === 'string' && rule.length_unit === undefined)
      invalidResponse('string length unit');
    if (!rule.supported && rule.default !== undefined) invalidResponse('unsupported default');
    if (
      (rule.key === 'prompt' || rule.key === 'negative_prompt') &&
      (rule.type !== 'string' || rule.default !== undefined || rule.enum !== undefined)
    )
      invalidResponse('prompt rule');
    if (
      rule.key === 'n' &&
      (rule.type !== 'integer' ||
        (rule.minimum !== undefined && rule.minimum < 1) ||
        (rule.maximum !== undefined && rule.maximum > 16))
    )
      invalidResponse('image count rule');
    if (
      rule.enum?.some((option) => !validScalar(rule, option)) ||
      (rule.default !== undefined && !validScalar(rule, rule.default))
    )
      invalidResponse('parameter default or choices');
    return rule;
  });
  if (new Set(result.map((r) => r.key)).size !== result.length)
    invalidResponse('duplicate parameter');
  return result;
}
export function decodeCombinations(value: unknown): CombinationRule[] {
  return array(value, 'parameter combinations', 64).map((entry) => {
    const v = record(entry, ['keys', 'allowed'], 'parameter combination');
    const keys = array(v.keys, 'combination keys', 8).map((key) =>
      oneOf(key, parameterKeys, 'combination key'),
    );
    if (
      keys.length === 0 ||
      new Set(keys).size !== keys.length ||
      keys.some((key) => key === 'prompt' || key === 'negative_prompt')
    )
      invalidResponse('combination keys');
    const allowed = array(v.allowed, 'allowed combinations', 128).map((row) => {
      const values = array(row, 'combination tuple', 8).map((cell) =>
        cell === null ? null : scalar(cell),
      );
      if (values.length !== keys.length) invalidResponse('combination tuple');
      return values;
    });
    if (allowed.length === 0) invalidResponse('allowed combinations');
    return { keys, allowed };
  });
}
export function decodeModel(value: unknown): ImageModel {
  const v = record(
    value,
    ['id', 'display_name', 'description', 'revision', 'price', 'parameters', 'combinations'],
    'image model',
  );
  const model = {
    id: opaqueID(v.id, 'imdl_', 'image model'),
    display_name: string(v.display_name, 'model display name', { min: 1, max: 128, bytes: 512 }),
    description: string(v.description, 'model description', {
      max: 4096,
      bytes: 4096,
      multiline: true,
    }),
    revision: revision(v.revision),
    price: decodePrice(v.price),
    parameters: decodeParameters(v.parameters),
    combinations: decodeCombinations(v.combinations),
  };
  if (
    (model.price.paper === '0' && model.price.brush === '0') ||
    !model.parameters.some((r) => r.key === 'prompt' && r.supported && r.required)
  )
    invalidResponse('image model');
  if (!model.parameters.some((rule) => rule.key === 'n' && rule.supported))
    invalidResponse('image count support');
  if (
    model.combinations.some((rule) =>
      rule.keys.some((key) => !model.parameters.some((p) => p.key === key && p.supported)),
    )
  )
    invalidResponse('combination support');
  return model;
}
export function decodeImageInfo(value: unknown): ImageInfo {
  const v = record(value, ['index', 'mime', 'bytes'], 'image metadata');
  return {
    index: integer(v.index, 'image index', 0, 15),
    mime: oneOf(v.mime, ['image/png', 'image/jpeg', 'image/webp'], 'image type'),
    bytes: integer(v.bytes, 'image size', 1, 32 * 1024 * 1024),
  };
}
export function decodeTask(value: unknown): ImageTask {
  const v = record(
    value,
    [
      'id',
      'model_id',
      'status',
      'billing_state',
      'n',
      'actual_images',
      'created_at',
      'dispatched_at',
      'completed_at',
      'charge',
      'refund',
      'queue_position',
      'result_expires_at',
      'result_available',
      'error_code',
      'images',
    ],
    'image task',
  );
  const result: ImageTask = {
    id: opaqueID(v.id, 'img_', 'image task'),
    model_id: opaqueID(v.model_id, 'imdl_', 'image model'),
    status: oneOf(v.status, taskStatuses, 'task state'),
    billing_state: oneOf(v.billing_state, ['reserved', 'charged', 'refunded'], 'billing state'),
    n: integer(v.n, 'requested images', 1, 16),
    actual_images: integer(v.actual_images, 'generated images', 0, 16),
    created_at: unixSecond(v.created_at, 'task creation'),
    dispatched_at: nullableUnixSecond(v.dispatched_at, 'task start'),
    completed_at: nullableUnixSecond(v.completed_at, 'task completion'),
    charge: decodePrice(v.charge),
    refund: decodePrice(v.refund),
    queue_position: nullableInteger(v.queue_position, 'queue position', 1, 10000),
    result_expires_at: nullableUnixSecond(v.result_expires_at, 'image expiry'),
    result_available: boolean(v.result_available, 'image availability'),
    error_code:
      v.error_code === null
        ? null
        : string(v.error_code, 'task error', { max: 96, bytes: 96, ascii: true }),
    images: array(v.images, 'task images', 16).map(decodeImageInfo),
  };

  const reserved = ['queued', 'dispatching', 'running'].includes(result.status);
  if (
    (result.billing_state === 'reserved') !== reserved ||
    (result.billing_state === 'charged') !== (result.status === 'succeeded') ||
    (result.billing_state === 'refunded'
      ? result.charge.paper !== '0' || result.charge.brush !== '0'
      : result.refund.paper !== '0' || result.refund.brush !== '0')
  )
    invalidResponse('task billing outcome');
  if (
    result.actual_images > result.n ||
    new Set(result.images.map((image) => image.index)).size !== result.images.length ||
    result.images.reduce((sum, image) => sum + image.bytes, 0) > 64 * 1024 * 1024 ||
    result.images.some((image) => image.index >= result.actual_images) ||
    (result.result_available
      ? result.status !== 'succeeded' ||
        result.images.length !== result.actual_images ||
        !result.images.length ||
        result.result_expires_at === null
      : result.images.length !== 0)
  )
    invalidResponse('task image outcome');
  return result;
}
export function decodeQueue(value: unknown): ImageQueue {
  const v = record(value, ['queued', 'running', 'own', 'dispatch_paused'], 'image queue');
  const own = array(v.own, 'own queue', 100).map((item) => {
    const p = record(item, ['task_id', 'position', 'accepted_at'], 'own task position');
    return {
      task_id: opaqueID(p.task_id, 'img_', 'own task'),
      position: nullableInteger(p.position, 'queue position', 1, 10000),
      accepted_at: unixSecond(p.accepted_at, 'queue acceptance'),
    };
  });
  if (new Set(own.map((p) => p.task_id)).size !== own.length) invalidResponse('own queue');
  return {
    queued: integer(v.queued, 'queue size', 0, 10000),
    running: integer(v.running, 'running tasks', 0, 10000),
    own,
    dispatch_paused: boolean(v.dispatch_paused, 'dispatch pause'),
  };
}
export function imagePage<T>(value: unknown, decode: (entry: unknown) => T): ImagePage<T> {
  const v = record(value, ['data', 'next_cursor'], 'image page');
  const cursor =
    v.next_cursor === null
      ? null
      : string(v.next_cursor, 'image cursor', { min: 1, max: 2048, bytes: 2048, ascii: true });
  return { data: array(v.data, 'image page entries', 100).map(decode), next_cursor: cursor };
}
export const getModels = (cursor?: string, signal?: AbortSignal) =>
  decoded(
    queryPath(pictureBookBase + '/models', { page_size: 100, cursor }),
    (v) => imagePage(v, decodeModel),
    { signal },
  );
export const getTasks = (cursor?: string, signal?: AbortSignal) =>
  decoded(
    queryPath(pictureBookBase + '/tasks', { page_size: 20, cursor }),
    (v) => imagePage(v, decodeTask),
    { signal },
  );
export const getQueue = (signal?: AbortSignal) =>
  decoded(pictureBookBase + '/queue', decodeQueue, { signal });
export const getTask = (id: string, signal?: AbortSignal) =>
  decoded(pictureBookBase + '/tasks/' + opaqueID(id, 'img_', 'task id'), decodeTask, { signal });
function taskResult(value: unknown) {
  return decodeTask(record(value, ['task'], 'task receipt').task);
}
export const submitTask = (input: SubmitInput, key: string) =>
  decoded(
    pictureBookBase + '/tasks',
    taskResult,
    idempotentOptions(key, { method: 'POST', json: input }),
  );
export const cancelTask = (id: string, key: string) =>
  decoded(
    pictureBookBase + '/tasks/' + opaqueID(id, 'img_', 'task id') + '/cancel',
    taskResult,
    idempotentOptions(key, { method: 'POST', json: {} }),
  );

export async function getImage(id: string, info: ImageInfo, signal: AbortSignal): Promise<Blob> {
  opaqueID(id, 'img_', 'task id');
  decodeImageInfo(info);
  let response: Response;
  try {
    response = await fetch(pictureBookBase + '/tasks/' + id + '/images/' + info.index, {
      signal,
      credentials: 'same-origin',
      cache: 'no-store',
      redirect: 'error',
      headers: { Accept: info.mime, 'Cache-Control': 'no-store' },
    });
  } catch {
    throw new ApiError('network_error', 'The image could not be retrieved.', 0);
  }
  if (!response.ok) {
    void response.body?.cancel();
    throw new ApiError(
      response.status === 401
        ? 'unauthorized'
        : response.status === 403
          ? 'forbidden'
          : 'image_unavailable',
      'The image is not available for collection.',
      response.status,
    );
  }
  const mime = response.headers.get('Content-Type')?.split(';')[0].trim().toLowerCase();
  const declared = response.headers.get('Content-Length');
  if (
    mime !== info.mime ||
    (declared !== null && Number(declared) !== info.bytes) ||
    !response.body
  ) {
    void response.body?.cancel();
    invalidResponse('image response');
  }
  const reader = response.body.getReader();
  const chunks: ArrayBuffer[] = [];
  let bytes = 0;
  try {
    for (;;) {
      const next = await reader.read();
      if (next.done) break;
      bytes += next.value.byteLength;
      if (bytes > info.bytes || bytes > 32 * 1024 * 1024) invalidResponse('image size');
      chunks.push(next.value.slice().buffer);
    }
  } finally {
    await reader.cancel().catch(() => undefined);
    reader.releaseLock();
  }
  if (bytes !== info.bytes) invalidResponse('image size');
  return new Blob(chunks, { type: info.mime });
}
