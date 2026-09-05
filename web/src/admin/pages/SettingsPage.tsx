import { useMemo, useState } from 'react';
import { Link } from 'react-router';
import {
  captureStationSession,
  stationSessionMatches,
  stationSessionWrite,
  StationSessionChangedError,
} from '@shared/charityManagement';
import { ApiError } from '@shared/query/http';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import type { TFunction } from 'i18next';
import { useTranslation } from 'react-i18next';
import { Card, EmptyState, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { MaintenancePanel } from '@shared/operations/MaintenancePanel';
import {
  adminCoreKeys,
  getSiteConfigBundle,
  patchSiteSettings,
  type SiteConfigBundle,
  type SiteConfigCatalogEntry,
} from '../features/operations/core';
import { LegalHoldPanel } from '../features/operations/LegalHoldPanel';
import { useRetainedOperation } from '../features/operations/useRetainedOperation';
import { humanReadableSeconds } from '../utils/catalogDisplay';
import '@shared/operations/operations.css';

const DANGEROUS_GROUPS = new Set(['access', 'abuse', 'legal']);
const MULTILINE_KEYS = new Set([
  'legal_privacy_override_zh',
  'legal_privacy_override_en',
  'legal_terms_override_zh',
  'legal_terms_override_en',
  'charity_donation_notice_zh',
  'charity_donation_notice_en',
]);
const MAX_AMOUNT_MILLI = 9_000_000_000_000_000n;

type CatalogValue = string | number | boolean | null;
type ParseResult = { value: CatalogValue; error: null } | { value: undefined; error: string };
type LineEndings = 'crlf' | 'lf' | 'cr' | 'mixed' | 'none';

const CATALOG_LOCALES = { en: 'en', zh: 'zh' } as const;
const SETTING_TYPE_LABEL_KEYS: Record<SiteConfigCatalogEntry['type'], string> = {
  boolean: 'admin.settings.booleanValue',
  integer: 'admin.settings.numberValue',
  amount: 'admin.settings.numberValue',
  string: 'admin.settings.textValue',
  text: 'admin.settings.textValue',
  enum: 'admin.settings.enumValue',
};
const GROUP_LABEL_KEYS: Record<string, string> = {
  identity: 'admin.settings.groups.identity',
  legal: 'admin.settings.groups.legal',
  limits: 'admin.settings.groups.limits',
  access: 'admin.settings.groups.access',
  economy: 'admin.settings.groups.economy',
  charity: 'admin.settings.groups.charity',
  abuse: 'admin.settings.groups.abuse',
  connector: 'admin.settings.groups.connector',
  games: 'admin.settings.groups.games',
  announcements: 'admin.settings.groups.announcements',
  activities: 'admin.settings.groups.activities',
  reports: 'admin.settings.groups.reports',
  alerts: 'admin.settings.groups.alerts',
};
const ENUM_VALUE_LABEL_KEYS: Record<string, string> = {
  '': 'admin.settings.enumValues.empty',
  zh: 'admin.settings.enumValues.zh',
  en: 'admin.settings.enumValues.en',
  enabled: 'admin.settings.enumValues.enabled',
  level_gated: 'admin.settings.enumValues.levelGated',
  disabled: 'admin.settings.enumValues.disabled',
};

const catalogLocale = (language: string) => {
  const base = language.split('-')[0] as keyof typeof CATALOG_LOCALES;
  return CATALOG_LOCALES[base] ?? 'en';
};
const settingTypeLabel = (t: TFunction, type: SiteConfigCatalogEntry['type']) =>
  t(SETTING_TYPE_LABEL_KEYS[type]);
const groupLabel = (t: TFunction, group: string) =>
  t(GROUP_LABEL_KEYS[group] ?? 'admin.settings.groups.unknown', { group });
const enumValueLabel = (t: TFunction, value: string) =>
  t(ENUM_VALUE_LABEL_KEYS[value] ?? 'admin.settings.enumValues.unknown', { value });

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
  if (!match) return null;
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
  t: TFunction,
): ParseResult {
  const invalid = (detail: string): ParseResult => ({
    value: undefined,
    error: t('admin.settings.validation.canonical', {
      type: settingTypeLabel(t, entry.type),
      detail,
    }),
  });
  if (explicitNull)
    return entry.null_writable
      ? { value: null, error: null }
      : invalid(t('admin.settings.validation.notNull'));
  if (entry.type === 'boolean') {
    return draft === 'true'
      ? { value: true, error: null }
      : draft === 'false'
        ? { value: false, error: null }
        : invalid(t('admin.settings.validation.boolean'));
  }
  if (entry.type === 'integer') {
    if (!/^-?(0|[1-9][0-9]*)$/.test(draft))
      return invalid(t('admin.settings.validation.integerSyntax'));
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
        t('admin.settings.validation.rangeStep', {
          minimum: String(entry.minimum),
          maximum: String(entry.maximum),
          step: String(entry.step),
        }),
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
        t('admin.settings.validation.amountRangeStep', {
          minimum: String(entry.minimum),
          maximum: String(entry.maximum),
          step: String(entry.step),
        }),
      );
    }
    return {
      value: `${value / 1000n}${value % 1000n ? '.' + (value % 1000n).toString().padStart(3, '0').replace(/0+$/, '') : ''}`,
      error: null,
    };
  }
  if (entry.type === 'enum')
    return entry.allowed_values.includes(draft)
      ? { value: draft, error: null }
      : invalid(t('admin.settings.validation.allowedChoice'));
  if (hasForbiddenControl(draft, entry.type === 'text'))
    return invalid(t('admin.settings.validation.noControlCharacters'));
  if (entry.type === 'text') {
    const maximum = catalogInteger(entry.maximum);
    return maximum !== null && utf8Bytes(draft) <= maximum
      ? { value: draft, error: null }
      : invalid(t('admin.settings.validation.maxUtf8Bytes', { maximum: String(entry.maximum) }));
  }
  const min = catalogInteger(entry.minimum);
  const max = catalogInteger(entry.maximum);
  const length = [...draft].length;
  return min !== null && max !== null && length >= min && length <= max
    ? { value: draft, error: null }
    : invalid(
        t('admin.settings.validation.unicodeLength', {
          minimum: String(entry.minimum),
          maximum: String(entry.maximum),
        }),
      );
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
type SettingDraft = { text: string; isNull: boolean; original: CatalogValue };
type EditorProps = {
  values: Record<string, CatalogValue>;
  drafts: Record<string, SettingDraft>;
  language: string;
  catalogLabels: Readonly<Record<string, string>>;
  busy: boolean;
  onEdit: (key: string, draft: SettingDraft) => void;
  onReset: (key: string) => void;
};

