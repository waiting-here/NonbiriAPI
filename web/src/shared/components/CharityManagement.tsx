import { useCallback, useEffect, useId, useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { Card, EmptyState, ErrorState, LoadingState, StatusBadge } from '@shared/components/States';
import { CursorPagination } from '@shared/operations/CursorPagination';
import { useCursorPager } from '@shared/operations/useCursorPager';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { formatDateTime } from '@shared/utils/datetime';
import {
  addManagedBindings,
  charityKeys,
  createManagedCharityModel,
  deleteManagedBinding,
  deleteManagedCharityModel,
  getManagedBindingCandidates,
  getManagedBindings,
  getManagedCharityModels,
  getManagedDonation,
  getManagedDonations,
  orderManagedBindings,
  patchManagedCharityModel,
  patchManagedDonationKey,
  reviewManagedDonation,
  type AdminDonation,
  type CharityModel,
  type CharityRole,
  type CharityState,
  type DonationStatus,
  type ManagedDonationKey,
  type StewardDonation,
  type TokenPrices,
} from '@shared/operations/charity';
import { useRetainedOperation } from '../../admin/features/operations/useRetainedOperation';
import '@shared/operations/operations.css';

type ManagedDonation = AdminDonation | StewardDonation;

const charityCopyKey = (role: CharityRole, key: string) =>
  `${role === 'admin' ? 'admin.charity' : 'user.steward'}.${key}`;
const charityStatusKey = (role: CharityRole, status: DonationStatus) =>
  charityCopyKey(role, `status.${status}`);
const charityStateKey = (state: CharityState) => `common.operations.charity.charityState.${state}`;
const reviewerRoleKey = (role: 'admin' | 'steward') => `common.operations.charity.role.${role}`;
const sourceTypeKey = (source: 'automatic' | 'manual') =>
  `common.operations.charity.sourceType.${source}`;
const tokenPriceKeyPart: Record<keyof TokenPrices, string> = {
  uncached_input: 'uncached',
  cache_write_input: 'cache_write',
  cache_read_input: 'cache_read',
  output: 'output',
};
const tokenPriceCopyKey = (
  role: CharityRole,
  side: 'userPrices' | 'donorRewards',
  field: keyof TokenPrices,
) =>
  charityCopyKey(
    role,
    `${tokenPriceKeyPart[field]}_${side === 'userPrices' ? 'user_price' : 'donor_reward'}_milli`,
  );

const MAX_MONEY_MILLI = 9_000_000_000_000_000n;
const MAX_TOKEN_RESERVE = 2_147_483_647;
const MAX_UNIX_SECOND = 253_402_300_799;
const CANONICAL_DECIMAL = /^(0|[1-9][0-9]*)$/;
const CANONICAL_AMOUNT = /^(0|[1-9][0-9]*)(?:\.([0-9]{0,2}[1-9]))?$/;

function hasForbiddenControl(value: string): boolean {
  return Array.from(value).some((character) => {
    const point = character.codePointAt(0) ?? 0;
    return point < 0x20 || (point >= 0x7f && point <= 0x9f);
  });
}

function validText(value: string, maximum: number, required = false): boolean {
  return (
    (!required || value.trim().length > 0) &&
    Array.from(value).length <= maximum &&
    !hasForbiddenControl(value)
  );
}

function validCount(value: string | null): boolean {
  if (value === null) return true;
  if (!CANONICAL_DECIMAL.test(value)) return false;
  try {
    return BigInt(value) <= MAX_MONEY_MILLI;
  } catch {
    return false;
  }
}

function validAmount(value: string | null): boolean {
  if (value === null) return true;
  const match = CANONICAL_AMOUNT.exec(value);
  if (!match) return false;
  try {
    const whole = BigInt(match[1]);
    const fraction = BigInt((match[2] ?? '').padEnd(3, '0') || '0');
    return whole * 1_000n + fraction <= MAX_MONEY_MILLI;
  } catch {
    return false;
  }
}

function validTokenReserve(value: number): boolean {
  return Number.isSafeInteger(value) && value >= 0 && value <= MAX_TOKEN_RESERVE;
}

function NullableValue({
  label,
  value,
  onChange,
}: {
  label: string;
  value: string | null;
  onChange: (value: string | null) => void;
}) {
  const { t } = useTranslation();
  const valueId = useId();
  const unlimitedId = useId();
  const [unlimited, setUnlimited] = useState(value === null);
  return (
    <div className="ops-form-field">
      <label htmlFor={valueId}>{label}</label>
      <input
        id={valueId}
        value={value ?? ''}
        disabled={unlimited}
        onChange={(event) => onChange(event.target.value)}
      />
      <label className="checkbox-label" htmlFor={unlimitedId}>
        <input
          id={unlimitedId}
          type="checkbox"
          checked={unlimited}
          onChange={(event) => {
            setUnlimited(event.target.checked);
            onChange(event.target.checked ? null : '0');
          }}
        />
        <span>{t('common.operations.charity.noLimit')}</span>
      </label>
    </div>
  );
}

interface KeySettingsDraft {
  price_limit: string | null;
  calls_limit: string | null;
  tokens_limit: string | null;
  token_reserve: number;
  enabled: boolean;
  safe_note: string;
}
interface KeyManagementDraft extends Omit<KeySettingsDraft, 'enabled'> {
  enabled: boolean | null;
}

const keySettingsDraft = (key: ManagedDonationKey): KeySettingsDraft => ({
  price_limit: key.limits.price,
  calls_limit: key.limits.calls,
  tokens_limit: key.limits.tokens,
  token_reserve: key.token_reserve,
  enabled:
    key.charity_state !== 'disabled' &&
    key.charity_state !== 'ended' &&
    key.charity_state !== 'expired',
  safe_note: key.safe_note,
});

const keyManagementDraft = (key: ManagedDonationKey): KeyManagementDraft => ({
  ...keySettingsDraft(key),
  enabled: null,
});

type KeySettingsValidation = 'priceLimit' | 'countLimits' | 'tokenReserve' | 'safeNote';

function keySettingsError(value: Omit<KeySettingsDraft, 'enabled'>): KeySettingsValidation | null {
  if (!validAmount(value.price_limit)) {
    return 'priceLimit';
  }
  if (!validCount(value.calls_limit) || !validCount(value.tokens_limit)) {
    return 'countLimits';
  }
  if (!validTokenReserve(value.token_reserve)) {
    return 'tokenReserve';
  }
  if (!validText(value.safe_note, 256)) {
    return 'safeNote';
  }
  return null;
}

function DonationKeyEditor({
  item,
  donation,
  role,
  refresh,
  onCapabilityLoss,
}: {
  item: ManagedDonationKey;
  donation: ManagedDonation;
  role: CharityRole;
  refresh: () => Promise<unknown>;
  onCapabilityLoss?: () => void;
}) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState(() => keyManagementDraft(item));
  const [reset, setReset] = useState(false);
  const save = useRetainedOperation<
    KeyManagementDraft & { reset_failure_streak: boolean },
    ManagedDonation
  >(
    (input: KeyManagementDraft & { reset_failure_streak: boolean }, key) =>
      patchManagedDonationKey(
        role,
        donation.id,
        item.id,
        {
          expected_revision: donation.revision,
          ...(input.enabled === null ? {} : { enabled: input.enabled }),
          price_limit: input.price_limit,
          calls_limit: input.calls_limit,
          tokens_limit: input.tokens_limit,
          token_reserve: input.token_reserve,
          safe_note: input.safe_note,
          ...(input.reset_failure_streak ? { reset_failure_streak: true } : {}),
        },
        key,
      ),
    refresh,
    charityKeys.root(role),
  );
  const validationError = keySettingsError(draft);
  const capabilityLost = isUnauthorized(save.error) || isForbidden(save.error);
  useEffect(() => {
    if (capabilityLost) onCapabilityLoss?.();
  }, [capabilityLost, onCapabilityLoss]);
  if (capabilityLost) {
    return (
      <p className="field-error" role="alert">
        {t('common.operations.charity.accessLost')}
      </p>
    );
  }
  return (
    <section className="ops-subcard">
      <h4>
        {t('common.operations.charity.keyHeading', {
          id: item.id,
          head: item.display_head,
          tail: item.display_tail,
        })}
      </h4>
      <p>
        {item.safe_source.connector_type} · {item.safe_source.base_url}
      </p>
      <div className="ops-toolbar">
        <StatusBadge
          active={item.charity_state === 'available'}
          danger={item.streak.failure_disabled}
          label={t(charityStateKey(item.charity_state))}
        />
        <span>
          {t('common.operations.charity.physicalState', {
            state: t(item.physical_enabled ? 'common.enabled' : 'common.disabled'),
          })}
        </span>
        <span>{t('common.operations.charity.failureStreak', { count: item.streak.count })}</span>
      </div>
      <dl className="ops-kv">
        <dt>{t('common.operations.charity.priceCredits')}</dt>
        <dd>
          {t('common.operations.charity.usageLimit', {
            used: item.usage.price_used,
            inflight: item.usage.price_inflight,
            limit: item.limits.price ?? t('common.operations.charity.unlimited'),
          })}
        </dd>
        <dt>{t('common.operations.charity.calls')}</dt>
        <dd>
          {t('common.operations.charity.usageLimit', {
            used: item.usage.calls_used,
            inflight: item.usage.calls_inflight,
            limit: item.limits.calls ?? t('common.operations.charity.unlimited'),
          })}
        </dd>
        <dt>{t('common.operations.charity.tokens')}</dt>
        <dd>
          {t('common.operations.charity.usageLimit', {
            used: item.usage.tokens_used,
            inflight: item.usage.tokens_inflight,
            limit: item.limits.tokens ?? t('common.operations.charity.unlimited'),
          })}
        </dd>
        {item.ended_reason ? (
          <>
            <dt>{t('common.operations.charity.endedReason')}</dt>
            <dd>{item.ended_reason}</dd>
          </>
        ) : null}
      </dl>
      {donation.status === 'approved' ? (
        <>
          <div className="ops-field-grid">
            <NullableValue
              label={t('common.operations.charity.priceLimitCredits')}
              value={draft.price_limit}
              onChange={(value) => setDraft({ ...draft, price_limit: value })}
            />
            <NullableValue
              label={t('common.operations.charity.callLimit')}
              value={draft.calls_limit}
              onChange={(value) => setDraft({ ...draft, calls_limit: value })}
            />
            <NullableValue
              label={t('common.operations.charity.tokenLimit')}
              value={draft.tokens_limit}
              onChange={(value) => setDraft({ ...draft, tokens_limit: value })}
            />
            <label>
              <span>{t('common.operations.charity.tokenReserve')}</span>
              <input
                type="number"
                min="0"
                max={MAX_TOKEN_RESERVE}
                step="1"
                value={draft.token_reserve}
                onChange={(event) =>
                  setDraft({ ...draft, token_reserve: Number(event.target.value) })
                }
              />
            </label>
            <label>
              <span>{t('common.operations.charity.safeNote')}</span>
              <input
                value={draft.safe_note}
                onChange={(event) => setDraft({ ...draft, safe_note: event.target.value })}
              />
            </label>
            <label>
              <span>{t('common.operations.charity.charitySwitchChange')}</span>
              <select
                value={draft.enabled === null ? '' : String(draft.enabled)}
                onChange={(event) =>
                  setDraft({
                    ...draft,
                    enabled: event.target.value === '' ? null : event.target.value === 'true',
                  })
                }
              >
                <option value="">{t('common.operations.charity.leaveUnchanged')}</option>
                <option value="true">{t('common.operations.charity.enableForCharity')}</option>
                <option value="false">{t('common.operations.charity.disableForCharity')}</option>
              </select>
            </label>
            {item.streak.failure_disabled ? (
              <label className="checkbox-label">
                <input
                  type="checkbox"
                  checked={reset}
                  onChange={(event) => setReset(event.target.checked)}
                />
                <span>{t('common.operations.charity.resetFailureStreak')}</span>
              </label>
            ) : null}
          </div>
          {validationError ? (
            <p className="field-error" role="alert">
              {t(`common.operations.charity.validation.${validationError}`)}
            </p>
          ) : null}
          {save.error ? <ErrorState error={save.error} /> : null}
          <button
            className="btn btn-secondary"
            type="button"
            disabled={save.isPending || Boolean(validationError)}
            onClick={() => save.mutate({ ...draft, reset_failure_streak: reset })}
          >
            {t(charityCopyKey(role, 'saveKeyLimits'))}
          </button>
        </>
      ) : (
        <p className="muted">{t('common.operations.charity.keyEditableAfterApproval')}</p>
      )}
    </section>
  );
}

