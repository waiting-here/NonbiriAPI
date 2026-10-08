import { NullableValue } from './charity/NullableValue';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { Card, EmptyState, ErrorState, LoadingState, StatusBadge } from '@shared/components/States';
import { DataTable, FilterBar, Fold, MoreMenu, Tabs } from '@shared/components/ui';
import {
  charityKeys,
  getManagedDonation,
  patchManagedDonationKey,
  reviewManagedDonation,
  type AdminDonation,
  type CharityRole,
  type DonationEndedReason,
  type DonationStatus,
  type ManagedDonationKey,
  type ManagedSafeSource,
  type StewardDonation,
} from '@shared/operations/charity';
import { validManagementSearch } from '@shared/operations/charityModelPages';
import {
  getManagedDonationKeysPage,
  getManagedDonationsPage,
  type ManagedDonationPageFilters,
} from '@shared/operations/donationPages';
import '@shared/operations/operations.css';
import { PagePagination } from '@shared/operations/PagePagination';
import { getScopedDonationKey, patchScopedDonationKey } from '@shared/operations/scopedDonationKey';
import { useDetailNavigation } from '@shared/operations/useDetailNavigation';
import { usePagePager } from '@shared/operations/usePagePager';
import { useSearchState } from '@shared/operations/useSearchState';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import '@shared/styles/charity-management.css';
import { createTimeDraft, timeDraftValue, type TimeDraft, type TimeStation } from '@shared/time';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { useRetainedOperation } from '../../admin/features/operations/useRetainedOperation';
import { ModelsPanel } from './charity/CharityModels';
import {
  charityCopyKey,
  charityStateKey,
  charityStatusKey,
  MAX_TOKEN_RESERVE,
  MAX_UNIX_SECOND,
  oneParam,
  reviewerRoleKey,
  samePageFamily,
  selectedID,
  snapshotPage,
  validAmount,
  validCount,
  validText,
  validTokenReserve,
} from './charity/managementFields';
import { charityControlCopy } from './charityControlCopy';
import { useCharityModelScope } from './charityModelScopeContext';
import { CharitySourceBrowser } from './CharitySourceBrowser';
import { DonationThanks } from './DonationControlFacts';
import { DonationDiscoveryControl } from './DonationDiscoveryControl';
import { DonationHandlingControl, DonationHandlingStatus } from './DonationHandling';
import { donationHandlingStateKey } from './donationHandlingCopy';
import { DonationKeyModels } from './DonationKeyModels';
import { FailurePolicyControl } from './FailurePolicyControl';
import { FailureResetControl } from './FailureResetControl';
import { KeyLimitSummary } from './KeyRoutingLimits';
import { MarkdownText } from './MarkdownText';
import { RecurringLimitsDisclosure } from './RecurringLimitsDisclosure';
import { TimeInput } from './TimeInput';

type ManagedDonation = AdminDonation | StewardDonation;
const approvalOriginCopy = {
  none: 'common.donationReview.origin.none',
  auto: 'common.donationReview.origin.auto',
  manual: 'common.donationReview.origin.manual',
  unknown: 'common.donationReview.origin.unknown',
} as const;
const forceUnavailableCopy: Record<string, string> = {
  not_automatically_approved: 'common.donationReview.unavailable.manual',
  approval_origin_unknown: 'common.donationReview.unavailable.unknown',
  review_material_unavailable: 'common.donationReview.materialUnavailable',
  not_approved: 'common.donationReview.unavailable.notApproved',
};
type DonationReviewInput = {
  revision: string;
  decision: 'approve' | 'reject' | 'force_reject';
  reason: string;
  settings: Record<
    string,
    ReturnType<typeof keySettingsBody> & { expected_review_revision?: string }
  >;
};