function parsedDraft(entry: SiteConfigCatalogEntry, draft: SettingDraft, t: TFunction) {
  const text =
    entry.type === 'text'
      ? restoreLineEndings(
          draft.text,
          lineEndings(typeof draft.original === 'string' ? draft.original : ''),
        )
      : entry.type === 'amount' || entry.type === 'integer'
        ? draft.text.trim()
        : draft.text;
  return parseSetting(entry, text, draft.isNull, t);
}

function SettingField({
  entry,
  values,
  drafts,
  language,
  catalogLabels,
  busy,
  onEdit,
  onReset,
}: EditorProps & { entry: SiteConfigCatalogEntry }) {
  const { t } = useTranslation();
  const value = values[entry.key];
  const draft = drafts[entry.key] ?? {
    text: value === null ? '' : String(value),
    isNull: value === null && entry.null_writable,
    original: value,
  };
  const dirty = Boolean(drafts[entry.key]);
  const parsed = parsedDraft(entry, draft, t);
  const locale = catalogLocale(language);
  const inputID = 'site-setting-' + entry.key;
  const change = (text: string) => onEdit(entry.key, { ...draft, text });
  let preview: string | null = null;
  if (
    parsed.error === null &&
    typeof parsed.value === 'number' &&
    entry.key === 'site_timezone_offset_minutes'
  ) {
    preview = t('admin.settings.timezonePreview', { value: timezonePreview(parsed.value) });
  } else if (
    parsed.error === null &&
    typeof parsed.value === 'number' &&
    entry.unit?.en === 'seconds'
  ) {
    preview = t('admin.settings.catalogDurationPreview', {
      value: humanReadableSeconds(parsed.value, language),
    });
  } else if (parsed.error === null && typeof parsed.value === 'string' && entry.type === 'amount') {
    preview = t('admin.settings.catalogMilliPreview', { credits: creditPreview(parsed.value) });
  }
  const control =
    entry.type === 'boolean' || entry.type === 'enum' ? (
      <select
        id={inputID}
        value={draft.text}
        disabled={busy || draft.isNull}
        onChange={(event) => change(event.target.value)}
      >
        {entry.type === 'boolean' ? (
          <>
            <option value="true">{t('common.enabled')}</option>
            <option value="false">{t('common.disabled')}</option>
          </>
        ) : (
          entry.allowed_values.map((allowed) => (
            <option key={allowed || 'empty'} value={allowed}>
              {enumValueLabel(t, allowed)}
            </option>
          ))
        )}
      </select>
    ) : entry.type === 'text' ? (
      <textarea
        id={inputID}
        rows={MULTILINE_KEYS.has(entry.key) ? 8 : 4}
        spellCheck={false}
        value={draft.text}
        disabled={busy || draft.isNull}
        onChange={(event) => change(event.target.value)}
      />
    ) : (
      <input
        id={inputID}
        type={entry.type === 'integer' ? 'number' : 'text'}
        inputMode={
          entry.type === 'integer' ? 'numeric' : entry.type === 'amount' ? 'decimal' : undefined
        }
        min={attribute(entry.minimum)}
        max={attribute(entry.maximum)}
        step={attribute(entry.step)}
        value={draft.text}
        disabled={busy || draft.isNull}
        onChange={(event) => change(event.target.value)}
      />
    );
  return (
    <div className="ops-setting">
      <div>
        <h3>
          <label htmlFor={inputID}>{entry.title[locale]}</label>
        </h3>
        <p>{entry.description[locale]}</p>
        {entry.independent_gates.length ? (
          <p className="muted">
            {t('admin.settings.catalogIndependentGate')}:{' '}
            {entry.independent_gates
              .map(
                (gate) =>
                  catalogLabels[gate] ?? t('admin.settings.unknownCatalogGate', { key: gate }),
              )
              .join(', ')}
          </p>
        ) : null}
        <details>
          <summary>{t('admin.settings.fieldDetails')}</summary>
          <small>
            {entry.key} · {settingTypeLabel(t, entry.type)}
            {entry.unit ? ' · ' + entry.unit[locale] : ''}
          </small>
          <p className="muted">
            {t('admin.settings.catalogDefault')} {scalar(entry.raw_default)} ·{' '}
            {t('admin.settings.catalogEffective')} {scalar(entry.effective_fallback)}
          </p>
          {entry.minimum !== null || entry.maximum !== null ? (
            <p className="muted">
              {t('admin.settings.catalogRange')} {scalar(entry.minimum)}–{scalar(entry.maximum)}
              {entry.step !== null
                ? ' · ' + t('admin.settings.catalogStep') + ' ' + scalar(entry.step)
                : ''}
            </p>
          ) : null}
        </details>
      </div>
      <div className="ops-setting-control">
        {control}
        {entry.null_writable ? (
          <label className="checkbox-label">
            <input
              type="checkbox"
              checked={draft.isNull}
              disabled={busy}
              onChange={(event) => onEdit(entry.key, { ...draft, isNull: event.target.checked })}
            />
            <span>{t('admin.settings.restoreFallback')}</span>
          </label>
        ) : null}
        {value === null && !entry.null_writable ? (
          <p className="muted">{t('admin.settings.notConfigured')}</p>
        ) : null}
        {entry.type === 'text' ? (
          <p className="muted">
            {t('admin.settings.legalBytes', { count: utf8Bytes(draft.text), max: entry.maximum })}
          </p>
        ) : null}
        {preview ? <p className="muted">{preview}</p> : null}
        {draft.text === '0' ? <p className="muted">{entry.zero_semantics[locale]}</p> : null}
        {draft.isNull ? <p className="muted">{entry.null_semantics[locale]}</p> : null}
        {!draft.isNull && draft.text === '' ? (
          <p className="muted">{entry.empty_semantics[locale]}</p>
        ) : null}
        {entry.key === 'site_logo_url' ? (
          <p className="inline-notice">{t('admin.settings.remoteLogoWarning')}</p>
        ) : null}
        {dirty && parsed.error ? (
          <p className="field-error" role="alert">
            {parsed.error}
          </p>
        ) : null}
        {dirty ? (
          <button
            type="button"
            className="btn btn-secondary"
            disabled={busy}
            onClick={() => onReset(entry.key)}
          >
            {t('admin.settings.restoreAuthorityValue')}
          </button>
        ) : null}
      </div>
    </div>
  );
}

