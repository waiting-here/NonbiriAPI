import { isAISDKGateway } from '@shared/operations/connectorPolicy';
import { DraftLeaveGuard } from './charity/DraftLeaveScope';
import { ConfirmDialog } from './ConfirmDialog';
import { Button } from '@shared/components/ui/Button';
import { Field, Fields, FormActions, Fold, Note, OutcomeNote } from '@shared/components/ui';
import { useEffect, useLayoutEffect, useRef, useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { apiFetch, isApiError } from '@shared/query/http';
import { useRequestAdaptationCopy, type RequestAdaptationCopyKey } from './requestAdaptationCopy';

type Mode = 'inherit' | 'replace';
type MapKind = 'fixed_headers' | 'body_defaults' | 'body_forced';
type ListKind = 'forward_headers' | 'native_extension_paths';
type SectionKind = MapKind | ListKind;
type Action = 'keep' | 'replace' | 'clear';
type Copy = ReturnType<typeof useRequestAdaptationCopy>;

interface ListProjection {
  mode: Mode;
  values: string[];
  source?: string;
}
interface MapProjection {
  mode: Mode;
  values: Record<
    string,
    { has_value: boolean; mask?: string; sensitive_paths?: string[]; value?: unknown }
  >;
  source?: string;
}
interface Projection {
  excluded_request_fields?: string[];
  revision: string;
  forward_headers: ListProjection;
  fixed_headers: MapProjection;
  body_defaults: MapProjection;
  body_forced: MapProjection;
  native_extension_paths: ListProjection;
}
interface View extends Projection {
  effective?: Projection;
}
interface Row {
  name: string;
  action: Action;
  value: string;
  savedValue?: string;
  existing: boolean;
  structuredCache?: boolean;
  sensitivePaths?: string[];
  sensitivePathText?: string;
  sensitivityChanged?: boolean;
}
interface ListDraft {
  mode: Mode;
  text: string;
}
interface MapDraft {
  mode: Mode;
  rows: Row[];
}
interface Draft {
  excluded: string;
  forward_headers: ListDraft;
  fixed_headers: MapDraft;
  body_defaults: MapDraft;
  body_forced: MapDraft;
  native_extension_paths: ListDraft;
}

const labels: Record<SectionKind, RequestAdaptationCopyKey> = {
  forward_headers: 'forwardHeaders',
  fixed_headers: 'fixedHeaders',
  body_defaults: 'bodyDefaults',
  body_forced: 'bodyForced',
  native_extension_paths: 'nativePaths',
};

function record(value: unknown): Record<string, unknown> {
  if (value === null || typeof value !== 'object' || Array.isArray(value))
    throw new Error('Invalid adaptation response');
  return value as Record<string, unknown>;
}

function mode(value: unknown): Mode {
  if (value !== 'inherit' && value !== 'replace') throw new Error('Invalid adaptation mode');
  return value;
}

function parseList(value: unknown): ListProjection {
  const section = record(value);
  if (!Array.isArray(section.values) || !section.values.every((item) => typeof item === 'string'))
    throw new Error('Invalid adaptation list');
  return {
    mode: mode(section.mode),
    values: section.values,
    ...(typeof section.source === 'string' ? { source: section.source } : {}),
  };
}

function parseMap(value: unknown): MapProjection {
  const section = record(value);
  const values = record(section.values);
  const safe: MapProjection['values'] = Object.create(null) as MapProjection['values'];
  for (const [name, raw] of Object.entries(values)) {
    const entry = record(raw);
    const readable = Object.hasOwn(entry, 'value');
    if (entry.has_value !== true || (!readable && typeof entry.mask !== 'string'))
      throw new Error('Invalid adaptation projection');
    safe[name] = {
      has_value: true,
      ...(readable ? { value: entry.value } : { mask: entry.mask as string }),
      ...(Array.isArray(entry.sensitive_paths) &&
      entry.sensitive_paths.every((path) => typeof path === 'string')
        ? { sensitive_paths: entry.sensitive_paths as string[] }
        : {}),
    };
  }
  return {
    mode: mode(section.mode),
    values: safe,
    ...(typeof section.source === 'string' ? { source: section.source } : {}),
  };
}

function parseProjection(value: unknown, effective = false): Projection {
  const root = record(value);
  if (
    typeof root.revision !== 'string' ||
    !(effective ? /^(0|[1-9][0-9]*)\/(0|[1-9][0-9]*)$/ : /^(0|[1-9][0-9]*)$/).test(root.revision)
  )
    throw new Error('Invalid adaptation revision');
  return {
    revision: root.revision,
    ...(Array.isArray(root.excluded_request_fields)
      ? { excluded_request_fields: root.excluded_request_fields as string[] }
      : {}),
    forward_headers: parseList(root.forward_headers),
    fixed_headers: parseMap(root.fixed_headers),
    body_defaults: parseMap(root.body_defaults),
    body_forced: parseMap(root.body_forced),
    native_extension_paths: parseList(root.native_extension_paths),
  };
}

function parseView(value: unknown): View {
  const root = record(value);
  const projection = parseProjection(value);
  return {
    ...projection,
    ...(root.effective === undefined ? {} : { effective: parseProjection(root.effective, true) }),
  };
}

function mapDraft(section: MapProjection, kind: MapKind): MapDraft {
  return {
    mode: section.mode,
    rows: Object.entries(section.values).map(([name, entry]) => {
      const savedValue = Object.hasOwn(entry, 'value')
        ? kind === 'fixed_headers'
          ? String(entry.value)
          : JSON.stringify(entry.value, null, 2)
        : undefined;
      return {
        name,
        action: 'keep',
        value: savedValue ?? '',
        savedValue,
        existing: true,
        sensitivePaths: entry.sensitive_paths,
      };
    }),
  };
}

function newDraft(view: Projection): Draft {
  return {
    excluded: (view.excluded_request_fields ?? []).join('\n'),
    forward_headers: {
      mode: view.forward_headers.mode,
      text: view.forward_headers.values.join('\n'),
    },
    fixed_headers: mapDraft(view.fixed_headers, 'fixed_headers'),
    body_defaults: mapDraft(view.body_defaults, 'body_defaults'),
    body_forced: mapDraft(view.body_forced, 'body_forced'),
    native_extension_paths: {
      mode: view.native_extension_paths.mode,
      text: view.native_extension_paths.values.join('\n'),
    },
  };
}

function buildPayload(draft: Draft, revision: string, copy: Copy) {
  const payload: Record<string, unknown> = { expected_revision: revision };
  for (const kind of ['forward_headers', 'native_extension_paths'] as const) {
    const section = draft[kind];
    const values =
      section.mode === 'inherit'
        ? []
        : section.text
            .split(/\r?\n/)
            .map((item) => item.trim())
            .filter(Boolean);
    if (new Set(values).size !== values.length) throw new Error(copy('duplicate'));
    payload[kind] = { mode: section.mode, values };
  }
  for (const kind of ['fixed_headers', 'body_defaults', 'body_forced'] as const) {
    const section = draft[kind];
    const values: Record<string, unknown> = Object.create(null) as Record<string, unknown>;
    if (section.mode === 'replace') {
      const names = new Set<string>();
      for (const row of section.rows) {
        const name = row.name.trim();
        if (!name) throw new Error(copy('name'));
        const unique = kind === 'fixed_headers' ? name.toLowerCase() : name;
        if (names.has(unique)) throw new Error(copy('duplicate'));
        names.add(unique);
        if (row.action === 'replace') {
          let value: unknown = row.value;
          if (kind !== 'fixed_headers') {
            try {
              value = JSON.parse(row.value) as unknown;
            } catch {
              throw new Error(copy('invalidJSON'));
            }
          }
          values[name] = { action: 'replace', value };
        } else {
          values[name] = { action: row.action };
        }
        if (row.action !== 'clear' && row.sensitivityChanged) {
          const paths =
            row.sensitivePathText === undefined
              ? (row.sensitivePaths ?? [])
              : row.sensitivePathText.split('\n').filter(Boolean);
          values[name] = { ...(values[name] as Record<string, unknown>), sensitive_paths: paths };
        }
      }
    }
    payload[kind] = { mode: section.mode, values };
  }
  return payload;
}

type TransferRules = Partial<Record<MapKind, Record<string, string | number | boolean>>> &
  Partial<Record<ListKind, string[]>>;
const transferSections = ['body_defaults', 'body_forced', 'fixed_headers'] as const;

function transferableValue(
  kind: MapKind,
  name: string,
  value: unknown,
): value is string | number | boolean {
  if (kind === 'fixed_headers') {
    if (typeof value !== 'string') return false;
    return (
      (name.toLowerCase() === 'x-client-version' &&
        typeof value === 'string' &&
        /^\d{1,4}(?:\.\d{1,4}){0,3}$/.test(value)) ||
      (['content-type', 'accept'].includes(name.toLowerCase()) &&
        ['application/json', 'text/event-stream'].includes(String(value)))
    );
  }
  if (typeof value === 'number' && Number.isFinite(value))
    return [
      '/temperature',
      '/top_p',
      '/top_k',
      '/max_tokens',
      '/max_completion_tokens',
      '/max_output_tokens',
      '/presence_penalty',
      '/frequency_penalty',
      '/seed',
      '/n',
    ].includes(name);
  if (typeof value === 'boolean')
    return ['/stream', '/parallel_tool_calls', '/logprobs'].includes(name);
  if (typeof value !== 'string') return false;
  return (
    (name === '/reasoning_effort' &&
      ['minimal', 'low', 'medium', 'high', 'xhigh'].includes(String(value))) ||
    (name === '/tool_choice' && ['auto', 'none', 'required'].includes(String(value)))
  );
}

function protectedRule(row: Row) {
  return Boolean(
    row.sensitivePaths?.length ||
    row.sensitivePathText?.trim() ||
    (row.existing && row.savedValue === undefined),
  );
}

function safeRuleName(kind: ListKind, value: string) {
  if (/authorization|cookie|token|secret|credential|password|api[-_]?key/i.test(value))
    return false;
  return kind === 'forward_headers'
    ? /^[a-z][a-z0-9-]{0,62}$/i.test(value)
    : /^\/[a-z][a-z0-9_]*(?:\/[a-z][a-z0-9_]*){0,5}$/i.test(value);
}

function exportRules(draft: Draft): TransferRules {
  const result: TransferRules = {};
  for (const kind of ['forward_headers', 'native_extension_paths'] as const) {
    if (draft[kind].mode !== 'replace') continue;
    const names = draft[kind].text
      .split(/\r?\n/)
      .map((name) => name.trim())
      .filter((name) => safeRuleName(kind, name));
    if (names.length) result[kind] = names;
  }
  for (const kind of transferSections) {
    if (draft[kind].mode !== 'replace') continue;
    for (const row of draft[kind].rows) {
      if (row.action === 'clear' || protectedRule(row)) continue;
      const value: unknown =
        kind === 'fixed_headers'
          ? row.value
          : validJson(row.value)
            ? JSON.parse(row.value)
            : undefined;
      if (transferableValue(kind, row.name, value)) (result[kind] ??= {})[row.name] = value;
    }
  }
  return result;
}

function mergeRules(draft: Draft, text: string): Draft {
  const rules = record(JSON.parse(text));
  const next = { ...draft };
  let count = 0;
  for (const [rawKind, rawRules] of Object.entries(rules)) {
    if (rawKind === 'forward_headers' || rawKind === 'native_extension_paths') {
      if (
        !Array.isArray(rawRules) ||
        !rawRules.every((name) => typeof name === 'string' && safeRuleName(rawKind, name))
      )
        throw new Error('invalid rule list');
      if (rawRules.length) {
        const prior = next[rawKind].text
          .split(/\r?\n/)
          .map((name) => name.trim())
          .filter(Boolean);
        next[rawKind] = { mode: 'replace', text: [...new Set([...prior, ...rawRules])].join('\n') };
        count += rawRules.length;
      }
      continue;
    }
    const kind = transferSections.find((key) => key === rawKind);
    if (!kind) throw new Error('invalid section');
    const rows = [...draft[kind].rows];
    for (const [name, value] of Object.entries(record(rawRules))) {
      if (!transferableValue(kind, name, value)) throw new Error('invalid portable rule');
      const index = rows.findIndex((row) =>
        kind === 'fixed_headers'
          ? row.name.toLowerCase() === name.toLowerCase()
          : row.name === name,
      );
      const previous = rows[index];
      if (previous && protectedRule(previous)) throw new Error('protected rule');
      const row: Row = {
        ...previous,
        existing: previous?.existing ?? false,
        name: previous?.name ?? name,
        action: 'replace',
        value: kind === 'fixed_headers' ? String(value) : JSON.stringify(value),
      };
      if (index < 0) rows.push(row);
      else rows[index] = row;
      count++;
    }
    if (Object.keys(record(rawRules)).length) next[kind] = { mode: 'replace', rows };
  }
  if (!count) throw new Error('empty portable rule set');
  return next;
}

function RuleTransfer({
  draft,
  setDraft,
  copy,
}: {
  draft: Draft;
  setDraft: (draft: Draft) => void;
  copy: Copy;
}) {
  const { t } = useTranslation();
  const [text, setText] = useState('');
  const [preview, setPreview] = useState('');
  const [error, setError] = useState(false);
  function perform(apply: boolean) {
    try {
      const next = mergeRules(draft, text);
      buildPayload(next, '0', copy);
      if (apply) {
        setDraft(next);
        setPreview('');
        setText('');
      } else setPreview(JSON.stringify(JSON.parse(text), null, 2));
      setError(false);
    } catch {
      setError(true);
      setPreview('');
    }
  }
  return (
    <Fold title={t('common.adaptationTransfer.title')} plain>
      <Field
        label={t('common.adaptationTransfer.rules')}
        help={t('common.adaptationTransfer.help')}
        error={error ? t('common.adaptationTransfer.invalid') : undefined}
      >
        {(field) => (
          <textarea
            {...field}
            rows={5}
            maxLength={65536}
            value={text}
            onChange={(event) => {
              setText(event.target.value);
              setPreview('');
              setError(false);
            }}
          />
        )}
      </Field>
      <Button
        type="button"
        onClick={() => {
          setText(JSON.stringify(exportRules(draft), null, 2));
          setPreview('');
          setError(false);
        }}
      >
        {t('common.adaptationTransfer.export')}
      </Button>
      <Button type="button" disabled={!text.trim()} onClick={() => perform(false)}>
        {t('common.adaptationTransfer.preview')}
      </Button>
      {preview && (
        <>
          <pre>{preview}</pre>
          <Button type="button" onClick={() => perform(true)}>
            {t('common.adaptationTransfer.apply')}
          </Button>
        </>
      )}
    </Fold>
  );
}

function newOperationKey(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(24));
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('');
}

