import { useEffect, useState, type FormEvent } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import {
  Card,
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
} from '@shared/components/States';
import { ApiError, apiFetch } from '@shared/query/http';
import { asRecord } from '@shared/query/normalize';
import {
  adminKeys,
  normalizeSiteConfig,
  type LocalizedCatalogText,
  type SiteConfigCatalogEntry,
  type SiteConfigValue,
  useSiteConfig,
  useSiteConfigCatalog,
} from '../data';
import { exactCreditDisplay, humanReadableSeconds } from '../utils/catalogDisplay';

const LEGAL_KEYS = new Set([
  'legal_privacy_override_zh',
  'legal_privacy_override_en',
  'legal_terms_override_zh',
  'legal_terms_override_en',
]);

function utf8Bytes(value: string): number {
  return new TextEncoder().encode(value).byteLength;
}

function localText(value: LocalizedCatalogText, language: string): string {
  return language.startsWith('zh') ? value.zh : value.en;
}

function valueLabel(value: SiteConfigValue, notConfigured: string): string {
  if (value === null) return notConfigured;
  if (value === '') return '""';
  return String(value);
}

type LegalLineEndingStyle = 'crlf' | 'lf' | 'cr' | 'mixed' | 'none';

function legalLineEndingStyle(value: string): LegalLineEndingStyle {
  const withoutCRLF = value.replace(/\r\n/g, '');
  const hasCRLF = value.includes('\r\n');
  const hasCR = withoutCRLF.includes('\r');
  const hasLF = withoutCRLF.includes('\n');
  const kinds = Number(hasCRLF) + Number(hasCR) + Number(hasLF);
  if (kinds > 1) return 'mixed';
  if (hasCRLF) return 'crlf';
  if (hasCR) return 'cr';
  if (hasLF) return 'lf';
  return 'none';
}

function restoreLegalLineEndings(value: string, style: LegalLineEndingStyle): string {
  if (style === 'crlf') return value.replace(/\r\n|\r|\n/g, '\r\n');
  if (style === 'cr') return value.replace(/\r\n|\n/g, '\r');
  return value;
}

function formatOffset(minutes: number): string {
  const sign = minutes < 0 ? '-' : '+';
  const absolute = Math.abs(minutes);
  return `UTC${sign}${String(Math.floor(absolute / 60)).padStart(2, '0')}:${String(absolute % 60).padStart(2, '0')}`;
}

function parseCatalogValue(
  entry: SiteConfigCatalogEntry,
  draft: string,
): SiteConfigValue | undefined {
  if (entry.value_type === 'boolean') return draft === 'true';
  if (entry.value_type === 'integer' || entry.value_type === 'optional_integer') {
    if (draft === '' && entry.null_writable === true) return null;
    if (!/^(0|-?[1-9]\d*)$/.test(draft)) return undefined;
    const parsed = Number(draft);
    if (!Number.isSafeInteger(parsed)) return undefined;
    if (typeof entry.minimum === 'number' && parsed < entry.minimum) return undefined;
    if (typeof entry.maximum === 'number' && parsed > entry.maximum) return undefined;
    if (typeof entry.step === 'number' && parsed % entry.step !== 0) return undefined;
    return parsed;
  }
  if (entry.value_type === 'amount' || entry.value_type === 'optional_amount') {
    if (!/^(0|[1-9]\d*)$/.test(draft)) return undefined;
    try {
      const parsed = BigInt(draft);
      if (typeof entry.minimum === 'string' && parsed < BigInt(entry.minimum)) return undefined;
      if (typeof entry.maximum === 'string' && parsed > BigInt(entry.maximum)) return undefined;
    } catch {
      return undefined;
    }
    return draft;
  }
  if (entry.value_type === 'locale' || entry.value_type === 'optional_locale' || entry.value_type === 'enum') {
    if (!entry.allowed_values || !entry.allowed_values.includes(draft)) return undefined;
  }
  if (LEGAL_KEYS.has(entry.key) && utf8Bytes(draft) > 65_536) return undefined;
  if (typeof entry.maximum === 'number' && utf8Bytes(draft) > entry.maximum) return undefined;
  return draft;
}

