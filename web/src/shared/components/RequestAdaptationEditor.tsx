import { useEffect, useRef, useState, type FormEvent } from 'react';
import { apiFetch, isApiError } from '@shared/query/http';
import {
  useRequestAdaptationCopy,
  type RequestAdaptationCopyKey,
} from './requestAdaptationCopy';

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
  values: Record<string, { has_value: boolean; mask?: string }>;
  source?: string;
}
interface Projection {
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
  existing: boolean;
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
    if (entry.has_value !== true || typeof entry.mask !== 'string')
      throw new Error('Invalid adaptation projection');
    safe[name] = { has_value: true, mask: entry.mask };
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

function newDraft(view: Projection): Draft {
  const map = (section: MapProjection): MapDraft => ({
    mode: section.mode,
    rows: Object.keys(section.values).map((name) => ({
      name,
      action: 'keep',
      value: '',
      existing: true,
    })),
  });
  return {
    forward_headers: {
      mode: view.forward_headers.mode,
      text: view.forward_headers.values.join('\n'),
    },
    fixed_headers: map(view.fixed_headers),
    body_defaults: map(view.body_defaults),
    body_forced: map(view.body_forced),
    native_extension_paths: {
      mode: view.native_extension_paths.mode,
      text: view.native_extension_paths.values.join('\n'),
    },
  };
}

function buildPayload(
  draft: Draft,
  revision: string,
  copy: Copy,
) {
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
      }
    }
    payload[kind] = { mode: section.mode, values };
  }
  return payload;
}

function newOperationKey(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(24));
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('');
}

function MapEditor({
  kind,
  section,
  editable,
  change,
}: {
  kind: MapKind;
  section: MapDraft;
  editable: boolean;
  change: (next: MapDraft) => void;
}) {
  const copy = useRequestAdaptationCopy();
  if (section.mode === 'inherit') return <p>{copy('inherited')}</p>;
  const changeRow = (index: number, next: Row) =>
    change({
      ...section,
      rows: section.rows.map((row, position) => (position === index ? next : row)),
    });
  return (
    <div className="core-form">
      <p>{copy(kind === 'fixed_headers' ? 'fixedHelp' : 'bodyHelp')}</p>
      {section.rows.length === 0 ? <p>{copy('empty')}</p> : null}
      {section.rows.map((row, index) => (
        <div className="core-field-grid" key={index}>
          <label>
            <span>{copy('name')}</span>
            <input
              value={row.name}
              maxLength={512}
              readOnly={row.existing || !editable}
              onChange={(event) => changeRow(index, { ...row, name: event.target.value })}
            />
          </label>
          <label>
            <span>{copy('action')}</span>
            <select
              value={row.action}
              disabled={!editable}
              onChange={(event) =>
                changeRow(index, { ...row, action: event.target.value as Action, value: '' })
              }
            >
              {row.existing ? <option value="keep">{copy('keep')}</option> : null}
              <option value="replace">{copy('change')}</option>
              <option value="clear">{copy('clear')}</option>
            </select>
          </label>
          {row.action === 'replace' ? (
            <label>
              <span>{copy('value')}</span>
              {kind === 'fixed_headers' ? (
                <input
                  type="password"
                  autoComplete="off"
                  value={row.value}
                  maxLength={4096}
                  disabled={!editable}
                  onChange={(event) => changeRow(index, { ...row, value: event.target.value })}
                />
              ) : (
                <textarea
                  value={row.value}
                  rows={2}
                  maxLength={65536}
                  disabled={!editable}
                  onChange={(event) => changeRow(index, { ...row, value: event.target.value })}
                />
              )}
            </label>
          ) : row.existing ? (
            <span>{copy('masked')}</span>
          ) : null}
        </div>
      ))}
      {editable ? (
        <button
          type="button"
          className="btn btn-secondary"
          onClick={() =>
            change({
              ...section,
              rows: [...section.rows, { name: '', action: 'replace', value: '', existing: false }],
            })
          }
        >
          {copy('add')}
        </button>
      ) : null}
    </div>
  );
}

