import { useMemo, useState, type ReactNode } from 'react';
import { Link, useSearchParams } from 'react-router';
import { ApiError } from '@shared/query/http';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import type { TFunction } from 'i18next';
import { useTranslation } from 'react-i18next';
import { EmptyState, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import {
  Affix,
  Panel,
  PanelHead,
  PanelBody,
  SaveBar,
  Segmented,
  Toggle,
} from '@shared/components/ui';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import './settings/settings.css';
import { MaintenancePanel } from '@shared/operations/MaintenancePanel';
import GatewayCapabilitiesSection from '../features/gateway/GatewayCapabilitiesSection';
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
      ? draft.text.replace(/\r\n|\r/g, '\n')
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
  const parsed = parsedDraft(entry, draft, t);
  const dirty = Boolean(drafts[entry.key]) && (parsed.error !== null || parsed.value !== value);
  const differsFromDefault = (parsed.error === null ? parsed.value : value) !== entry.raw_default;
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
  }
  const inputProps = {
    id: inputID,
    type: entry.type === 'integer' ? 'number' : 'text',
    inputMode:
      entry.type === 'integer'
        ? ('numeric' as const)
        : entry.type === 'amount'
          ? ('decimal' as const)
          : undefined,
    min: attribute(entry.minimum),
    max: attribute(entry.maximum),
    step: attribute(entry.step),
    value: draft.text,
    disabled: busy || draft.isNull,
    onChange: (event: React.ChangeEvent<HTMLInputElement>) => change(event.target.value),
  };
  const control =
    entry.type === 'boolean' ? (
      <div className="setting-toggle">
        <Toggle
          label={entry.title[locale]}
          checked={draft.text === 'true'}
          disabled={busy || draft.isNull}
          onChange={(checked) => change(String(checked))}
        />
      </div>
    ) : entry.type === 'enum' && entry.allowed_values.length <= 4 ? (
      <Segmented
        label={entry.title[locale]}
        value={draft.text}
        disabled={busy || draft.isNull}
        options={entry.allowed_values.map((value) => ({ value, label: enumValueLabel(t, value) }))}
        onChange={change}
      />
    ) : entry.type === 'enum' ? (
      <select
        id={inputID}
        value={draft.text}
        disabled={busy || draft.isNull}
        onChange={(event) => change(event.target.value)}
      >
        {entry.allowed_values.map((allowed) => (
          <option key={allowed || 'empty'} value={allowed}>
            {enumValueLabel(t, allowed)}
          </option>
        ))}
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
    ) : entry.type === 'amount' || (entry.type === 'integer' && entry.unit) ? (
      <Affix {...inputProps} unit={entry.unit?.[locale] ?? ''} />
    ) : (
      <input {...inputProps} />
    );
  return (
    <div className={`nb-setting${dirty ? ' is-dirty' : ''}`}>
      <div className="nb-setting__text">
        <h3>
          {entry.type === 'boolean' ||
          (entry.type === 'enum' && entry.allowed_values.length <= 4) ? (
            entry.title[locale]
          ) : (
            <label htmlFor={inputID}>{entry.title[locale]}</label>
          )}
        </h3>
        {differsFromDefault ? (
          <span className="nb-badge nb-badge--warn">{t('admin.settings.modified')}</span>
        ) : null}
        <p>{entry.description[locale]}</p>
      </div>
      <div className="nb-setting__control">
        {control}
        <div className="setting-meta-line">
          <p className="nb-setting__meta">
            {t('admin.settings.catalogDefault')}{' '}
            {entry.raw_default === null
              ? t('admin.settings.notConfigured')
              : entry.type === 'boolean'
                ? t(entry.raw_default ? 'common.enabled' : 'common.disabled')
                : entry.type === 'enum'
                  ? enumValueLabel(t, String(entry.raw_default))
                  : entry.raw_default === ''
                    ? t('admin.settings.enumValues.empty')
                    : scalar(entry.raw_default)}
          </p>
          <details className="setting-info">
            <summary aria-label={t('admin.settings.fieldDetails')}>ⓘ</summary>
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
            {entry.type === 'text' ? (
              <p className="muted">
                {t('admin.settings.legalBytes', {
                  count: utf8Bytes(draft.text),
                  max: entry.maximum,
                })}
              </p>
            ) : null}
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
            {value === null && !entry.null_writable ? (
              <p className="muted">{t('admin.settings.notConfigured')}</p>
            ) : null}
            {preview ? <p className="muted">{preview}</p> : null}
            {draft.text === '0' ? <p className="muted">{entry.zero_semantics[locale]}</p> : null}
            {draft.isNull ? <p className="muted">{entry.null_semantics[locale]}</p> : null}
            {!draft.isNull && draft.text === '' ? (
              <p className="muted">{entry.empty_semantics[locale]}</p>
            ) : null}
          </details>
        </div>

        {differsFromDefault && (entry.raw_default !== null || entry.null_writable) ? (
          <button
            type="button"
            className="nb-btn nb-btn--ghost"
            disabled={busy}
            onClick={() =>
              onEdit(entry.key, {
                ...draft,
                text: entry.raw_default === null ? '' : String(entry.raw_default),
                isNull: entry.raw_default === null,
              })
            }
          >
            {t('admin.settings.restoreDefault')}
          </button>
        ) : null}

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
            className="nb-btn nb-btn--ghost"
            disabled={busy}
            onClick={() => onReset(entry.key)}
          >
            {t('admin.settings.original', {
              defaultValue: 'Previously {{value}}',
              value: scalar(draft.original),
            })}{' '}
            · {t('admin.settings.restoreAuthorityValue')}
          </button>
        ) : null}
      </div>
    </div>
  );
}

