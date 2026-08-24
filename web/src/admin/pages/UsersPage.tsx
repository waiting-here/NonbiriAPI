import { Fragment, useEffect, useState, type FormEvent } from 'react';
import { formatDateTime } from '@shared/utils/datetime';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import {
  Card,
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
  Pagination,
  ReadOnlyValue,
  StatusBadge,
} from '@shared/components/States';
import { apiFetch } from '@shared/query/http';
import { asRecord, hasControlCharacters, optionalText } from '@shared/query/normalize';
import { displayCreditsToMilliString } from '../utils/economyInput';
import {
  adminKeys,
  useAdminAdjustUserCredits,
  useAdminBanUser,
  useAdminUnbanUser,
  useAdminUpdateUserLevel,
  type AdminUser,
  useAdminUsers,
} from '../data';
import {
  formatCompact,
  formatCount,
  formatCreditsFromMilli,
  parseEconomyString,
  type FormattedNumber,
} from '@shared/utils/formatNumber';

const PAGE_SIZE_OPTIONS = [10, 20, 50] as const;

// The server ceiling for one ban lifetime (10*366 days in seconds).
const MAX_BAN_SECONDS = 316_224_000;

const BAN_UNIT_SECONDS = {
  minutes: 60,
  hours: 3_600,
  days: 86_400,
} as const;

type BanUnit = keyof typeof BAN_UNIT_SECONDS;

// Preset ban lifetimes in days; 'permanent' and 'custom' are handled apart.
const BAN_PRESET_DAYS = [1, 3, 7, 30] as const;

function elevatedToken(payload: unknown): string | undefined {
  const record = asRecord(payload);
  if (!record) return undefined;
  for (const key of ['token', 'elevated_token', 'capability']) {
    const value = record[key];
    if (
      typeof value === 'string' &&
      value.length >= 8 &&
      value.length <= 512 &&
      !hasControlCharacters(value)
    ) {
      return value;
    }
  }
  return undefined;
}

// One idempotency key per logical confirmation. A secure-context browser has
// crypto.randomUUID; the fallback keeps the key unique without it.
function newOperationId(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    try {
      return crypto.randomUUID();
    } catch {
      // fall through to the timestamp form
    }
  }
  return `op-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 12)}`;
}

// Display-credits column form: the display value is whole display credits
// (abbreviated per the shared compact rules when large); the exact figure for
// the tooltip keeps the full milli-credit balance. Presentation only — the
// server stays the accounting authority.
function formatBalance(milli: string): FormattedNumber {
  const parsed = parseEconomyString(milli);
  if (parsed === null) {
    return { display: '—', exact: '—', abbreviated: false };
  }
  const credits =
    parsed >= 0n ? (parsed * 2n + 1000n) / 2000n : -((-parsed * 2n + 1000n) / 2000n);
  const compact = formatCompact(credits);
  return {
    display: compact.display,
    exact: formatCreditsFromMilli(milli).exact,
    abbreviated: compact.abbreviated,
  };
}

function BalanceCell({ label, milli }: { label: string; milli: string }) {
  const { t } = useTranslation();
  const formatted = formatBalance(milli);
  const exact = t('admin.users.exactMilli', { value: formatted.exact });
  return (
    <span className="compact-number" tabIndex={0} title={exact} aria-label={`${label} · ${exact}`}>
      {formatted.display}
    </span>
  );
}