function catalogValueLabel(
  entry: SiteConfigCatalogEntry,
  value: SiteConfigValue,
  notConfigured: string,
  language: string,
): string {
  const raw = valueLabel(value, notConfigured);
  if (value !== null && entry.unit.en === 'seconds' && typeof value === 'number') {
    return `${raw} (${humanReadableSeconds(value, language)})`;
  }
  if (value !== null && entry.unit.en === 'milli-credits' && typeof value === 'string') {
    const display = exactCreditDisplay(value);
    if (display === null) return raw;
    const displayUnit = language.startsWith('zh') ? '积分' : 'credits';
    return `${raw} ${localText(entry.unit, language)} (${display} ${displayUnit})`;
  }
  return raw;
}

function CatalogFacts({ entry, language }: { entry: SiteConfigCatalogEntry; language: string }) {
  const { t } = useTranslation();
  const range = entry.minimum !== null || entry.maximum !== null
    ? `${entry.minimum === null ? '—' : catalogValueLabel(entry, entry.minimum, t('admin.settings.notConfigured'), language)} … ${entry.maximum === null ? '—' : catalogValueLabel(entry, entry.maximum, t('admin.settings.notConfigured'), language)}${entry.step !== undefined && entry.step !== null ? ` · ${t('admin.settings.catalogStep')} ${catalogValueLabel(entry, entry.step, t('admin.settings.notConfigured'), language)}` : ''}`
    : '—';
  return (
    <div className="table-note">
      <span>
        {t('admin.settings.catalogDefault')}: {catalogValueLabel(entry, entry.raw_default, t('admin.settings.notConfigured'), language)}
        {' · '}{t('admin.settings.catalogEffective')}: {catalogValueLabel(entry, entry.effective_fallback, t('admin.settings.notConfigured'), language)}
        {' · '}{t('admin.settings.catalogRange')}: {range}
        {' · '}{localText(entry.unit, language)}
      </span>
      <details>
        <summary>{t('admin.settings.catalogSemantics')}</summary>
        <ul className="plain-list">
          {entry.zero_semantics ? <li>{localText(entry.zero_semantics, language)}</li> : null}
          <li>{localText(entry.null_semantics, language)}</li>
          {entry.empty_semantics ? <li>{localText(entry.empty_semantics, language)}</li> : null}
          {entry.independent_gates.map((gate, index) => (
            <li key={`${entry.key}-gate-${index}`}>
              {t('admin.settings.catalogIndependentGate')}: {localText(gate, language)}
            </li>
          ))}
        </ul>
      </details>
    </div>
  );
}

function TimezoneContext({ draft, entry, language }: {
  draft: string;
  entry: SiteConfigCatalogEntry;
  language: string;
}) {
  const { t } = useTranslation();
  const parsed = parseCatalogValue(entry, draft);
  const preview = typeof parsed === 'number' ? formatOffset(parsed) : t('admin.settings.timezonePreviewInvalid');
  return (
    <div className="inline-notice">
      <strong>{t('admin.settings.timezoneSignedMinutes')}</strong>
      <p>{t('admin.settings.timezoneExamples')}</p>
      <p>{t('admin.settings.timezonePreview', { value: preview })}</p>
      <p>{localText(entry.null_semantics, language)} {entry.zero_semantics ? localText(entry.zero_semantics, language) : ''}</p>
      <p><strong>{t('admin.settings.timezoneImmutable')}</strong></p>
    </div>
  );
}

