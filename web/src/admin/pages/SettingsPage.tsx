import { useMemo, useState, type FormEvent } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import {
  Card,
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
  StatusBadge,
} from '@shared/components/States';
import { MaintenancePanel } from '@shared/operations/MaintenancePanel';
import {
  adminCoreKeys,
  getSiteConfigBundle,
  patchSiteSetting,
  type SiteConfigCatalogEntry,
} from '../features/operations/core';
import { LegalHoldPanel } from '../features/operations/LegalHoldPanel';
import { useRetainedOperation } from '../features/operations/useRetainedOperation';
import { humanReadableSeconds } from '../utils/catalogDisplay';
import '@shared/operations/operations.css';

const DANGEROUS_GROUPS = new Set(['access', 'abuse', 'legal']);
const LEGAL_KEYS = new Set([
  'legal_privacy_override_zh',
  'legal_privacy_override_en',
  'legal_terms_override_zh',
  'legal_terms_override_en',
]);
const LEGAL_MAX_BYTES = 65_536;
const MAX_AMOUNT_MILLI = 9_000_000_000_000_000n;

type CatalogValue = string | number | boolean | null;
type ParseResult = { value: CatalogValue; error: null } | { value: undefined; error: string };
type LineEndings = 'crlf' | 'lf' | 'cr' | 'mixed' | 'none';

const utf8Bytes = (value: string) => new TextEncoder().encode(value).byteLength;

function lineEndings(value: string): LineEndings {
  if (!value.includes('\r') && !value.includes('\n')) return 'none';
  const remainder = value.replaceAll('\r\n', '');
  const kinds = [value.includes('\r\n'), remainder.includes('\r'), remainder.includes('\n')].filter(
    Boolean,
  ).length;
  if (kinds > 1) return 'mixed';
  return value.includes('\r\n') ? 'crlf' : remainder.includes('\r') ? 'cr' : 'lf';
}

function restoreLineEndings(value: string, style: LineEndings): string {
  if (style === 'crlf') return value.replace(/\r\n|\r|\n/g, '\r\n');
  if (style === 'cr') return value.replace(/\r\n|\r|\n/g, '\r');
  return value;
}

function amountMilli(value: string): bigint | null {
  const match = /^(0|[1-9][0-9]*)(?:\.([0-9]{1,3}))?$/.exec(value);
  if (!match || match[2]?.endsWith('0')) return null;
  try {
    const result = BigInt(match[1]) * 1_000n + BigInt((match[2] ?? '').padEnd(3, '0') || '0');
    return result <= MAX_AMOUNT_MILLI ? result : null;
  } catch {
    return null;
  }
}

const catalogInteger = (value: unknown) =>
  typeof value === 'number' && Number.isSafeInteger(value) ? value : null;

function hasForbiddenControl(value: string, multiline: boolean): boolean {
  for (const character of value) {
    const code = character.codePointAt(0)!;
    if (code === 127 || (code >= 128 && code <= 159)) return true;
    if (code < 32 && !(multiline && (code === 9 || code === 10 || code === 13))) return true;
  }
  return false;
}

function parseSetting(
  entry: SiteConfigCatalogEntry,
  draft: string,
  explicitNull: boolean,
): ParseResult {
  const invalid = (detail: string): ParseResult => ({
    value: undefined,
    error: `Enter a canonical ${entry.type} value ${detail}.`,
  });
  if (explicitNull)
    return entry.null_writable ? { value: null, error: null } : invalid('that is not null');
  if (entry.type === 'boolean') {
    return draft === 'true'
      ? { value: true, error: null }
      : draft === 'false'
        ? { value: false, error: null }
        : invalid('using true or false');
  }
  if (entry.type === 'integer') {
    if (!/^-?(0|[1-9][0-9]*)$/.test(draft))
      return invalid('without whitespace, fractions, exponents, or leading zeroes');
    const value = Number(draft);
    const min = catalogInteger(entry.minimum);
    const max = catalogInteger(entry.maximum);
    const step = catalogInteger(entry.step);
    if (
      !Number.isSafeInteger(value) ||
      min === null ||
      max === null ||
      step === null ||
      step <= 0 ||
      value < min ||
      value > max ||
      (value - min) % step !== 0
    ) {
      return invalid(
        `within ${String(entry.minimum)}–${String(entry.maximum)} on step ${String(entry.step)}`,
      );
    }
    return { value, error: null };
  }
  if (entry.type === 'amount') {
    const value = amountMilli(draft);
    const min = typeof entry.minimum === 'string' ? amountMilli(entry.minimum) : null;
    const max = typeof entry.maximum === 'string' ? amountMilli(entry.maximum) : null;
    const step = typeof entry.step === 'string' ? amountMilli(entry.step) : null;
    if (
      value === null ||
      min === null ||
      max === null ||
      step === null ||
      step <= 0n ||
      value < min ||
      value > max ||
      (value - min) % step !== 0n
    ) {
      return invalid(
        `within ${String(entry.minimum)}–${String(entry.maximum)} credits on step ${String(entry.step)}`,
      );
    }
    return { value: draft, error: null };
  }
  if (entry.type === 'enum')
    return entry.allowed_values.includes(draft)
      ? { value: draft, error: null }
      : invalid('from the allowed choices');
  if (hasForbiddenControl(draft, entry.type === 'text'))
    return invalid('without control characters');
  if (LEGAL_KEYS.has(entry.key)) {
    return utf8Bytes(draft) <= LEGAL_MAX_BYTES
      ? { value: draft, error: null }
      : invalid(`no larger than ${LEGAL_MAX_BYTES} UTF-8 bytes`);
  }
  const min = catalogInteger(entry.minimum);
  const max = catalogInteger(entry.maximum);
  const length = [...draft].length;
  return min !== null && max !== null && length >= min && length <= max
    ? { value: draft, error: null }
    : invalid(`with ${String(entry.minimum)}–${String(entry.maximum)} Unicode characters`);
}