function UserLimits({ user }: { user: AdminUser }) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [endpointLimit, setEndpointLimit] = useState(
    user.endpoint_limit === undefined ? '' : String(user.endpoint_limit),
  );
  const [rpmLimit, setRpmLimit] = useState(user.rpm_limit === undefined ? '' : String(user.rpm_limit));
  const [concurrencyLimit, setConcurrencyLimit] = useState(
    user.concurrency_limit === undefined ? '' : String(user.concurrency_limit),
  );
  const [validationError, setValidationError] = useState('');
  const mutation = useMutation({
    mutationFn: (values: {
      endpoint_limit: number | null;
      rpm_limit: number | null;
      concurrency_limit: number | null;
    }) =>
      apiFetch<AdminUser>(`/admin/api/users/${encodeURIComponent(user.id)}`, {
        method: 'PATCH',
        json: values,
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: adminKeys.usersRoot });
    },
  });

  const parseLimit = (value: string, minimum: number, maximum: number): number | null | undefined => {
    if (value === '') return null;
    if (!/^(0|[1-9]\d*)$/.test(value)) return undefined;
    const parsed = Number(value);
    return Number.isSafeInteger(parsed) && parsed >= minimum && parsed <= maximum
      ? parsed
      : undefined;
  };

  const save = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setValidationError('');
    const endpoint = parseLimit(endpointLimit, 0, 10_000);
    const rpm = parseLimit(rpmLimit, 1, 4_096);
    const concurrency = parseLimit(concurrencyLimit, 1, 100_000);
    if (endpoint === undefined || rpm === undefined || concurrency === undefined) {
      setValidationError(t('admin.users.limitInvalid'));
      return;
    }
    mutation.mutate({
      endpoint_limit: endpoint,
      rpm_limit: rpm,
      concurrency_limit: concurrency,
    });
  };

  return (
    <form className="limit-form" onSubmit={save} noValidate>
      <div className="limit-fields">
        <label>
          <span>{t('admin.users.endpointLimit')}</span>
          <input
            type="number"
            min="0"
            max="10000"
            step="1"
            value={endpointLimit}
            onChange={(event) => setEndpointLimit(event.target.value)}
            aria-label={`${t('admin.users.endpointLimit')} · ${user.username}`}
          />
          <small className="muted">
            {t('admin.users.limitRawEffective', {
              raw: user.endpoint_limit ?? t('admin.users.limitInherited'),
              effective: user.effective_endpoint_limit,
            })}
          </small>
        </label>
        <label>
          <span>{t('admin.users.rpmLimit')}</span>
          <input
            type="number"
            min="1"
            max="4096"
            step="1"
            value={rpmLimit}
            onChange={(event) => setRpmLimit(event.target.value)}
            aria-label={`${t('admin.users.rpmLimit')} · ${user.username}`}
          />
          <small className="muted">
            {t('admin.users.limitRawEffective', {
              raw: user.rpm_limit ?? t('admin.users.limitInherited'),
              effective: user.effective_rpm_limit,
            })}
          </small>
        </label>
        <label>
          <span>{t('admin.users.concurrencyLimit')}</span>
          <input
            type="number"
            min="1"
            max="100000"
            step="1"
            value={concurrencyLimit}
            onChange={(event) => setConcurrencyLimit(event.target.value)}
            aria-label={`${t('admin.users.concurrencyLimit')} · ${user.username}`}
          />
          <small className="muted">
            {t('admin.users.limitRawEffective', {
              raw: user.concurrency_limit ?? t('admin.users.limitInherited'),
              effective: user.effective_concurrency_limit,
            })}
          </small>
        </label>
      </div>
      <small className="muted">{t('admin.users.limitHint')}</small>
      {validationError ? <p className="field-error" role="alert">{validationError}</p> : null}
      {mutation.error ? <ErrorState error={mutation.error} /> : null}
      {mutation.isSuccess ? <p className="inline-success" role="status">{t('admin.users.saved')}</p> : null}
      <div className="table-actions">
        <button type="submit" className="btn btn-quiet" disabled={mutation.isPending}>
          {mutation.isPending ? t('common.working') : t('admin.users.saveLimits')}
        </button>
        <button
          type="button"
          className="btn btn-link"
          onClick={() => {
            setEndpointLimit('');
            setRpmLimit('');
            setConcurrencyLimit('');
          }}
        >
          {t('admin.users.restoreDefault')}
        </button>
      </div>
    </form>
  );
}

// One confirmed logical adjustment: the operation id is generated when the
// administrator confirms these exact values and reused verbatim while the same
// values are resubmitted (the server dedupes on it); any change to the values
// or a fresh intent means a fresh id.
interface ConfirmedAdjustment {
  id: string;
  credits: string;
  donation: string;
  reason: string;
}

