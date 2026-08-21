import { useState, type FormEvent } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import {
  Card,
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
} from '@shared/components/States';
import { apiFetch } from '@shared/query/http';
import { adminKeys, type SiteConfigValue, useSiteConfig } from '../data';
import {
  displayCreditsToMilliString,
  milliStringToDisplayInput,
} from '../utils/economyInput';

// The economy & level keys get dedicated typed editors; the generic key list
// keeps every other known key.
const ECONOMY_KEYS = [
  'site_timezone_offset_minutes',
  'checkin_mode',
  'checkin_award_min_milli',
  'checkin_award_max_milli',
  'credits_cap_milli',
  'level_threshold_2_milli',
  'level_threshold_3_milli',
  'level_threshold_4_milli',
] as const;

const ECONOMY_KEY_SET = new Set<string>(ECONOMY_KEYS);

const CHECKIN_MODES = ['enabled', 'level_gated', 'disabled'] as const;

// Bounded offset display: UTC+08:00 / UTC-05:30.
function formatOffset(minutes: number): string {
  const sign = minutes < 0 ? '-' : '+';
  const abs = Math.abs(minutes);
  const hours = String(Math.floor(abs / 60)).padStart(2, '0');
  const mins = String(abs % 60).padStart(2, '0');
  return `UTC${sign}${hours}:${mins}`;
}

// Shared PATCH helper for one typed editor: shows the server message verbatim
// (including readable conflict refusals) and refetches the config on success.
function useConfigPatch() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [saved, setSaved] = useState(false);
  const patch = async (key: string, value: unknown): Promise<void> => {
    setError('');
    setSaved(false);
    setBusy(true);
    try {
      await apiFetch<unknown>(`/admin/api/site-config/${encodeURIComponent(key)}`, {
        method: 'PATCH',
        json: { value },
      });
      await queryClient.invalidateQueries({ queryKey: adminKeys.siteConfig });
      setSaved(true);
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : t('common.errorBody'));
    } finally {
      setBusy(false);
    }
  };
  return { busy, error, saved, patch };
}

function EditorFeedback({ error, saved }: { error: string; saved: boolean }) {
  const { t } = useTranslation();
  return (
    <>
      {error ? <p className="field-error" role="alert">{error}</p> : null}
      {saved ? <p className="inline-success" role="status">{t('admin.settings.saved')}</p> : null}
    </>
  );
}

// Nullable site timezone: JSON null means "never configured" and is distinct
// from an explicit UTC (0). Once any day-keyed data exists the value is
// permanently frozen server-side and every further write is a conflict.
function TimezoneEditor({ value }: { value: number | null }) {
  const { t } = useTranslation();
  const [input, setInput] = useState(value === null ? '' : String(value));
  const [validationError, setValidationError] = useState('');
  const { busy, error, saved, patch } = useConfigPatch();

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setValidationError('');
    const trimmed = input.trim();
    const parsed = Number(trimmed);
    if (
      !/^-?\d+$/.test(trimmed) ||
      !Number.isSafeInteger(parsed) ||
      parsed < -720 ||
      parsed > 840 ||
      parsed % 30 !== 0
    ) {
      setValidationError(t('admin.settings.economy.timezoneInvalid'));
      return;
    }
    await patch('site_timezone_offset_minutes', parsed);
  };

  return (
    <form className="config-row" onSubmit={submit} noValidate>
      <div className="config-key-info">
        <strong>{t('admin.settings.economy.timezoneTitle')}</strong>
        <span className="table-note">{t('admin.settings.economy.timezoneDescription')}</span>
        <span className="mono table-note">site_timezone_offset_minutes</span>
      </div>
      <div className="config-control">
        {value === null ? (
          <>
            <p className="field-error" role="alert">
              {t('admin.settings.economy.timezoneNotSet')}
            </p>
            <p className="inline-notice">
              {t('admin.settings.economy.timezoneNotSetBody')}
            </p>
          </>
        ) : (
          <span className="table-note">
            {t('admin.settings.economy.timezoneCurrent', { value: formatOffset(value) })}
          </span>
        )}
        <p className="inline-notice">{t('admin.settings.economy.timezoneFreezeWarning')}</p>
        <label>
          <span>{t('admin.settings.economy.timezoneInput')}</span>
          <input
            type="number"
            step="30"
            min="-720"
            max="840"
            value={input}
            onChange={(event) => setInput(event.target.value)}
            placeholder="480"
            aria-label={t('admin.settings.economy.timezoneInput')}
          />
        </label>
        {validationError ? <p className="field-error" role="alert">{validationError}</p> : null}
        <EditorFeedback error={error} saved={saved} />
        <button type="submit" className="btn btn-secondary" disabled={busy}>
          {busy ? t('common.working') : t('admin.settings.save')}
        </button>
      </div>
    </form>
  );
}