const scalar = (value: unknown) => (value === null ? 'null' : value === '' ? '""' : String(value));
const attribute = (value: unknown) =>
  typeof value === 'string' || typeof value === 'number' ? String(value) : undefined;
const creditPreview = (value: string) => {
  const [whole, fraction] = value.split('.');
  return `${whole.replace(/\B(?=(\d{3})+(?!\d))/g, ',')}${fraction ? `.${fraction}` : ''}`;
};
const timezonePreview = (minutes: number) => {
  const absolute = Math.abs(minutes);
  return `UTC${minutes < 0 ? '-' : '+'}${String(Math.floor(absolute / 60)).padStart(2, '0')}:${String(absolute % 60).padStart(2, '0')}`;
};
function SettingField({
  entry,
  value,
  revision,
  language,
  reload,
}: {
  entry: SiteConfigCatalogEntry;
  value: CatalogValue;
  revision: string;
  language: string;
  reload: () => Promise<unknown>;
}) {
  const initial = value === null ? '' : String(value);
  const [draft, setDraft] = useState(initial);
  const [explicitNull, setExplicitNull] = useState(value === null && entry.null_writable);
  const [endingStyle] = useState(() => lineEndings(initial));
  const [dirty, setDirty] = useState(false);
  const [reloading, setReloading] = useState(false);
  const writable = entry.write_endpoint.startsWith('/admin/api/site-config/');
  const save = useRetainedOperation<
    { value: CatalogValue },
    { key: string; value: unknown; revision: string }
  >((input, key) => patchSiteSetting(entry, input.value, key), reload);
  const submissionDraft = LEGAL_KEYS.has(entry.key)
    ? restoreLineEndings(draft, endingStyle)
    : draft;
  const parsed = parseSetting(entry, submissionDraft, explicitNull);
  const busy = save.isPending || reloading;
  const locale = language.startsWith('zh') ? 'zh' : 'en';
  const inputID = `site-setting-${entry.key}`;
  const changeDraft = (next: string) => {
    save.reset();
    setDirty(true);
    setDraft(next);
  };
  const changeNull = (next: boolean) => {
    save.reset();
    setDirty(true);
    setExplicitNull(next);
  };
  const restoreAuthorityValue = () => {
    save.reset();
    setDirty(false);
    setDraft(initial);
    setExplicitNull(value === null && entry.null_writable);
  };
  const reloadAuthority = async () => {
    save.reset();
    setReloading(true);
    try {
      await reload();
    } finally {
      setReloading(false);
    }
  };

  let preview: string | null = null;
  if (
    parsed.error === null &&
    typeof parsed.value === 'number' &&
    entry.key === 'site_timezone_offset_minutes'
  ) {
    preview = `Preview: ${timezonePreview(parsed.value)}`;
  } else if (
    parsed.error === null &&
    typeof parsed.value === 'number' &&
    entry.unit?.en === 'seconds'
  ) {
    preview = `Duration preview: ${humanReadableSeconds(parsed.value, language)}`;
  } else if (parsed.error === null && typeof parsed.value === 'string' && entry.type === 'amount') {
    preview = `Amount preview: ${creditPreview(parsed.value)} credits`;
  }

  const control =
    entry.type === 'boolean' ? (
      <select
        id={inputID}
        value={draft}
        disabled={busy || explicitNull || !writable}
        onChange={(event) => changeDraft(event.target.value)}
      >
        <option value="true">Enabled</option>
        <option value="false">Disabled</option>
      </select>
    ) : entry.type === 'enum' ? (
      <select
        id={inputID}
        value={draft}
        disabled={busy || explicitNull || !writable}
        onChange={(event) => changeDraft(event.target.value)}
      >
        {entry.allowed_values.map((allowed) => (
          <option key={allowed || 'empty'} value={allowed}>
            {allowed || '""'}
          </option>
        ))}
      </select>
    ) : entry.type === 'text' ? (
      <textarea
        id={inputID}
        rows={LEGAL_KEYS.has(entry.key) ? 12 : 5}
        spellCheck={false}
        value={draft}
        disabled={busy || explicitNull || !writable}
        onChange={(event) => changeDraft(event.target.value)}
      />
    ) : (
      <input
        id={inputID}
        type={entry.type === 'integer' || entry.type === 'amount' ? 'number' : 'text'}
        inputMode={
          entry.type === 'integer' ? 'numeric' : entry.type === 'amount' ? 'decimal' : undefined
        }
        min={attribute(entry.minimum)}
        max={attribute(entry.maximum)}
        step={attribute(entry.step)}
        value={draft}
        disabled={busy || explicitNull || !writable}
        onChange={(event) => changeDraft(event.target.value)}
      />
    );

  return (
    <form
      className="ops-setting"
      noValidate
      onSubmit={(event: FormEvent) => {
        event.preventDefault();
        if (parsed.error === null) save.mutate({ value: parsed.value });
      }}
    >
      <div>
        <h3>{entry.title[locale]}</h3>
        <p>{entry.description[locale]}</p>
        <small>
          {entry.key} · {entry.type}
          {entry.unit ? ` · ${entry.unit[locale]}` : ''}
        </small>
        <p className="muted">
          Default {scalar(entry.raw_default)} · effective fallback{' '}
          {scalar(entry.effective_fallback)}
        </p>
        {entry.minimum !== null || entry.maximum !== null ? (
          <p className="muted">
            Range {scalar(entry.minimum)}–{scalar(entry.maximum)}
            {entry.step !== null ? ` · step ${scalar(entry.step)}` : ''}
          </p>
        ) : null}
      </div>
      <div>
        <label htmlFor={inputID}>
          <span>{entry.title[locale]}</span>
        </label>
        {control}
        {entry.null_writable ? (
          <label>
            <input
              type="checkbox"
              checked={explicitNull}
              disabled={busy || !writable}
              onChange={(event) => changeNull(event.target.checked)}
            />
            Remove the explicit override and use the effective fallback
          </label>
        ) : null}
        {value === null && !entry.null_writable ? (
          <p className="muted">Not configured yet. Enter the first authoritative value.</p>
        ) : null}
        {LEGAL_KEYS.has(entry.key) ? (
          <p className="muted">
            {utf8Bytes(submissionDraft)} / {LEGAL_MAX_BYTES} UTF-8 bytes
          </p>
        ) : null}
        {preview ? <p className="muted">{preview}</p> : null}
        {draft === '0' ? <p className="muted">{entry.zero_semantics[locale]}</p> : null}
        {explicitNull ? <p className="muted">{entry.null_semantics[locale]}</p> : null}
        {!explicitNull && draft === '' ? (
          <p className="muted">{entry.empty_semantics[locale]}</p>
        ) : null}
        {entry.key === 'site_logo_url' ? (
          <p className="inline-notice">
            A remote logo makes a public third-party browser request and may fail independently.
            Never place a secret in this URL.
          </p>
        ) : null}
        {entry.independent_gates.length ? (
          <p className="muted">Independent gates: {entry.independent_gates.join(', ')}</p>
        ) : null}
        {!writable ? (
          <p className="muted">
            Read-only here; this value is owned by its dedicated authority page.
          </p>
        ) : null}
        {dirty && parsed.error ? (
          <p className="field-error" role="alert">
            {parsed.error}
          </p>
        ) : null}
        {save.error ? <ErrorState error={save.error} /> : null}
        <div className="ops-actions">
          <button
            className="btn btn-primary"
            type="submit"
            disabled={!writable || parsed.error !== null || busy}
          >
            {save.isPending ? 'Saving…' : explicitNull ? 'Remove override' : 'Save value'}
          </button>
          <button
            className="btn btn-secondary"
            type="button"
            disabled={busy}
            onClick={restoreAuthorityValue}
          >
            Restore authority value
          </button>
          <button
            className="btn btn-secondary"
            type="button"
            disabled={busy}
            onClick={() => void reloadAuthority()}
          >
            {reloading ? 'Reloading…' : 'Reload authority'}
          </button>
        </div>
        <small>
          Authority revision {revision}. A conflict or unknown response reloads the complete
          authority snapshot without resubmitting.
        </small>
      </div>
    </form>
  );
}
function Group({
  name,
  entries,
  values,
  revision,
  language,
  reload,
}: {
  name: string;
  entries: SiteConfigCatalogEntry[];
  values: Record<string, CatalogValue>;
  revision: string;
  language: string;
  reload: () => Promise<unknown>;
}) {
  const [open, setOpen] = useState(false);
  return (
    <Card>
      <button
        className="btn btn-link ops-disclosure"
        type="button"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
      >
        <span>{name}</span>
        <span>{entries.length} settings</span>
      </button>
      {open ? (
        <div className="ops-stack">
          {entries.map((entry) => (
            <SettingField
              key={`${entry.key}:${revision}`}
              entry={entry}
              value={values[entry.key]}
              revision={revision}
              language={language}
              reload={reload}
            />
          ))}
        </div>
      ) : null}
    </Card>
  );
}