function EconomyAdjustForm({ user }: { user: AdminUser }) {
  const { t } = useTranslation();
  const adjust = useAdminAdjustUserCredits();
  const [creditsDelta, setCreditsDelta] = useState('');
  const [donationDelta, setDonationDelta] = useState('');
  const [reason, setReason] = useState('');
  const [validationError, setValidationError] = useState('');
  const [confirmed, setConfirmed] = useState<ConfirmedAdjustment | null>(null);

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setValidationError('');
    // Inputs are display credits (up to three fraction digits); the wire delta
    // is the canonical milli-credit string produced by the same BigInt
    // conversion the settings editors use — the value never passes through
    // Number(). An invalid shape is rejected before any request is built.
    const credits = creditsDelta.trim();
    const donation = donationDelta.trim();
    const creditsMilli = credits ? displayCreditsToMilliString(credits) : '';
    const donationMilli = donation ? displayCreditsToMilliString(donation) : '';
    if (!credits && !donation) {
      setValidationError(t('admin.users.economyMissingDelta'));
      return;
    }
    if ((credits && creditsMilli === null) || (donation && donationMilli === null)) {
      setValidationError(t('admin.users.economyInvalidDelta'));
      return;
    }
    const trimmedReason = reason.trim();
    if (!trimmedReason) {
      setValidationError(t('admin.users.economyReasonRequired'));
      return;
    }
    const sameSubmission =
      confirmed !== null &&
      confirmed.credits === credits &&
      confirmed.donation === donation &&
      confirmed.reason === trimmedReason;
    const operationId = sameSubmission ? confirmed.id : newOperationId();
    setConfirmed({ id: operationId, credits, donation, reason: trimmedReason });
    adjust.mutate(
      {
        userId: user.id,
        creditsDelta: creditsMilli || undefined,
        donationCreditDelta: donationMilli || undefined,
        operationId,
        reason: trimmedReason,
      },
      {
        // After a committed adjustment the id is spent: resubmitting the same
        // values is a NEW intent and must draw a fresh id, while a retry after
        // a failure keeps this one (handled by sameSubmission above).
        onSuccess: () => setConfirmed(null),
      },
    );
  };

  const reset = () => {
    setCreditsDelta('');
    setDonationDelta('');
    setReason('');
    setValidationError('');
    setConfirmed(null);
    adjust.reset();
  };

  return (
    <form className="limit-form" onSubmit={submit} noValidate>
      <div className="limit-fields">
        <label>
          <span>{t('admin.users.economyCreditsDelta')}</span>
          <input
            type="text"
            inputMode="decimal"
            value={creditsDelta}
            onChange={(event) => setCreditsDelta(event.target.value)}
            placeholder="-1.5"
            aria-label={`${t('admin.users.economyCreditsDelta')} · ${user.username}`}
          />
        </label>
        <label>
          <span>{t('admin.users.economyDonationDelta')}</span>
          <input
            type="text"
            inputMode="decimal"
            value={donationDelta}
            onChange={(event) => setDonationDelta(event.target.value)}
            placeholder="2.5"
            aria-label={`${t('admin.users.economyDonationDelta')} · ${user.username}`}
          />
        </label>
      </div>
      <label>
        <span>{t('admin.users.economyReason')}</span>
        <input
          type="text"
          value={reason}
          onChange={(event) => setReason(event.target.value)}
          placeholder={t('admin.users.economyReasonPlaceholder')}
          maxLength={1024}
          aria-label={`${t('admin.users.economyReason')} · ${user.username}`}
        />
      </label>
      <small className="muted">{t('admin.users.economyHint')}</small>
      {validationError ? <p className="field-error" role="alert">{validationError}</p> : null}
      {adjust.error ? <ErrorState error={adjust.error} /> : null}
      {adjust.error ? (
        <p className="inline-notice">{t('admin.users.economyRetryHint')}</p>
      ) : null}
      {adjust.isSuccess && adjust.data ? (
        <p className="inline-success" role="status">
          {t('admin.users.economyDone', {
            credits: formatCreditsFromMilli(adjust.data.credits_balance).display,
            donation: formatCreditsFromMilli(adjust.data.donation_credit_balance).display,
          })}
        </p>
      ) : null}
      <div className="table-actions">
        <button type="submit" className="btn btn-quiet" disabled={adjust.isPending}>
          {adjust.isPending ? t('common.working') : t('admin.users.economySubmit')}
        </button>
        <button type="button" className="btn btn-link" onClick={reset}>
          {t('admin.users.economyClear')}
        </button>
      </div>
    </form>
  );
}