interface KeySettingsDraft {
  input_tokens_limit: string | null;
  output_tokens_limit: string | null;
  input_token_reserve: string | null;
  output_token_reserve: string | null;
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
  input_tokens_limit: key.limits.input_tokens ?? null,
  output_tokens_limit: key.limits.output_tokens ?? null,
  input_token_reserve: key.input_token_reserve ?? null,
  output_token_reserve: key.output_token_reserve ?? null,
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
  const split = [
    value.input_tokens_limit,
    value.output_tokens_limit,
    value.input_token_reserve,
    value.output_token_reserve,
  ];
  if (
    split.some(
      (item) =>
        item !== null &&
        (!/^(0|[1-9][0-9]{0,18})$/.test(item) || BigInt(item) > 9_223_372_036_854_775_807n),
    )
  )
    return 'countLimits';
  if (
    (value.input_token_reserve === null) !== (value.output_token_reserve === null) ||
    ((value.input_tokens_limit !== null || value.output_tokens_limit !== null) &&
      value.input_token_reserve === null) ||
    (value.input_token_reserve !== null &&
      value.output_token_reserve !== null &&
      (BigInt(value.input_token_reserve) + BigInt(value.output_token_reserve) === 0n ||
        BigInt(value.input_token_reserve) + BigInt(value.output_token_reserve) >
          9_223_372_036_854_775_807n))
  )
    return 'tokenReserve';
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
    input_tokens_limit: value.input_tokens_limit,
    output_tokens_limit: value.output_tokens_limit,
    input_token_reserve: value.input_token_reserve,
    output_token_reserve: value.output_token_reserve,
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
  const formatDateTime = useDateTimeFormatter();
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

function KeyQuotaFields({
  value,
  onChange,
}: {
  value: Omit<KeySettingsDraft, 'enabled'>;
  onChange: (value: Partial<KeySettingsDraft>) => void;
}) {
  const { t, i18n } = useTranslation();
  const copy = charityControlCopy(i18n.language);
  return (
    <>
      <fieldset className="ops-form-section">
        <legend>{copy.cumulativeLimits}</legend>
        <div className="ops-field-grid">
          <NullableValue
            label={t('common.operations.charity.priceLimitCredits')}
            value={value.price_limit}
            onChange={(next) => onChange({ price_limit: next })}
          />
          <NullableValue
            label={t('common.operations.charity.callLimit')}
            value={value.calls_limit}
            onChange={(next) => onChange({ calls_limit: next })}
          />
          <NullableValue
            label={t('common.operations.charity.tokenLimit')}
            value={value.tokens_limit}
            onChange={(next) => onChange({ tokens_limit: next })}
          />
          <NullableValue
            label={copy.inputTokens}
            value={value.input_tokens_limit}
            onChange={(next) => onChange({ input_tokens_limit: next })}
          />
          <NullableValue
            label={copy.outputTokens}
            value={value.output_tokens_limit}
            onChange={(next) => onChange({ output_tokens_limit: next })}
          />
        </div>
      </fieldset>
      <fieldset className="ops-form-section">
        <legend>{copy.tokenReservations}</legend>
        <p className="muted">{copy.splitHint}</p>
        <div className="ops-field-grid">
          <NullableValue
            label={copy.inputReserve}
            value={value.input_token_reserve}
            onChange={(next) => onChange({ input_token_reserve: next })}
          />
          <NullableValue
            label={copy.outputReserve}
            value={value.output_token_reserve}
            onChange={(next) => onChange({ output_token_reserve: next })}
          />
          <label>
            <span>{t('common.operations.charity.tokenReserve')}</span>
            <input
              type="number"
              min="0"
              max={MAX_TOKEN_RESERVE}
              step="1"
              value={value.token_reserve}
              onChange={(event) => onChange({ token_reserve: Number(event.target.value) })}
            />
          </label>
        </div>
      </fieldset>
    </>
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
  donation: Pick<ManagedDonation, 'id' | 'revision' | 'status'>;
  role: CharityRole;
  refresh: () => Promise<unknown>;
  onCapabilityLoss?: () => void;
}) {
  const formatDateTime = useDateTimeFormatter();
  const { t } = useTranslation();
  const modelID = useCharityModelScope();
  const copy = charityControlCopy(useTranslation().i18n.language);
  const [draft, setDraft] = useState(() => keyManagementDraft(item));
  const [reset, setReset] = useState(false);
  const save = useRetainedOperation<
    ReturnType<typeof keySettingsBody> & { reset_failure_streak: boolean },
    unknown
  >(
    (input, key) =>
      (modelID
        ? (_role: CharityRole, donationID: string, keyID: string, input: unknown, key: string) =>
            patchScopedDonationKey(modelID, donationID, keyID, input, key)
        : patchManagedDonationKey)(
        role,
        donation.id,
        item.id,
        {
          expected_revision: donation.revision,
          ...(input.enabled === null ? {} : { enabled: input.enabled }),
          price_limit: input.price_limit,
          calls_limit: input.calls_limit,
          tokens_limit: input.tokens_limit,
          input_tokens_limit: input.input_tokens_limit,
          output_tokens_limit: input.output_tokens_limit,
          input_token_reserve: input.input_token_reserve,
          output_token_reserve: input.output_token_reserve,
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
    <section className="ops-subcard ops-unframed donation-key-editor">
      <h4>
        {t('common.donationReview.memberHeading', {
          head: item.display_head,
          tail: item.display_tail,
        })}
      </h4>
      <p>{safeSourceLabel(item.safe_source, t)}</p>
      {!modelID ? (
        item.endpoint_key_id ? (
          <div className="ops-actions">
            <Link
              className="nb-btn nb-btn--secondary"
              to={
                role === 'admin'
                  ? `/logs?endpoint_key_id=${encodeURIComponent(item.endpoint_key_id)}`
                  : `/steward?tab=logs&endpoint_key_id=${encodeURIComponent(item.endpoint_key_id)}`
              }
            >
              {t('common.operations.logs.viewKeyLogs')}
            </Link>
          </div>
        ) : (
          <p className="muted">{t('common.operations.logs.keyLogsUnavailable')}</p>
        )
      ) : null}
      <p className="muted">
        {item.safe_source.connector_type} · {item.safe_source.base_url}
      </p>
      <KeyLimitSummary concurrency={item.max_concurrency} rpm={item.max_rpm} readOnly />
      <Fold
        title={t('common.operations.charity.failureStrategy', { defaultValue: 'Failure strategy' })}
      >
        <FailurePolicyControl
          role={role}
          donationID={donation.id}
          keyID={item.id}
          revision={donation.revision}
          threshold={item.failure_disable_threshold}
          refresh={refresh}
        />
      </Fold>
      <h5>{copy.keyUsage}</h5>
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
        <dt>{copy.inputTokens}</dt>
        <dd>
          {item.usage.input_tokens_used ?? '0'} + {item.usage.input_tokens_inflight ?? '0'} /{' '}
          {item.limits.input_tokens ?? t('common.operations.charity.unlimited')}
        </dd>
        <dt>{copy.outputTokens}</dt>
        <dd>
          {item.usage.output_tokens_used ?? '0'} + {item.usage.output_tokens_inflight ?? '0'} /{' '}
          {item.limits.output_tokens ?? t('common.operations.charity.unlimited')}
        </dd>
        <dt>{copy.breakdown}</dt>
        <dd>{item.breakdown_started_at ? formatDateTime(item.breakdown_started_at) : '—'}</dd>
        <dt>{copy.unattributed}</dt>
        <dd>{item.usage.unattributed_total_tokens ?? '0'}</dd>
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
          <Fold
            title={t('common.operations.charity.quotaReserve', {
              defaultValue: 'Quota and reserve',
            })}
          >
            <KeyQuotaFields
              value={draft}
              onChange={(next) => setDraft((current) => ({ ...current, ...next }))}
            />
          </Fold>
          <fieldset className="ops-form-section">
            <legend>{copy.keyManagement}</legend>
            <div className="ops-field-grid">
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
                station={role === 'admin' ? 'admin' : 'steward'}
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
          </fieldset>
          {validationError ? (
            <p className="field-error" role="alert">
              {t(`common.operations.charity.validation.${validationError}`)}
            </p>
          ) : null}
          {save.error ? <ErrorState error={save.error} /> : null}
          <button
            className="nb-btn nb-btn--primary"
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
  const formatDateTime = useDateTimeFormatter();
  const { t } = useTranslation();
  const reviewPager = usePagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'donation-review-keys',
    scopeKey: `${accountId}:${item.id}`,
  });
  const reviewPage = snapshotPage(item.keys, reviewPager.page, reviewPager.pageSize);
  const [decision, setDecision] = useState<DonationReviewInput['decision']>(
    item.status === 'approved' ? 'force_reject' : 'approve',
  );
  const [reason, setReason] = useState('');
  const [confirmed, setConfirmed] = useState(false);
  const [forceConfirmation, setForceConfirmation] = useState(false);
  const [keys, setKeys] = useState<Record<string, KeySettingsDraft>>(() =>
    Object.fromEntries(item.keys.map((key) => [key.id, keySettingsDraft(key)])),
  );
  const review = useRetainedOperation<DonationReviewInput, ManagedDonation>(
    (input, key) =>
      reviewManagedDonation(
        role,
        item.id,
        input.decision !== 'approve'
          ? { decision: input.decision, expected_revision: input.revision, reason: input.reason }
          : {
              decision: 'approve',
              expected_revision: input.revision,
              reason: input.reason,
              key_settings: Object.entries(input.settings).map(([id, settings]) => ({
                donation_key_id: id,
                ...settings,
              })),
            },
        key,
      ),
    async (_input, _error, context) => {
      context.assertCurrent();
      return refresh();
    },
    charityKeys.root(role),
  );
  useEffect(() => {
    onPendingChange(review.isPending);
    return () => onPendingChange(false);
  }, [onPendingChange, review.isPending]);
  const owner = item.owner;
  let validationError:
    KeySettingsValidation | 'reviewReason' | 'completeSettings' | 'reviewMaterial' | null = null;
  if (!validText(reason.trim(), 1_024, true)) {
    validationError = 'reviewReason';
  } else if (decision === 'approve') {
    if (
      item.keys.some(
        (entry) =>
          keys[entry.id]?.enabled &&
          entry.review?.required &&
          (!entry.review.material_available || !entry.review.revision),
      )
    ) {
      validationError = 'reviewMaterial';
    } else if (item.keys.length === 0 || item.keys.some((entry) => !keys[entry.id])) {
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
        <h3>{t('common.donationReview.title')}</h3>
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
          {item.status !== 'pending' ? (
            <span>{t(approvalOriginCopy[item.first_approval_origin ?? 'unknown'])}</span>
          ) : null}
        </div>
        <dl className="ops-kv">
          <dt>{t('common.operations.charity.owner')}</dt>
          <dd>
            {owner ? owner.display_name : t('common.operations.charity.deidentified')}
            {owner
              ? ` · ${owner.discord_id ?? t('common.operations.charity.discordDetached')}`
              : ''}
          </dd>
          <dt>{t('common.operations.charity.donorDescription')}</dt>
          <dd>
            <MarkdownText>
              {item.description || t('common.operations.charity.noDescription')}
            </MarkdownText>
            <DonationThanks value={item.discord_public_thanks} />
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
      {item.status === 'approved' &&
      !item.can_force_reject &&
      item.force_reject_unavailable_reason ? (
        <p role="note">
          {t(
            forceUnavailableCopy[item.force_reject_unavailable_reason] ??
              'common.donationReview.unavailable.unknown',
          )}
        </p>
      ) : null}
      {item.status === 'pending' || item.can_force_reject ? (
        <Card>
          <h3>
            {t(
              item.status === 'pending'
                ? 'common.operations.charity.pendingReviewTitle'
                : 'common.donationReview.forceTitle',
            )}
          </h3>
          <p>
            {t(
              item.status === 'pending'
                ? 'common.operations.charity.pendingReviewBody'
                : 'common.donationReview.forceHelp',
            )}
          </p>
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
                {item.status === 'pending' ? (
                  <>
                    <option value="approve">
                      {t('common.operations.charity.decision.approve')}
                    </option>
                    <option value="reject">{t('common.operations.charity.decision.reject')}</option>
                  </>
                ) : (
                  <option value="force_reject">{t('common.donationReview.forceTitle')}</option>
                )}
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
                    {entry.review?.required ? (
                      <p role="note">
                        {t(
                          entry.review.material_available
                            ? 'common.donationReview.required'
                            : 'common.donationReview.materialUnavailable',
                        )}
                      </p>
                    ) : null}
                    <KeyLimitSummary
                      concurrency={entry.max_concurrency}
                      rpm={entry.max_rpm}
                      readOnly
                    />
                    <p>{t('common.operations.charity.embeddingQuotaHelp')}</p>
                    <Fold
                      title={t('common.operations.charity.quotaReserve', {
                        defaultValue: 'Quota and reserve',
                      })}
                    >
                      <KeyQuotaFields
                        value={draft}
                        onChange={(next) =>
                          setKeys((current) => ({
                            ...current,
                            [entry.id]: { ...current[entry.id], ...next },
                          }))
                        }
                      />
                    </Fold>
                    <div className="ops-field-grid">
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
                        station={role === 'admin' ? 'admin' : 'steward'}
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
          {decision !== 'force_reject' ? (
            <label className="checkbox-label">
              <input
                type="checkbox"
                checked={confirmed}
                onChange={(event) => setConfirmed(event.target.checked)}
              />
              <span>{t('common.operations.charity.confirmReview')}</span>
            </label>
          ) : null}
          {validationError && (reason.length > 0 || confirmed) ? (
            <p className="field-error" role="alert">
              {t(
                validationError === 'reviewMaterial'
                  ? 'common.donationReview.materialUnavailable'
                  : `common.operations.charity.validation.${validationError}`,
              )}
            </p>
          ) : null}
          {review.error ? <ErrorState error={review.error} /> : null}
          {review.outcome === 'unknown' ? (
            <p role="status">{t('common.donationReview.unknown')}</p>
          ) : null}
          {review.outcome === 'unknown' && review.variables ? (
            <button
              type="button"
              className="nb-btn nb-btn--secondary"
              onClick={() => review.variables && review.mutate(review.variables)}
            >
              {t('common.donationReview.retrySame')}
            </button>
          ) : null}
          {review.refreshError ? (
            <ErrorState error={review.refreshError} onRetry={() => void review.refresh()} />
          ) : null}
          <button
            className={decision !== 'approve' ? 'nb-btn nb-btn--danger' : 'nb-btn nb-btn--primary'}
            type="button"
            disabled={
              !reason.trim() ||
              (decision !== 'force_reject' && !confirmed) ||
              review.isPending ||
              review.outcome === 'unknown' ||
              Boolean(validationError)
            }
            onClick={() => {
              if (decision === 'force_reject') {
                setForceConfirmation(true);
                return;
              }
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
                revision: item.revision,
                decision,
                reason: reason.trim(),
                settings:
                  decision === 'approve'
                    ? Object.fromEntries(
                        item.keys.map((entry) => [
                          entry.id,
                          {
                            ...keySettingsBody(keys[entry.id]),
                            ...(keys[entry.id].enabled &&
                            entry.review?.required &&
                            entry.review.revision
                              ? { expected_review_revision: entry.review.revision }
                              : {}),
                          },
                        ]),
                      )
                    : {},
              });
            }}
          >
            {t(
              decision === 'force_reject'
                ? 'common.donationReview.forceTitle'
                : charityCopyKey(role, decision === 'approve' ? 'approve' : 'reject'),
            )}
          </button>
          <ConfirmDialog
            open={forceConfirmation}
            title={t('common.donationReview.forceTitle')}
            description={
              <>
                <p>
                  {owner?.display_name ?? t('common.operations.charity.deidentified')} ·{' '}
                  {item.description}
                </p>
                <p>{t('common.donationReview.forceHelp')}</p>
                <p>
                  {t('common.operations.charity.reason')}: {reason.trim()}
                </p>
              </>
            }
            confirmLabel={t('common.donationReview.forceTitle')}
            confirmDisabled={
              Boolean(validationError) || !item.can_force_reject || review.outcome === 'unknown'
            }
            busy={review.isPending}
            danger
            onCancel={() => setForceConfirmation(false)}
            onConfirm={() => {
              if (validationError || !item.can_force_reject || review.outcome === 'unknown') return;
              review.mutate(
                {
                  revision: item.revision,
                  decision: 'force_reject',
                  reason: reason.trim(),
                  settings: {},
                },
                { onSuccess: () => setForceConfirmation(false) },
              );
            }}
          />
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
      <DonationDiscoveryControl
        key={`discovery:${role}:${accountId}:${item.id}`}
        role={role}
        target={{ donation_id: item.id }}
        disabled={keys.isFetching || Boolean(keys.error) || staleRevision}
        onCapabilityLoss={onCapabilityLoss}
      />
      {keys.data ? (
        <FailureResetControl
          key={`${role}:${accountId}:${item.id}`}
          role={role}
          selection={{ view: 'donation_keys', donation_id: item.id }}
          disabled={keys.isFetching || Boolean(keys.error) || staleRevision}
          onCapabilityLoss={onCapabilityLoss}
          choices={keys.data.data.map((key) => ({
            id: key.id,
            label: `${key.display_head}…${key.display_tail}`,
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
          <button type="button" className="nb-btn nb-btn--secondary" onClick={() => void refresh()}>
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
                <DonationKeyModels
                  role={role}
                  accountId={accountId}
                  donationId={item.id}
                  keyId={key.id}
                  editable={key.charity_state !== 'ended' && key.charity_state !== 'expired'}
                  onCapabilityLoss={onCapabilityLoss}
                />
                <DonationDiscoveryControl
                  key={`discovery:${role}:${accountId}:${key.id}`}
                  role={role}
                  target={{ donation_id: item.id, key_id: key.id }}
                  disabled={key.charity_state !== 'available'}
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
  const [batch, setBatch] = useState<'discovery' | 'reset' | null>(null);
  const resetBatchRef = useRef<HTMLDivElement>(null);
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
    <div className={`charity-donations${selected ? ' nb-md' : ''}`} ref={listRef} tabIndex={-1}>
      <div className="charity-donations__list">
        <Card>
          <div className="charity-donation-toolbar">
            <FilterBar
              secondaryLabel={t('common.filter')}
              activeCount={[handling, status, query].filter(Boolean).length}
              search={
                <div className="charity-donation-search">
                  {' '}
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
                    className="nb-btn nb-btn--secondary"
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
                </div>
              }
              secondary={
                <>
                  {' '}
                  <label>
                    <span>{t('common.donationHandling.filter')}</span>
                    <select
                      value={handling}
                      onChange={(event) => updateFilters(event.target.value, query)}
                    >
                      <option value="">{t('common.donationHandling.all')}</option>
                      {(['pending', 'processed', 'closed', 'legacy'] as const).map((state) => (
                        <option key={state} value={state}>
                          {t(donationHandlingStateKey[state])}
                        </option>
                      ))}
                    </select>
                  </label>
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
                </>
              }
              chips={[
                ...(handling
                  ? [
                      {
                        key: 'handling',
                        label: t(
                          donationHandlingStateKey[
                            handling as keyof typeof donationHandlingStateKey
                          ],
                        ),
                        onRemove: () => updateFilters('', query, status),
                        removeLabel: t('common.resetFilter'),
                      },
                    ]
                  : []),
                ...(status
                  ? [
                      {
                        key: 'status',
                        label: t(charityStatusKey(role, status as DonationStatus)),
                        onRemove: () => updateFilters(handling, query, ''),
                        removeLabel: t('common.resetFilter'),
                      },
                    ]
                  : []),
                ...(query
                  ? [
                      {
                        key: 'query',
                        label: query,
                        onRemove: () => updateFilters(handling, '', status),
                        removeLabel: t('common.resetFilter'),
                      },
                    ]
                  : []),
              ]}
              onClearAll={() => updateFilters('', '', '')}
              clearAllLabel={t('common.resetFilter')}
              onSubmit={() => {
                if (queryValid) updateFilters(handling, queryDraft);
              }}
            />
            <div className="charity-batch-menu">
              <span>
                {t('common.operations.charity.batchActions', { defaultValue: 'Batch actions' })}
              </span>
              <MoreMenu
                label={t('common.operations.charity.batchActions', {
                  defaultValue: 'Batch actions',
                })}
                items={[
                  {
                    label: t('common.operations.charity.batchDiscovery', {
                      defaultValue: 'Discover all donations',
                    }),
                    onSelect: () => setBatch('discovery'),
                  },
                  {
                    label: t('common.operations.charity.batchFailureReset', {
                      defaultValue: 'Reset failure streaks',
                    }),
                    disabled: !list.data || list.isFetching || Boolean(list.error),
                    onSelect: () => {
                      setBatch('reset');
                      const details = resetBatchRef.current?.querySelector('details');
                      if (details) details.open = true;
                    },
                  },
                ]}
              />
            </div>
          </div>
          <div hidden={batch !== 'discovery'} className="charity-batch-section">
            <DonationDiscoveryControl
              key={`discovery-all:${role}:${accountId}`}
              role={role}
              target={{ donation_id: null }}
              onCapabilityLoss={onCapabilityLoss}
            />
          </div>
          <div hidden={batch !== 'reset'} ref={resetBatchRef} className="charity-batch-section">
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
                  label: item.description || t('common.operations.charity.noDescription'),
                  target: { view: 'donation_keys', donation_id: item.id },
                }))}
              />
            ) : null}
          </div>
          {batch ? (
            <button type="button" className="nb-btn nb-btn--ghost" onClick={() => setBatch(null)}>
              {t('common.close')}
            </button>
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
              <DataTable
                caption={t(charityCopyKey(role, 'donationsTitle'))}
                rows={list.data.data}
                rowKey={(item) => item.id}
                selectedKey={selected}
                columns={[
                  {
                    key: 'description',
                    header: t('common.operations.charity.description'),
                    cell: 'title',
                    render: (item) => (
                      <div className="charity-donation-summary">
                        <span className="charity-donation-description">
                          {item.description || t('common.operations.charity.noDescription')}
                        </span>
                        <small
                          className="charity-donation-meta"
                          title={[
                            item.id,
                            ...item.sources.map(
                              (source) =>
                                `${source.kind === 'mainstream' ? `${source.name} · ` : ''}${source.base_url}`,
                            ),
                          ].join(' · ')}
                        >
                          {t('common.itemId')} · {item.id} ·{' '}
                          {item.sources[0] ? safeSourceLabel(item.sources[0], t) : '—'}
                          {BigInt(item.source_count) > 1n
                            ? ' · ' +
                              t('common.operations.charity.sourcePreview', {
                                shown: Math.min(1, item.sources.length),
                                total: item.source_count,
                              })
                            : ''}
                        </small>
                      </div>
                    ),
                  },
                  {
                    key: 'owner',
                    header: t('common.operations.charity.owner'),
                    mobileLabel: t('common.operations.charity.owner'),
                    cell: 'meta',
                    render: (item) =>
                      item.owner?.display_name ?? t('common.operations.charity.deidentified'),
                  },
                  {
                    key: 'status',
                    header: t('common.status'),
                    cell: 'status',
                    render: (item) => (
                      <StatusBadge
                        active={item.status === 'approved'}
                        label={t(charityStatusKey(role, item.status))}
                      />
                    ),
                  },
                  {
                    key: 'handling',
                    header: t('common.donationHandling.title'),
                    mobileLabel: t('common.donationHandling.title'),
                    cell: 'meta',
                    render: (item) => <DonationHandlingStatus handling={item.handling} />,
                  },
                  {
                    key: 'keys',
                    header: t('common.operations.charity.keys'),
                    mobileLabel: t('common.operations.charity.keys'),
                    cell: 'meta',
                    render: (item) => (
                      <>
                        {item.key_count}
                        <small className="charity-available-count">
                          {t(charityStateKey('available'))}: {item.state_counts.available}
                        </small>
                      </>
                    ),
                  },
                  {
                    key: 'actions',
                    header: t('common.operations.charity.open'),
                    cell: 'action',
                    render: (item) => (
                      <button
                        type="button"
                        className="nb-btn nb-btn--secondary ops-row-action"
                        disabled={list.isFetching}
                        onClick={() => setSelected(item.id)}
                      >
                        {t('common.operations.charity.openReview')}
                      </button>
                    ),
                  },
                ]}
              />
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
      </div>
      {selected ? (
        <div className="ops-stack ops-detail-target" ref={detailRef} tabIndex={-1}>
          <button className="nb-btn nb-btn--ghost" type="button" onClick={() => setSelected('')}>
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

export function CharityManagement(props: {
  frame: CharityRole;
  accountId?: string;
  onCapabilityLoss?: () => void;
  trainee?: boolean;
}) {
  if (!props.accountId) return <LoadingState />;
  return (
    <CharityManagementAccount
      key={`${props.frame}:${props.accountId}:${props.trainee ? 'trainee' : 'full'}`}
      {...props}
      accountId={props.accountId}
    />
  );
}

function CharityManagementAccount({
  frame,
  accountId,
  onCapabilityLoss,
  trainee = false,
}: {
  frame: CharityRole;
  accountId: string;
  onCapabilityLoss?: () => void;
  trainee?: boolean;
}) {
  const { t } = useTranslation();
  const [params, setParams] = useSearchState();
  const rawSection = oneParam(params, 'charity_section');
  const section = trainee
    ? 'models'
    : rawSection === 'models' || rawSection === 'sources'
      ? rawSection
      : 'donations';
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
      <Tabs
        label={t('common.operations.charity.sectionsLabel')}
        value={section}
        onChange={setSection}
        tabs={(
          [
            {
              value: 'donations',
              label: t(
                frame === 'admin' ? 'admin.charity.donationsTitle' : 'user.steward.donationsTitle',
              ),
              id: 'charity-tab-donations',
              panelId: 'charity-panel',
            },
            {
              value: 'models',
              label: t(
                frame === 'admin' ? 'admin.charity.modelsTitle' : 'user.steward.modelsTitle',
              ),
              id: 'charity-tab-models',
              panelId: 'charity-panel',
            },
            {
              value: 'sources',
              label: t('common.operations.charity.sourceGroups'),
              id: 'charity-tab-sources',
              panelId: 'charity-panel',
            },
          ] as const
        ).filter((tab) => !trainee || tab.value === 'models')}
      />
      <div id="charity-panel" role="tabpanel" aria-labelledby={`charity-tab-${section}`}>
        {section === 'donations' ? (
          <DonationsPanel role={frame} accountId={accountId} onCapabilityLoss={clearCapability} />
        ) : section === 'models' ? (
          <ModelsPanel
            renderScopedKeys={(props) => <ScopedKeyBrowser key={props.modelID} {...props} />}
            role={frame}
            accountId={accountId}
            onCapabilityLoss={clearCapability}
            trainee={trainee}
          />
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
                for (const name of [
                  'donation_key',
                  'donation_keys_page',
                  'donation_keys_page_size',
                ])
                  next.delete(name);
                if (keyId) next.set('donation_key', keyId);
                return next;
              })
            }
          />
        )}
      </div>
    </div>
  );
}

function ScopedKeyBrowser({
  modelID,
  accountId,
  onCapabilityLoss,
}: {
  modelID: string;
  accountId: string;
  onCapabilityLoss?: () => void;
}) {
  const [selected, setSelected] = useState<{ donationID: string; keyID: string } | null>(null);
  const copy = charityControlCopy(useTranslation().i18n.language);
  const client = useQueryClient();
  const detail = useQuery({
    queryKey: [
      ...charityKeys.root('steward'),
      'scoped-key',
      accountId,
      modelID,
      selected?.donationID,
      selected?.keyID,
    ],
    queryFn: ({ signal }) =>
      getScopedDonationKey(modelID, selected!.donationID, selected!.keyID, signal),
    enabled: selected !== null,
    retry: false,
  });
  const refresh = () => client.invalidateQueries({ queryKey: charityKeys.root('steward') });
  const lost = isUnauthorized(detail.error) || isForbidden(detail.error);
  useEffect(() => {
    if (lost) onCapabilityLoss?.();
  }, [lost, onCapabilityLoss]);
  return (
    <div className="ops-stack">
      <CharitySourceBrowser
        role="steward"
        accountId={accountId}
        enabled
        charityModelID={modelID}
        onCapabilityLoss={onCapabilityLoss}
        onOpenDonation={(donationID, keyID) => {
          if (keyID) setSelected({ donationID, keyID });
        }}
      />
      {selected ? (
        detail.isPending ? (
          <LoadingState />
        ) : detail.error ? (
          <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />
        ) : detail.data ? (
          <Card key={`${modelID}:${detail.data.key_id}:${detail.data.donation_revision}`}>
            <p>{copy.shared}</p>
            <p>
              {copy.totalBindings}: {detail.data.binding_count}
            </p>
            <p>
              {copy.visibleModels}:{' '}
              {(detail.data.visible_models ?? []).map((model) => model.full_name).join(', ') || '—'}{' '}
              {detail.data.visible_models_truncated ? copy.truncated : ''}
            </p>
            <dl className="ops-kv">
              <dt>{copy.donorNote}</dt>
              <dd>{detail.data.donation_note || '—'}</dd>
              <dt>{copy.approvalNote}</dt>
              <dd>{detail.data.approval_note ?? '—'}</dd>
            </dl>
            <DonationKeyEditor
              item={detail.data}
              donation={{
                id: detail.data.donation_id,
                revision: detail.data.donation_revision,
                status: detail.data.charity_state === 'pending' ? 'pending' : 'approved',
              }}
              role="steward"
              refresh={refresh}
              onCapabilityLoss={onCapabilityLoss}
            />
            <DonationDiscoveryControl
              role="steward"
              target={{ donation_id: detail.data.donation_id, key_id: detail.data.key_id }}
              onCapabilityLoss={onCapabilityLoss}
            />
            <DonationKeyModels
              role="steward"
              accountId={accountId}
              donationId={detail.data.donation_id}
              keyId={detail.data.key_id}
              editable={
                detail.data.charity_state !== 'ended' && detail.data.charity_state !== 'expired'
              }
              onCapabilityLoss={onCapabilityLoss}
            />
            <RecurringLimitsDisclosure
              role="steward"
              accountId={accountId}
              donationId={detail.data.donation_id}
              keyId={detail.data.key_id}
              onSaved={() => void refresh()}
              onCapabilityLoss={onCapabilityLoss}
            />
          </Card>
        ) : null
      ) : null}
    </div>
  );
}