function CheckinModeEditor({ value }: { value: string }) {
  const { t } = useTranslation();
  const initial = (CHECKIN_MODES as readonly string[]).includes(value) ? value : 'disabled';
  const [selection, setSelection] = useState<string>(initial);
  const { busy, error, saved, patch } = useConfigPatch();

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    await patch('checkin_mode', selection);
  };

  return (
    <form className="config-row" onSubmit={submit} noValidate>
      <div className="config-key-info">
        <strong>{t('admin.settings.economy.checkinModeTitle')}</strong>
        <span className="table-note">{t('admin.settings.economy.checkinModeDescription')}</span>
        <span className="mono table-note">checkin_mode</span>
      </div>
      <div className="config-control">
        <select
          value={selection}
          onChange={(event) => setSelection(event.target.value)}
          aria-label={t('admin.settings.economy.checkinModeTitle')}
        >
          {CHECKIN_MODES.map((mode) => (
            <option key={mode} value={mode}>
              {t(`admin.settings.economy.checkinMode.${mode}`)}
            </option>
          ))}
        </select>
        <EditorFeedback error={error} saved={saved} />
        <button type="submit" className="btn btn-secondary" disabled={busy}>
          {busy ? t('common.working') : t('admin.settings.save')}
        </button>
      </div>
    </form>
  );
}

// A display-credits amount editor: the input is in whole (fractional to three
// digits) display credits and is converted to the canonical milli string via
// the BigInt helpers — never through Number().
function AmountEditor({
  name,
  value,
  title,
  hint,
}: {
  name: string;
  value: string;
  title: string;
  hint: string;
}) {
  const { t } = useTranslation();
  const [input, setInput] = useState(milliStringToDisplayInput(value));
  const [validationError, setValidationError] = useState('');
  const { busy, error, saved, patch } = useConfigPatch();

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setValidationError('');
    const milli = displayCreditsToMilliString(input);
    if (milli === null) {
      setValidationError(t('admin.settings.economy.amountInvalid'));
      return;
    }
    await patch(name, milli);
  };

  return (
    <form className="config-row" onSubmit={submit} noValidate>
      <div className="config-key-info">
        <strong>{title}</strong>
        <span className="table-note">{hint}</span>
        <span className="mono table-note">{name}</span>
      </div>
      <div className="config-control">
        <input
          type="text"
          inputMode="decimal"
          value={input}
          onChange={(event) => setInput(event.target.value)}
          aria-label={title}
        />
        {validationError ? <p className="field-error" role="alert">{validationError}</p> : null}
        <EditorFeedback error={error} saved={saved} />
        <button type="submit" className="btn btn-secondary" disabled={busy}>
          {busy ? t('common.working') : t('admin.settings.save')}
        </button>
      </div>
    </form>
  );
}

function EconomySection({ config }: { config: Record<string, SiteConfigValue> }) {
  const { t } = useTranslation();
  const timezone = config['site_timezone_offset_minutes'];
  const checkinMode = config['checkin_mode'];
  const read = (key: string): string => (typeof config[key] === 'string' ? String(config[key]) : '0');
  return (
    <Card>
      <div className="card-title-row">
        <h2>{t('admin.settings.economy.title')}</h2>
      </div>
      <p className="inline-notice">{t('admin.settings.economy.description')}</p>
      <div className="config-list">
        <TimezoneEditor value={typeof timezone === 'number' ? timezone : null} />
        <CheckinModeEditor value={typeof checkinMode === 'string' ? checkinMode : 'disabled'} />
        <AmountEditor
          name="checkin_award_min_milli"
          value={read('checkin_award_min_milli')}
          title={t('admin.settings.economy.awardMinTitle')}
          hint={t('admin.settings.economy.awardMinHint')}
        />
        <AmountEditor
          name="checkin_award_max_milli"
          value={read('checkin_award_max_milli')}
          title={t('admin.settings.economy.awardMaxTitle')}
          hint={t('admin.settings.economy.awardMaxHint')}
        />
        <AmountEditor
          name="credits_cap_milli"
          value={read('credits_cap_milli')}
          title={t('admin.settings.economy.capTitle')}
          hint={t('admin.settings.economy.capHint')}
        />
        {[2, 3, 4].map((level) => (
          <AmountEditor
            key={`level_threshold_${level}_milli`}
            name={`level_threshold_${level}_milli`}
            value={read(`level_threshold_${level}_milli`)}
            title={t('admin.settings.economy.thresholdTitle', { level })}
            hint={t('admin.settings.economy.thresholdHint')}
          />
        ))}
      </div>
    </Card>
  );
}