// These pairs are declared by the site configuration catalog's check-in bounds.
const REWARD_PAIRS = [
  ['checkin_award_min_milli', 'checkin_award_max_milli'],
  ['game_checkin_award_min_milli', 'game_checkin_award_max_milli'],
] as const;
const LEVEL_NAMES = Array.from({ length: 6 }, (_, index) => `level_display_name_${index + 1}`);
const GROUP_ORDER = [
  'economy',
  'charity',
  'connector',
  'identity',
  'limits',
  'reports',
  'gateway',
  'access',
  'abuse',
  'legal',
  'legal-hold',
  'maintenance',
];
const EXTRA_GROUP_LABELS: Record<string, string> = {
  gateway: 'admin.settings.gatewayGroup',
  'legal-hold': 'admin.settings.legalHoldGroup',
  maintenance: 'admin.settings.maintenanceGroup',
};
const extraGroupDefaults: Record<string, string> = {
  gateway: 'Gateway model capabilities',
  'legal-hold': 'Legal holds',
  maintenance: 'Maintenance mode',
};
function navigationLabel(t: TFunction, name: string) {
  return EXTRA_GROUP_LABELS[name]
    ? t(EXTRA_GROUP_LABELS[name], { defaultValue: extraGroupDefaults[name] })
    : groupLabel(t, name);
}
function Group({
  name,
  entries,
  ...editor
}: EditorProps & { name: string; entries: SiteConfigCatalogEntry[] }) {
  const { t } = useTranslation();
  const field = (entry: SiteConfigCatalogEntry) => (
    <SettingField key={entry.key} entry={entry} {...editor} />
  );
  const byKey = new Map(entries.map((entry) => [entry.key, entry]));
  const used = new Set<string>();
  const pairs = REWARD_PAIRS.filter((pair) => pair.every((key) => byKey.has(key)));
  const names = LEVEL_NAMES.map((key) => byKey.get(key)).filter(
    (entry): entry is SiteConfigCatalogEntry => Boolean(entry),
  );
  return (
    <Panel tone={DANGEROUS_GROUPS.has(name) ? 'danger' : undefined}>
      <PanelHead
        title={groupLabel(t, name)}
        description={t('admin.settings.groupCount', { count: entries.length })}
      />
      <PanelBody>
        {entries.map((entry) => {
          if (used.has(entry.key)) return null;
          const pair = pairs.find((keys) => keys.includes(entry.key as never));
          if (pair) {
            pair.forEach((key) => used.add(key));
            return (
              <div className="nb-setting settings-composite" key={pair[0]}>
                <div className="nb-setting__text">
                  <h3>
                    {t(
                      pair[0].startsWith('game_')
                        ? 'admin.settings.gameRewardRange'
                        : 'admin.settings.rewardRange',
                      {
                        defaultValue: pair[0].startsWith('game_')
                          ? 'Game check-in reward'
                          : 'Check-in reward',
                      },
                    )}
                  </h3>
                  <p>
                    {t('admin.settings.rewardRangeHelp', {
                      defaultValue: 'Each check-in awards an amount within this range.',
                    })}
                  </p>
                </div>
                <div className="settings-paired">{pair.map((key) => field(byKey.get(key)!))}</div>
              </div>
            );
          }
          if (names.length === 6 && LEVEL_NAMES.includes(entry.key)) {
            names.forEach((name) => used.add(name.key));
            return (
              <div className="nb-setting settings-composite" key="level-names">
                <div className="nb-setting__text">
                  <h3>{t('admin.settings.levelNames', { defaultValue: 'Level display names' })}</h3>
                  <p>
                    {t('admin.settings.levelNamesHelp', {
                      defaultValue: 'Leave a name empty to use its default.',
                    })}
                  </p>
                </div>
                <div className="settings-level-names">{names.map(field)}</div>
              </div>
            );
          }
          return field(entry);
        })}
      </PanelBody>
    </Panel>
  );
}