function ConfigEditor({ entry, initialValue }: {
  entry: SiteConfigCatalogEntry;
  initialValue: SiteConfigValue;
}) {
  const { t, i18n } = useTranslation();
  const language = i18n.resolvedLanguage ?? i18n.language;
  const queryClient = useQueryClient();
  const isLegal = LEGAL_KEYS.has(entry.key);
  const [draft, setDraft] = useState(initialValue === null ? '' : String(initialValue));
  const [lineEndingStyle, setLineEndingStyle] = useState<LegalLineEndingStyle>(
    legalLineEndingStyle(initialValue === null ? '' : String(initialValue)),
  );
  const [legalDraftTouched, setLegalDraftTouched] = useState(false);
  const [error, setError] = useState('');
  const [saved, setSaved] = useState(false);
  const [busy, setBusy] = useState(false);

  const changeDraft = (value: string) => {
    setDraft(value);
    if (isLegal) setLegalDraftTouched(true);
    setError('');
    setSaved(false);
  };

  useEffect(() => {
    // A successful write and a 409 both refetch the authoritative snapshot.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setDraft(initialValue === null ? '' : String(initialValue));
    setLineEndingStyle(legalLineEndingStyle(initialValue === null ? '' : String(initialValue)));
    setLegalDraftTouched(false);
  }, [initialValue]);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError('');
    setSaved(false);
    const submissionDraft = isLegal && legalDraftTouched
      ? restoreLegalLineEndings(draft, lineEndingStyle)
      : draft;
    const nextValue = parseCatalogValue(entry, submissionDraft);
    if (nextValue === undefined) {
      setError(t('admin.settings.catalogInvalid'));
      return;
    }
    setBusy(true);
    try {
      const payload = await apiFetch<unknown>(entry.write_endpoint, {
        method: 'PATCH',
        json: { value: nextValue },
      });
      const response = asRecord(payload);
      if (!response || response.key !== entry.key || !Object.hasOwn(response, 'value')) {
        throw new ApiError('invalid_response', t('common.errorBody'), 200);
      }
      const normalized = normalizeSiteConfig({ [entry.key]: response.value }, [entry]);
      setDraft(normalized[entry.key] === null ? '' : String(normalized[entry.key]));
      setLineEndingStyle(legalLineEndingStyle(normalized[entry.key] === null ? '' : String(normalized[entry.key])));
      setLegalDraftTouched(false);
      await queryClient.invalidateQueries({ queryKey: adminKeys.siteConfig });
      setSaved(true);
    } catch (requestError) {
      if (requestError instanceof ApiError && requestError.status === 409) {
        await queryClient.refetchQueries({ queryKey: adminKeys.siteConfig });
        const refreshed = queryClient.getQueryState(adminKeys.siteConfig)?.status === 'success';
        setError(entry.key === 'site_timezone_offset_minutes'
          ? refreshed ? t('admin.settings.timezoneConflict') : t('admin.settings.timezoneRefreshFailed')
          : requestError.message);
      } else {
        setError(requestError instanceof Error ? requestError.message : t('common.errorBody'));
      }
    } finally {
      setBusy(false);
    }
  };

  const allowedValues = entry.allowed_values ?? [];
  const isChoice = allowedValues.length > 0;
  const input = entry.value_type === 'boolean' ? (
    <select value={draft} onChange={(event) => changeDraft(event.target.value)} aria-label={localText(entry.title, language)}>
      <option value="true">{t('common.yes')}</option>
      <option value="false">{t('common.no')}</option>
    </select>
  ) : isChoice ? (
    <select value={draft} onChange={(event) => changeDraft(event.target.value)} aria-label={localText(entry.title, language)}>
      {allowedValues.map((value) => <option key={value || 'empty'} value={value}>{value || '""'}</option>)}
    </select>
  ) : isLegal ? (
    <>
      <textarea
        value={draft}
        onChange={(event) => changeDraft(event.target.value)}
        aria-label={localText(entry.title, language)}
        rows={12}
        spellCheck={false}
      />
      <small className="muted">{t('admin.settings.legalBytes', {
        count: utf8Bytes(legalDraftTouched ? restoreLegalLineEndings(draft, lineEndingStyle) : draft),
        max: 65_536,
      })}</small>
    </>
  ) : (
    <input
      type="text"
      inputMode={entry.value_type.includes('amount') ||
        (entry.value_type.includes('integer') &&
          (typeof entry.minimum !== 'number' || entry.minimum >= 0)) ? 'numeric' : undefined}
      value={draft}
      onChange={(event) => changeDraft(event.target.value)}
      aria-label={localText(entry.title, language)}
      step={entry.step === undefined || entry.step === null ? undefined : String(entry.step)}
      min={entry.minimum === null ? undefined : String(entry.minimum)}
      max={entry.maximum === null ? undefined : String(entry.maximum)}
      placeholder={entry.nullable ? t('admin.settings.notConfigured') : undefined}
    />
  );

  return (
    <form className="config-row" onSubmit={submit} noValidate>
      <div className="config-key-info">
        <strong>{localText(entry.title, language)}</strong>
        <span className="table-note">{localText(entry.description, language)}</span>
        <span className="mono table-note">{entry.key} · {entry.value_type}</span>
        <CatalogFacts entry={entry} language={language} />
      </div>
      <div className="config-control">
        {entry.key === 'site_timezone_offset_minutes'
          ? <TimezoneContext draft={draft} entry={entry} language={language} />
          : null}
        {input}
        {entry.unit.en === 'seconds' && typeof parseCatalogValue(entry, draft) === 'number' ? (
          <small className="muted">{t('admin.settings.catalogDurationPreview', {
            value: humanReadableSeconds(parseCatalogValue(entry, draft) as number, language),
          })}</small>
        ) : null}
        {entry.unit.en === 'milli-credits' && typeof parseCatalogValue(entry, draft) === 'string' &&
          exactCreditDisplay(parseCatalogValue(entry, draft) as string) !== null ? (
            <small className="muted">{t('admin.settings.catalogMilliPreview', {
              raw: parseCatalogValue(entry, draft) as string,
              credits: exactCreditDisplay(parseCatalogValue(entry, draft) as string),
            })}</small>
          ) : null}
        {error ? <p className="field-error" role="alert">{error}</p> : null}
        {saved ? <p className="inline-success" role="status">{t('admin.settings.saved')}</p> : null}
        <button type="submit" className="btn btn-secondary" disabled={busy}>
          {busy ? t('common.working') : entry.null_writable === true && draft === ''
            ? t('admin.settings.restoreFallback') : t('admin.settings.save')}
        </button>
      </div>
    </form>
  );
}