function LevelControl({ user }: { user: AdminUser }) {
  const { t } = useTranslation();
  const mutation = useAdminUpdateUserLevel();
  const [selection, setSelection] = useState<string>(
    user.level !== undefined ? String(user.level) : 'auto',
  );

  const apply = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    mutation.mutate({
      userId: user.id,
      level: selection === 'auto' ? null : Number(selection),
    });
  };

  return (
    <form className="limit-form" onSubmit={apply} noValidate>
      <dl className="detail-grid">
        <div className="detail-row">
          <dt>{t('admin.users.levelAutoHighwater')}</dt>
          <dd>Lv{user.auto_level}</dd>
        </div>
        <div className="detail-row">
          <dt>{t('admin.users.levelManualCurrent')}</dt>
          <dd>
            {user.level !== undefined ? `Lv${user.level}` : t('admin.users.levelManualNone')}
          </dd>
        </div>
      </dl>
      <div className="limit-fields">
        <label>
          <span>{t('admin.users.levelControl')}</span>
          <select
            value={selection}
            onChange={(event) => setSelection(event.target.value)}
            aria-label={`${t('admin.users.levelControl')} · ${user.username}`}
          >
            <option value="auto">{t('admin.users.levelAutoOption', { level: user.auto_level })}</option>
            {[1, 2, 3, 4, 5].map((level) => (
              <option key={level} value={String(level)}>
                {t('admin.users.levelManualOption', { level })}
              </option>
            ))}
          </select>
        </label>
      </div>
      <small className="muted">{t('admin.users.levelHint')}</small>
      {mutation.error ? <ErrorState error={mutation.error} /> : null}
      {mutation.isSuccess ? (
        <p className="inline-success" role="status">{t('admin.users.levelSaved')}</p>
      ) : null}
      <div className="table-actions">
        <button type="submit" className="btn btn-quiet" disabled={mutation.isPending}>
          {mutation.isPending ? t('common.working') : t('admin.users.levelApply')}
        </button>
      </div>
    </form>
  );
}