function DonationDetail({
  item,
  role,
  refresh,
  onCapabilityLoss,
}: {
  item: ManagedDonation;
  role: CharityRole;
  refresh: () => Promise<unknown>;
  onCapabilityLoss?: () => void;
}) {
  const { t } = useTranslation();
  const [decision, setDecision] = useState<'approve' | 'reject'>('approve');
  const [reason, setReason] = useState('');
  const [expires, setExpires] = useState(() => dateTimeDraft(item.expires_at).value);
  const [noExpiry, setNoExpiry] = useState(item.expires_at === null);
  const [confirmed, setConfirmed] = useState(false);
  const [keys, setKeys] = useState<Record<string, KeySettingsDraft>>(() =>
    Object.fromEntries(item.keys.map((key) => [key.id, keySettingsDraft(key)])),
  );
  const review = useRetainedOperation<
    {
      decision: 'approve' | 'reject';
      reason: string;
      expires_at: number | null;
      settings: Record<string, KeySettingsDraft>;
    },
    ManagedDonation
  >(
    (
      input: {
        decision: 'approve' | 'reject';
        reason: string;
        expires_at: number | null;
        settings: Record<string, KeySettingsDraft>;
      },
      key,
    ) =>
      reviewManagedDonation(
        role,
        item.id,
        input.decision === 'reject'
          ? { decision: 'reject', expected_revision: item.revision, reason: input.reason }
          : {
              decision: 'approve',
              expected_revision: item.revision,
              reason: input.reason,
              expires_at: input.expires_at,
              key_settings: item.keys.map((entry) => ({
                donation_key_id: entry.id,
                ...input.settings[entry.id],
              })),
            },
        key,
      ),
    refresh,
    charityKeys.root(role),
  );
  const owner = item.owner ?? {
    user_id: t('common.operations.charity.deidentified'),
    display_name: t('common.operations.charity.deidentified'),
  };
  const expiry = expires ? Math.floor(Date.parse(expires) / 1_000) : NaN;
  let validationError:
    KeySettingsValidation | 'reviewReason' | 'expiry' | 'completeSettings' | null = null;
  if (!validText(reason.trim(), 1_024, true)) {
    validationError = 'reviewReason';
  } else if (decision === 'approve') {
    if (!noExpiry && (!Number.isSafeInteger(expiry) || expiry < 0 || expiry > MAX_UNIX_SECOND)) {
      validationError = 'expiry';
    } else if (item.keys.length === 0 || item.keys.some((entry) => !keys[entry.id])) {
      validationError = 'completeSettings';
    } else {
      validationError =
        item.keys
          .map((entry) => keySettingsError(keys[entry.id]))
          .find((error): error is KeySettingsValidation => error !== null) ?? null;
    }
  }
  const capabilityLost = isUnauthorized(review.error) || isForbidden(review.error);
  useEffect(() => {
    if (capabilityLost) onCapabilityLoss?.();
  }, [capabilityLost, onCapabilityLoss]);
  if (capabilityLost) {
    return (
      <Card>
        <p className="field-error" role="alert">
          {t('common.operations.charity.accessLost')}
        </p>
      </Card>
    );
  }
  return (
    <div className="ops-stack">
      <Card>
        <h3>{t(charityCopyKey(role, 'donationNumber'), { id: item.id })}</h3>
        <div className="ops-toolbar">
          <StatusBadge
            active={item.status === 'approved'}
            danger={
              item.status === 'rejected' || item.status === 'deleted' || item.status === 'expired'
            }
            label={t(charityStatusKey(role, item.status))}
          />
          <span>{t('common.operations.charity.revision', { revision: item.revision })}</span>
        </div>
        <dl className="ops-kv">
          <dt>{t('common.operations.charity.owner')}</dt>
          <dd>
            {owner.display_name} · {owner.user_id}
            {role === 'admin' && 'discord_id' in owner
              ? ` · ${owner.discord_id ?? t('common.operations.charity.discordDetached')}`
              : ''}
          </dd>
          <dt>{t('common.operations.charity.donorDescription')}</dt>
          <dd>{item.description || t('common.operations.charity.noDescription')}</dd>
          <dt>{t('common.operations.charity.createdUpdated')}</dt>
          <dd>
            {formatDateTime(item.created_at)} / {formatDateTime(item.updated_at)}
          </dd>
          {item.expires_at !== null ? (
            <>
              <dt>{t(charityCopyKey(role, 'expires'))}</dt>
              <dd>{formatDateTime(item.expires_at)}</dd>
            </>
          ) : null}
          {item.review_result ? (
            <>
              <dt>{t('common.operations.charity.review')}</dt>
              <dd>
                {t(`common.operations.charity.decision.${item.review_result.decision}`)} ·{' '}
                {item.review_result.reason} · {formatDateTime(item.review_result.reviewed_at)}
              </dd>
            </>
          ) : null}
          <dt>{t('common.operations.charity.reviewer')}</dt>
          <dd>
            {item.reviewer
              ? `${t(reviewerRoleKey(item.reviewer.role))} · ${item.reviewer.user_id ?? t('common.operations.charity.deidentified')}`
              : t('common.operations.charity.notReviewed')}
          </dd>
        </dl>
      </Card>
      {item.status === 'pending' ? (
        <Card>
          <h3>{t('common.operations.charity.pendingReviewTitle')}</h3>
          <p>{t('common.operations.charity.pendingReviewBody')}</p>
          <div className="ops-field-grid">
            <label>
              <span>{t('common.operations.charity.decisionLabel')}</span>
              <select
                value={decision}
                onChange={(event) => {
                  setDecision(event.target.value as typeof decision);
                  setConfirmed(false);
                }}
              >
                <option value="approve">{t('common.operations.charity.decision.approve')}</option>
                <option value="reject">{t('common.operations.charity.decision.reject')}</option>
              </select>
            </label>
            <label>
              <span>{t('common.operations.charity.reason')}</span>
              <input value={reason} onChange={(event) => setReason(event.target.value)} />
            </label>
            {decision === 'approve' ? (
              <>
                <label>
                  <span>{t('common.operations.charity.wholeDonationExpiry')}</span>
                  <input
                    type="datetime-local"
                    step="1"
                    value={expires}
                    disabled={noExpiry}
                    onChange={(event) => setExpires(event.target.value)}
                  />
                </label>
                <label className="checkbox-label">
                  <input
                    type="checkbox"
                    checked={noExpiry}
                    onChange={(event) => setNoExpiry(event.target.checked)}
                  />
                  <span>{t('common.operations.charity.noExpiry')}</span>
                </label>
              </>
            ) : null}
          </div>
          {decision === 'approve' ? (
            <div className="ops-stack">
              {item.keys.map((entry) => {
                const draft = keys[entry.id];
                return (
                  <section key={entry.id} className="ops-subcard">
                    <h4>
                      {entry.display_head}…{entry.display_tail} · {entry.safe_source.base_url}
                    </h4>
                    <div className="ops-field-grid">
                      <NullableValue
                        label={t('common.operations.charity.priceLimitCredits')}
                        value={draft.price_limit}
                        onChange={(value) =>
                          setKeys({ ...keys, [entry.id]: { ...draft, price_limit: value } })
                        }
                      />
                      <NullableValue
                        label={t('common.operations.charity.callLimit')}
                        value={draft.calls_limit}
                        onChange={(value) =>
                          setKeys({ ...keys, [entry.id]: { ...draft, calls_limit: value } })
                        }
                      />
                      <NullableValue
                        label={t('common.operations.charity.tokenLimit')}
                        value={draft.tokens_limit}
                        onChange={(value) =>
                          setKeys({ ...keys, [entry.id]: { ...draft, tokens_limit: value } })
                        }
                      />
                      <label>
                        <span>{t('common.operations.charity.tokenReserve')}</span>
                        <input
                          type="number"
                          min="0"
                          max={MAX_TOKEN_RESERVE}
                          step="1"
                          value={draft.token_reserve}
                          onChange={(event) =>
                            setKeys({
                              ...keys,
                              [entry.id]: { ...draft, token_reserve: Number(event.target.value) },
                            })
                          }
                        />
                      </label>
                      <label>
                        <span>{t('common.operations.charity.safeNote')}</span>
                        <input
                          value={draft.safe_note}
                          onChange={(event) =>
                            setKeys({
                              ...keys,
                              [entry.id]: { ...draft, safe_note: event.target.value },
                            })
                          }
                        />
                      </label>
                      <label className="checkbox-label">
                        <input
                          type="checkbox"
                          checked={draft.enabled}
                          onChange={(event) =>
                            setKeys({
                              ...keys,
                              [entry.id]: { ...draft, enabled: event.target.checked },
                            })
                          }
                        />
                        <span>{t('common.operations.charity.enableForCharity')}</span>
                      </label>
                    </div>
                  </section>
                );
              })}
            </div>
          ) : null}
          <label className="checkbox-label">
            <input
              type="checkbox"
              checked={confirmed}
              onChange={(event) => setConfirmed(event.target.checked)}
            />
            <span>{t('common.operations.charity.confirmReview')}</span>
          </label>
          {validationError && (reason.length > 0 || confirmed) ? (
            <p className="field-error" role="alert">
              {t(`common.operations.charity.validation.${validationError}`)}
            </p>
          ) : null}
          {review.error ? <ErrorState error={review.error} /> : null}
          <button
            className={decision === 'reject' ? 'btn btn-danger' : 'btn btn-primary'}
            type="button"
            disabled={!reason.trim() || !confirmed || review.isPending || Boolean(validationError)}
            onClick={() =>
              review.mutate({
                decision,
                reason: reason.trim(),
                expires_at: decision === 'approve' && !noExpiry ? expiry : null,
                settings: keys,
              })
            }
          >
            {t(charityCopyKey(role, decision === 'approve' ? 'approve' : 'reject'))}
          </button>
        </Card>
      ) : null}
      <Card>
        <h3>{t('common.operations.charity.donationKeys')}</h3>
        <div className="ops-stack">
          {item.keys.map((key) => (
            <DonationKeyEditor
              key={`${key.id}:${item.revision}`}
              item={key}
              donation={item}
              role={role}
              refresh={refresh}
              onCapabilityLoss={onCapabilityLoss}
            />
          ))}
        </div>
      </Card>
    </div>
  );
}