function validJson(text: string) {
  try {
    JSON.parse(text);
    return true;
  } catch {
    return false;
  }
}

function MapEditor({
  kind,
  section,
  editable,
  change,
  gatewayCacheDefaults = false,
  endpointScope = false,
  inheritedHelp,
  effective,
}: {
  kind: MapKind;
  section: MapDraft;
  editable: boolean;
  change: (next: MapDraft) => void;
  gatewayCacheDefaults?: boolean;
  endpointScope?: boolean;
  inheritedHelp?: string;
  effective?: MapProjection;
}) {
  const copy = useRequestAdaptationCopy(endpointScope);
  const { t } = useTranslation();
  if (section.mode === 'inherit')
    return (
      <>
        <p>{inheritedHelp ?? copy('inherited')}</p>
        {gatewayCacheDefaults ? <p>{t('requestAdaptation.cache.inherited')}</p> : null}
        {effective?.mode === 'replace' ? (
          <MapEditor
            kind={kind}
            section={mapDraft(effective, kind)}
            editable={false}
            change={() => undefined}
            gatewayCacheDefaults={gatewayCacheDefaults}
            endpointScope={endpointScope}
          />
        ) : null}
      </>
    );
  const structuredCache = (row: Row) =>
    row.structuredCache ||
    (row.existing && row.action !== 'replace' && row.savedValue === undefined);
  const cacheRows = gatewayCacheDefaults
    ? section.rows.filter((row) => row.name.trim() === '/cache_control')
    : [];
  const cacheRow = cacheRows.find((row) => !structuredCache(row)) ?? cacheRows[0];
  const structuredCacheRow = !cacheRow || structuredCache(cacheRow);
  const savedCache = cacheRows.some((row) => row.existing);
  const cacheValue = !cacheRow
    ? 'off'
    : !structuredCacheRow
      ? 'custom'
      : cacheRow.action === 'clear'
        ? 'off'
        : cacheRow.action === 'keep'
          ? 'keep'
          : cacheRow.value === JSON.stringify({ type: 'ephemeral', ttl: '1h' })
            ? '1h'
            : '5m';
  const changeCache = (ttl: string) => {
    const rows = section.rows.filter((row) => row.name.trim() !== '/cache_control');
    if (ttl !== 'off' || savedCache)
      rows.push({
        name: '/cache_control',
        existing: savedCache,
        structuredCache: true,
        action: ttl === 'off' ? 'clear' : ttl === 'keep' ? 'keep' : 'replace',
        value: ttl === '5m' || ttl === '1h' ? JSON.stringify({ type: 'ephemeral', ttl }) : '',
      });
    change({ ...section, rows });
  };
  const changeRow = (index: number, next: Row) =>
    change({
      ...section,
      rows: section.rows.map((row, position) => (position === index ? next : row)),
    });
  return (
    <div className="core-form">
      {gatewayCacheDefaults ? (
        <Field label={t('requestAdaptation.cache.label')} help={t('requestAdaptation.cache.help')}>
          {(field) => (
            <select
              {...field}
              value={cacheValue}
              disabled={!editable}
              onChange={(event) => changeCache(event.target.value)}
            >
              {!structuredCacheRow ? (
                <option value="custom" disabled>
                  {t('requestAdaptation.cache.custom')}
                </option>
              ) : null}
              {savedCache ? (
                <option value="keep">{t('requestAdaptation.cache.keep')}</option>
              ) : null}
              <option value="off">{t('requestAdaptation.cache.off')}</option>
              <option value="5m">{t('requestAdaptation.cache.fiveMinutes')}</option>
              <option value="1h">{t('requestAdaptation.cache.oneHour')}</option>
            </select>
          )}
        </Field>
      ) : null}
      <p>{copy(kind === 'fixed_headers' ? 'fixedHelp' : 'bodyHelp')}</p>
      {section.rows.length === 0 ? <p>{copy('empty')}</p> : null}
      {section.rows.map((row, index) =>
        gatewayCacheDefaults &&
        structuredCache(row) &&
        row.name.trim() === '/cache_control' ? null : (
          <Fields key={index}>
            <Field label={copy('name')}>
              {(field) => (
                <input
                  {...field}
                  value={row.name}
                  placeholder={kind === 'fixed_headers' ? 'User-Agent' : '/temperature'}
                  maxLength={512}
                  readOnly={row.existing || !editable}
                  onChange={(event) => changeRow(index, { ...row, name: event.target.value })}
                />
              )}
            </Field>
            <Field size="short" label={copy('action')}>
              {(field) => (
                <select
                  {...field}
                  value={row.action}
                  disabled={!editable}
                  onChange={(event) =>
                    changeRow(index, {
                      ...row,
                      action: event.target.value as Action,
                      value: row.savedValue ?? '',
                    })
                  }
                >
                  {row.existing ? <option value="keep">{copy('keep')}</option> : null}
                  <option value="replace">{copy('change')}</option>
                  <option value="clear">{copy('clear')}</option>
                </select>
              )}
            </Field>
            {row.action === 'replace' || (row.action === 'keep' && row.savedValue !== undefined) ? (
              <Field
                size="long"
                label={copy('value')}
                error={
                  row.action === 'replace' && kind !== 'fixed_headers' && !validJson(row.value)
                    ? copy('invalidJSON')
                    : undefined
                }
              >
                {(field) =>
                  kind === 'fixed_headers' ? (
                    <input
                      {...field}
                      type="text"
                      autoComplete="off"
                      value={row.value}
                      maxLength={4096}
                      readOnly={!editable}
                      onChange={(event) =>
                        changeRow(index, { ...row, action: 'replace', value: event.target.value })
                      }
                    />
                  ) : (
                    <textarea
                      {...field}
                      value={row.value}
                      placeholder={kind === 'body_defaults' ? '0.7' : '"low"'}
                      rows={2}
                      maxLength={65536}
                      readOnly={!editable}
                      onChange={(event) =>
                        changeRow(index, { ...row, action: 'replace', value: event.target.value })
                      }
                    />
                  )
                }
              </Field>
            ) : row.existing && row.action === 'keep' ? (
              <span>{copy('masked')}</span>
            ) : null}
            {!row.existing && editable && (
              <Button
                type="button"
                variant="ghost"
                onClick={() =>
                  change({
                    ...section,
                    rows: section.rows.filter((_, position) => position !== index),
                  })
                }
              >
                {t('common.remove')}
              </Button>
            )}
            {editable && kind !== 'fixed_headers' && row.action === 'replace' && (
              <Button
                type="button"
                variant="ghost"
                disabled={!validJson(row.value)}
                onClick={() =>
                  changeRow(index, {
                    ...row,
                    value: JSON.stringify(JSON.parse(row.value), null, 2),
                  })
                }
              >
                {t('common.adaptationTransfer.format')}
              </Button>
            )}
            <div>
              <Field label={copy('protection')} help={copy('protectionHelp')}>
                {(field) => (
                  <select
                    {...field}
                    aria-label={`${copy('protection')}: ${row.name}`}
                    value={
                      row.sensitivePaths?.includes('')
                        ? 'whole'
                        : row.sensitivePaths?.length
                          ? 'leaves'
                          : 'off'
                    }
                    disabled={!editable || row.action === 'clear'}
                    onChange={(event) =>
                      changeRow(index, {
                        ...row,
                        sensitivePaths:
                          event.target.value === 'whole'
                            ? ['']
                            : event.target.value === 'leaves'
                              ? ['/token']
                              : [],
                        sensitivePathText: undefined,
                        sensitivityChanged: true,
                      })
                    }
                  >
                    <option value="off">{copy('protectionOff')}</option>
                    <option value="whole">{copy('protectionWhole')}</option>
                    {kind !== 'fixed_headers' ? (
                      <option value="leaves">{copy('protectionLeaves')}</option>
                    ) : null}
                  </select>
                )}
              </Field>
              {kind !== 'fixed_headers' &&
              row.sensitivePaths?.length &&
              !row.sensitivePaths.includes('') ? (
                <Field size="long" label={copy('protectedPaths')} help={copy('protectedPathsHelp')}>
                  {(field) => (
                    <textarea
                      {...field}
                      aria-label={copy('protectedPaths')}
                      value={row.sensitivePathText ?? row.sensitivePaths?.join('\n') ?? ''}
                      rows={2}
                      placeholder="/token"
                      disabled={!editable || row.action === 'clear'}
                      onChange={(event) =>
                        changeRow(index, {
                          ...row,
                          sensitivePathText: event.target.value,
                          sensitivityChanged: true,
                        })
                      }
                    />
                  )}
                </Field>
              ) : null}
            </div>
          </Fields>
        ),
      )}
      {editable ? (
        <Button
          type="button"
          variant="ghost"
          onClick={() =>
            change({
              ...section,
              rows: [
                ...section.rows,
                {
                  name:
                    kind === 'fixed_headers'
                      ? 'x-client-version'
                      : kind === 'body_defaults'
                        ? '/temperature'
                        : '/tool_choice',
                  value:
                    kind === 'fixed_headers' ? '1' : kind === 'body_defaults' ? '0.7' : '"auto"',
                  action: 'replace',
                  existing: false,
                },
              ],
            })
          }
        >
          {copy('example')}
        </Button>
      ) : null}
      {editable ? (
        <Button
          type="button"

          onClick={() =>
            change({
              ...section,
              rows: [...section.rows, { name: '', action: 'replace', value: '', existing: false }],
            })
          }
        >
          {copy('add')}
        </Button>
      ) : null}
    </div>
  );
}