export function RequestAdaptationEditor({
  url,
  scope,
  connectorType,
  editable,
}: {
  url: string;
  scope: 'endpoint' | 'charity-model' | 'binding';
  connectorType?: string;
  editable: boolean;
}) {
  const copy = useRequestAdaptationCopy();
  const [reload, setReload] = useState(0);
  const [view, setView] = useState<View | null>(null);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState('');
  const [loadError, setLoadError] = useState<'stale' | 'loadError' | null>(null);
  const [blocked, setBlocked] = useState(false);
  const [loadedContext, setLoadedContext] = useState<string | null>(null);
  const loadedContextRef = useRef<string | null>(null);
  const contextChanged =
    loadedContext !== null && loadedContext !== url;
  const saveBlocked = blocked || contextChanged;

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
          setLoadError(hasPriorProjection ? 'stale' : 'loadError');
          setBlocked(true);
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [url, reload]);

  const updateList = (kind: ListKind, next: ListDraft) =>
    setDraft((current) => (current ? { ...current, [kind]: next } : current));
  const updateMap = (kind: MapKind, next: MapDraft) =>
    setDraft((current) => (current ? { ...current, [kind]: next } : current));
  const save = async (event: FormEvent) => {
    event.preventDefault();
    if (!editable || !view || !draft || saving || saveBlocked) return;
    let payload: Record<string, unknown>;
    try {
      payload = buildPayload(draft, view.revision, copy);
    } catch (error) {
      setNotice(error instanceof Error ? error.message : copy('saveError'));
      return;
    }
    setSaving(true);
    setNotice('');
    let successfulResponseReceived = false;
    try {
      const response = await apiFetch<unknown>(url, {
        method: 'PUT',
        headers: { 'Idempotency-Key': newOperationKey() },
        json: payload,
      });
      successfulResponseReceived = true;
      const next = parseView(response);
      setView(next);
      setDraft(newDraft(next));
      setNotice(copy('saved'));
    } catch (error) {
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
      setSaving(false);
    }
  };

  return (
    <section className="core-card">
      <div className="core-card__header">
        <h3>{copy('title')}</h3>
        <button
          type="button"
          className="btn btn-secondary"
          disabled={saving}
          onClick={() => {
            setLoading(true);
            setLoadError(null);
            if (loadedContextRef.current !== null) {
              setBlocked(true);
              setNotice('');
            }
            setReload((value) => value + 1);
          }}
        >
          {copy('refresh')}
        </button>
      </div>
      <p>{copy('description')}</p>
      <p>
        {connectorType === 'openai-compatible'
          ? copy('openaiSupport')
          : connectorType
            ? copy('convertedSupport')
            : copy('allSupport')}
      </p>
      {loading ? <p role="status">{copy('loading')}</p> : null}
      {notice ? <p role="status">{notice}</p> : null}
      {loadError ? <p role="status">{copy(loadError)}</p> : null}
      {!editable && view ? <p>{copy('readOnly')}</p> : null}
      {view && draft && !loading && !contextChanged ? (
        <form className="core-form" onSubmit={(event) => void save(event)}>
          <p>
            {copy('revision')}: {view.revision}
          </p>
          {(
            [
              'forward_headers',
              'fixed_headers',
              'body_defaults',
              'body_forced',
              'native_extension_paths',
            ] as const
          ).map((kind) => {
            const section = draft[kind];
            const inherited = scope === 'binding';
            const setMode = (next: Mode) => {
              if (kind === 'forward_headers' || kind === 'native_extension_paths')
                updateList(kind, {
                  mode: next,
                  text: next === 'inherit' ? '' : (section as ListDraft).text,
                });
              else
                updateMap(kind, {
                  mode: next,
                  rows: next === 'inherit' ? [] : (section as MapDraft).rows,
                });
            };
            return (
              <fieldset key={kind} className="core-form">
                <legend>{copy(labels[kind])}</legend>
                {inherited ? (
                  <label>
                    <span>{copy('mode')}</span>
                    <select
                      value={section.mode}
                      disabled={!editable}
                      onChange={(event) => setMode(event.target.value as Mode)}
                    >
                      <option value="inherit">{copy('inherit')}</option>
                      <option value="replace">{copy('replace')}</option>
                    </select>
                  </label>
                ) : null}
                {kind === 'forward_headers' || kind === 'native_extension_paths' ? (
                  section.mode === 'inherit' ? (
                    <p>{copy('inherited')}</p>
                  ) : (
                    <label>
                      <span>{copy('listHelp')}</span>
                      <textarea
                        value={(section as ListDraft).text}
                        rows={3}
                        maxLength={65536}
                        disabled={!editable}
                        onChange={(event) =>
                          updateList(kind, { mode: section.mode, text: event.target.value })
                        }
                      />
                    </label>
                  )
                ) : (
                  <MapEditor
                    kind={kind}
                    section={section as MapDraft}
                    editable={editable}
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
          })}
          {editable ? (
            <button className="btn btn-primary" type="submit" disabled={saving || saveBlocked}>
              {copy('save')}
            </button>
          ) : null}
        </form>
      ) : null}
    </section>
  );
}