function ConfigEditor({ name, initialValue }: { name: string; initialValue: SiteConfigValue }) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [value, setValue] = useState(initialValue === null ? '' : String(initialValue));
  const [error, setError] = useState('');
  const [saved, setSaved] = useState(false);
  const [busy, setBusy] = useState(false);

  const typeLabel =
    typeof initialValue === 'boolean'
      ? t('admin.settings.booleanValue')
      : typeof initialValue === 'number'
        ? t('admin.settings.numberValue')
        : t('admin.settings.textValue');
  const title = t(`admin.settings.configKeys.${name}.title`, { defaultValue: name });
  const description = t(`admin.settings.configKeys.${name}.description`, { defaultValue: '' });

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError('');
    setSaved(false);
    let nextValue: SiteConfigValue;
    if (typeof initialValue === 'boolean') {
      nextValue = value === 'true';
    } else if (typeof initialValue === 'number') {
      if (!value.trim() || !Number.isFinite(Number(value))) {
        setError(t('admin.settings.invalidNumber'));
        return;
      }
      nextValue = Number(value);
    } else if (initialValue === null) {
      nextValue = value.trim() ? value.trim() : null;
    } else {
      nextValue = value;
    }

    setBusy(true);
    try {
      await apiFetch<unknown>(`/admin/api/site-config/${encodeURIComponent(name)}`, {
        method: 'PATCH',
        json: { value: nextValue },
      });
      await queryClient.invalidateQueries({ queryKey: adminKeys.siteConfig });
      setSaved(true);
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : t('common.errorBody'));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form className="config-row" onSubmit={submit} noValidate>
      <div className="config-key-info">
        <strong>{title}</strong>
        {description ? <span className="table-note">{description}</span> : null}
        <span className="mono table-note">{name} · {typeLabel}</span>
      </div>
      <div className="config-control">
        {typeof initialValue === 'boolean' ? (
          <select value={value} onChange={(event) => setValue(event.target.value)} aria-label={name}>
            <option value="true">{t('common.yes')}</option>
            <option value="false">{t('common.no')}</option>
          </select>
        ) : name.startsWith('legal_privacy_override') || name.startsWith('legal_terms_override') ? (
          <textarea
            value={value}
            onChange={(event) => setValue(event.target.value)}
            aria-label={name}
            maxLength={65536}
            rows={14}
            spellCheck={false}
          />
        ) : (
          <input
            type={typeof initialValue === 'number' ? 'number' : 'text'}
            value={value}
            onChange={(event) => setValue(event.target.value)}
            aria-label={name}
            maxLength={512}
          />
        )}
        <button type="submit" className="btn btn-secondary" disabled={busy}>
          {busy ? t('common.working') : t('admin.settings.save')}
        </button>
      </div>
      {error ? <p className="field-error" role="alert">{error}</p> : null}
      {saved ? <p className="inline-success" role="status">{t('admin.settings.saved')}</p> : null}
    </form>
  );
}

export function SettingsPage() {
  const { t } = useTranslation();
  const config = useSiteConfig();

  return (
    <div className="page">
      <PageHeader
        eyebrow={t('app.name')}
        title={t('admin.settings.title')}
        description={t('admin.settings.description')}
      />
      {config.isPending ? (
        <Card>
          <LoadingState />
        </Card>
      ) : config.error ? (
        <Card>
          <ErrorState error={config.error} onRetry={() => void config.refetch()} />
        </Card>
      ) : Object.keys(config.data).length === 0 ? (
        <Card>
          <EmptyState title={t('admin.settings.empty')} body={t('admin.settings.emptyBody')} />
        </Card>
      ) : (
        <>
          <EconomySection config={config.data} />
          <Card>
            <div className="card-title-row">
              <h2>{t('admin.settings.listTitle')}</h2>
            </div>
            <p className="inline-notice">{t('admin.settings.sensitiveHint')}</p>
            <div className="config-list">
              {Object.entries(config.data)
                .filter(([name]) => !ECONOMY_KEY_SET.has(name))
                .sort(([left], [right]) => left.localeCompare(right))
                .map(([name, value]) => (
                  <ConfigEditor key={name} name={name} initialValue={value} />
                ))}
            </div>
          </Card>
        </>
      )}
    </div>
  );
}