export function RequestAdaptationEditor({
  url,
  scope,
  connectorType,
  editable,
  gatewayCacheDefaults = false,
  onConfiguredCount,
}: {
  url: string;
  scope: 'endpoint' | 'personal-model' | 'charity-model' | 'binding';
  connectorType?: string;
  editable: boolean;
  gatewayCacheDefaults?: boolean;
  onConfiguredCount?: (count: number) => void;
}) {
  const { t } = useTranslation();
  const modelScope = scope === 'personal-model' || scope === 'charity-model';
  const copy = useRequestAdaptationCopy(scope === 'endpoint' || scope === 'personal-model');
  const [confirmRefresh, setConfirmRefresh] = useState(false);
  const [reload, setReload] = useState(0);
  const [view, setView] = useState<View | null>(null);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState('');
  const [loadError, setLoadError] = useState<'stale' | 'loadError' | 'savedRefreshFailed' | null>(
    null,
  );
  const [blocked, setBlocked] = useState(false);
  const generation = useRef(0);
  useLayoutEffect(() => {
    generation.current++;
    setSaving(false);
    return () => {
      generation.current++;
    };
  }, [url]);
  const [loadedContext, setLoadedContext] = useState<string | null>(null);
  const loadedContextRef = useRef<string | null>(null);
  const contextChanged = loadedContext !== null && loadedContext !== url;
  const dirty = Boolean(
    editable &&
    view &&
    draft &&
    !contextChanged &&
    JSON.stringify(draft) !== JSON.stringify(newDraft(view)),
  );
  const saveBlocked = blocked || contextChanged;
  const canEdit = editable && !saving && !saveBlocked;

  useEffect(() => {
    const controller = new AbortController();
    const hasPriorProjection = loadedContextRef.current !== null;
    void apiFetch<unknown>(url, { signal: controller.signal })
      .then((payload) => {
        if (controller.signal.aborted) return;
        const next = parseView(payload);
        loadedContextRef.current = url;
        setLoadedContext(url);
        setView(next);
        setDraft(newDraft(next));
        setNotice('');
        setLoadError(null);
        setBlocked(false);
      })
      .catch(() => {
        if (!controller.signal.aborted) {
          setLoadError((current) =>
            current === 'savedRefreshFailed' ? current : hasPriorProjection ? 'stale' : 'loadError',
          );
          setBlocked(true);
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [url, reload]);

  useEffect(() => {
    if (view)
      onConfiguredCount?.(
        view.forward_headers.values.length +
          view.native_extension_paths.values.length +
          Object.keys(view.fixed_headers.values).length +
          Object.keys(view.body_defaults.values).length +
          Object.keys(view.body_forced.values).length +
          (view.excluded_request_fields?.length ?? 0),
      );
  }, [view, onConfiguredCount]);

  const updateList = (kind: ListKind, next: ListDraft) => {
    setNotice('');
    setDraft((current) => (current ? { ...current, [kind]: next } : current));
  };
  const updateMap = (kind: MapKind, next: MapDraft) => {
    setNotice('');
    setDraft((current) => (current ? { ...current, [kind]: next } : current));
  };
  const refresh = () => {
    setLoading(true);
    if (loadedContextRef.current !== null) {
      setBlocked(true);
      if (loadError !== 'savedRefreshFailed') setNotice('');
    }
    setReload((value) => value + 1);
  };
  const save = async (event: FormEvent) => {
    event.preventDefault();
    if (!editable || !view || !draft || saving || saveBlocked) return;
    let payload: Record<string, unknown>;
    try {
      payload = buildPayload(draft, view.revision, copy);
      if (scope === 'personal-model')
        payload.excluded_request_fields = draft.excluded
          .split(/\r?\n/)
          .map((x) => x.trim())
          .filter(Boolean);
    } catch (error) {
      event.currentTarget.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus();
      setNotice(error instanceof Error ? error.message : copy('saveError'));
      return;
    }
    const operationGeneration = generation.current;
    setSaving(true);
    setNotice('');
    let successfulResponseReceived = false;
    try {
      const response = await apiFetch<unknown>(url, {
        method: 'PUT',
        headers: { 'Idempotency-Key': newOperationKey() },
        json: payload,
      });
      if (generation.current !== operationGeneration) return;
      successfulResponseReceived = true;
      const next = parseView(response);
      setView(next);
      setDraft(newDraft(next));
      setLoadError(null);
      try {
        const current = parseView(await apiFetch<unknown>(url));
        if (generation.current !== operationGeneration) return;
        setView(current);
        setDraft(newDraft(current));
        setBlocked(false);
      } catch {
        if (generation.current !== operationGeneration) return;
        setLoadError('savedRefreshFailed');
        setBlocked(true);
      }
      setNotice(modelScope ? t('requestAdaptation.model.saved') : copy('saved'));
    } catch (error) {
      if (generation.current !== operationGeneration) return;
      if (isApiError(error) && error.status === 409) {
        setNotice(copy('conflict'));
        setBlocked(true);
      } else if (
        successfulResponseReceived ||
        (isApiError(error) &&
          (error.code === 'network_error' ||
            (error.code === 'invalid_response' && error.status >= 200 && error.status < 300)))
      ) {
        setNotice(copy('unknown'));
        setBlocked(true);
      } else {
        setNotice(copy('saveError'));
      }
    } finally {
      if (generation.current === operationGeneration) setSaving(false);
    }
  };

  const renderSection = (kind: SectionKind) => {
    if (!draft || !view) return null;
    const section = draft[kind];
    const inherited = scope === 'binding' || scope === 'personal-model';
    const setMode = (next: Mode) => {
      if (kind === 'forward_headers' || kind === 'native_extension_paths')
        updateList(kind, {
          mode: next,
          text: (section as ListDraft).text,
        });
      else
        updateMap(kind, {
          mode: next,
          rows: (section as MapDraft).rows,
        });
    };
    return (
      <fieldset key={kind} className="core-form">
        <legend>
          {modelScope && (kind === 'forward_headers' || kind === 'fixed_headers')
            ? t(
                kind === 'forward_headers'
                  ? 'requestAdaptation.model.forwardHeaders'
                  : 'requestAdaptation.model.fixedHeaders',
              )
            : copy(labels[kind])}
        </legend>
        {inherited ? (
          <Field label={copy('mode')} size="short">
            {(props) => (
              <select
                {...props}
                value={section.mode}
                disabled={!canEdit}
                onChange={(event) => setMode(event.target.value as Mode)}
              >
                <option value="inherit">
                  {scope === 'personal-model'
                    ? t('requestAdaptation.personal.inheritEndpoint')
                    : copy('inherit')}
                </option>
                <option value="replace">{copy('replace')}</option>
              </select>
            )}
          </Field>
        ) : null}
        {kind === 'forward_headers' || kind === 'native_extension_paths' ? (
          section.mode === 'inherit' ? (
            <p>
              {scope === 'personal-model'
                ? t('requestAdaptation.personal.inheritedEndpoint')
                : copy('inherited')}
              {view.effective?.[kind].values.length ? (
                <code>{view.effective[kind].values.join(', ')}</code>
              ) : null}
            </p>
          ) : (
            <div>
              <Field
                size="long"
                label={t(
                  kind === 'forward_headers'
                    ? 'requestAdaptation.personal.headerListHelp'
                    : 'requestAdaptation.personal.nativeListHelp',
                )}
              >
                {(props) => (
                  <textarea
                    {...props}
                    value={(section as ListDraft).text}
                    rows={3}
                    maxLength={65536}
                    disabled={!canEdit}
                    onChange={(event) =>
                      updateList(kind, { mode: section.mode, text: event.target.value })
                    }
                  />
                )}
              </Field>
              {canEdit ? (
                <Button
                  type="button"
                  variant="ghost"
                  onClick={() =>
                    updateList(kind, {
                      mode: section.mode,
                      text: [
                        (section as ListDraft).text,
                        kind === 'forward_headers' ? 'x-client-version' : '/custom_parameter',
                      ]
                        .filter(Boolean)
                        .join('\n'),
                    })
                  }
                >
                  {copy('example')}
                </Button>
              ) : null}
            </div>
          )
        ) : (
          <MapEditor
            kind={kind}
            endpointScope={scope === 'endpoint' || scope === 'personal-model'}
            inheritedHelp={
              scope === 'personal-model'
                ? t('requestAdaptation.personal.inheritedEndpoint')
                : undefined
            }
            section={section as MapDraft}
            effective={view.effective?.[kind]}
            editable={canEdit}
            gatewayCacheDefaults={
              kind === 'body_defaults' && (isAISDKGateway(connectorType) || gatewayCacheDefaults)
            }
            change={(next) => updateMap(kind, next)}
          />
        )}
        {view.effective ? (
          <p>
            {copy('effective')}:{' '}
            {view.effective[kind].source === 'binding'
              ? copy('sourceBinding')
              : copy('sourceModel')}
          </p>
        ) : null}
      </fieldset>
    );
  };

  const technicalDetails = (
    <>
      <p>
        {connectorType === 'openai-compatible'
          ? copy('openaiSupport')
          : connectorType === 'openai-chat-completions'
            ? copy('chatCompletionsSupport')
            : connectorType === 'typesafe-systemone'
              ? copy('systemOneSupport')
              : connectorType?.endsWith('-rerank')
                ? copy('rerankSupport')
                : connectorType === 'google-gemini'
                  ? copy('geminiSupport')
                  : connectorType === 'openai-responses'
                    ? copy('responsesSupport')
                    : connectorType === 'openai-decisions'
                      ? copy('decisionsSupport')
                      : connectorType
                        ? copy('convertedSupport')
                        : copy('allSupport')}
      </p>
      {view ? (
        <p>
          {copy('revision')}: {view.revision}
        </p>
      ) : null}
    </>
  );
  return (
    <section className="core-card">
      <DraftLeaveGuard dirty={dirty} />
      <ConfirmDialog
        open={confirmRefresh}
        title={t('common.leave.title')}
        description={t('common.leave.body')}
        confirmLabel={copy('refresh')}
        cancelLabel={t('common.leave.stay')}
        onCancel={() => setConfirmRefresh(false)}
        onConfirm={() => {
          setConfirmRefresh(false);
          refresh();
        }}
      />
      <div className="core-card__header">
        <h3>{modelScope ? t('requestAdaptation.model.title') : copy('title')}</h3>
        <Button
          type="button"

          disabled={saving}
          onClick={() => (dirty && !blocked ? setConfirmRefresh(true) : refresh())}
        >
          {copy('refresh')}
        </Button>
      </div>
      <p>
        {scope === 'personal-model'
          ? t('requestAdaptation.personal.personalDescription')
          : copy('description')}
      </p>
      {modelScope ? <p className="nb-sub">{t('requestAdaptation.model.saveHelp')}</p> : null}
      {scope === 'endpoint' ? (
        <Fold title={t('common.operations.management.details')} plain>
          {technicalDetails}
        </Fold>
      ) : (
        technicalDetails
      )}
      {loading ? <p role="status">{copy('loading')}</p> : null}
      {loadError === 'savedRefreshFailed' ? (
        <OutcomeNote
          outcome={{ kind: 'savedRefreshFailed', recheck: refresh }}
          busy={loading || saving}
        />
      ) : (
        <>
          {notice ? <Note>{notice}</Note> : null}
          {loadError ? <Note tone="warn">{copy(loadError)}</Note> : null}
        </>
      )}
      {!editable && view ? <p>{copy('readOnly')}</p> : null}
      {view && draft && !loading && !contextChanged ? (
        <form className="core-form" onSubmit={(event) => void save(event)}>
          {modelScope ? (
            <>
              <fieldset className="core-form ops-unframed">
                <legend>{t('requestAdaptation.model.headers')}</legend>
                {(['forward_headers', 'fixed_headers'] as const).map(renderSection)}
              </fieldset>
              <fieldset className="core-form ops-unframed">
                <legend>{t('requestAdaptation.model.body')}</legend>
                {scope === 'personal-model' ? (
                  <Field
                    size="long"
                    label={t('requestAdaptation.personal.excluded')}
                    help={t('requestAdaptation.personal.excludedHelp')}
                  >
                    {(props) => (
                      <textarea
                        {...props}
                        rows={3}
                        value={draft.excluded}
                        disabled={!canEdit}
                        placeholder={'temperature\nmax_tokens'}
                        onChange={(event) => setDraft({ ...draft, excluded: event.target.value })}
                      />
                    )}
                  </Field>
                ) : null}
                {(['body_defaults', 'body_forced', 'native_extension_paths'] as const).map(
                  renderSection,
                )}
              </fieldset>
            </>
          ) : (
            (
              [
                'forward_headers',
                'fixed_headers',
                'body_defaults',
                'body_forced',
                'native_extension_paths',
              ] as const
            ).map(renderSection)
          )}
          {canEdit && <RuleTransfer key={url} draft={draft} setDraft={setDraft} copy={copy} />}
          {editable ? (
            <FormActions>
              {dirty && (
                <Button
                  type="button"
                  disabled={saving}
                  onClick={() => {
                    setDraft(newDraft(view));
                    setNotice('');
                  }}
                >
                  {t('common.localDraftDiscard')}
                </Button>
              )}
              <Button variant="primary" type="submit" disabled={saving || saveBlocked}>
                {modelScope ? t('requestAdaptation.model.save') : copy('save')}
              </Button>
            </FormActions>
          ) : null}
        </form>
      ) : null}
    </section>
  );
}
