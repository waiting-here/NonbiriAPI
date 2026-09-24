import { decoded, queryPath } from '@shared/operations/api';
import {
  array,
  boolean,
  decimal,
  integer,
  invalidResponse,
  nullableString,
  nullableUnixSecond,
  oneOf,
  opaqueID,
  record,
  string,
  unixSecond,
} from '@shared/operations/wire';

export const assets = ['general', 'game', 'sketch_paper', 'sketch_brush'] as const;
export type Asset = (typeof assets)[number];
export type View = 'series' | 'channels' | 'operations';
export interface AuditFilter {
  asset: Asset;
  from: number;
  to: number;
  bucket: 'hour' | 'day';
  kind?: string;
  channel?: string;
  cursor?: string;
}

function money(value: unknown, signed = false): string {
  const result = string(value, 'audit amount', { min: 1, max: 65, ascii: true });
  if (!(signed ? /^(?:0|-?[1-9][0-9]{0,63})$/ : /^(?:0|[1-9][0-9]{0,63})$/).test(result))
    invalidResponse('audit amount');
  return result;
}
function nullableMoney(value: unknown) {
  return value === null ? null : money(value, true);
}
function sequence(value: unknown) {
  return decimal(string(value, 'ledger sequence', { max: 19 }), 'ledger sequence');
}

export function metrics(value: unknown) {
  const v = record(
    value,
    ['issued', 'reclaimed', 'user_income', 'user_expense', 'internal_transfer', 'operations'],
    'audit metrics',
  );
  return {
    issued: money(v.issued),
    reclaimed: money(v.reclaimed),
    user_income: money(v.user_income),
    user_expense: money(v.user_expense),
    internal_transfer: money(v.internal_transfer),
    operations: sequence(v.operations),
  };
}
export type Metrics = ReturnType<typeof metrics>;

function metadata(value: unknown) {
  const v = record(
    value,
    [
      'asset',
      'from',
      'to',
      'unit',
      'scale',
      'offset_minutes',
      'ledger_seq',
      'projected_seq',
      'snapshot_at',
      'coverage',
    ],
    'audit metadata',
  );
  if (v.unit !== 'milliunits' || v.scale !== '1000') invalidResponse('audit unit');
  const offset = integer(v.offset_minutes, 'site offset', -720, 840);
  if (offset % 30 !== 0) invalidResponse('site offset');
  const c = record(
    v.coverage,
    ['status', 'first_ledger_seq', 'first_occurred_at', 'unclassified_operations', 'opening_known'],
    'audit coverage',
  );
  return {
    asset: oneOf(v.asset, assets, 'asset'),
    from: unixSecond(v.from, 'range start'),
    to: unixSecond(v.to, 'range end'),
    offset_minutes: offset,
    ledger_seq: sequence(v.ledger_seq),
    projected_seq: sequence(v.projected_seq),
    snapshot_at: unixSecond(v.snapshot_at, 'snapshot time'),
    coverage: {
      status: oneOf(
        c.status,
        ['complete', 'catching_up', 'unclassified', 'opening_unknown'] as const,
        'coverage',
      ),
      first_ledger_seq: c.first_ledger_seq === null ? null : sequence(c.first_ledger_seq),
      first_occurred_at: nullableUnixSecond(c.first_occurred_at, 'coverage start'),
      unclassified_operations: sequence(c.unclassified_operations),
      opening_known: boolean(c.opening_known, 'opening known'),
    },
  };
}
export type Metadata = ReturnType<typeof metadata>;

function inventory(value: unknown) {
  const keys = [
    'user_available',
    'frozen',
    'pools',
    'platform',
    'negative_users',
    'negative_frozen',
    'negative_pools',
    'negative_platform',
    'net',
  ] as const;
  const v = record(value, keys, 'inventory');
  return {
    user_available: money(v.user_available),
    frozen: money(v.frozen),
    pools: money(v.pools),
    platform: money(v.platform),
    negative_users: money(v.negative_users),
    negative_frozen: money(v.negative_frozen),
    negative_pools: money(v.negative_pools),
    negative_platform: money(v.negative_platform),
    net: money(v.net, true),
  };
}

export function decodeSummary(value: unknown) {
  const v = record(value, ['metadata', 'flows', 'inventory', 'reconciliation'], 'audit summary');
  const r = record(
    v.reconciliation,
    [
      'status',
      'scope',
      'inventory_net',
      'ledger_net',
      'interval_opening_net',
      'interval_closing_net',
      'interval_net_change',
    ],
    'reconciliation',
  );
  if (r.scope !== 'retained_ledger') invalidResponse('reconciliation scope');
  return {
    metadata: metadata(v.metadata),
    flows: metrics(v.flows),
    inventory: v.inventory === null ? null : inventory(v.inventory),
    reconciliation: {
      status: oneOf(
        r.status,
        ['matched', 'mismatch', 'catching_up', 'unclassified', 'opening_unknown'] as const,
        'reconciliation status',
      ),
      inventory_net: nullableMoney(r.inventory_net),
      ledger_net: nullableMoney(r.ledger_net),
      interval_opening_net: nullableMoney(r.interval_opening_net),
      interval_closing_net: nullableMoney(r.interval_closing_net),
      interval_net_change: nullableMoney(r.interval_net_change),
    },
  };
}
export type Summary = ReturnType<typeof decodeSummary>;