// Ban control: the request wire expresses a time ban as a lifetime duration in
// seconds (the server stores and reports the absolute deadline), so the inputs
// are durations only; the absolute deadline is displayed, never entered.
function BanControl({ user }: { user: AdminUser }) {
  const { t } = useTranslation();
  const ban = useAdminBanUser();
  const unban = useAdminUnbanUser();
  const [mode, setMode] = useState<string>('permanent');
  const [customValue, setCustomValue] = useState('');
  const [customUnit, setCustomUnit] = useState<BanUnit>('days');
  const [reason, setReason] = useState('');
  const [validationError, setValidationError] = useState('');
  const [confirming, setConfirming] = useState<'ban' | 'unban' | null>(null);
  // Remaining-time display needs "now". The clock is an external source:
  // reads happen in timer callbacks (never during render), refreshed once a
  // minute and whenever the deadline changes.
  const [nowSeconds, setNowSeconds] = useState(0);
  useEffect(() => {
    const tick = () => setNowSeconds(Math.floor(Date.now() / 1000));
    const timer = window.setTimeout(tick, 0);
    const interval = window.setInterval(tick, 60_000);
    return () => {
      window.clearTimeout(timer);
      window.clearInterval(interval);
    };
  }, [user.banned_until]);

  const remainingText = (until: number): string => {
    if (nowSeconds === 0) return '…';
    const seconds = until - nowSeconds;
    if (seconds <= 0) return t('admin.users.banExpiring');
    const days = Math.floor(seconds / 86_400);
    const hours = Math.floor((seconds % 86_400) / 3_600);
    return t('admin.users.banRemaining', { days, hours });
  };

  // undefined = permanent; null = invalid; number = seconds from now.
  const durationSeconds = (): number | null | undefined => {
    if (mode === 'permanent') return undefined;
    if (mode !== 'custom') {
      return Number(mode) * 86_400;
    }
    if (!/^\d+$/.test(customValue.trim())) return null;
    const value = Number(customValue.trim());
    if (!Number.isSafeInteger(value) || value < 1) return null;
    const seconds = value * BAN_UNIT_SECONDS[customUnit];
    if (seconds < 1 || seconds > MAX_BAN_SECONDS) return null;
    return seconds;
  };

  const durationLabel = (): string => {
    if (mode === 'permanent') return t('admin.users.banPermanent');
    if (mode !== 'custom') {
      return t('admin.users.banPresetLabel', { days: Number(mode) });
    }
    return t('admin.users.banCustomLabel', {
      value: customValue.trim(),
      unit: t(`admin.users.banUnit.${customUnit}`),
    });
  };

  const openBanConfirm = () => {
    setValidationError('');
    if (durationSeconds() === null) {
      setValidationError(t('admin.users.banInvalidDuration'));
      return;
    }
    setConfirming('ban');
  };

  const applyBan = () => {
    const seconds = durationSeconds();
    if (seconds === null) {
      setConfirming(null);
      setValidationError(t('admin.users.banInvalidDuration'));
      return;
    }
    ban.mutate(
      {
        userId: user.id,
        reason: optionalText(reason, 512),
        durationSeconds: seconds,
      },
      {
        onSuccess: () => {
          setConfirming(null);
          setReason('');
          setMode('permanent');
          setCustomValue('');
        },
      },
    );
  };

  const applyUnban = () => {
    unban.mutate(user.id, {
      onSuccess: () => setConfirming(null),
    });
  };

  return (
    <div className="limit-form">
      {user.is_banned ? (
        <dl className="detail-grid">
          <div className="detail-row">
            <dt>{t('admin.users.banState')}</dt>
            <dd>
              {user.banned_until !== undefined
                ? t('admin.users.banUntil', {
                    time: formatDateTime(user.banned_until),
                    remaining: remainingText(user.banned_until),
                  })
                : t('admin.users.banPermanent')}
            </dd>
          </div>
          {user.banned_reason ? (
            <div className="detail-row">
              <dt>{t('admin.users.banReasonLabel')}</dt>
              <dd>{user.banned_reason}</dd>
            </div>
          ) : null}
        </dl>
      ) : (
        <p className="muted">{t('admin.users.notBanned')}</p>
      )}
      {!user.is_banned ? (
        <>
          <div className="limit-fields">
            <label>
              <span>{t('admin.users.banDuration')}</span>
              <select
                value={mode}
                onChange={(event) => setMode(event.target.value)}
                aria-label={`${t('admin.users.banDuration')} · ${user.username}`}
              >
                <option value="permanent">{t('admin.users.banPermanent')}</option>
                {BAN_PRESET_DAYS.map((days) => (
                  <option key={days} value={String(days)}>
                    {t('admin.users.banPresetLabel', { days })}
                  </option>
                ))}
                <option value="custom">{t('admin.users.banCustom')}</option>
              </select>
            </label>
            {mode === 'custom' ? (
              <>
                <label>
                  <span>{t('admin.users.banCustomValue')}</span>
                  <input
                    type="number"
                    min="1"
                    step="1"
                    value={customValue}
                    onChange={(event) => setCustomValue(event.target.value)}
                    aria-label={`${t('admin.users.banCustomValue')} · ${user.username}`}
                  />
                </label>
                <label>
                  <span>{t('admin.users.banCustomUnit')}</span>
                  <select
                    value={customUnit}
                    onChange={(event) => setCustomUnit(event.target.value as BanUnit)}
                    aria-label={`${t('admin.users.banCustomUnit')} · ${user.username}`}
                  >
                    <option value="minutes">{t('admin.users.banUnit.minutes')}</option>
                    <option value="hours">{t('admin.users.banUnit.hours')}</option>
                    <option value="days">{t('admin.users.banUnit.days')}</option>
                  </select>
                </label>
              </>
            ) : null}
          </div>
          <label>
            <span>{t('admin.users.banReason')}</span>
            <input
              type="text"
              value={reason}
              onChange={(event) => setReason(event.target.value)}
              placeholder={t('admin.users.banReasonPlaceholder')}
              maxLength={512}
              aria-label={`${t('admin.users.banReason')} · ${user.username}`}
            />
          </label>
          <small className="muted">{t('admin.users.banHint')}</small>
        </>
      ) : null}
      {validationError ? <p className="field-error" role="alert">{validationError}</p> : null}
      {ban.error ? <ErrorState error={ban.error} /> : null}
      {unban.error ? <ErrorState error={unban.error} /> : null}
      <div className="table-actions">
        {!user.is_banned ? (
          <button type="button" className="btn btn-danger" disabled={ban.isPending} onClick={openBanConfirm}>
            {ban.isPending ? t('common.working') : t('admin.users.ban')}
          </button>
        ) : (
          <button
            type="button"
            className="btn btn-secondary"
            disabled={unban.isPending}
            onClick={() => setConfirming('unban')}
          >
            {unban.isPending ? t('common.working') : t('admin.users.unban')}
          </button>
        )}
      </div>
      <ConfirmDialog
        open={confirming === 'ban'}
        title={t('admin.users.banTitle')}
        description={t('admin.users.banSummary', { duration: durationLabel() })}
        confirmLabel={t('admin.users.banConfirm')}
        danger
        busy={ban.isPending}
        onCancel={() => setConfirming(null)}
        onConfirm={applyBan}
      />
      <ConfirmDialog
        open={confirming === 'unban'}
        title={t('admin.users.unbanTitle')}
        description={t('admin.users.unbanBody')}
        confirmLabel={t('admin.users.unbanConfirm')}
        busy={unban.isPending}
        onCancel={() => setConfirming(null)}
        onConfirm={applyUnban}
      />
    </div>
  );
}

