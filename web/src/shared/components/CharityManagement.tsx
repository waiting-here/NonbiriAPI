import { useCallback, useEffect, useId, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useSearchState } from '@shared/operations/useSearchState';
import { useTranslation } from 'react-i18next';
import { CharityBindingPicker, type CharitySelection } from './CharityBindingPicker';
import { CharitySourceBrowser } from './CharitySourceBrowser';
import { FailureResetControl } from './FailureResetControl';
import { FailurePolicyControl } from './FailurePolicyControl';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { KeyLimitSummary } from './KeyRoutingLimits';
import { DonationHandlingControl, DonationHandlingStatus } from './DonationHandling';
import { donationHandlingStateKey } from './donationHandlingCopy';
import { MarkdownText } from './MarkdownText';
import { Card, EmptyState, ErrorState, LoadingState, StatusBadge } from '@shared/components/States';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { usePagePager } from '@shared/operations/usePagePager';
import { useDetailNavigation } from '@shared/operations/useDetailNavigation';
import { type PageSize } from '@shared/operations/pageNumbers';
import {
  getManagedDonationsPage,
  getManagedDonationKeysPage,
  type ManagedDonationPageFilters,
} from '@shared/operations/donationPages';
import {
  getManagedCharityModel,
  getManagedCharityModelsPage,
  managementResourceID,
  validManagementSearch,
} from '@shared/operations/charityModelPages';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { formatDateTime } from '@shared/utils/datetime';
import { TimeInput } from './TimeInput';
import { RecurringLimitsDisclosure } from './RecurringLimitsDisclosure';
import { createTimeDraft, timeDraftValue, type TimeDraft, type TimeStation } from '@shared/time';
import {
  addManagedBindings,
  charityKeys,
  createManagedCharityModel,
  containsForbiddenControl as hasForbiddenControl,
  deleteManagedBinding,
  deleteManagedCharityModel,
  getManagedBindings,
  getManagedDonation,
  orderManagedBindings,
  normalizePublicDescription,
  patchManagedCharityModel,
  patchManagedDonationKey,
  reviewManagedDonation,
  type AdminDonation,
  type CharityModel,
  type CharityRole,
  type CharityState,
  type DonationEndedReason,
  type DonationStatus,
  type ManagedSafeSource,
  type ManagedDonationKey,
  type StewardDonation,
  type TokenPrices,
} from '@shared/operations/charity';
import { useRetainedOperation } from '../../admin/features/operations/useRetainedOperation';
import { responseOutcomeUnknown } from '@shared/operations/api';
import { amount } from '@shared/operations/wire';
import '@shared/operations/operations.css';

type ManagedDonation = AdminDonation | StewardDonation;

function snapshotPage<T>(items: readonly T[], requestedPage: string, pageSize: PageSize) {
  const totalPages = Math.max(1, Math.ceil(items.length / pageSize));
  const page = Math.min(Number(requestedPage), totalPages);
  const offset = (page - 1) * pageSize;
  return {
    data: items.slice(offset, offset + pageSize),
    offset,
    pagination: {
      page: String(page),
      page_size: pageSize,
      total_items: String(items.length),
      total_pages: String(totalPages),
    },
  };
}

function oneParam(params: URLSearchParams, name: string): string {
  const values = params.getAll(name);
  return values.length === 1 ? values[0] : '';
}

function selectedID(params: URLSearchParams, name: string): string {
  const value = oneParam(params, name);
  return managementResourceID(value) ? value : '';
}

function samePageFamily(
  previous: readonly unknown[] | undefined,
  current: readonly unknown[],
): boolean {
  return (
    previous?.length === current.length &&
    current.slice(0, -2).every((part, index) => part === previous[index])
  );
}

const charityCopyKey = (role: CharityRole, key: string) =>
  `${role === 'admin' ? 'admin.charity' : 'user.steward'}.${key}`;
const charityStatusKey = (role: CharityRole, status: DonationStatus) =>
  charityCopyKey(role, `status.${status}`);
const charityStateKey = (state: CharityState) => `common.operations.charity.charityState.${state}`;
const reviewerRoleKey = (role: 'admin' | 'steward') => `common.operations.charity.role.${role}`;
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
const MODEL_LEVELS = [1, 2, 3, 4, 5] as const;

function validText(value: string, maximum: number, required = false): boolean {
  return (
    (!required || value.trim().length > 0) &&
    Array.from(value).length <= maximum &&
    !hasForbiddenControl(value)
  );
}

function normalizePublicDescriptionInput(value: string): string {
  return value.replace(/\r\n/g, '\n');
}

function validPublicDescription(value: string): boolean {
  try {
    normalizePublicDescription(value, 'description');
    return true;
  } catch {
    return false;
  }
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
  try {
    amount(value, 'credits', false, MAX_MONEY_MILLI);
    return true;
  } catch {
    return false;
  }
}

function validTokenReserveCredits(value: string | null): boolean {
  return value === null || (value !== '0' && validAmount(value));
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
  expiry: TimeDraft;
  no_expiry: boolean;
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
  expiry: createTimeDraft(key.expires_at, 'second'),
  no_expiry: key.expires_at === null,
});

const keyManagementDraft = (key: ManagedDonationKey): KeyManagementDraft => ({
  ...keySettingsDraft(key),
  enabled: null,
});

type KeySettingsValidation =
  'priceLimit' | 'countLimits' | 'tokenReserve' | 'safeNote' | 'expiry' | 'expiryAuthorization';

function effectiveExpiry(value: Omit<KeySettingsDraft, 'enabled'>): number | null | undefined {
  return value.no_expiry ? null : timeDraftValue(value.expiry);
}

function keySettingsError(
  value: Omit<KeySettingsDraft, 'enabled'>,
  authorizedExpiresAt: number | null,
): KeySettingsValidation | null {
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
  const expiry = effectiveExpiry(value);
  if (
    !value.no_expiry &&
    (expiry === undefined ||
      expiry === null ||
      !Number.isSafeInteger(expiry) ||
      expiry < 0 ||
      expiry > MAX_UNIX_SECOND)
  ) {
    return 'expiry';
  }
  if (
    authorizedExpiresAt !== null &&
    (expiry === undefined || expiry === null || expiry > authorizedExpiresAt)
  ) {
    return 'expiryAuthorization';
  }
  return null;
}

function keySettingsBody(value: KeySettingsDraft | KeyManagementDraft) {
  const expiresAt = effectiveExpiry(value);
  if (expiresAt === undefined) throw new Error('Time is not ready');
  return {
    price_limit: value.price_limit,
    calls_limit: value.calls_limit,
    tokens_limit: value.tokens_limit,
    token_reserve: value.token_reserve,
    enabled: value.enabled,
    safe_note: value.safe_note,
    expires_at: expiresAt,
  };
}

const endedReasonKey: Record<DonationEndedReason, string> = {
  withdrawn: 'withdrawn',
  terminated: 'terminated',
  expired: 'expired',
  member_removed: 'memberRemoved',
  account_deleted: 'accountDeleted',
};

function safeSourceLabel(
  source: ManagedSafeSource,
  t: (key: string, options?: Record<string, unknown>) => string,
): string {
  if (source.kind === 'custom') {
    return t('common.operations.charity.customSource', {
      connector: source.connector_type,
      baseUrl: source.base_url,
    });
  }
  if (source.category === 'subscription') {
    return t('common.operations.charity.mainstreamSubscription', { name: source.name });
  }
  if (source.category === 'api_platform') {
    return t('common.operations.charity.mainstreamApiPlatform', { name: source.name });
  }
  return t('common.operations.charity.mainstreamSource', { name: source.name });
}