function Group({
  name,
  entries,
  ...editor
}: EditorProps & { name: string; entries: SiteConfigCatalogEntry[] }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  return (
    <Card>
      <button
        className="btn btn-link ops-disclosure"
        type="button"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
      >
        <span>{groupLabel(t, name)}</span>
        <span>{t('admin.settings.groupCount', { count: entries.length })}</span>
      </button>
      {open ? (
        <div className="ops-stack">
          {entries.map((entry) => (
            <SettingField key={entry.key} entry={entry} {...editor} />
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
  const { t, i18n } = useTranslation();
  const client = useQueryClient();
  const [drafts, setDrafts] = useState<Record<string, SettingDraft>>({});
  const resetField = (key: string) =>
    setDrafts((current) =>
      Object.fromEntries(Object.entries(current).filter(([name]) => name !== key)),
    );
  const editField = (key: string, draft: SettingDraft) => {
    save.reset();
    setDrafts((current) => ({ ...current, [key]: draft }));
  };
  const save = useRetainedOperation(
    async (input: { expected_revision: string; values: Record<string, CatalogValue> }, key) => {
      const station = captureStationSession(client, 'admin');
      const result = await stationSessionWrite(client, 'admin', () =>
        patchSiteSettings(input, key),
      );
      await client.cancelQueries({ queryKey: adminCoreKeys.settings });
      if (!stationSessionMatches(client, 'admin', station)) throw new StationSessionChangedError();
      client.setQueryData<SiteConfigBundle>(adminCoreKeys.settings, (current) =>
        current
          ? {
              ...current,
              revision: result.revision,
              values: { ...current.values, ...input.values },
            }
          : current,
      );
      setDrafts({});
      return result;
    },
    () => authority.refetch(),
  );
  const pending: Record<string, CatalogValue> = {};
  const changedElsewhere: string[] = [];
  let invalidDraft = false;
  let pendingCount = 0;
  for (const entry of authority.data?.catalog ?? []) {
    const draft = drafts[entry.key];
    if (!draft) continue;
    const parsed = parsedDraft(entry, draft, t);
    if (parsed.error !== null) {
      invalidDraft = true;
      pendingCount += 1;
      continue;
    }
    const current = authority.data?.values[entry.key];
    if (parsed.value !== current) {
      pendingCount += 1;
      pending[entry.key] = parsed.value;
      if (draft.original !== current)
        changedElsewhere.push(entry.title[catalogLocale(i18n.language)]);
    }
  }
  const proposed = { ...authority.data?.values, ...pending };
  const dependencyError =
    proposed.donation_accept_enabled && !proposed.charity_enabled
      ? t('admin.settings.dependencies.donations')
      : typeof proposed.checkin_mode === 'string' &&
          proposed.checkin_mode !== 'disabled' &&
          proposed.site_timezone_offset_minutes === null
        ? t('admin.settings.dependencies.timezone')
        : amountMilli(String(proposed.checkin_award_min_milli ?? '0'))! >
            amountMilli(String(proposed.checkin_award_max_milli ?? '0'))!
          ? t('admin.settings.dependencies.checkinBounds')
          : null;
  const errorMessages: Record<string, string> = {
    'Enable shared models before accepting donations.': t('admin.settings.dependencies.donations'),
    'The minimum check-in reward must not exceed the maximum.': t(
      'admin.settings.dependencies.checkinBounds',
    ),
    'Enabled level thresholds must increase from level 2 to level 4.': t(
      'admin.settings.dependencies.levels',
    ),
    'Set the site timezone before enabling check-in.': t('admin.settings.dependencies.timezone'),
    'The site timezone is locked because daily activity records already exist.': t(
      'admin.settings.dependencies.timezoneLocked',
    ),
  };
  const [search, setSearch] = useState('');
  const saveErrorMessage = save.error instanceof ApiError
    ? save.error.message.replace(/^\[NonbiriAPI\]\s*/, '')
    : '';
  const locale = catalogLocale(i18n.resolvedLanguage ?? i18n.language);
  const groups = useMemo(() => {
    if (!authority.data) return new Map<string, SiteConfigCatalogEntry[]>();
    const needle = search.trim().toLocaleLowerCase();
    const result = new Map<string, SiteConfigCatalogEntry[]>();
    for (const entry of authority.data.catalog) {
      if (
        entry.key === 'default_locale' ||
        !entry.write_endpoint.startsWith('/admin/api/site-config/')
      )
        continue;
      const searchable = `${entry.key} ${entry.title.en} ${entry.title.zh} ${entry.description.en} ${entry.description.zh}`;
      if (needle && !searchable.toLocaleLowerCase().includes(needle)) continue;
      const list = result.get(entry.group) ?? [];
      list.push(entry);
      result.set(entry.group, list);
    }
    return result;
  }, [authority.data, search]);
  const catalogLabels = useMemo(
    () =>
      ({
        charity_model_pricing: t('admin.settings.charityModelPricing'),
        ...Object.fromEntries(
          (authority.data?.catalog ?? []).map((entry) => [entry.key, entry.title[locale]]),
        ),
      }),
    [authority.data, locale, t],
  );
  const ordinary = [...groups].filter(([name]) => !DANGEROUS_GROUPS.has(name));
  const dangerous = [...groups].filter(([name]) => DANGEROUS_GROUPS.has(name));
  const initialFailure = !authority.data && authority.error;

  return (
    <div className="page ops-page">
      <PageHeader title={t('admin.settings.title')} description={t('admin.settings.description')} />
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
                <span>{t('common.search')}</span>
                <input value={search} onChange={(event) => setSearch(event.target.value)} />
              </label>
              <div className="ops-actions">
                <Link to="/activities">{t('admin.activities.nav')}</Link>
                <Link to="/games">{t('admin.games.nav')}</Link>
              </div>
            </div>
          </Card>
          <Card className="ops-save-bar">
            <span>
              {t('admin.settings.pendingChanges', { count: pendingCount })}
            </span>
            <div className="ops-actions">
              <button
                type="button"
                className="btn btn-primary"
                disabled={
                  save.isPending ||
                  invalidDraft ||
                  Boolean(dependencyError) ||
                  changedElsewhere.length > 0 ||
                  !Object.keys(pending).length ||
                  Boolean(authority.error)
                }
                onClick={() =>
                  authority.data &&
                  save.mutate({ expected_revision: authority.data.revision, values: pending })
                }
              >
                {save.isPending ? t('common.working') : t('admin.settings.saveAll')}
              </button>
              <button
                type="button"
                className="btn btn-secondary"
                disabled={save.isPending || !Object.keys(drafts).length}
                onClick={() => {
                  setDrafts({});
                  save.reset();
                }}
              >
                {t('admin.settings.discardAll')}
              </button>
            </div>
            {dependencyError ? (
              <p role="alert" className="field-error">
                {dependencyError}
              </p>
            ) : null}
            {changedElsewhere.length ? (
              <p role="alert" className="field-error">
                {t('admin.settings.changedElsewhere', { fields: changedElsewhere.join(', ') })}
              </p>
            ) : null}
            {save.error ? (
              errorMessages[saveErrorMessage] ? (
                <p role="alert" className="field-error">
                  {errorMessages[saveErrorMessage]}
                </p>
              ) : (
                <ErrorState error={save.error} />
              )
            ) : null}
            {save.isSuccess ? <p role="status">{t('admin.settings.saved')}</p> : null}
          </Card>
          <div className="ops-settings-columns">
            <section>
              <h2>{t('admin.settings.ordinaryTitle')}</h2>
              {ordinary.length ? (
                ordinary.map(([name, entries]) => (
                  <Group
                    key={name}
                    name={name}
                    entries={entries}
                    values={authority.data.values}
                    drafts={drafts}
                    busy={save.isPending}
                    onEdit={editField}
                    onReset={resetField}
                    language={i18n.language}
                    catalogLabels={catalogLabels}
                  />
                ))
              ) : (
                <EmptyState
                  title={t('admin.settings.ordinaryEmpty')}
                  body={t('admin.settings.ordinaryEmptyBody')}
                />
              )}
            </section>
            <section className="ops-danger">
              <h2>{t('admin.settings.dangerousTitle')}</h2>
              <p>{t('admin.settings.dangerousDescription')}</p>
              {dangerous.length ? (
                dangerous.map(([name, entries]) => (
                  <Group
                    key={name}
                    name={name}
                    entries={entries}
                    values={authority.data.values}
                    drafts={drafts}
                    busy={save.isPending}
                    onEdit={editField}
                    onReset={resetField}
                    language={i18n.language}
                    catalogLabels={catalogLabels}
                  />
                ))
              ) : (
                <EmptyState
                  title={t('admin.settings.dangerousEmpty')}
                  body={t('admin.settings.dangerousEmptyBody')}
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