export function decodeSeries(value: unknown) {
  const v = record(value, ['metadata', 'bucket', 'data'], 'audit series');
  const bucket = oneOf(v.bucket, ['hour', 'day'] as const, 'audit bucket');
  return {
    metadata: metadata(v.metadata),
    bucket,
    data: array(v.data, 'audit series', bucket === 'hour' ? 744 : 366).map((item) => {
      const p = record(item, ['start', 'end', 'metrics'], 'audit point');
      return {
        start: unixSecond(p.start, 'point start'),
        end: unixSecond(p.end, 'point end'),
        metrics: metrics(p.metrics),
      };
    }),
  };
}
export type Series = ReturnType<typeof decodeSeries>;

export function decodeChannels(value: unknown) {
  const v = record(value, ['metadata', 'data'], 'audit channels');
  return {
    metadata: metadata(v.metadata),
    data: array(v.data, 'audit channels', 100).map((item) => {
      const c = record(
        item,
        ['kind', 'source_type', 'channel', 'known', 'metrics'],
        'audit channel',
      );
      return {
        kind: string(c.kind, 'operation kind', { max: 64 }),
        source_type: string(c.source_type, 'source type', { max: 32 }),
        channel: string(c.channel, 'channel', { max: 32 }),
        known: boolean(c.known, 'classified'),
        metrics: metrics(c.metrics),
      };
    }),
  };
}
export type Channels = ReturnType<typeof decodeChannels>;

export function decodeOperations(value: unknown) {
  const v = record(value, ['metadata', 'anchor_seq', 'data', 'next_cursor'], 'audit operations');
  return {
    metadata: metadata(v.metadata),
    anchor_seq: sequence(v.anchor_seq),
    next_cursor: nullableString(v.next_cursor, 'audit cursor', { min: 1, max: 2048 }),
    data: array(v.data, 'audit operations', 100).map((item) => {
      const o = record(
        item,
        [
          'id',
          'ledger_seq',
          'kind',
          'source_type',
          'source_id',
          'created_at',
          'classification',
          'entries',
        ],
        'audit operation',
      );
      const c = record(
        o.classification,
        ['channel', 'behavior', 'known'],
        'operation classification',
      );
      return {
        id: opaqueID(o.id, 'op_', 'operation ID'),
        ledger_seq: sequence(o.ledger_seq),
        kind: string(o.kind, 'operation kind', { max: 64 }),
        source_type: string(o.source_type, 'source type', { max: 32 }),
        source_id: string(o.source_id, 'source ID', { max: 64 }),
        created_at: unixSecond(o.created_at, 'operation time'),
        classification: {
          channel: string(c.channel, 'channel', { max: 32 }),
          behavior: string(c.behavior, 'behavior', { max: 32 }),
          known: boolean(c.known, 'classified'),
        },
        entries: array(o.entries, 'operation entries', 256).map((item) => {
          const e = record(item, ['asset', 'account_kind', 'user_id', 'delta'], 'ledger entry');
          return {
            asset: oneOf(e.asset, assets, 'entry asset'),
            account_kind: oneOf(
              e.account_kind,
              ['user', 'pool', 'platform', 'external'] as const,
              'account type',
            ),
            user_id: e.user_id === null ? null : sequence(e.user_id),
            delta: money(e.delta, true),
          };
        }),
      };
    }),
  };
}
export type Operations = ReturnType<typeof decodeOperations>;

export const getSummary = (filter: AuditFilter, signal?: AbortSignal) =>
  decoded(
    queryPath('/admin/api/economy-audit/summary', { ...filter, cursor: undefined }),
    decodeSummary,
    { signal },
  );
export const getSeries = (filter: AuditFilter, signal?: AbortSignal) =>
  decoded(
    queryPath('/admin/api/economy-audit/series', { ...filter, cursor: undefined }),
    decodeSeries,
    { signal },
  );
export const getChannels = (filter: AuditFilter, signal?: AbortSignal) =>
  decoded(
    queryPath('/admin/api/economy-audit/channels', { ...filter, cursor: undefined }),
    decodeChannels,
    { signal },
  );
export const getOperations = (filter: AuditFilter, signal?: AbortSignal) =>
  decoded(queryPath('/admin/api/economy-audit/operations', { ...filter }), decodeOperations, {
    signal,
  });

export function displayAmount(milli: string): string {
  const value = BigInt(milli),
    negative = value < 0n,
    absolute = negative ? -value : value;
  const whole = (absolute / 1000n).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',');
  const remainder = (absolute % 1000n).toString().padStart(3, '0').replace(/0+$/, '');
  return `${negative ? '-' : ''}${whole}${remainder ? `.${remainder}` : ''}`;
}

export function siteDateTime(at: number, offset: number): string {
  return new Date((at + offset * 60) * 1000).toISOString().slice(0, 19);
}
export function parseSiteDateTime(value: string, offset: number): number | null {
  if (!/^\d{4}-\d\d-\d\dT\d\d:\d\d(?::\d\d)?$/.test(value)) return null;
  const ms = Date.parse(`${value.length === 16 ? `${value}:00` : value}Z`);
  if (!Number.isFinite(ms)) return null;
  const at = ms / 1000 - offset * 60;
  if (
    at < 0 ||
    at > 253402300799 ||
    siteDateTime(at, offset) !== (value.length === 16 ? `${value}:00` : value)
  )
    return null;
  return at;
}