function DonationsPanel({
  role,
  onCapabilityLoss,
}: {
  role: CharityRole;
  onCapabilityLoss?: () => void;
}) {
  const { t } = useTranslation();
  const pager = useCursorPager();
  const [status, setStatus] = useState('');
  const [selected, setSelected] = useState('');
  const list = useQuery({
    queryKey: charityKeys.donations(role, status, pager.cursor),
    queryFn: () => getManagedDonations(role, status, pager.cursor),
    retry: false,
  });
  const detail = useQuery({
    queryKey: charityKeys.donation(role, selected),
    queryFn: () => getManagedDonation(role, selected),
    enabled: Boolean(selected),
    retry: false,
  });
  const capabilityLost =
    isUnauthorized(list.error) ||
    isForbidden(list.error) ||
    isUnauthorized(detail.error) ||
    isForbidden(detail.error);
  useEffect(() => {
    if (capabilityLost) onCapabilityLoss?.();
  }, [capabilityLost, onCapabilityLoss]);
  const refresh = async () => {
    await Promise.all([list.refetch(), selected ? detail.refetch() : Promise.resolve()]);
  };
  if (capabilityLost) {
    return (
      <Card>
        <p className="field-error" role="alert">
          {t('common.operations.charity.accessLost')}
        </p>
      </Card>
    );
  }
  return (
    <div className="ops-stack">
      <Card>
        <div className="ops-toolbar">
          <label>
            <span>{t(charityCopyKey(role, 'statusFilter'))}</span>
            <select
              value={status}
              onChange={(event) => {
                pager.reset();
                setSelected('');
                setStatus(event.target.value);
              }}
            >
              <option value="">{t(charityCopyKey(role, 'allStatuses'))}</option>
              {['pending', 'approved', 'rejected', 'deleted', 'expired'].map((value) => (
                <option key={value} value={value}>
                  {t(charityStatusKey(role, value as DonationStatus))}
                </option>
              ))}
            </select>
          </label>
        </div>
        {list.isPending ? (
          <LoadingState />
        ) : list.error ? (
          <ErrorState error={list.error} onRetry={() => void list.refetch()} />
        ) : list.data.data.length === 0 ? (
          <EmptyState
            title={t(charityCopyKey(role, 'noDonations'))}
            body={t(charityCopyKey(role, 'noDonationsBody'))}
          />
        ) : (
          <>
            <div className="ops-table-scroll">
              <table className="ops-table">
                <thead>
                  <tr>
                    <th>{t('common.operations.charity.donation')}</th>
                    <th>{t('common.operations.charity.owner')}</th>
                    <th>{t('common.status')}</th>
                    <th>{t('common.operations.charity.keys')}</th>
                    <th>{t('common.operations.charity.open')}</th>
                  </tr>
                </thead>
                <tbody>
                  {list.data.data.map((item) => (
                    <tr key={item.id}>
                      <td>
                        {item.id}
                        <small>{item.description}</small>
                      </td>
                      <td>
                        {item.owner?.display_name ?? t('common.operations.charity.deidentified')}
                      </td>
                      <td>
                        <StatusBadge
                          active={item.status === 'approved'}
                          label={t(charityStatusKey(role, item.status))}
                        />
                      </td>
                      <td>{item.keys.length}</td>
                      <td>
                        <button
                          className="btn btn-secondary"
                          type="button"
                          onClick={() => setSelected(item.id)}
                        >
                          {t('common.operations.charity.openReview')}
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <CursorPagination
              page={pager.page}
              nextCursor={list.data.next_cursor}
              onPrevious={pager.previous}
              onNext={pager.next}
              labels={{
                previous: t(charityCopyKey(role, 'previous')),
                next: t(charityCopyKey(role, 'next')),
                page: t('common.operations.charity.page'),
              }}
            />
          </>
        )}
      </Card>
      {selected ? (
        detail.isPending ? (
          <LoadingState />
        ) : detail.error ? (
          <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />
        ) : (
          <DonationDetail
            key={`${detail.data.id}:${detail.data.revision}`}
            item={detail.data}
            role={role}
            refresh={refresh}
            onCapabilityLoss={onCapabilityLoss}
          />
        )
      ) : null}
    </div>
  );
}

interface DateTimeDraft {
  value: string;
  original: number | null;
  dirty: boolean;
}

interface ModelDraft {
  provider: string;
  model: string;
  enabled: boolean;
  mode: 'per_request' | 'per_token';
  requestUser: string;
  requestDonor: string;
  userPrices: TokenPrices;
  donorRewards: TokenPrices;
  discountEnabled: boolean;
  discountPercent: number;
  discountStart: DateTimeDraft;
  discountEnd: DateTimeDraft;
  flatten: boolean;
}
const zeroPrices = (): TokenPrices => ({
  uncached_input: '0',
  cache_write_input: '0',
  cache_read_input: '0',
  output: '0',
});

const datePart = (value: number) => String(value).padStart(2, '0');
function dateTimeDraft(epoch: number | null | undefined): DateTimeDraft {
  if (epoch === null || epoch === undefined) return { value: '', original: null, dirty: false };
  const date = new Date(epoch * 1_000);
  return {
    value: `${date.getFullYear()}-${datePart(date.getMonth() + 1)}-${datePart(date.getDate())}T${datePart(date.getHours())}:${datePart(date.getMinutes())}:${datePart(date.getSeconds())}`,
    original: epoch,
    dirty: false,
  };
}

function dateTimeEpoch(draft: DateTimeDraft): number | null {
  if (!draft.dirty) return draft.original;
  return draft.value ? Math.floor(Date.parse(draft.value) / 1_000) : null;
}

function modelDraft(model?: CharityModel): ModelDraft {
  return {
    provider: model?.provider ?? '',
    model: model?.model ?? '',
    enabled: model?.enabled ?? true,
    mode: model?.pricing.mode ?? 'per_request',
    requestUser: model?.pricing.mode === 'per_request' ? model.pricing.user_price : '0',
    requestDonor: model?.pricing.mode === 'per_request' ? model.pricing.donor_reward : '0',
    userPrices: model?.pricing.mode === 'per_token' ? model.pricing.user_prices : zeroPrices(),
    donorRewards: model?.pricing.mode === 'per_token' ? model.pricing.donor_rewards : zeroPrices(),
    discountEnabled: model?.discount.enabled ?? false,
    discountPercent: model?.discount.percent ?? 0,
    discountStart: dateTimeDraft(model?.discount.start_at),
    discountEnd: dateTimeDraft(model?.discount.end_at),
    flatten: model?.flatten_tool_calls ?? false,
  };
}
function modelBody(draft: ModelDraft) {
  return {
    provider: draft.provider.trim(),
    model: draft.model.trim(),
    enabled: draft.enabled,
    pricing:
      draft.mode === 'per_request'
        ? { mode: 'per_request', user_price: draft.requestUser, donor_reward: draft.requestDonor }
        : { mode: 'per_token', user_prices: draft.userPrices, donor_rewards: draft.donorRewards },
    discount: {
      enabled: draft.discountEnabled,
      percent: draft.discountPercent,
      start_at: dateTimeEpoch(draft.discountStart),
      end_at: dateTimeEpoch(draft.discountEnd),
    },
    flatten_tool_calls: draft.flatten,
  };
}

type ModelValidation = 'modelIdentity' | 'modelPrices' | 'discountPercent' | 'discountDates';

function modelDraftError(draft: ModelDraft): ModelValidation | null {
  const provider = draft.provider.trim();
  const model = draft.model.trim();
  if (
    !validText(provider, 64, true) ||
    !validText(model, 64, true) ||
    provider.startsWith('[公益]')
  ) {
    return 'modelIdentity';
  }
  const prices =
    draft.mode === 'per_request'
      ? [draft.requestUser, draft.requestDonor]
      : [...Object.values(draft.userPrices), ...Object.values(draft.donorRewards)];
  if (prices.some((value) => !validAmount(value))) {
    return 'modelPrices';
  }
  if (
    !Number.isInteger(draft.discountPercent) ||
    draft.discountPercent < 0 ||
    draft.discountPercent > 100
  ) {
    return 'discountPercent';
  }
  const start = dateTimeEpoch(draft.discountStart);
  const end = dateTimeEpoch(draft.discountEnd);
  if (
    (start !== null && (!Number.isSafeInteger(start) || start < 0 || start > MAX_UNIX_SECOND)) ||
    (end !== null && (!Number.isSafeInteger(end) || end < 0 || end > MAX_UNIX_SECOND)) ||
    (start !== null && end !== null && end <= start)
  ) {
    return 'discountDates';
  }
  return null;
}

function ModelForm({
  role,
  model,
  refresh,
  onDeleted,
  onCapabilityLoss,
}: {
  role: CharityRole;
  model?: CharityModel;
  refresh: () => Promise<unknown>;
  onDeleted?: () => void;
  onCapabilityLoss?: () => void;
}) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState(() => modelDraft(model));
  const [confirmDelete, setConfirmDelete] = useState(false);
  const save = useRetainedOperation<ModelDraft, CharityModel>(
    (input: ModelDraft, key) =>
      model
        ? patchManagedCharityModel(
            role,
            model.id,
            { expected_revision: model.revision, ...modelBody(input) },
            key,
          )
        : createManagedCharityModel(role, modelBody(input), key),
    refresh,
    charityKeys.root(role),
  );
  const remove = useRetainedOperation<{ id: string; revision: string }, void>(
    (_input: { id: string; revision: string }, key) =>
      deleteManagedCharityModel(role, model!.id, model!.revision, key),
    refresh,
    charityKeys.root(role),
  );
  const validationError = modelDraftError(draft);
  const capabilityLost =
    isUnauthorized(save.error) ||
    isForbidden(save.error) ||
    isUnauthorized(remove.error) ||
    isForbidden(remove.error);
  useEffect(() => {
    if (capabilityLost) onCapabilityLoss?.();
  }, [capabilityLost, onCapabilityLoss]);
  if (capabilityLost) {
    return (
      <Card>
        <p className="field-error" role="alert">
          {t('common.operations.charity.accessLost')}
        </p>
      </Card>
    );
  }
  const setPrice = (side: 'userPrices' | 'donorRewards', field: keyof TokenPrices, value: string) =>
    setDraft({ ...draft, [side]: { ...draft[side], [field]: value } });
  return (
    <Card>
      <h3>{model ? model.full_name : t(charityCopyKey(role, 'newModel'))}</h3>
      {model ? (
        <p>
          {t('common.operations.charity.modelSummary', {
            revision: model.revision,
            bindings: model.binding_count,
            success:
              model.rolling_success.percent === null
                ? t('common.operations.charity.noSample')
                : t(charityCopyKey(role, 'successRate'), {
                    rate: model.rolling_success.percent,
                    count: model.rolling_success.sample_count,
                  }),
          })}
        </p>
      ) : null}
      <div className="ops-field-grid">
        <label>
          <span>{t(charityCopyKey(role, 'provider'))}</span>
          <input
            value={draft.provider}
            onChange={(event) => setDraft({ ...draft, provider: event.target.value })}
          />
        </label>
        <label>
          <span>{t(charityCopyKey(role, 'model'))}</span>
          <input
            value={draft.model}
            onChange={(event) => setDraft({ ...draft, model: event.target.value })}
          />
        </label>
        <label>
          <span>{t(charityCopyKey(role, 'pricingMode'))}</span>
          <select
            value={draft.mode}
            onChange={(event) =>
              setDraft({ ...draft, mode: event.target.value as ModelDraft['mode'] })
            }
          >
            <option value="per_request">{t(charityCopyKey(role, 'perRequest'))}</option>
            <option value="per_token">{t(charityCopyKey(role, 'perToken'))}</option>
          </select>
        </label>
        <label className="checkbox-label">
          <input
            type="checkbox"
            checked={draft.enabled}
            onChange={(event) => setDraft({ ...draft, enabled: event.target.checked })}
          />
          <span>{t('common.operations.charity.enabledForNewClaims')}</span>
        </label>
        <label className="checkbox-label">
          <input
            type="checkbox"
            checked={draft.flatten}
            onChange={(event) => setDraft({ ...draft, flatten: event.target.checked })}
          />
          <span>{t(charityCopyKey(role, 'flattenExperimental'))}</span>
        </label>
      </div>
      {draft.mode === 'per_request' ? (
        <div className="ops-field-grid">
          <label>
            <span>{t(charityCopyKey(role, 'request_user_price_milli'))}</span>
            <input
              value={draft.requestUser}
              onChange={(event) => setDraft({ ...draft, requestUser: event.target.value })}
            />
          </label>
          <label>
            <span>{t(charityCopyKey(role, 'request_donor_reward_milli'))}</span>
            <input
              value={draft.requestDonor}
              onChange={(event) => setDraft({ ...draft, requestDonor: event.target.value })}
            />
          </label>
        </div>
      ) : (
        <div className="ops-grid">
          {(['userPrices', 'donorRewards'] as const).map((side) => (
            <section key={side} className="ops-subcard">
              <h4>
                {t(
                  `common.operations.charity.${side === 'userPrices' ? 'userPrices' : 'donorRewards'}`,
                )}
              </h4>
              {(Object.keys(draft[side]) as (keyof TokenPrices)[]).map((field) => (
                <label key={field}>
                  <span>{t(tokenPriceCopyKey(role, side, field))}</span>
                  <input
                    value={draft[side][field]}
                    onChange={(event) => setPrice(side, field, event.target.value)}
                  />
                </label>
              ))}
            </section>
          ))}
        </div>
      )}
      <div className="ops-field-grid">
        <label className="checkbox-label">
          <input
            type="checkbox"
            checked={draft.discountEnabled}
            onChange={(event) => setDraft({ ...draft, discountEnabled: event.target.checked })}
          />
          <span>{t(charityCopyKey(role, 'discountEnabled'))}</span>
        </label>
        <label>
          <span>{t(charityCopyKey(role, 'discountPercent'))}</span>
          <input
            type="number"
            min="0"
            max="100"
            value={draft.discountPercent}
            onChange={(event) =>
              setDraft({ ...draft, discountPercent: Number(event.target.value) })
            }
          />
        </label>
        <label>
          <span>{t(charityCopyKey(role, 'discountStart'))}</span>
          <input
            type="datetime-local"
            step="1"
            value={draft.discountStart.value}
            onChange={(event) =>
              setDraft({
                ...draft,
                discountStart: { ...draft.discountStart, value: event.target.value, dirty: true },
              })
            }
          />
        </label>
        <label>
          <span>{t(charityCopyKey(role, 'discountEnd'))}</span>
          <input
            type="datetime-local"
            step="1"
            value={draft.discountEnd.value}
            onChange={(event) =>
              setDraft({
                ...draft,
                discountEnd: { ...draft.discountEnd, value: event.target.value, dirty: true },
              })
            }
          />
        </label>
      </div>
      {save.error ? (
        <ErrorState error={save.error} />
      ) : remove.error ? (
        <ErrorState error={remove.error} />
      ) : null}
      {validationError ? (
        <p className="field-error" role="alert">
          {t(`common.operations.charity.validation.${validationError}`)}
        </p>
      ) : null}
      <div className="ops-actions">
        <button
          className="btn btn-primary"
          type="button"
          disabled={Boolean(validationError) || save.isPending}
          onClick={() => save.mutate(draft)}
        >
          {model ? t('common.operations.charity.saveModel') : t(charityCopyKey(role, 'newModel'))}
        </button>
        {model ? (
          <button className="btn btn-danger" type="button" onClick={() => setConfirmDelete(true)}>
            {t('common.operations.charity.deleteModel')}
          </button>
        ) : null}
      </div>
      {confirmDelete && model ? (
        <ConfirmDialog
          open
          title={t(charityCopyKey(role, 'deleteModelTitle'))}
          description={t('common.operations.charity.deleteModelBody')}
          confirmLabel={t('common.operations.charity.deleteModelConfirm')}
          danger
          busy={remove.isPending}
          onCancel={() => setConfirmDelete(false)}
          onConfirm={() => {
            setConfirmDelete(false);
            remove.mutate({ id: model.id, revision: model.revision }, { onSuccess: onDeleted });
          }}
        />
      ) : null}
    </Card>
  );
}

function BindingsPanel({
  role,
  model,
  onCapabilityLoss,
}: {
  role: CharityRole;
  model: CharityModel;
  onCapabilityLoss?: () => void;
}) {
  const { t } = useTranslation();
  const pager = useCursorPager();
  const [queryDraft, setQueryDraft] = useState('');
  const [query, setQuery] = useState('');
  const [selected, setSelected] = useState<Record<string, boolean>>({});
  const bindings = useQuery({
    queryKey: charityKeys.bindings(role, model.id),
    queryFn: () => getManagedBindings(role, model.id),
    retry: false,
  });
  const candidates = useQuery({
    queryKey: charityKeys.candidates(role, model.id, query, pager.cursor),
    queryFn: () => getManagedBindingCandidates(role, model.id, query, pager.cursor),
    retry: false,
  });
  const capabilityLost =
    isUnauthorized(bindings.error) ||
    isForbidden(bindings.error) ||
    isUnauthorized(candidates.error) ||
    isForbidden(candidates.error);
  useEffect(() => {
    if (capabilityLost) onCapabilityLoss?.();
  }, [capabilityLost, onCapabilityLoss]);
  const reconcile = async () => {
    await Promise.all([bindings.refetch(), candidates.refetch()]);
  };
  const add = useRetainedOperation(
    (
      input: {
        selections: { donation_key_id: string; upstream_model_id: string }[];
        revision: string;
      },
      key,
    ) => addManagedBindings(role, model.id, input.revision, input.selections, key),
    reconcile,
    charityKeys.root(role),
  );
  const order = useRetainedOperation(
    (input: { ids: string[]; revision: string }, key) =>
      orderManagedBindings(role, model.id, input.revision, input.ids, key),
    reconcile,
    charityKeys.root(role),
  );
  const remove = useRetainedOperation(
    (input: { id: string; revision: string }, key) =>
      deleteManagedBinding(role, model.id, input.id, input.revision, key),
    reconcile,
    charityKeys.root(role),
  );
  const mutationCapabilityLost =
    isUnauthorized(add.error) ||
    isForbidden(add.error) ||
    isUnauthorized(order.error) ||
    isForbidden(order.error) ||
    isUnauthorized(remove.error) ||
    isForbidden(remove.error);
  useEffect(() => {
    if (mutationCapabilityLost) onCapabilityLoss?.();
  }, [mutationCapabilityLost, onCapabilityLoss]);
  const move = (index: number, offset: number) => {
    if (!bindings.data) return;
    const ids = bindings.data.bindings.map((entry) => entry.id);
    [ids[index], ids[index + offset]] = [ids[index + offset], ids[index]];
    order.mutate({ ids, revision: bindings.data.binding_revision });
  };
  const chosen = useMemo(
    () =>
      candidates.data?.data
        .filter((entry) => selected[`${entry.donation_key_id}:${entry.upstream_model_id}`])
        .map((entry) => ({
          donation_key_id: entry.donation_key_id,
          upstream_model_id: entry.upstream_model_id,
        })) ?? [],
    [candidates.data, selected],
  );
  if (capabilityLost || mutationCapabilityLost) {
    return (
      <Card>
        <p className="field-error" role="alert">
          {t('common.operations.charity.accessLost')}
        </p>
      </Card>
    );
  }
  return (
    <Card>
      <h3>{t('common.operations.charity.orderedBindings')}</h3>
      {bindings.isPending ? (
        <LoadingState />
      ) : bindings.error ? (
        <ErrorState error={bindings.error} onRetry={() => void bindings.refetch()} />
      ) : bindings.data.bindings.length === 0 ? (
        <EmptyState
          title={t(charityCopyKey(role, 'noBindings'))}
          body={t('common.operations.charity.noBindingsBody')}
        />
      ) : (
        <div className="ops-table-scroll">
          <table className="ops-table">
            <thead>
              <tr>
                <th>{t(charityCopyKey(role, 'order'))}</th>
                <th>{t('common.operations.charity.source')}</th>
                <th>{t(charityCopyKey(role, 'upstreamModel'))}</th>
                <th>{t(charityCopyKey(role, 'actions'))}</th>
              </tr>
            </thead>
            <tbody>
              {bindings.data.bindings.map((entry, index) => (
                <tr key={entry.id}>
                  <td>{entry.ord}</td>
                  <td>
                    {entry.source.connector_type} · {entry.source.canonical_base_url} ·{' '}
                    {entry.source.display_head}…{entry.source.display_tail}
                  </td>
                  <td>{entry.upstream_model_id}</td>
                  <td>
                    <button
                      className="btn btn-secondary"
                      type="button"
                      disabled={index === 0 || order.isPending}
                      onClick={() => move(index, -1)}
                    >
                      {t('common.operations.charity.moveUp')}
                    </button>
                    <button
                      className="btn btn-secondary"
                      type="button"
                      disabled={index === bindings.data.bindings.length - 1 || order.isPending}
                      onClick={() => move(index, 1)}
                    >
                      {t('common.operations.charity.moveDown')}
                    </button>
                    <button
                      className="btn btn-danger"
                      type="button"
                      disabled={remove.isPending}
                      onClick={() =>
                        remove.mutate({ id: entry.id, revision: bindings.data.binding_revision })
                      }
                    >
                      {t('common.operations.charity.remove')}
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <h3>{t('common.operations.charity.bindingCandidates')}</h3>
      <form
        className="ops-toolbar"
        onSubmit={(event) => {
          event.preventDefault();
          pager.reset();
          setQuery(queryDraft.trim());
        }}
      >
        <label>
          <span>{t('common.operations.charity.safeSourceSearch')}</span>
          <input value={queryDraft} onChange={(event) => setQueryDraft(event.target.value)} />
        </label>
        <button className="btn btn-secondary" type="submit">
          {t('common.search')}
        </button>
      </form>
      {candidates.isPending ? (
        <LoadingState />
      ) : candidates.error ? (
        <ErrorState error={candidates.error} onRetry={() => void candidates.refetch()} />
      ) : candidates.data.data.length === 0 ? (
        <EmptyState
          title={t('common.operations.charity.noCandidates')}
          body={t('common.operations.charity.noCandidatesBody')}
        />
      ) : (
        <>
          {candidates.data.data.map((entry) => {
            const key = `${entry.donation_key_id}:${entry.upstream_model_id}`;
            return (
              <label key={key} className="ops-subcard checkbox-label">
                <input
                  type="checkbox"
                  checked={Boolean(selected[key])}
                  onChange={(event) => setSelected({ ...selected, [key]: event.target.checked })}
                />
                <span>
                  {entry.source.connector_type} · {entry.source.canonical_base_url} ·{' '}
                  {entry.upstream_model_id} (
                  {entry.source_types.map((source) => t(sourceTypeKey(source))).join(' / ')})
                </span>
              </label>
            );
          })}
          <CursorPagination
            page={pager.page}
            nextCursor={candidates.data.next_cursor}
            onPrevious={pager.previous}
            onNext={pager.next}
            labels={{
              previous: t(charityCopyKey(role, 'previous')),
              next: t(charityCopyKey(role, 'next')),
              page: t('common.operations.charity.page'),
            }}
          />
        </>
      )}
      <div className="ops-actions">
        <button
          className="btn btn-primary"
          type="button"
          disabled={!bindings.data || chosen.length === 0 || add.isPending}
          onClick={() =>
            bindings.data &&
            add.mutate(
              { selections: chosen, revision: bindings.data.binding_revision },
              { onSuccess: () => setSelected({}) },
            )
          }
        >
          {t('common.operations.charity.addSelected')}
        </button>
      </div>
      {add.error ? (
        <ErrorState error={add.error} />
      ) : order.error ? (
        <ErrorState error={order.error} />
      ) : remove.error ? (
        <ErrorState error={remove.error} />
      ) : null}
    </Card>
  );
}

function ModelsPanel({
  role,
  onCapabilityLoss,
}: {
  role: CharityRole;
  onCapabilityLoss?: () => void;
}) {
  const { t } = useTranslation();
  const pager = useCursorPager();
  const [queryDraft, setQueryDraft] = useState('');
  const [query, setQuery] = useState('');
  const [enabled, setEnabled] = useState('');
  const [selected, setSelected] = useState<CharityModel | null>(null);
  const models = useQuery({
    queryKey: charityKeys.models(role, query, enabled, pager.cursor),
    queryFn: () => getManagedCharityModels(role, query, enabled, pager.cursor),
    retry: false,
  });
  const capabilityLost = isUnauthorized(models.error) || isForbidden(models.error);
  useEffect(() => {
    if (capabilityLost) onCapabilityLoss?.();
  }, [capabilityLost, onCapabilityLoss]);
  const refresh = async () => {
    const result = await models.refetch();
    if (selected && result.data)
      setSelected(result.data.data.find((item) => item.id === selected.id) ?? null);
  };
  if (capabilityLost) {
    return (
      <Card>
        <p className="field-error" role="alert">
          {t('common.operations.charity.accessLost')}
        </p>
      </Card>
    );
  }
  return (
    <div className="ops-stack">
      <ModelForm role={role} refresh={refresh} onCapabilityLoss={onCapabilityLoss} />
      <Card>
        <form
          className="ops-toolbar"
          onSubmit={(event) => {
            event.preventDefault();
            pager.reset();
            setQuery(queryDraft.trim());
          }}
        >
          <label>
            <span>{t('common.operations.charity.searchModels')}</span>
            <input value={queryDraft} onChange={(event) => setQueryDraft(event.target.value)} />
          </label>
          <label>
            <span>{t(charityCopyKey(role, 'enabled'))}</span>
            <select
              value={enabled}
              onChange={(event) => {
                pager.reset();
                setEnabled(event.target.value);
              }}
            >
              <option value="">{t('common.all')}</option>
              <option value="true">{t(charityCopyKey(role, 'enabled'))}</option>
              <option value="false">{t(charityCopyKey(role, 'disabled'))}</option>
            </select>
          </label>
          <button className="btn btn-secondary" type="submit">
            {t('common.applyFilter')}
          </button>
        </form>
        {models.isPending ? (
          <LoadingState />
        ) : models.error ? (
          <ErrorState error={models.error} onRetry={() => void models.refetch()} />
        ) : models.data.data.length === 0 ? (
          <EmptyState
            title={t(charityCopyKey(role, 'noModels'))}
            body={t(charityCopyKey(role, 'noModelsBody'))}
          />
        ) : (
          <>
            <div className="ops-table-scroll">
              <table className="ops-table">
                <thead>
                  <tr>
                    <th>{t(charityCopyKey(role, 'model'))}</th>
                    <th>{t('common.operations.charity.state')}</th>
                    <th>{t('common.operations.charity.pricing')}</th>
                    <th>{t(charityCopyKey(role, 'bindings'))}</th>
                    <th>{t('common.operations.charity.open')}</th>
                  </tr>
                </thead>
                <tbody>
                  {models.data.data.map((model) => (
                    <tr key={model.id}>
                      <td>
                        {model.full_name}
                        <small>{model.id}</small>
                      </td>
                      <td>
                        <StatusBadge
                          active={model.enabled}
                          label={t(charityCopyKey(role, model.enabled ? 'enabled' : 'disabled'))}
                        />
                      </td>
                      <td>
                        {t(
                          charityCopyKey(
                            role,
                            model.pricing.mode === 'per_request' ? 'perRequest' : 'perToken',
                          ),
                        )}
                      </td>
                      <td>{model.binding_count}</td>
                      <td>
                        <button
                          className="btn btn-secondary"
                          type="button"
                          onClick={() => setSelected(model)}
                        >
                          {t('common.operations.charity.manage')}
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <CursorPagination
              page={pager.page}
              nextCursor={models.data.next_cursor}
              onPrevious={pager.previous}
              onNext={pager.next}
              labels={{
                previous: t(charityCopyKey(role, 'previous')),
                next: t(charityCopyKey(role, 'next')),
                page: t('common.operations.charity.page'),
              }}
            />
          </>
        )}
      </Card>
      {selected && !capabilityLost ? (
        <>
          <ModelForm
            key={`model:${selected.id}:${selected.revision}`}
            role={role}
            model={selected}
            refresh={refresh}
            onDeleted={() => setSelected(null)}
            onCapabilityLoss={onCapabilityLoss}
          />
          <BindingsPanel
            key={`bindings:${selected.id}:${selected.binding_revision}`}
            role={role}
            model={selected}
            onCapabilityLoss={onCapabilityLoss}
          />
        </>
      ) : null}
    </div>
  );
}

export function CharityManagement({
  frame,
  onCapabilityLoss,
}: {
  frame: CharityRole;
  onCapabilityLoss?: () => void;
}) {
  const { t } = useTranslation();
  const [section, setSection] = useState<'donations' | 'models'>('donations');
  const [capabilityLost, setCapabilityLost] = useState(false);
  const clearCapability = useCallback(() => {
    setCapabilityLost(true);
    onCapabilityLoss?.();
  }, [onCapabilityLoss]);
  if (capabilityLost) {
    return (
      <Card>
        <p className="field-error" role="alert">
          {t('common.operations.charity.accessLost')}
        </p>
      </Card>
    );
  }
  return (
    <div className="ops-stack">
      <div
        className="ops-tabs"
        role="tablist"
        aria-label={t('common.operations.charity.sectionsLabel')}
      >
        <button
          className={section === 'donations' ? 'btn btn-primary' : 'btn btn-secondary'}
          type="button"
          role="tab"
          aria-selected={section === 'donations'}
          onClick={() => setSection('donations')}
        >
          {t(charityCopyKey(frame, 'donationsTitle'))}
        </button>
        <button
          className={section === 'models' ? 'btn btn-primary' : 'btn btn-secondary'}
          type="button"
          role="tab"
          aria-selected={section === 'models'}
          onClick={() => setSection('models')}
        >
          {t(charityCopyKey(frame, 'modelsTitle'))}
        </button>
      </div>
      {section === 'donations' ? (
        <DonationsPanel role={frame} onCapabilityLoss={clearCapability} />
      ) : (
        <ModelsPanel role={frame} onCapabilityLoss={clearCapability} />
      )}
    </div>
  );
}