function KeyExpiryEditor({
  station,
  draft,
  authorizedExpiresAt,
  onChange,
}: {
  station: TimeStation;
  draft: Pick<KeySettingsDraft, 'expiry' | 'no_expiry'>;
  authorizedExpiresAt: number | null;
  onChange: (
    update: (
      value: Pick<KeySettingsDraft, 'expiry' | 'no_expiry'>,
    ) => Pick<KeySettingsDraft, 'expiry' | 'no_expiry'>,
  ) => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="ops-form-field">
      <TimeInput
        label={t('common.operations.charity.effectiveExpiry')}
        station={station}
        draft={draft.expiry}
        disabled={draft.no_expiry}
        onChange={(update) =>
          onChange((current) => ({ ...current, expiry: update(current.expiry) }))
        }
      />
      <label className="checkbox-label">
        <input
          type="checkbox"
          checked={draft.no_expiry}
          disabled={authorizedExpiresAt !== null}
          onChange={(event) => {
            const checked = event.target.checked;
            onChange((current) => ({ ...current, no_expiry: checked }));
          }}
        />
        <span>{t('common.operations.charity.noExpiry')}</span>
      </label>
      <small className="muted">
        {authorizedExpiresAt === null
          ? t('common.operations.charity.permanentAuthorizationHint')
          : t('common.operations.charity.authorizedExpiryHint', {
              value: formatDateTime(authorizedExpiresAt),
            })}
      </small>
    </div>
  );
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
    ReturnType<typeof keySettingsBody> & { reset_failure_streak: boolean },
    ManagedDonation
  >(
    (input, key) =>
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
          expires_at: input.expires_at,
          ...(input.reset_failure_streak ? { reset_failure_streak: true } : {}),
        },
        key,
      ),
    refresh,
    charityKeys.root(role),
  );
  const validationError = keySettingsError(draft, item.authorized_expires_at);
  const terminal = item.charity_state === 'ended' || item.charity_state === 'expired';
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
    <section className="ops-subcard ops-unframed">
      <h4>
        {t('common.operations.charity.keyHeading', {
          id: item.id,
          head: item.display_head,
          tail: item.display_tail,
        })}
      </h4>
      <p>{safeSourceLabel(item.safe_source, t)}</p>
      <p className="muted">
        {item.safe_source.connector_type} · {item.safe_source.base_url}
      </p>
      <KeyLimitSummary concurrency={item.max_concurrency} rpm={item.max_rpm} readOnly />
      <FailurePolicyControl
        role={role}
        donationID={donation.id}
        keyID={item.id}
        revision={donation.revision}
        threshold={item.failure_disable_threshold}
        refresh={refresh}
      />
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
        <span>
          {t(item.idle ? 'common.donationHandling.idle' : 'common.donationHandling.bound', {
            count: item.binding_count,
          })}
        </span>
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
        <dt>{t('common.operations.charity.effectiveExpiry')}</dt>
        <dd>
          {item.expires_at === null
            ? t('common.operations.charity.noExpiry')
            : formatDateTime(item.expires_at)}
        </dd>
        <dt>{t('common.operations.charity.authorizedExpiry')}</dt>
        <dd>
          {item.authorized_expires_at === null
            ? t('common.operations.charity.noExpiry')
            : formatDateTime(item.authorized_expires_at)}
        </dd>
        {item.ended_reason ? (
          <>
            <dt>{t('common.operations.charity.endedReason')}</dt>
            <dd>
              {t(`common.operations.charity.endedReasonValue.${endedReasonKey[item.ended_reason]}`)}
            </dd>
          </>
        ) : null}
      </dl>
      {donation.status === 'approved' && !terminal ? (
        <>
          <p>{t('common.operations.charity.embeddingQuotaHelp')}</p>
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
            <KeyExpiryEditor
              station={role === 'admin' ? 'admin' : 'user'}
              draft={draft}
              authorizedExpiresAt={item.authorized_expires_at}
              onChange={(update) => setDraft((current) => ({ ...current, ...update(current) }))}
            />
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
            onClick={() => {
              const expiresAt = effectiveExpiry(draft);
              if (expiresAt === undefined || keySettingsError(draft, item.authorized_expires_at))
                return;
              save.mutate({ ...keySettingsBody(draft), reset_failure_streak: reset });
            }}
          >
            {t(charityCopyKey(role, 'saveKeyLimits'))}
          </button>
        </>
      ) : (
        <p className="muted">
          {t(
            terminal
              ? 'common.operations.charity.terminalKeyImmutable'
              : 'common.operations.charity.keyEditableAfterApproval',
          )}
        </p>
      )}
    </section>
  );
}

interface DonationDetailProps {
  item: ManagedDonation;
  role: CharityRole;
  accountId: string;
  busy: boolean;
  refresh: () => Promise<unknown>;
  onCapabilityLoss?: () => void;
}

function DonationDetail(props: DonationDetailProps) {
  const [reviewPending, setReviewPending] = useState(false);
  return (
    <fieldset className="ops-stack ops-unframed" disabled={props.busy || reviewPending}>
      <DonationReview key={props.item.revision} {...props} onPendingChange={setReviewPending} />
      <DonationKeyPages
        item={props.item}
        role={props.role}
        accountId={props.accountId}
        refresh={props.refresh}
        onCapabilityLoss={props.onCapabilityLoss}
      />
    </fieldset>
  );
}