// The per-user edit surface: limits, idempotent credit deltas, the level
// tri-state and ban control. Every section talks to the server through its
// own mutually exclusive request mode; nothing is mixed into one call.
function UserManagePanel({ user }: { user: AdminUser }) {
  const { t } = useTranslation();
  return (
    <div className="manage-panel">
      <section className="manage-section" aria-label={`${t('admin.users.limits')} · ${user.username}`}>
        <h3>{t('admin.users.manageLimitsTitle')}</h3>
        <UserLimits user={user} />
      </section>
      <section className="manage-section" aria-label={`${t('admin.users.economyTitle')} · ${user.username}`}>
        <h3>{t('admin.users.economyTitle')}</h3>
        <EconomyAdjustForm user={user} />
      </section>
      <section className="manage-section" aria-label={`${t('admin.users.levelSectionTitle')} · ${user.username}`}>
        <h3>{t('admin.users.levelSectionTitle')}</h3>
        <LevelControl user={user} />
      </section>
      <section className="manage-section" aria-label={`${t('admin.users.banSectionTitle')} · ${user.username}`}>
        <h3>{t('admin.users.banSectionTitle')}</h3>
        <BanControl user={user} />
      </section>
    </div>
  );
}

export function UsersPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState<number>(PAGE_SIZE_OPTIONS[1]);
  const [draftBanned, setDraftBanned] = useState<'all' | 'normal' | 'banned'>('all');
  const [bannedFilter, setBannedFilter] = useState<boolean | undefined>(undefined);
  const [draftQ, setDraftQ] = useState('');
  const [appliedQ, setAppliedQ] = useState('');
  const users = useAdminUsers(page, pageSize, bannedFilter, appliedQ || undefined);
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<AdminUser | null>(null);
  const [deletePassword, setDeletePassword] = useState('');
  const [deleteError, setDeleteError] = useState<unknown>(null);
  const [deleteBusy, setDeleteBusy] = useState(false);

  const confirmDelete = async () => {
    if (!deleteTarget) return;
    const password = deletePassword;
    if (!password) {
      setDeleteError(new Error(t('admin.users.deletePasswordRequired')));
      return;
    }
    setDeletePassword('');
    setDeleteError(null);
    setDeleteBusy(true);
    try {
      const elevation = await apiFetch<unknown>('/admin/api/auth/elevate', {
        method: 'POST',
        json: { password },
      });
      const token = elevatedToken(elevation);
      await apiFetch<void>(`/admin/api/users/${encodeURIComponent(deleteTarget.id)}`, {
        method: 'DELETE',
        headers: token ? { 'X-Elevated-Token': token } : undefined,
      });
      await queryClient.invalidateQueries({ queryKey: adminKeys.usersRoot });
      closeDeleteDialog();
    } catch (error) {
      setDeleteError(error);
    } finally {
      setDeleteBusy(false);
    }
  };

  function closeDeleteDialog() {
    setDeleteTarget(null);
    setDeletePassword('');
    setDeleteError(null);
  }

  return (
    <div className="page">
      <PageHeader
        eyebrow={t('app.name')}
        title={t('admin.users.title')}
        description={t('admin.users.description')}
      />
      <Card>
        <div className="card-title-row">
          <h2>{t('admin.users.listTitle')}</h2>
          <span className="muted">{t('common.page', { page })}</span>
        </div>
        <form
          className="filter-bar"
          onSubmit={(event) => {
            event.preventDefault();
            setBannedFilter(
              draftBanned === 'all' ? undefined : draftBanned === 'banned',
            );
            setAppliedQ(draftQ.trim());
            setPage(1);
          }}
        >
          <label>
            <span>{t('common.search')}</span>
            <input
              type="search"
              value={draftQ}
              maxLength={128}
              onChange={(event) => setDraftQ(event.target.value)}
              placeholder={t('admin.users.searchPlaceholder')}
              aria-label={t('admin.users.searchAria')}
            />
          </label>
          <label>
            <span>{t('admin.users.filterStatus')}</span>
            <select
              value={draftBanned}
              onChange={(event) => setDraftBanned(event.target.value as 'all' | 'normal' | 'banned')}
              aria-label={t('common.filterBannedAria')}
            >
              <option value="all">{t('common.all')}</option>
              <option value="normal">{t('common.normalStatus')}</option>
              <option value="banned">{t('common.banned')}</option>
            </select>
          </label>
          <div className="filter-actions">
            <button type="submit" className="btn btn-quiet">
              {t('common.applyFilter')}
            </button>
            <button
              type="button"
              className="btn btn-link"
              onClick={() => {
                setDraftBanned('all');
                setBannedFilter(undefined);
                setDraftQ('');
                setAppliedQ('');
                setPage(1);
              }}
            >
              {t('common.resetFilter')}
            </button>
          </div>
        </form>
        {users.isPending ? <LoadingState /> : users.error ? <ErrorState error={users.error} onRetry={() => void users.refetch()} /> : users.data.items.length === 0 ? (
          <EmptyState title={t('admin.users.empty')} body={t('admin.users.emptyBody')} />
        ) : (
          <>
            <div className="table-wrap">
              <table>
                <caption>{t('admin.users.listTitle')}</caption>
                <thead>
                  <tr>
                    <th scope="col">{t('admin.users.username')}</th>
                    <th scope="col">{t('admin.users.siteId')}</th>
                    <th scope="col">{t('admin.users.discordId')}</th>
                    <th scope="col">{t('admin.users.status')}</th>
                    <th scope="col">{t('admin.users.level')}</th>
                    <th scope="col">{t('admin.users.balances')}</th>
                    <th scope="col">{t('admin.users.usage')}</th>
                    <th scope="col">{t('admin.users.created')}</th>
                    <th scope="col">{t('admin.users.actions')}</th>
                  </tr>
                </thead>
                <tbody>
                  {users.data.items.map((user) => (
                    <Fragment key={user.id}>
                      <tr>
                        <td>
                          <strong>{user.username}</strong>
                          {user.banned_reason ? <p className="table-note">{user.banned_reason}</p> : null}
                        </td>
                        <td><ReadOnlyValue value={user.id} /></td>
                        <td><ReadOnlyValue value={user.discord_id} /></td>
                        <td>
                          <StatusBadge
                            active={!user.is_banned}
                            label={user.is_banned ? t('admin.users.banned') : t('admin.users.active')}
                          />
                          {user.is_banned && user.banned_until !== undefined ? (
                            <p className="table-note">
                              {t('admin.users.banUntilShort', {
                                time: formatDateTime(user.banned_until),
                              })}
                            </p>
                          ) : null}
                        </td>
                        <td>
                          {user.level !== undefined ? (
                            <span>
                              Lv{user.level} <span className="tag">{t('admin.users.levelManualTag')}</span>
                            </span>
                          ) : (
                            <span>
                              Lv{user.auto_level} <span className="tag">{t('admin.users.levelAutoTag')}</span>
                            </span>
                          )}
                        </td>
                        <td>
                          <div className="table-note">
                            <BalanceCell
                              label={t('admin.users.creditsBalance')}
                              milli={user.credits_balance}
                            />
                          </div>
                          <div className="table-note">
                            <BalanceCell
                              label={t('admin.users.donationBalance')}
                              milli={user.donation_credit_balance}
                            />
                          </div>
                        </td>
                        <td>
                          <span className="table-note">
                            {formatCount(user.total_requests).display} {t('admin.users.requests')}
                          </span>
                          <span className="table-note">
                            {formatCount(user.total_unknown_usage_requests).display}{' '}
                            {t('admin.users.unknown')}
                          </span>
                        </td>
                        <td><ReadOnlyValue value={formatDateTime(user.created_at)} /></td>
                        <td>
                          <div className="table-actions">
                            <button
                              type="button"
                              className="btn btn-quiet"
                              aria-expanded={expandedId === user.id}
                              onClick={() => setExpandedId(expandedId === user.id ? null : user.id)}
                            >
                              {expandedId === user.id
                                ? t('admin.users.manageHide')
                                : t('admin.users.manage')}
                            </button>
                            <button
                              type="button"
                              className="btn btn-danger"
                              onClick={() => setDeleteTarget(user)}
                            >
                              {t('admin.users.delete')}
                            </button>
                          </div>
                        </td>
                      </tr>
                      {expandedId === user.id ? (
                        <tr className="manage-row">
                          <td colSpan={9}>
                            <UserManagePanel user={user} />
                          </td>
                        </tr>
                      ) : null}
                    </Fragment>
                  ))}
                </tbody>
              </table>
            </div>
            <Pagination
              page={page}
              hasNext={users.data.hasNext}
              onChange={setPage}
              pageSize={pageSize}
              pageSizeOptions={PAGE_SIZE_OPTIONS}
              onPageSizeChange={(size) => {
                setPageSize(size);
                setPage(1);
              }}
              onJumpToPage={setPage}
            />
          </>
        )}
      </Card>

      <ConfirmDialog
        open={Boolean(deleteTarget)}
        title={t('admin.users.deleteTitle')}
        description={t('admin.users.deleteBody')}
        confirmLabel={t('admin.users.deleteConfirm')}
        danger
        busy={deleteBusy}
        onCancel={closeDeleteDialog}
        onConfirm={() => void confirmDelete()}
      >
        <label>
          <span>{t('admin.users.elevatedPassword')}</span>
          <input
            type="password"
            value={deletePassword}
            onChange={(event) => setDeletePassword(event.target.value)}
            autoComplete="current-password"
            maxLength={512}
            aria-describedby="admin-delete-password-hint"
          />
          <small id="admin-delete-password-hint" className="muted">
            {t('admin.users.elevatedPasswordHint')}
          </small>
        </label>
        {deleteError ? <ErrorState error={deleteError} /> : null}
      </ConfirmDialog>
    </div>
  );
}