export function SettingsPage() {
  const authority = useQuery({
    queryKey: adminCoreKeys.settings,
    queryFn: getSiteConfigBundle,
    retry: false,
  });
  const { i18n } = useTranslation();
  const [search, setSearch] = useState('');
  const groups = useMemo(() => {
    if (!authority.data) return new Map<string, SiteConfigCatalogEntry[]>();
    const needle = search.trim().toLocaleLowerCase();
    const result = new Map<string, SiteConfigCatalogEntry[]>();
    for (const entry of authority.data.catalog) {
      if (entry.key === 'default_locale') continue;
      const searchable = `${entry.key} ${entry.title.en} ${entry.title.zh} ${entry.description.en} ${entry.description.zh}`;
      if (needle && !searchable.toLocaleLowerCase().includes(needle)) continue;
      const list = result.get(entry.group) ?? [];
      list.push(entry);
      result.set(entry.group, list);
    }
    return result;
  }, [authority.data, search]);
  const ordinary = [...groups].filter(([name]) => !DANGEROUS_GROUPS.has(name));
  const dangerous = [...groups].filter(([name]) => DANGEROUS_GROUPS.has(name));
  const initialFailure = !authority.data && authority.error;

  return (
    <div className="page ops-page">
      <PageHeader
        title="Site settings"
        description="Search the server catalog, edit ordinary fields by scope, and keep access/legal controls physically isolated."
      />
      {authority.isPending ? (
        <LoadingState />
      ) : initialFailure ? (
        <ErrorState error={initialFailure} onRetry={() => void authority.refetch()} />
      ) : authority.data ? (
        <>
          {authority.error ? (
            <ErrorState error={authority.error} onRetry={() => void authority.refetch()} />
          ) : null}
          <Card>
            <div className="ops-toolbar">
              <label>
                <span>Search catalog</span>
                <input value={search} onChange={(event) => setSearch(event.target.value)} />
              </label>
              <StatusBadge active label={`revision ${authority.data.revision}`} />
            </div>
          </Card>
          <div className="ops-settings-columns">
            <section>
              <h2>Ordinary settings</h2>
              {ordinary.length ? (
                ordinary.map(([name, entries]) => (
                  <Group
                    key={name}
                    name={name}
                    entries={entries}
                    values={authority.data.values}
                    revision={authority.data.revision}
                    language={i18n.language}
                    reload={() => authority.refetch()}
                  />
                ))
              ) : (
                <EmptyState
                  title="No ordinary settings"
                  body="No ordinary catalog entry matches the search."
                />
              )}
            </section>
            <section className="ops-danger">
              <h2>Dangerous and legal settings</h2>
              <p>
                Access, abuse, and legal changes can affect admission or published policy. Review
                each server description before saving.
              </p>
              {dangerous.length ? (
                dangerous.map(([name, entries]) => (
                  <Group
                    key={name}
                    name={name}
                    entries={entries}
                    values={authority.data.values}
                    revision={authority.data.revision}
                    language={i18n.language}
                    reload={() => authority.refetch()}
                  />
                ))
              ) : (
                <EmptyState
                  title="No dangerous settings"
                  body="No dangerous catalog entry matches the search."
                />
              )}
            </section>
          </div>
        </>
      ) : null}
      <MaintenancePanel role="admin" />
      <LegalHoldPanel />
    </div>
  );
}