function DonationReview({
  item,
  role,
  accountId,
  busy,
  refresh,
  onCapabilityLoss,
  onPendingChange,
}: DonationDetailProps & {
  onPendingChange: (pending: boolean) => void;
}) {
  const { t } = useTranslation();
  const reviewPager = usePagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'donation-review-keys',
    scopeKey: `${accountId}:${item.id}`,
  });
  const reviewPage = snapshotPage(item.keys, reviewPager.page, reviewPager.pageSize);
  const [decision, setDecision] = useState<'approve' | 'reject'>('approve');
  const [reason, setReason] = useState('');
  const [confirmed, setConfirmed] = useState(false);
  const [keys, setKeys] = useState<Record<string, KeySettingsDraft>>(() =>
    Object.fromEntries(item.keys.map((key) => [key.id, keySettingsDraft(key)])),
  );
  const review = useRetainedOperation<
    {
      decision: 'approve' | 'reject';
      reason: string;
      settings: Record<string, ReturnType<typeof keySettingsBody>>;
    },
    ManagedDonation
  >(
    (
      input: {
        decision: 'approve' | 'reject';
        reason: string;
        settings: Record<string, ReturnType<typeof keySettingsBody>>;
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
  useEffect(() => {
    onPendingChange(review.isPending);
    return () => onPendingChange(false);
  }, [onPendingChange, review.isPending]);
  const owner = item.owner;
  let validationError: KeySettingsValidation | 'reviewReason' | 'completeSettings' | null = null;
  if (!validText(reason.trim(), 1_024, true)) {
    validationError = 'reviewReason';
  } else if (decision === 'approve') {
    if (item.keys.length === 0 || item.keys.some((entry) => !keys[entry.id])) {
      validationError = 'completeSettings';
    } else {
      validationError =
        item.keys
          .map((entry) => keySettingsError(keys[entry.id], entry.authorized_expires_at))
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
    <fieldset className="ops-stack ops-unframed" disabled={busy || review.isPending}>
      <Card>
        <h3>{t(charityCopyKey(role, 'donationNumber'), { id: item.id })}</h3>
        <DonationHandlingControl
          donationID={item.id}
          role={role}
          handling={item.handling}
          refresh={refresh}
          onCapabilityLoss={onCapabilityLoss}
        />
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
            {owner
              ? `${owner.display_name} · ${owner.user_id}`
              : t('common.operations.charity.deidentified')}
            {owner
              ? ` · ${owner.discord_id ?? t('common.operations.charity.discordDetached')}`
              : ''}
          </dd>
          <dt>{t('common.operations.charity.donorDescription')}</dt>
          <dd>
            <MarkdownText>
              {item.description || t('common.operations.charity.noDescription')}
            </MarkdownText>
          </dd>
          <dt>{t('common.operations.charity.createdUpdated')}</dt>
          <dd>
            {formatDateTime(item.created_at)} / {formatDateTime(item.updated_at)}
          </dd>
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
          </div>
          {decision === 'approve' ? (
            <div className="ops-stack">
              {reviewPage.data.map((entry) => {
                const draft = keys[entry.id];
                return (
                  <section key={entry.id} className="ops-subcard">
                    <h4>
                      {entry.display_head}…{entry.display_tail} · {entry.safe_source.base_url}
                    </h4>
                    <KeyLimitSummary
                      concurrency={entry.max_concurrency}
                      rpm={entry.max_rpm}
                      readOnly
                    />
                    <p>{t('common.operations.charity.embeddingQuotaHelp')}</p>
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
                      <KeyExpiryEditor
                        station={role === 'admin' ? 'admin' : 'user'}
                        draft={draft}
                        authorizedExpiresAt={entry.authorized_expires_at}
                        onChange={(update) =>
                          setKeys((current) => ({
                            ...current,
                            [entry.id]: { ...current[entry.id], ...update(current[entry.id]) },
                          }))
                        }
                      />
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
              <PagePagination
                metadata={reviewPage.pagination}
                requestedPage={reviewPager.page}
                onPageChange={reviewPager.setPage}
                onPageSizeChange={reviewPager.setPageSize}
                busy={review.isPending}
              />
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
            onClick={() => {
              if (
                !confirmed ||
                !validText(reason.trim(), 1024, true) ||
                (decision === 'approve' &&
                  item.keys.some(
                    (entry) =>
                      !keys[entry.id] ||
                      keySettingsError(keys[entry.id], entry.authorized_expires_at),
                  ))
              )
                return;
              review.mutate({
                decision,
                reason: reason.trim(),
                settings:
                  decision === 'approve'
                    ? Object.fromEntries(
                        Object.entries(keys).map(([id, value]) => [id, keySettingsBody(value)]),
                      )
                    : {},
              });
            }}
          >
            {t(charityCopyKey(role, decision === 'approve' ? 'approve' : 'reject'))}
          </button>
        </Card>
      ) : null}
    </fieldset>
  );
}

function DonationKeyPages({
  item,
  role,
  accountId,
  refresh,
  onCapabilityLoss,
}: {
  item: ManagedDonation;
  role: CharityRole;
  accountId: string;
  refresh: () => Promise<unknown>;
  onCapabilityLoss?: () => void;
}) {
  const { t } = useTranslation();
  const [params, setParams] = useSearchState();
  const pager = useUrlPagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'managed-donation-keys',
    scopeKey: `${accountId}:${item.id}`,
    pageParam: 'donation_keys_page',
    pageSizeParam: 'donation_keys_page_size',
  });
  const focusKey = selectedID(params, 'donation_key');
  const focusIndex = focusKey ? item.keys.findIndex((key) => key.id === focusKey) : -1;
  const needsFocusPage = focusIndex >= 0 && !params.has('donation_keys_page');
  const page = needsFocusPage ? String(Math.floor(focusIndex / pager.pageSize) + 1) : pager.page;
  useEffect(() => {
    if (needsFocusPage)
      setParams(
        (current) => {
          const next = new URLSearchParams(current);
          next.set('donation_keys_page', page);
          return next;
        },
        { replace: true },
      );
  }, [needsFocusPage, page, setParams]);
  const queryKey = [
    ...charityKeys.root(role),
    'key-pages',
    accountId,
    item.id,
    page,
    pager.pageSize,
  ] as const;
  const keys = useQuery({
    queryKey,
    queryFn: ({ signal }) =>
      getManagedDonationKeysPage(role, item.id, page, pager.pageSize, signal),
    retry: false,
    placeholderData: (previous, query) =>
      samePageFamily(query?.queryKey, queryKey) ? previous : undefined,
  });
  const capabilityLost = isForbidden(keys.error) || isUnauthorized(keys.error);
  useEffect(() => {
    if (capabilityLost) onCapabilityLoss?.();
  }, [capabilityLost, onCapabilityLoss]);
  const staleRevision = keys.data?.data.some((key) => key.donation_revision !== item.revision);
  if (capabilityLost)
    return (
      <p className="field-error" role="alert">
        {t('common.operations.charity.accessLost')}
      </p>
    );
  return (
    <Card>
      <h3>{t('common.operations.charity.donationKeys')}</h3>
      {keys.data ? (
        <FailureResetControl
          key={`${role}:${accountId}:${item.id}`}
          role={role}
          selection={{ view: 'donation_keys', donation_id: item.id }}
          disabled={keys.isFetching || Boolean(keys.error) || staleRevision}
          onCapabilityLoss={onCapabilityLoss}
          choices={keys.data.data.map((key) => ({
            id: key.id,
            label: `${key.id} · ${key.display_head}…${key.display_tail}`,
            target: {
              donation_id: item.id,
              key_id: key.id,
              expected_revision: key.donation_revision,
            },
          }))}
        />
      ) : null}
      {focusKey && focusIndex === -1 ? (
        <p className="inline-notice">{t('common.operations.charity.selectedKeyMissing')}</p>
      ) : null}
      {keys.isPending ? (
        <LoadingState />
      ) : keys.error ? (
        <ErrorState error={keys.error} onRetry={() => void keys.refetch()} />
      ) : null}
      {staleRevision ? (
        <div role="status">
          <p>{t('common.operations.charity.changedWhileReading')}</p>
          <button type="button" className="btn btn-secondary" onClick={() => void refresh()}>
            {t('common.refresh')}
          </button>
        </div>
      ) : null}
      {keys.data ? (
        <>
          <fieldset
            className="ops-stack ops-unframed"
            disabled={keys.isFetching || Boolean(keys.error) || staleRevision}
          >
            {keys.data.data.map((key) => (
              <section
                className={key.id === focusKey ? 'ops-subcard is-selected' : 'ops-subcard'}
                key={key.id}
              >
                <DonationKeyEditor
                  key={item.revision}
                  item={key}
                  donation={item}
                  role={role}
                  refresh={refresh}
                  onCapabilityLoss={onCapabilityLoss}
                />
                <RecurringLimitsDisclosure
                  accountId={accountId}
                  role={role}
                  donationId={item.id}
                  keyId={key.id}
                  label={t('common.operations.charity.keyHeading', {
                    id: key.id,
                    head: key.display_head,
                    tail: key.display_tail,
                  })}
                  readOnly={key.charity_state === 'ended' || key.charity_state === 'expired'}
                  onSaved={() => {
                    void refresh();
                  }}
                  onCapabilityLoss={onCapabilityLoss}
                />
              </section>
            ))}
          </fieldset>
          <PagePagination
            metadata={keys.data.pagination}
            requestedPage={page}
            onPageChange={pager.setPage}
            onPageSizeChange={pager.setPageSize}
            busy={keys.isFetching}
          />
        </>
      ) : null}
    </Card>
  );
}

function DonationsPanel({
  role,
  accountId,
  onCapabilityLoss,
}: {
  role: CharityRole;
  accountId: string;
  onCapabilityLoss?: () => void;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [searchParams, setSearchParams] = useSearchState();
  const rawHandling = oneParam(searchParams, 'handling');
  const handling = ['pending', 'processed', 'legacy', 'closed'].includes(rawHandling)
    ? rawHandling
    : '';
  const rawQuery = oneParam(searchParams, 'q');
  const query = validManagementSearch(rawQuery) ? rawQuery : '';
  const rawStatus = oneParam(searchParams, 'donation_status');
  const status = ['pending', 'approved', 'rejected', 'deleted', 'expired'].includes(rawStatus)
    ? rawStatus
    : '';
  const selected = selectedID(searchParams, 'donation_id');
  const [draft, setDraft] = useState({ query, text: query });
  const queryDraft = draft.query === query ? draft.text : query;
  const setQueryDraft = (text: string) => setDraft({ query, text });
  const queryValid = validManagementSearch(queryDraft);
  const pager = useUrlPagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'managed-donations',
    scopeKey: accountId,
    pageParam: 'donations_page',
    pageSizeParam: 'donations_page_size',
  });
  const updateFilters = (nextHandling: string, nextQuery: string, nextStatus = status) => {
    setSearchParams((current) => {
      const next = new URLSearchParams(current);
      for (const [name, value] of [
        ['handling', nextHandling],
        ['q', nextQuery],
        ['donation_status', nextStatus],
      ]) {
        if (value) next.set(name, value);
        else next.delete(name);
      }
      next.set('donations_page', '1');
      return next;
    });
  };
  const setSelected = (id: string) => {
    if (id) remember();
    setSearchParams((current) => {
      const next = new URLSearchParams(current);
      if (id) next.set('donation_id', id);
      else next.delete('donation_id');
      for (const name of ['donation_key', 'donation_keys_page', 'donation_keys_page_size'])
        next.delete(name);
      const from = oneParam(current, 'donation_from');
      if (!id && (from === 'sources' || from === 'models')) next.set('charity_section', from);
      next.delete('donation_from');
      return next;
    });
  };
  useEffect(() => {
    const desired = { handling, q: query, donation_status: status, donation_id: selected };
    if (
      Object.entries(desired).every(
        ([name, value]) =>
          searchParams.getAll(name).length <= 1 && (searchParams.get(name) ?? '') === value,
      )
    )
      return;
    setSearchParams(
      (current) => {
        const next = new URLSearchParams(current);
        for (const [name, value] of Object.entries(desired)) {
          next.delete(name);
          if (value) next.set(name, value);
        }
        return next;
      },
      { replace: true },
    );
  }, [handling, query, status, selected, searchParams, setSearchParams]);
  const listKey = [
    ...charityKeys.root(role),
    'donation-pages',
    accountId,
    status,
    handling,
    query,
    pager.page,
    pager.pageSize,
  ] as const;
  const list = useQuery({
    queryKey: listKey,
    queryFn: ({ signal }) =>
      getManagedDonationsPage(
        role,
        { status, handling, q: query } as ManagedDonationPageFilters,
        pager.page,
        pager.pageSize,
        signal,
      ),
    retry: false,
    placeholderData: (previous, previousQuery) =>
      samePageFamily(previousQuery?.queryKey, listKey) ? previous : undefined,
  });
  const detail = useQuery({
    queryKey: [...charityKeys.donation(role, selected), accountId],
    queryFn: ({ signal }) => getManagedDonation(role, selected, signal),
    enabled: Boolean(selected),
    retry: false,
  });
  const navigationReady = selected
    ? !list.isPending && !list.isFetching && !detail.isPending && !detail.isFetching
    : !list.isPending && !list.isFetching;
  const { listRef, detailRef, remember } = useDetailNavigation(selected, navigationReady);
  const capabilityLost = [list.error, detail.error].some(
    (error) => isUnauthorized(error) || isForbidden(error),
  );
  useEffect(() => {
    if (capabilityLost) onCapabilityLoss?.();
  }, [capabilityLost, onCapabilityLoss]);
  const refresh = () => client.invalidateQueries({ queryKey: charityKeys.root(role) });
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
    <div className="ops-stack" ref={listRef} tabIndex={-1}>
      <Card>
        <div className="ops-toolbar">
          <label>
            <span>{t('common.donationHandling.filter')}</span>
            <select value={handling} onChange={(event) => updateFilters(event.target.value, query)}>
              <option value="">{t('common.donationHandling.all')}</option>
              {(['pending', 'processed', 'closed', 'legacy'] as const).map((state) => (
                <option key={state} value={state}>
                  {t(donationHandlingStateKey[state])}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span>{t('common.donationHandling.search')}</span>
            <input
              type="search"
              value={queryDraft}
              aria-invalid={!queryValid}
              onChange={(event) => setQueryDraft(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === 'Enter' && queryValid) {
                  event.preventDefault();
                  updateFilters(handling, queryDraft);
                }
              }}
            />
          </label>
          <button
            type="button"
            className="btn btn-secondary"
            disabled={!queryValid}
            onClick={() => updateFilters(handling, queryDraft)}
          >
            {t('common.donationHandling.applySearch')}
          </button>
          {!queryValid ? (
            <p className="field-error" role="alert">
              {t('common.donationHandling.searchInvalid')}
            </p>
          ) : null}
          <label>
            <span>{t(charityCopyKey(role, 'statusFilter'))}</span>
            <select
              value={status}
              onChange={(event) => {
                updateFilters(handling, query, event.target.value);
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
        {list.data ? (
          <FailureResetControl
            key={`${role}:${accountId}:${status}:${handling}:${query}`}
            role={role}
            selection={{
              view: 'donations',
              ...({ status, handling, q: query } as ManagedDonationPageFilters),
            }}
            disabled={list.isFetching || Boolean(list.error)}
            onCapabilityLoss={onCapabilityLoss}
            choices={list.data.data.map((item) => ({
              id: item.id,
              label: `${item.id} · ${item.description}`,
              target: { view: 'donation_keys', donation_id: item.id },
            }))}
          />
        ) : null}
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
              <table className="ops-table ops-table--responsive">
                <thead>
                  <tr>
                    <th>{t('common.itemId')}</th>
                    <th>{t('common.operations.charity.description')}</th>
                    <th>{t('common.operations.charity.owner')}</th>
                    <th>{t('common.status')}</th>
                    <th>{t('common.donationHandling.title')}</th>
                    <th>{t('common.operations.charity.keys')}</th>
                    <th>{t('common.operations.charity.open')}</th>
                  </tr>
                </thead>
                <tbody>
                  {list.data.data.map((item) => (
                    <tr key={item.id}>
                      <td className="ops-id" data-label={t('common.itemId')}>
                        {item.id}
                      </td>
                      <td
                        className="ops-cell-wide"
                        data-label={t('common.operations.charity.description')}
                      >
                        <MarkdownText>
                          {item.description || t('common.operations.charity.noDescription')}
                        </MarkdownText>
                        <ul className="ops-source-preview">
                          {item.sources.map((source) => (
                            <li
                              key={`${source.kind}:${source.kind === 'mainstream' ? source.channel_id : source.base_url}:${source.connector_type}`}
                            >
                              {source.kind === 'mainstream' ? `${source.name} · ` : ''}
                              {source.base_url}
                            </li>
                          ))}
                        </ul>
                        {BigInt(item.source_count) > BigInt(item.sources.length) ? (
                          <small>
                            {t('common.operations.charity.sourcePreview', {
                              shown: item.sources.length,
                              total: item.source_count,
                            })}
                          </small>
                        ) : null}
                      </td>
                      <td
                        className="ops-cell-wide"
                        data-label={t('common.operations.charity.owner')}
                      >
                        {item.owner?.display_name ?? t('common.operations.charity.deidentified')}
                      </td>
                      <td data-label={t('common.status')}>
                        <StatusBadge
                          active={item.status === 'approved'}
                          label={t(charityStatusKey(role, item.status))}
                        />
                      </td>
                      <td className="ops-cell-wide" data-label={t('common.donationHandling.title')}>
                        <DonationHandlingStatus handling={item.handling} />
                      </td>
                      <td data-label={t('common.operations.charity.keys')}>
                        {item.key_count}
                        <div className="muted">
                          {Object.entries(item.state_counts)
                            .filter(([, count]) => count !== '0')
                            .map(([state, count]) => (
                              <span className="ops-summary-part" key={state}>
                                {t(charityStateKey(state as CharityState))}: {count}
                              </span>
                            ))}
                        </div>
                      </td>
                      <td
                        className="ops-cell-wide"
                        data-label={t('common.operations.charity.open')}
                      >
                        <button
                          className="btn btn-secondary ops-row-action"
                          type="button"
                          disabled={list.isFetching}
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
          </>
        )}
        {list.data && !list.error ? (
          <PagePagination
            metadata={list.data.pagination}
            requestedPage={pager.page}
            onPageChange={pager.setPage}
            onPageSizeChange={pager.setPageSize}
            busy={list.isFetching}
          />
        ) : null}
      </Card>
      {selected ? (
        <div className="ops-stack ops-detail-target" ref={detailRef} tabIndex={-1}>
          <button className="btn btn-quiet" type="button" onClick={() => setSelected('')}>
            {t(
              oneParam(searchParams, 'donation_from') === 'models'
                ? 'common.operations.charity.returnToModel'
                : 'common.operations.charity.returnToList',
            )}
          </button>
          {detail.isPending ? (
            <LoadingState />
          ) : detail.error ? (
            <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />
          ) : (
            <>
              <DonationDetail
                key={detail.data.id}
                item={detail.data}
                accountId={accountId}
                busy={detail.isFetching || list.isFetching || Boolean(list.error)}
                role={role}
                refresh={refresh}
                onCapabilityLoss={onCapabilityLoss}
              />
            </>
          )}
        </div>
      ) : null}
    </div>
  );
}

interface ModelDraft {
  routeStrategy: CharityModel['route_strategy'];
  provider: string;
  model: string;
  enabled: boolean;
  allowedLevels: number[];
  publicDescription: string;
  tokenReserveCredits: string | null;
  mode: 'per_request' | 'per_token';
  requestUser: string;
  requestDonor: string;
  userPrices: TokenPrices;
  donorRewards: TokenPrices;
  discountEnabled: boolean;
  discountPercent: number;
  discountStart: TimeDraft;
  discountEnd: TimeDraft;
  flatten: boolean;
}
const zeroPrices = (): TokenPrices => ({
  uncached_input: '0',
  cache_write_input: '0',
  cache_read_input: '0',
  output: '0',
});

function modelDraft(model?: CharityModel): ModelDraft {
  return {
    routeStrategy: model?.route_strategy ?? 'expiry_weighted',
    provider: model?.provider ?? '',
    model: model?.model ?? '',
    enabled: model?.enabled ?? true,
    allowedLevels: model ? [...model.allowed_levels] : [...MODEL_LEVELS],
    publicDescription: model ? model.public_description : '',
    tokenReserveCredits: model?.token_reserve_credits ?? null,
    mode: model?.pricing.mode ?? 'per_request',
    requestUser: model?.pricing.mode === 'per_request' ? model.pricing.user_price : '0',
    requestDonor: model?.pricing.mode === 'per_request' ? model.pricing.donor_reward : '0',
    userPrices: model?.pricing.mode === 'per_token' ? model.pricing.user_prices : zeroPrices(),
    donorRewards: model?.pricing.mode === 'per_token' ? model.pricing.donor_rewards : zeroPrices(),
    discountEnabled: model?.discount.enabled ?? false,
    discountPercent: model?.discount.percent ?? 0,
    discountStart: createTimeDraft(model?.discount.start_at ?? null, 'second'),
    discountEnd: createTimeDraft(model?.discount.end_at ?? null, 'second'),
    flatten: model?.flatten_tool_calls ?? false,
  };
}
function modelBody(draft: ModelDraft, includeTokenReserveCredits: boolean) {
  const start = timeDraftValue(draft.discountStart);
  const end = timeDraftValue(draft.discountEnd);
  if (start === undefined || end === undefined) throw new Error('Time is not ready');
  const body = {
    route_strategy: draft.routeStrategy,
    provider: draft.provider.trim(),
    model: draft.model.trim(),
    enabled: draft.enabled,
    allowed_levels: MODEL_LEVELS.filter((level) => draft.allowedLevels.includes(level)),
    public_description: normalizePublicDescriptionInput(draft.publicDescription),
    pricing:
      draft.mode === 'per_request'
        ? { mode: 'per_request', user_price: draft.requestUser, donor_reward: draft.requestDonor }
        : { mode: 'per_token', user_prices: draft.userPrices, donor_rewards: draft.donorRewards },
    discount: {
      enabled: draft.discountEnabled,
      percent: draft.discountPercent,
      start_at: start,
      end_at: end,
    },
    flatten_tool_calls: draft.flatten,
  };
  return includeTokenReserveCredits
    ? { ...body, token_reserve_credits: draft.tokenReserveCredits }
    : body;
}

type ModelValidation =
  | 'modelIdentity'
  | 'modelLevels'
  | 'publicDescription'
  | 'modelPrices'
  | 'tokenReserveCredits'
  | 'discountPercent'
  | 'discountDates';

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
  if (
    draft.allowedLevels.some(
      (level) => !MODEL_LEVELS.includes(level as (typeof MODEL_LEVELS)[number]),
    ) ||
    new Set(draft.allowedLevels).size !== draft.allowedLevels.length
  ) {
    return 'modelLevels';
  }
  if (!validPublicDescription(draft.publicDescription)) return 'publicDescription';
  const prices =
    draft.mode === 'per_request'
      ? [draft.requestUser, draft.requestDonor]
      : [...Object.values(draft.userPrices), ...Object.values(draft.donorRewards)];
  if (prices.some((value) => !validAmount(value))) {
    return 'modelPrices';
  }
  if (draft.mode === 'per_token' && !validTokenReserveCredits(draft.tokenReserveCredits))
    return 'tokenReserveCredits';
  if (
    !Number.isInteger(draft.discountPercent) ||
    draft.discountPercent < 0 ||
    draft.discountPercent > 100
  ) {
    return 'discountPercent';
  }
  const start = timeDraftValue(draft.discountStart);
  const end = timeDraftValue(draft.discountEnd);
  if (
    start === undefined ||
    end === undefined ||
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
  const [baseRevision, setBaseRevision] = useState(model?.revision);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const save = useRetainedOperation<
    { body: ReturnType<typeof modelBody>; revision?: string },
    CharityModel
  >(
    async (input, key) => {
      const result = model
        ? await patchManagedCharityModel(
            role,
            model.id,
            { expected_revision: input.revision, ...input.body },
            key,
          )
        : await createManagedCharityModel(role, input.body, key);
      if (model) setBaseRevision(result.revision);
      return result;
    },
    refresh,
    charityKeys.root(role),
  );
  const remove = useRetainedOperation<{ id: string; revision: string }, void>(
    (input, key) => deleteManagedCharityModel(role, input.id, input.revision, key),
    refresh,
    charityKeys.root(role),
  );
  const validationError = modelDraftError(draft);
  const unknownSave = save.error && responseOutcomeUnknown(save.error);
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
      <p>{t('common.operations.charity.namingHelp')}</p>
      <p className="ops-model-preview">
        <span>{t('common.operations.charity.namePreview')}</span>
        <output>{`[公益]${draft.provider.trim() || t(charityCopyKey(role, 'provider'))}/${draft.model.trim() || t(charityCopyKey(role, 'model'))}`}</output>
      </p>
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
          <span>{t('common.operations.charity.routeStrategy')}</span>
          <select
            value={draft.routeStrategy}
            onChange={(event) =>
              setDraft({
                ...draft,
                routeStrategy: event.target.value as ModelDraft['routeStrategy'],
              })
            }
          >
            <option value="ordered">{t('common.operations.charity.routeOrdered')}</option>
            <option value="random">{t('common.operations.charity.routeRandom')}</option>
            <option value="expiry_weighted">
              {t('common.operations.charity.routeExpiryWeighted')}
            </option>
          </select>
          <small>
            {t(
              draft.routeStrategy === 'ordered'
                ? 'common.operations.charity.routeOrderedHelp'
                : draft.routeStrategy === 'random'
                  ? 'common.operations.charity.routeRandomHelp'
                  : 'common.operations.charity.routeExpiryWeightedHelp',
            )}
          </small>
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
          <span>{t('common.operations.charity.enableCharityModel')}</span>
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
      <p>{t('common.operations.charity.operationHelp')}</p>
      <p>{t('common.operations.charity.embeddingBillingHelp')}</p>
      <div className="ops-model-settings">
        <fieldset className="ops-model-levels">
          <legend>{t('common.operations.charity.allowedLevels')}</legend>
          <div className="ops-model-level-actions">
            <button
              className="btn btn-secondary"
              type="button"
              onClick={() =>
                setDraft((current) => ({ ...current, allowedLevels: [...MODEL_LEVELS] }))
              }
            >
              {t('common.operations.charity.selectAllLevels')}
            </button>
            <button
              className="btn btn-secondary"
              type="button"
              onClick={() => setDraft((current) => ({ ...current, allowedLevels: [] }))}
            >
              {t('common.operations.charity.clearAllLevels')}
            </button>
          </div>
          <div className="ops-model-level-options">
            {MODEL_LEVELS.map((level) => (
              <label className="checkbox-label" key={level}>
                <input
                  type="checkbox"
                  checked={draft.allowedLevels.includes(level)}
                  onChange={(event) =>
                    setDraft((current) => ({
                      ...current,
                      allowedLevels: event.target.checked
                        ? MODEL_LEVELS.filter(
                            (candidate) =>
                              candidate === level || current.allowedLevels.includes(candidate),
                          )
                        : current.allowedLevels.filter((candidate) => candidate !== level),
                    }))
                  }
                />
                <span>{t('common.operations.charity.levelLabel', { level })}</span>
              </label>
            ))}
          </div>
          {draft.allowedLevels.length === 0 ? (
            <small className="ops-model-level-empty">
              {t('common.operations.charity.noAllowedLevels')}
            </small>
          ) : null}
        </fieldset>
        <label className="ops-model-description">
          <span>{t('common.operations.charity.publicDescription')}</span>
          <textarea
            rows={5}
            value={draft.publicDescription}
            aria-label={t('common.operations.charity.publicDescription')}
            onChange={(event) =>
              setDraft((current) => ({ ...current, publicDescription: event.target.value }))
            }
          />
          <small>{t('common.operations.charity.publicDescriptionHelp')}</small>
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
        <>
          <div className="ops-field-grid">
            <label>
              <span>{t('common.operations.charity.tokenReserveCredits')}</span>
              <input
                aria-label={t('common.operations.charity.tokenReserveCredits')}
                inputMode="decimal"
                value={draft.tokenReserveCredits ?? ''}
                onChange={(event) =>
                  setDraft((current) => ({
                    ...current,
                    tokenReserveCredits: event.target.value || null,
                  }))
                }
              />
              <small>{t('common.operations.charity.tokenReserveCreditsHelp')}</small>
            </label>
          </div>
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
        </>
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
        <TimeInput
          label={t(charityCopyKey(role, 'discountStart'))}
          station={role === 'admin' ? 'admin' : 'user'}
          draft={draft.discountStart}
          onChange={(update) =>
            setDraft((current) => ({ ...current, discountStart: update(current.discountStart) }))
          }
        />
        <TimeInput
          label={t(charityCopyKey(role, 'discountEnd'))}
          station={role === 'admin' ? 'admin' : 'user'}
          draft={draft.discountEnd}
          onChange={(update) =>
            setDraft((current) => ({ ...current, discountEnd: update(current.discountEnd) }))
          }
        />
      </div>
      {save.error ? (
        <ErrorState error={save.error} />
      ) : remove.error ? (
        <ErrorState error={remove.error} />
      ) : null}
      {model && model.revision !== baseRevision ? (
        <p role="status">{t('common.operations.charity.modelChanged')}</p>
      ) : null}
      {validationError ? (
        <p className="field-error" role="alert">
          {validationError === 'modelLevels'
            ? t('common.operations.charity.validation.modelLevels')
            : validationError === 'publicDescription'
              ? t('common.operations.charity.validation.publicDescription')
              : t(`common.operations.charity.validation.${validationError}`)}
        </p>
      ) : null}
      <div className="ops-actions">
        <button
          className="btn btn-primary"
          type="button"
          disabled={
            Boolean(validationError) || save.isPending || remove.isPending || Boolean(unknownSave)
          }
          onClick={() => {
            if (!modelDraftError(draft))
              save.mutate({
                body: modelBody(
                  draft,
                  draft.mode === 'per_token' &&
                    (!model || draft.tokenReserveCredits !== (model.token_reserve_credits ?? null)),
                ),
                revision: baseRevision,
              });
          }}
        >
          {model ? t('common.operations.charity.saveModel') : t(charityCopyKey(role, 'newModel'))}
        </button>
        {unknownSave && save.variables ? (
          <button
            type="button"
            className="btn btn-secondary"
            disabled={save.isPending || remove.isPending}
            onClick={() => save.mutate(save.variables!)}
          >
            {t('common.operations.charity.retrySavedModel')}
          </button>
        ) : null}
        {model ? (
          <button
            type="button"
            className="btn btn-secondary"
            disabled={save.isPending || remove.isPending}
            onClick={() => {
              setDraft(modelDraft(model));
              setBaseRevision(model.revision);
              save.reset();
            }}
          >
            {t('common.operations.charity.reloadModel')}
          </button>
        ) : null}
        {model ? (
          <button
            className="btn btn-danger"
            type="button"
            disabled={save.isPending || remove.isPending || Boolean(unknownSave)}
            onClick={() => setConfirmDelete(true)}
          >
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
            remove.mutate({ id: model.id, revision: baseRevision! }, { onSuccess: onDeleted });
          }}
        />
      ) : null}
    </Card>
  );
}

function BindingsPanel({
  role,
  accountId,
  refresh,
  model,
  onCapabilityLoss,
}: {
  role: CharityRole;
  accountId: string;
  refresh: () => Promise<unknown>;
  model: CharityModel;
  onCapabilityLoss?: () => void;
}) {
  const { t } = useTranslation();
  const [, setParams] = useSearchState();
  const pager = usePagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'charity-binding-order',
    scopeKey: `${accountId}:${model.id}`,
  });
  const [selected, setSelected] = useState<Record<string, CharitySelection>>({});
  const [pickerBlocked, setPickerBlocked] = useState(true);
  const [orderDraft, setOrderDraft] = useState<{ revision: string; ids: string[] } | null>(null);
  const bindings = useQuery({
    queryKey: [...charityKeys.bindings(role, model.id), accountId],
    queryFn: ({ signal }) => getManagedBindings(role, model.id, signal),
    retry: false,
  });

  const capabilityLost = isUnauthorized(bindings.error) || isForbidden(bindings.error);
  useEffect(() => {
    if (capabilityLost) onCapabilityLoss?.();
  }, [capabilityLost, onCapabilityLoss]);
  const reconcile = async () => {
    await refresh();
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
    const ids = orderedBindings.map((entry) => entry.id);
    [ids[index], ids[index + offset]] = [ids[index + offset], ids[index]];
    setOrderDraft({ ids, revision: bindings.data.binding_revision });
  };
  const orderedBindings =
    orderDraft && orderDraft.revision === bindings.data?.binding_revision
      ? orderDraft.ids.flatMap(
          (id) => bindings.data?.bindings.filter((entry) => entry.id === id) ?? [],
        )
      : (bindings.data?.bindings ?? []);
  const orderChanged = Boolean(
    bindings.data &&
    orderedBindings.some((entry, index) => entry.id !== bindings.data.bindings[index]?.id),
  );
  const bindingPage = snapshotPage(orderedBindings, pager.page, pager.pageSize);
  const busy =
    bindings.isFetching ||
    Boolean(bindings.error) ||
    order.isPending ||
    add.isPending ||
    remove.isPending;
  const chosen = Object.values(selected).map((entry) => ({
    donation_key_id: entry.donation_key_id,
    upstream_model_id: entry.upstream_model_id,
  }));
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
          <table className="ops-table ops-table--responsive">
            <thead>
              <tr>
                <th>{t(charityCopyKey(role, 'order'))}</th>
                <th>{t('common.operations.charity.source')}</th>
                <th>{t(charityCopyKey(role, 'upstreamModel'))}</th>
                <th>{t(charityCopyKey(role, 'actions'))}</th>
              </tr>
            </thead>
            <tbody>
              {bindingPage.data.map((entry, pageIndex) => {
                const index = bindingPage.offset + pageIndex;
                return (
                  <tr key={entry.id}>
                    <td data-label={t(charityCopyKey(role, 'order'))}>{index + 1}</td>
                    <td
                      className="ops-cell-wide"
                      data-label={t('common.operations.charity.source')}
                    >
                      {entry.source.connector_type} · {entry.source.canonical_base_url} ·{' '}
                      {entry.source.display_head}…{entry.source.display_tail}
                      <KeyLimitSummary
                        concurrency={entry.source.max_concurrency}
                        rpm={entry.source.max_rpm}
                        readOnly
                      />
                    </td>
                    <td
                      className="ops-cell-wide"
                      data-label={t(charityCopyKey(role, 'upstreamModel'))}
                    >
                      {entry.upstream_model_id}
                    </td>
                    <td className="ops-cell-wide" data-label={t(charityCopyKey(role, 'actions'))}>
                      <button
                        className="btn btn-secondary"
                        type="button"
                        disabled={busy || orderChanged}
                        onClick={() =>
                          setParams((current) => {
                            const next = new URLSearchParams(current);
                            next.set('charity_section', 'donations');
                            next.set('donation_id', entry.donation_id);
                            next.set('donation_key', entry.donation_key_id);
                            next.set('donation_from', 'models');
                            next.delete('donation_keys_page');
                            next.delete('donation_keys_page_size');
                            return next;
                          })
                        }
                      >
                        {t('common.operations.charity.sourceBrowser.manageKey', {
                          id: entry.donation_key_id,
                        })}
                      </button>
                      <button
                        className="btn btn-secondary"
                        type="button"
                        disabled={index === 0 || busy}
                        onClick={() => move(index, -1)}
                      >
                        {t('common.operations.charity.moveUp')}
                      </button>
                      <button
                        className="btn btn-secondary"
                        type="button"
                        disabled={index === orderedBindings.length - 1 || busy}
                        onClick={() => move(index, 1)}
                      >
                        {t('common.operations.charity.moveDown')}
                      </button>
                      <button
                        className="btn btn-danger"
                        type="button"
                        disabled={busy || orderChanged}
                        onClick={() =>
                          remove.mutate({ id: entry.id, revision: bindings.data.binding_revision })
                        }
                      >
                        {t('common.operations.charity.remove')}
                      </button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
      {bindings.data && !bindings.error ? (
        <PagePagination
          metadata={bindingPage.pagination}
          requestedPage={pager.page}
          onPageChange={pager.setPage}
          onPageSizeChange={pager.setPageSize}
          busy={busy}
        />
      ) : null}
      {orderChanged ? (
        <div className="ops-actions">
          <button
            type="button"
            className="btn btn-primary"
            disabled={busy}
            onClick={() => {
              if (orderDraft) order.mutate(orderDraft, { onSuccess: () => setOrderDraft(null) });
            }}
          >
            {t('common.operations.charity.saveOrder')}
          </button>
          <button
            type="button"
            className="btn btn-quiet"
            disabled={busy}
            onClick={() => setOrderDraft(null)}
          >
            {t('common.cancel')}
          </button>
          <span>{t('common.operations.charity.saveOrderHelp')}</span>
        </div>
      ) : null}
      <h3>{t('common.operations.charity.bindingCandidates')}</h3>
      <CharityBindingPicker
        key={model.id}
        role={role}
        modelId={model.id}
        selected={selected}
        onChange={setSelected}
        locked={busy || !bindings.data}
        onCapabilityLoss={onCapabilityLoss}
        onReadStateChange={setPickerBlocked}
      />
      <div className="ops-actions">
        <button
          className="btn btn-primary"
          type="button"
          disabled={!bindings.data || chosen.length === 0 || busy || orderChanged || pickerBlocked}
          onClick={() =>
            bindings.data &&
            !pickerBlocked &&
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
  accountId,
  onCapabilityLoss,
}: {
  role: CharityRole;
  accountId: string;
  onCapabilityLoss?: () => void;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [params, setParams] = useSearchState();
  const rawQuery = oneParam(params, 'model_q');
  const query = validManagementSearch(rawQuery) ? rawQuery : '';
  const rawEnabled = oneParam(params, 'model_enabled');
  const enabled = ['true', 'false'].includes(rawEnabled) ? rawEnabled : '';
  const selectedId = selectedID(params, 'charity_model');
  const [draft, setDraft] = useState({ query, text: query });
  const queryDraft = draft.query === query ? draft.text : query;
  const setQueryDraft = (text: string) => setDraft({ query, text });
  const pager = useUrlPagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'managed-charity-models',
    scopeKey: accountId,
    pageParam: 'models_page',
    pageSizeParam: 'models_page_size',
  });
  const updateFilters = (nextQuery: string, nextEnabled = enabled) => {
    setParams((current) => {
      const next = new URLSearchParams(current);
      for (const [name, value] of [
        ['model_q', nextQuery],
        ['model_enabled', nextEnabled],
      ]) {
        next.delete(name);
        if (value) next.set(name, value);
      }
      next.set('models_page', '1');
      return next;
    });
  };
  const setSelected = (id: string) => {
    if (id) remember();
    setParams((current) => {
      const next = new URLSearchParams(current);
      if (id) next.set('charity_model', id);
      else next.delete('charity_model');
      return next;
    });
  };
  useEffect(() => {
    const desired = { model_q: query, model_enabled: enabled, charity_model: selectedId };
    if (
      Object.entries(desired).every(
        ([name, value]) => params.getAll(name).length <= 1 && (params.get(name) ?? '') === value,
      )
    )
      return;
    setParams(
      (current) => {
        const next = new URLSearchParams(current);
        for (const [name, value] of Object.entries(desired)) {
          next.delete(name);
          if (value) next.set(name, value);
        }
        return next;
      },
      { replace: true },
    );
  }, [query, enabled, selectedId, params, setParams]);
  const listKey = [
    ...charityKeys.root(role),
    'model-pages',
    accountId,
    query,
    enabled,
    pager.page,
    pager.pageSize,
  ] as const;
  const models = useQuery({
    queryKey: listKey,
    queryFn: ({ signal }) =>
      getManagedCharityModelsPage(role, query, enabled, pager.page, pager.pageSize, signal),
    retry: false,
    placeholderData: (previous, previousQuery) =>
      samePageFamily(previousQuery?.queryKey, listKey) ? previous : undefined,
  });
  const detail = useQuery({
    queryKey: [...charityKeys.root(role), 'model-detail', accountId, selectedId],
    queryFn: ({ signal }) => getManagedCharityModel(role, selectedId, signal),
    retry: false,
    enabled: Boolean(selectedId),
  });
  const selected = detail.data;
  const navigationReady = selectedId
    ? !models.isPending && !models.isFetching && !detail.isPending && !detail.isFetching
    : !models.isPending && !models.isFetching;
  const { listRef, detailRef, remember } = useDetailNavigation(selectedId, navigationReady);
  const capabilityLost = [models.error, detail.error].some(
    (error) => isUnauthorized(error) || isForbidden(error),
  );
  useEffect(() => {
    if (capabilityLost) onCapabilityLoss?.();
  }, [capabilityLost, onCapabilityLoss]);
  const refresh = () => client.invalidateQueries({ queryKey: charityKeys.root(role) });
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
    <div className="ops-stack" ref={listRef} tabIndex={-1}>
      <ModelForm role={role} refresh={refresh} onCapabilityLoss={onCapabilityLoss} />
      <Card>
        <form
          className="ops-toolbar"
          onSubmit={(event) => {
            event.preventDefault();
            if (validManagementSearch(queryDraft)) updateFilters(queryDraft.trim());
          }}
        >
          <label>
            <span>{t('common.operations.charity.searchModels')}</span>
            <input
              maxLength={256}
              aria-invalid={!validManagementSearch(queryDraft)}
              value={queryDraft}
              onChange={(event) => setQueryDraft(event.target.value)}
            />
          </label>
          <label>
            <span>{t(charityCopyKey(role, 'enabled'))}</span>
            <select
              value={enabled}
              onChange={(event) => {
                updateFilters(query, event.target.value);
              }}
            >
              <option value="">{t('common.all')}</option>
              <option value="true">{t(charityCopyKey(role, 'enabled'))}</option>
              <option value="false">{t(charityCopyKey(role, 'disabled'))}</option>
            </select>
          </label>
          <button
            className="btn btn-secondary"
            type="submit"
            disabled={!validManagementSearch(queryDraft)}
          >
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
              <table className="ops-table ops-table--responsive">
                <thead>
                  <tr>
                    <th>{t(charityCopyKey(role, 'model'))}</th>
                    <th>{t('common.operations.charity.state')}</th>
                    <th>{t('common.operations.charity.publicDescription')}</th>
                    <th>{t('common.operations.charity.allowedLevels')}</th>
                    <th>{t('common.operations.charity.pricing')}</th>
                    <th>{t(charityCopyKey(role, 'bindings'))}</th>
                    <th>{t('common.operations.charity.open')}</th>
                  </tr>
                </thead>
                <tbody>
                  {models.data.data.map((model) => (
                    <tr key={model.id}>
                      <td className="ops-cell-wide" data-label={t(charityCopyKey(role, 'model'))}>
                        {model.full_name}
                      </td>
                      <td data-label={t('common.operations.charity.state')}>
                        <StatusBadge
                          active={model.enabled}
                          label={t(charityCopyKey(role, model.enabled ? 'enabled' : 'disabled'))}
                        />
                      </td>
                      <td
                        className="ops-cell-wide ops-model-description-cell"
                        data-label={t('common.operations.charity.publicDescription')}
                      >
                        {model.public_description}
                      </td>
                      <td data-label={t('common.operations.charity.allowedLevels')}>
                        {model.allowed_levels.length > 0
                          ? model.allowed_levels
                              .map((level) => t('common.operations.charity.levelLabel', { level }))
                              .join(', ')
                          : t('common.operations.charity.noAllowedLevels')}
                      </td>
                      <td data-label={t('common.operations.charity.pricing')}>
                        {t(
                          charityCopyKey(
                            role,
                            model.pricing.mode === 'per_request' ? 'perRequest' : 'perToken',
                          ),
                        )}
                      </td>
                      <td data-label={t(charityCopyKey(role, 'bindings'))}>
                        {model.binding_count}
                      </td>
                      <td
                        className="ops-cell-wide"
                        data-label={t('common.operations.charity.open')}
                      >
                        <button
                          className="btn btn-secondary ops-row-action"
                          type="button"
                          disabled={models.isFetching}
                          onClick={() => setSelected(model.id)}
                        >
                          {t('common.operations.charity.manage')}
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </>
        )}
        {models.data && !models.error ? (
          <PagePagination
            metadata={models.data.pagination}
            requestedPage={pager.page}
            onPageChange={pager.setPage}
            onPageSizeChange={pager.setPageSize}
            busy={models.isFetching}
          />
        ) : null}
      </Card>
      {selectedId ? (
        <div className="ops-stack ops-detail-target" ref={detailRef} tabIndex={-1}>
          <button className="btn btn-quiet" type="button" onClick={() => setSelected('')}>
            {t('common.operations.charity.returnToList')}
          </button>
          {detail.isPending ? (
            <LoadingState />
          ) : detail.error ? (
            <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />
          ) : selected ? (
            <fieldset
              className="ops-stack ops-unframed"
              disabled={detail.isFetching || models.isFetching || Boolean(models.error)}
            >
              <ModelForm
                key={`model:${selected.id}`}
                role={role}
                model={selected}
                refresh={refresh}
                onDeleted={() => setSelected('')}
                onCapabilityLoss={onCapabilityLoss}
              />
              <BindingsPanel
                key={`bindings:${selected.id}`}
                role={role}
                accountId={accountId}
                model={selected}
                refresh={refresh}
                onCapabilityLoss={onCapabilityLoss}
              />
            </fieldset>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

export function CharityManagement(props: {
  frame: CharityRole;
  accountId?: string;
  onCapabilityLoss?: () => void;
}) {
  if (!props.accountId) return <LoadingState />;
  return (
    <CharityManagementAccount
      key={`${props.frame}:${props.accountId}`}
      {...props}
      accountId={props.accountId}
    />
  );
}

function CharityManagementAccount({
  frame,
  accountId,
  onCapabilityLoss,
}: {
  frame: CharityRole;
  accountId: string;
  onCapabilityLoss?: () => void;
}) {
  const { t } = useTranslation();
  const [params, setParams] = useSearchState();
  const rawSection = oneParam(params, 'charity_section');
  const section = rawSection === 'models' || rawSection === 'sources' ? rawSection : 'donations';
  const setSection = (nextSection: 'donations' | 'models' | 'sources') =>
    setParams((current) => {
      const next = new URLSearchParams(current);
      next.set('charity_section', nextSection);
      return next;
    });
  const [capabilityLost, setCapabilityLost] = useState(false);
  const clearCapability = useCallback(() => {
    setCapabilityLost(true);
    onCapabilityLoss?.();
  }, [onCapabilityLoss]);
  if (capabilityLost)
    return (
      <Card>
        <p className="field-error" role="alert">
          {t('common.operations.charity.accessLost')}
        </p>
      </Card>
    );
  return (
    <div className="ops-stack charity-management">
      <div
        className="ops-tabs"
        role="tablist"
        aria-label={t('common.operations.charity.sectionsLabel')}
      >
        {(
          [
            [
              'donations',
              frame === 'admin'
                ? t('admin.charity.donationsTitle')
                : t('user.steward.donationsTitle'),
            ],
            [
              'models',
              frame === 'admin' ? t('admin.charity.modelsTitle') : t('user.steward.modelsTitle'),
            ],
            ['sources', t('common.operations.charity.sourceGroups')],
          ] as const
        ).map(([value, label]) => (
          <button
            key={value}
            className={section === value ? 'btn btn-primary' : 'btn btn-secondary'}
            type="button"
            role="tab"
            aria-selected={section === value}
            onClick={() => setSection(value)}
          >
            {label}
          </button>
        ))}
      </div>
      {section === 'donations' ? (
        <DonationsPanel role={frame} accountId={accountId} onCapabilityLoss={clearCapability} />
      ) : section === 'models' ? (
        <ModelsPanel role={frame} accountId={accountId} onCapabilityLoss={clearCapability} />
      ) : (
        <CharitySourceBrowser
          role={frame}
          accountId={accountId}
          enabled
          onCapabilityLoss={clearCapability}
          onOpenDonation={(donationId, keyId) =>
            setParams((current) => {
              const next = new URLSearchParams(current);
              next.set('charity_section', 'donations');
              next.set('donation_id', donationId);
              next.set('donation_from', 'sources');
              for (const name of ['donation_key', 'donation_keys_page', 'donation_keys_page_size'])
                next.delete(name);
              if (keyId) next.set('donation_key', keyId);
              return next;
            })
          }
        />
      )}
    </div>
  );
}