function RetainedSettingsPanel({ active, children }: { active: boolean; children: ReactNode }) {
  const [visited, setVisited] = useState(active);
  if (active && !visited) setVisited(true);
  return <div hidden={!active}>{active || visited ? children : null}</div>;
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
  const [params, setParams] = useSearchParams();
  const [confirmation, setConfirmation] = useState<{
    expected_revision: string;
    values: Record<string, CatalogValue>;
  } | null>(null);
  const resetField = (key: string) => {
    save.reset();
    setDrafts((current) =>
      Object.fromEntries(Object.entries(current).filter(([name]) => name !== key)),
    );
  };
  const editField = (key: string, draft: SettingDraft) => {
    save.reset();
    setDrafts((current) => ({ ...current, [key]: draft }));
  };
  const save = useRetainedOperation(
    async (
      input: { expected_revision: string; values: Record<string, CatalogValue> },
      key,
      context,
    ) => {
      const result = await patchSiteSettings(input, key);
      context.assertCurrent();
      await client.cancelQueries({ queryKey: adminCoreKeys.settings });
      context.commit(() => {
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
      });
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
  const saveErrorMessage =
    save.error instanceof ApiError ? save.error.message.replace(/^\[NonbiriAPI\]\s*/, '') : '';
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
    () => ({
      charity_model_pricing: t('admin.settings.charityModelPricing'),
      ...Object.fromEntries(
        (authority.data?.catalog ?? []).map((entry) => [entry.key, entry.title[locale]]),
      ),
    }),
    [authority.data, locale, t],
  );
  const allGroups = [
    ...new Set(
      (authority.data?.catalog ?? [])
        .filter(
          (entry) =>
            entry.key !== 'default_locale' &&
            entry.write_endpoint.startsWith('/admin/api/site-config/'),
        )
        .map((entry) => entry.group),
    ),
  ];
  const navigationGroups = [
    ...GROUP_ORDER.filter((name) => allGroups.includes(name) || EXTRA_GROUP_LABELS[name]),
    ...allGroups.filter((name) => !GROUP_ORDER.includes(name)),
  ];
  const requestedGroup = params.get('group');
  const activeGroup =
    requestedGroup && navigationGroups.includes(requestedGroup)
      ? requestedGroup
      : (navigationGroups[0] ?? 'economy');
  const editedGroups = [
    ...new Set(
      (authority.data?.catalog ?? [])
        .filter((entry) => {
          const draft = drafts[entry.key];
          if (!draft) return false;
          const parsed = parsedDraft(entry, draft, t);
          return parsed.error !== null || parsed.value !== authority.data?.values[entry.key];
        })
        .map((entry) => entry.group),
    ),
  ];
  const dangerousChanges = editedGroups.some((name) => DANGEROUS_GROUPS.has(name));
  const saveDisabled =
    invalidDraft ||
    Boolean(dependencyError) ||
    changedElsewhere.length > 0 ||
    !Object.keys(pending).length ||
    Boolean(authority.error);
  const requestSave = () => {
    if (!authority.data || saveDisabled || save.isPending) return;
    const input = { expected_revision: authority.data.revision, values: pending };
    if (dangerousChanges) setConfirmation(input);
    else save.mutate(input);
  };
  const editor = {
    values: authority.data?.values ?? {},
    drafts,
    busy: save.isPending,
    onEdit: editField,
    onReset: resetField,
    language: i18n.language,
    catalogLabels,
  };
  const initialFailure = !authority.data && authority.error;

  return (
    <div className="page ops-page">
      <PageHeader
        title={t('admin.settings.title')}
        description={t('admin.settings.description')}
        actions={
          <form role="search" autoComplete="off" onSubmit={(event) => event.preventDefault()}>
            <input
              id="site-config-search"
              name="site-config-search"
              autoComplete="off"
              className="settings-search"
              type="search"
              aria-label={t('common.search')}
              value={search}
              placeholder={t('admin.settings.searchHelp')}
              onChange={(event) => setSearch(event.target.value)}
            />
          </form>
        }
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
          <div className="nb-settings">
            <nav
              className="nb-settings__nav"
              aria-label={t('admin.settings.navigation', { defaultValue: 'Setting groups' })}
            >
              {[false, true].map((dangerous) => (
                <div className="settings-nav-group" key={String(dangerous)}>
                  <h2>
                    {t(dangerous ? 'admin.settings.cautious' : 'admin.settings.regular', {
                      defaultValue: dangerous ? 'Use with care' : 'General',
                    })}
                  </h2>
                  {navigationGroups
                    .filter(
                      (name) =>
                        (DANGEROUS_GROUPS.has(name) ||
                          name === 'legal-hold' ||
                          name === 'maintenance') === dangerous,
                    )
                    .map((name) => (
                      <button
                        type="button"
                        key={name}
                        aria-current={!search.trim() && activeGroup === name ? 'page' : undefined}
                        className={editedGroups.includes(name) ? 'is-dirty' : undefined}
                        onClick={() => {
                          setSearch('');
                          setParams((current) => {
                            const next = new URLSearchParams(current);
                            next.set('group', name);
                            return next;
                          });
                        }}
                      >
                        {navigationLabel(t, name)}
                        {editedGroups.includes(name) ? (
                          <span
                            className="settings-dirty-dot"
                            aria-label={t('admin.settings.modified', { defaultValue: 'Modified' })}
                          />
                        ) : null}
                      </button>
                    ))}
                </div>
              ))}
              <div className="settings-other">
                <h2>
                  {t('admin.settings.otherConfiguration', { defaultValue: 'Other configuration' })}
                </h2>
                <Link to="/activities">{t('admin.activities.nav')}</Link>
                <Link to="/games">{t('admin.games.nav')}</Link>
              </div>
            </nav>
            <div className="settings-content">
              {search.trim() ? (
                groups.size ? (
                  [...groups].map(([name, entries]) => (
                    <Group key={name} name={name} entries={entries} {...editor} />
                  ))
                ) : (
                  <EmptyState
                    title={t('admin.settings.ordinaryEmpty')}
                    body={t('admin.settings.ordinaryEmptyBody')}
                  />
                )
              ) : !EXTRA_GROUP_LABELS[activeGroup] ? (
                <Group name={activeGroup} entries={groups.get(activeGroup) ?? []} {...editor} />
              ) : null}
              <RetainedSettingsPanel active={!search.trim() && activeGroup === 'gateway'}>
                <GatewayCapabilitiesSection />
              </RetainedSettingsPanel>
              <RetainedSettingsPanel active={!search.trim() && activeGroup === 'legal-hold'}>
                <LegalHoldPanel />
              </RetainedSettingsPanel>
              <RetainedSettingsPanel active={!search.trim() && activeGroup === 'maintenance'}>
                <MaintenancePanel role="admin" />
              </RetainedSettingsPanel>
            </div>
          </div>
          <div className="settings-feedback">
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
          </div>
          <SaveBar
            dirtyCount={pendingCount}
            scope={
              <>
                {editedGroups.map((name) => navigationLabel(t, name)).join(' · ')}
                {dangerousChanges ? (
                  <small className="settings-save-warning">
                    {t('admin.settings.confirmationHint', {
                      defaultValue: 'Settings requiring care will be confirmed before saving.',
                    })}
                  </small>
                ) : null}
              </>
            }
            busy={save.isPending}
            saveDisabled={saveDisabled}
            onSave={requestSave}
            onDiscard={() => {
              setDrafts({});
              save.reset();
            }}
            saveLabel={save.isPending ? t('common.working') : t('admin.settings.saveAll')}
            discardLabel={t('admin.settings.discardAll')}
            dirtyLabel={(count) => t('admin.settings.pendingChanges', { count })}
          />
          <ConfirmDialog
            open={confirmation !== null}
            danger
            title={t('admin.settings.confirmTitle', {
              defaultValue: 'Save settings requiring care?',
            })}
            description={t('admin.settings.dangerousDescription')}
            confirmLabel={t('admin.settings.saveAll')}
            confirmDisabled={saveDisabled}
            busy={save.isPending}
            onCancel={() => setConfirmation(null)}
            onConfirm={() => {
              if (!confirmation || saveDisabled) return;
              save.mutate(confirmation);
              setConfirmation(null);
            }}
          />
        </>
      ) : null}
    </div>
  );
}