export function SettingsPage() {
  const { t } = useTranslation();
  const catalog = useSiteConfigCatalog();
  const config = useSiteConfig(catalog.data, catalog.isSuccess);
  const error = config.error ?? catalog.error;
  const pending = config.isPending || catalog.isPending;
  const entries = (catalog.data ?? []).filter((entry) => entry.write_endpoint !== '/admin/api/games/config');

  return (
    <div className="page">
      <PageHeader eyebrow={t('app.name')} title={t('admin.settings.title')} description={t('admin.settings.description')} />
      {error ? (
        <Card><ErrorState error={error} onRetry={() => { void config.refetch(); void catalog.refetch(); }} /></Card>
      ) : pending ? (
        <Card><LoadingState /></Card>
      ) : entries.length === 0 ? (
        <Card><EmptyState title={t('admin.settings.empty')} body={t('admin.settings.emptyBody')} /></Card>
      ) : (
        <Card>
          <div className="card-title-row"><h2>{t('admin.settings.listTitle')}</h2></div>
          <p className="inline-notice">{t('admin.settings.sensitiveHint')}</p>
          <div className="config-list">
            {entries.map((entry) => (
              <ConfigEditor key={entry.key} entry={entry} initialValue={config.data?.[entry.key] ?? entry.raw_default} />
            ))}
          </div>
        </Card>
      )}
    </div>
  );
}
