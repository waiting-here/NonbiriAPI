import { useEffect, useId, useState, type FormEvent, type ReactNode } from 'react';
import { Link } from 'react-router';
import { useTranslation } from 'react-i18next';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { CopyValue } from '@shared/components/CopyValue';
import { MarkdownText } from '@shared/components/MarkdownText';
import { CharityPriceTable, type CharityPriceRow } from '@shared/components/CharityPriceTable';
import { Card, EmptyState, ErrorState, StatusBadge } from '@shared/components/States';
import { formatDateTime } from '@shared/utils/datetime';
import { TimeInput } from '@shared/components/TimeInput';
import { RecurringLimitsDisclosure } from '@shared/components/RecurringLimitsDisclosure';
import { createTimeDraft, timeDraftValue, type TimeDraft } from '@shared/time';
import { isConflictError, isResponseUnknown, type CreateDonationInput } from './api';
import { charityControlCopy } from '@shared/components/charityControlCopy';
import { DonationThanks } from '@shared/components/DonationControlFacts';
import { CreditAmount, ExactCount } from './ExactValue';
import { maskedKey } from './format';
import { isDimensionExhausted } from './normalize';
import { DonationResourcePicker } from './DonationResourcePicker';
import { OwnerFailureReset } from './OwnerFailureReset';
import {
  FailurePolicyControl,
  FailureThresholdInput,
} from '@shared/components/FailurePolicyControl';
import { validFailureThreshold } from '@shared/operations/failurePolicy';
import {
  useCreateDonation,
  useEditDonation,
  useTerminateDonation,
  useWithdrawDonation,
} from './queries';
import type {
  CharityCapability,
  Donation,
  DonationIntakeState,
  DonationKey,
  EndpointKeyChoice,
  EndpointOriginMainstream,
} from './types';

const DRAFT_STORAGE_PREFIX = 'nonbiri:charity-donation-draft:v1';

export type DonationOverviewFilter = 'all' | 'available' | 'blocked' | 'ended';

type DonationSelectionMode =
  | { kind: 'custom' }
  | { kind: 'mainstream'; channelId: string; channelName: string }
  | { kind: 'mixed' | 'cross_channel' };

function classifyDonationSelection(choices: readonly EndpointKeyChoice[]): DonationSelectionMode {
  if (choices.length === 0) return { kind: 'custom' };
  const mainstream = choices
    .map((choice) => choice.endpoint.origin)
    .filter((origin): origin is EndpointOriginMainstream => origin.kind === 'mainstream');
  if (mainstream.length === 0) return { kind: 'custom' };
  if (mainstream.length !== choices.length) return { kind: 'mixed' };
  const first = mainstream[0];
  if (mainstream.some((origin) => origin.channelId !== first.channelId)) {
    return { kind: 'cross_channel' };
  }
  return { kind: 'mainstream', channelId: first.channelId, channelName: first.name };
}

function validDonationDescription(value: string): boolean {
  if (!value.trim() || Array.from(value).length > 1024) return false;
  for (const character of value) {
    const codePoint = character.codePointAt(0) ?? 0;
    if (codePoint < 0x20 || (codePoint >= 0x7f && codePoint <= 0x9f)) return false;
  }
  return true;
}

function statusDanger(status: Donation['status']): boolean {
  return status === 'rejected' || status === 'deleted' || status === 'expired';
}

function MutationNotice({ error, successKey }: { error: unknown; successKey?: string }) {
  const { t } = useTranslation();
  if (error) {
    if (isResponseUnknown(error)) {
      return (
        <p className="inline-notice economy-notice economy-notice--warning" role="alert">
          {t('user.charity.mutationResponseUnknown')}
        </p>
      );
    }
    if (isConflictError(error)) {
      return (
        <p className="inline-notice economy-notice economy-notice--warning" role="alert">
          {t('user.charity.mutationConflictRefreshed')}
        </p>
      );
    }
    return <ErrorState error={error} />;
  }
  return successKey ? (
    <p className="inline-notice economy-notice" role="status">
      {t(successKey)}
    </p>
  ) : null;
}

export function CharityCapabilityPanel({ capability }: { capability: CharityCapability }) {
  const { t } = useTranslation();
  const [modelQuery, setModelQuery] = useState('');
  const [pricingMode, setPricingMode] = useState('');
  const available = capability.state === 'available';
  const models = capability.models.filter(
    (model) =>
      (!pricingMode || model.pricing.mode === pricingMode) &&
      model.fullName.toLocaleLowerCase().includes(modelQuery.trim().toLocaleLowerCase()),
  );
  return (
    <Card className="economy-capability-card">
      <div className="card-title-row">
        <div>
          <h2>{t('user.charity.callCapabilityTitle')}</h2>
        </div>
        <StatusBadge
          active={available}
          danger={capability.state === 'feature_disabled'}
          label={t(`user.charity.capabilityState.${capability.state}`)}
        />
      </div>
      <p>{t(`user.charity.capabilityBody.${capability.state}`)}</p>
      {available && (capability.models.length > 1 || modelQuery || pricingMode) ? (
        <div className="economy-model-filters">
          <label>
            <span>{t('user.charity.searchModels')}</span>
            <input
              type="search"
              value={modelQuery}
              maxLength={133}
              onChange={(event) => setModelQuery(event.target.value)}
            />
          </label>
          <label>
            <span>{t('user.charity.pricingFilter')}</span>
            <select
              aria-label={t('user.charity.pricingFilter')}
              value={pricingMode}
              onChange={(event) => setPricingMode(event.target.value)}
            >
              <option value="">{t('user.charity.allPricing')}</option>
              <option value="per_request">{t('user.charity.requestPrice')}</option>
              <option value="per_token">{t('user.charity.tokenPricing')}</option>
            </select>
          </label>
          <span className="muted">
            {t('common.choices.count', { count: models.length, total: capability.models.length })}
          </span>
        </div>
      ) : null}
      {available && models.length === 0 ? <p>{t('common.noResultsBody')}</p> : null}
      {available ? (
        <ul className="economy-model-list" aria-label={t('user.charity.availableModels')}>
          {models.map((model) => {
            const rows: CharityPriceRow[] =
              model.pricing.mode === 'per_request'
                ? [
                    {
                      label: t('user.charity.requestPrice'),
                      userMilli: model.pricing.userPriceMilli,
                      discountedUserMilli: model.pricing.discountedUserPriceMilli,
                    },
                  ]
                : [
                    {
                      label: t('user.charity.uncachedInputPrice'),
                      userMilli: model.pricing.userPricesMilli.uncachedInput,
                      discountedUserMilli: model.pricing.discountedUserPricesMilli.uncachedInput,
                    },
                    {
                      label: t('user.charity.cacheWriteInputPrice'),
                      userMilli: model.pricing.userPricesMilli.cacheWriteInput,
                      discountedUserMilli: model.pricing.discountedUserPricesMilli.cacheWriteInput,
                    },
                    {
                      label: t('user.charity.cacheReadInputPrice'),
                      userMilli: model.pricing.userPricesMilli.cacheReadInput,
                      discountedUserMilli: model.pricing.discountedUserPricesMilli.cacheReadInput,
                    },
                    {
                      label: t('user.charity.outputPrice'),
                      userMilli: model.pricing.userPricesMilli.output,
                      discountedUserMilli: model.pricing.discountedUserPricesMilli.output,
                    },
                  ];
            return (
              <li key={model.id}>
                <div className="economy-model-heading">
                  <CopyValue value={model.fullName} label={t('user.charity.modelName')} />
                </div>
                <CharityPriceTable
                  mode={model.pricing.mode}
                  rows={rows}
                  serverNow={capability.serverNow}
                  discount={{
                    enabled: model.discount.enabled,
                    percent: model.discount.percent,
                    ...(model.discount.startAt !== null ? { startAt: model.discount.startAt } : {}),
                    ...(model.discount.endAt !== null ? { endAt: model.discount.endAt } : {}),
                  }}
                />
              </li>
            );
          })}
        </ul>
      ) : null}
    </Card>
  );
}

export function DonationIntakePanel({ state }: { state: DonationIntakeState }) {
  const { t } = useTranslation();
  return (
    <Card className="economy-capability-card">
      <div className="card-title-row">
        <div>
          <h2>{t('user.charity.intakeTitle')}</h2>
        </div>
        <StatusBadge
          active={state === 'open'}
          danger={state === 'closed'}
          label={t(`user.charity.intakeState.${state}`)}
        />
      </div>
      <p>{t(`user.charity.intakeBody.${state}`)}</p>
    </Card>
  );
}

function draftStorageKey(namespace: string): string {
  return `${DRAFT_STORAGE_PREFIX}:${namespace}`;
}

function parseDraft(namespace: string): string {
  try {
    const raw = sessionStorage.getItem(draftStorageKey(namespace));
    if (!raw) return '';
    const parsed = JSON.parse(raw) as unknown;
    if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) return '';
    const description = (parsed as Record<string, unknown>).description;
    return typeof description === 'string' && validDonationDescription(description)
      ? description
      : '';
  } catch {
    return '';
  }
}

function storeDraft(namespace: string, description: string): void {
  try {
    const key = draftStorageKey(namespace);
    if (description) sessionStorage.setItem(key, JSON.stringify({ description }));
    else sessionStorage.removeItem(key);
  } catch {
    // Draft persistence is an optional convenience; the form remains usable.
  }
}

export function DonationComposer({
  draftNamespace,
  notice,
  enabled = true,
}: {
  draftNamespace: string;
  notice?: string;
  enabled?: boolean;
}) {
  const { t } = useTranslation();
  const donationNotice = notice?.trim() ? notice : t('user.charity.donationNoticeDefault');
  const formID = useId();
  const mutation = useCreateDonation();
  const [description, setDescription] = useState(() => parseDraft(draftNamespace));
  const [publicThanks, setPublicThanks] = useState<'' | 'yes' | 'no'>('');
  const controlCopy = charityControlCopy(useTranslation().i18n.language);
  const [selectedChoices, setSelectedChoices] = useState<readonly EndpointKeyChoice[]>([]);
  const [expiryByKey, setExpiryByKey] = useState<Record<string, TimeDraft>>({});
  const [thresholdByKey, setThresholdByKey] = useState<Record<string, string>>({});
  const [readBlocked, setReadBlocked] = useState(true);
  const [authorized, setAuthorized] = useState(false);
  const [validation, setValidation] = useState('');
  const [success, setSuccess] = useState(false);
  const [blockedAuthority, setBlockedAuthority] = useState<{ baselineGeneration: number } | null>(
    null,
  );
  const authorityAdvanced =
    blockedAuthority !== null && mutation.reconcileGeneration > blockedAuthority.baselineGeneration;
  const waitingForAuthority = blockedAuthority !== null && !authorityAdvanced;
  const selectionMode = classifyDonationSelection(selectedChoices);
  const locked = !enabled || mutation.isPending || mutation.isReconciling || waitingForAuthority;
  const invalidSelection =
    selectedChoices.length === 0 ||
    selectedChoices.length > 100 ||
    selectedChoices.some((choice) => choice.eligibility !== 'eligible') ||
    new Set(selectedChoices.map((choice) => choice.key.id)).size !== selectedChoices.length;
  const invalidExpiry = selectedChoices.some(
    (choice) =>
      expiryByKey[choice.key.id] && timeDraftValue(expiryByKey[choice.key.id]) === undefined,
  );

  useEffect(() => {
    storeDraft(draftNamespace, description);
  }, [description, draftNamespace]);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (locked || readBlocked) return;
    setValidation('');
    setSuccess(false);
    setBlockedAuthority(null);
    mutation.reset();
    if (!validDonationDescription(description)) {
      setValidation(t('user.charity.descriptionInvalid'));
      return;
    }
    if (invalidSelection) {
      setValidation(t('user.charity.chooseExistingKeys'));
      return;
    }
    if (!authorized) {
      setValidation(t('user.charity.authorizationRequired'));
      return;
    }
    if (publicThanks === '') {
      setValidation(controlCopy.thanks);
      return;
    }
    if (selectionMode.kind === 'mixed' || selectionMode.kind === 'cross_channel') {
      setValidation(t('user.charity.splitDonationSources'));
      return;
    }
    if (
      selectedChoices.some(
        (choice) => !validFailureThreshold(thresholdByKey[choice.key.id] ?? '10'),
      )
    ) {
      setValidation(t('common.failurePolicy.invalid'));
      return;
    }
    const keys = selectedChoices.map((choice) => {
      const failureDisableThreshold = thresholdByKey[choice.key.id] ?? '10';
      if (!validFailureThreshold(failureDisableThreshold)) return undefined;
      const expiresAt = expiryByKey[choice.key.id]
        ? timeDraftValue(expiryByKey[choice.key.id])
        : null;
      return expiresAt === undefined
        ? undefined
        : { endpointKeyId: choice.key.id, expiresAt, failureDisableThreshold };
    });
    if (keys.some((key) => key === undefined)) {
      setValidation(t('user.charity.expiryInvalid'));
      return;
    }
    const input: CreateDonationInput = {
      discordPublicThanks: publicThanks === 'yes',
      description,
      keys: keys as CreateDonationInput['keys'],
      ownershipAuthorized: true,
    };
    const baselineGeneration = mutation.reconcileGeneration;
    try {
      await mutation.mutateAsync(input);
      setDescription('');
      setSelectedChoices([]);
      setExpiryByKey({});
      setThresholdByKey({});
      setAuthorized(false);
      setPublicThanks('');
      storeDraft(draftNamespace, '');
      setSuccess(true);
      setBlockedAuthority({ baselineGeneration });
    } catch (error) {
      if (isConflictError(error) || isResponseUnknown(error)) {
        setSelectedChoices([]);
        setExpiryByKey({});
        setAuthorized(false);
        setBlockedAuthority({ baselineGeneration });
      }
      if (isConflictError(error)) setValidation(t('user.charity.reselectAfterConflict'));
    }
  };

  return (
    <Card className="economy-donation-composer">
      <div className="card-title-row">
        <div>
          <p className="eyebrow">{t('user.charity.existingResourcesOnly')}</p>
          <h2>{t('user.charity.submitDonation')}</h2>
        </div>
      </div>
      <section className="economy-donation-notice" aria-labelledby="donation-notice-title">
        <h3 id="donation-notice-title">{t('user.charity.donationNoticeTitle')}</h3>
        <MarkdownText>{donationNotice}</MarkdownText>
      </section>
      <div className="economy-donation-form">
        <form id={formID} onSubmit={submit} noValidate>
          <label className="full-width">
            <span>{controlCopy.thanks}</span>
            <select
              aria-label={controlCopy.thanks}
              value={publicThanks}
              required
              disabled={locked}
              onChange={(event) => setPublicThanks(event.target.value as '' | 'yes' | 'no')}
            >
              <option value="">{controlCopy.choose}</option>
              <option value="yes">{controlCopy.yes}</option>
              <option value="no">{controlCopy.no}</option>
            </select>
            <small>{controlCopy.thanksHint}</small>
          </label>
          <label className="full-width">
            <span>{t('user.charity.donationDescription')}</span>
            <textarea
              value={description}
              disabled={locked}
              onChange={(event) => setDescription(event.target.value)}
              required
              aria-invalid={Boolean(validation) && !validDonationDescription(description)}
              aria-describedby={
                validation && !validDonationDescription(description)
                  ? formID + '-description-error'
                  : undefined
              }
            />
            {validation && !validDonationDescription(description) ? (
              <p id={formID + '-description-error'} className="field-error" role="alert">
                {validation}
              </p>
            ) : null}
          </label>
        </form>
        <DonationResourcePicker
          accountId={draftNamespace}
          selected={selectedChoices}
          onChange={setSelectedChoices}
          disabled={locked}
          enabled={enabled}
          onReadStateChange={setReadBlocked}
          renderSelected={(choice) => (
            <div className="economy-key-expiry">
              <span>{t('user.charity.keyExpiry')}</span>
              <TimeInput
                station="user"
                disabled={locked}
                draft={expiryByKey[choice.key.id] ?? createTimeDraft()}
                onChange={(update) =>
                  setExpiryByKey((current) => ({
                    ...current,
                    [choice.key.id]: update(current[choice.key.id] ?? createTimeDraft()),
                  }))
                }
                onClick={(event) => event.stopPropagation()}
                aria-label={t('user.charity.keyExpiryFor', {
                  key: maskedKey(choice.key.displayHead, choice.key.displayTail),
                })}
              />
              <small className="muted">{t('user.charity.expiryHint')}</small>
              <FailureThresholdInput
                value={thresholdByKey[choice.key.id] ?? '10'}
                disabled={locked}
                onChange={(value) =>
                  setThresholdByKey((current) => ({ ...current, [choice.key.id]: value }))
                }
              />
            </div>
          )}
        />
        {selectedChoices.length > 0 ? (
          <p className="inline-notice" role="status">
            {selectionMode.kind === 'mainstream'
              ? t('user.charity.selectedMainstreamChannel', { name: selectionMode.channelName })
              : selectionMode.kind === 'custom'
                ? t('user.charity.selectedCustomSources')
                : t('user.charity.splitDonationSources')}
          </p>
        ) : null}
        <section className="economy-disclosure" aria-labelledby="donation-disclosure-title">
          <h3 id="donation-disclosure-title">{t('user.charity.disclosureTitle')}</h3>
          <ul>
            <li>{t('user.charity.disclosureMasked')}</li>
            <li>{t('user.charity.disclosureThirdParty')}</li>
            <li>{t('user.charity.disclosureCost')}</li>
            <li>{t('user.charity.disclosureManagement')}</li>
            <li>{t('user.charity.disclosureLimitCounts')}</li>
            <li>{t('user.charity.disclosureResponsibility')}</li>
            <li>{t('user.charity.disclosureDeletion')}</li>
          </ul>
        </section>
        <label className="checkbox-label economy-authorization">
          <input
            type="checkbox"
            checked={authorized}
            form={formID}
            disabled={locked}
            onChange={(event) => setAuthorized(event.target.checked)}
          />
          <span>{t('user.charity.ownershipAuthorization')}</span>
        </label>
        {validation && validDonationDescription(description) ? (
          <p className="field-error" role="alert">
            {validation}
          </p>
        ) : null}
        <MutationNotice
          error={authorityAdvanced ? null : mutation.error}
          successKey={
            success
              ? 'user.charity.submitted'
              : authorityAdvanced
                ? 'user.charity.mutationReconciled'
                : undefined
          }
        />
        {waitingForAuthority && mutation.reconcileError ? (
          <ErrorState
            error={mutation.reconcileError}
            onRetry={() => void mutation.retryReconcile()}
          />
        ) : null}
        <div className="form-actions">
          <button
            className="btn btn-primary"
            type="submit"
            form={formID}
            disabled={locked || readBlocked || invalidSelection || invalidExpiry}
          >
            {mutation.isPending || mutation.isReconciling
              ? t('common.working')
              : t('user.charity.submit')}
          </button>
        </div>
      </div>
    </Card>
  );
}

function keyStateKeys(key: DonationKey): string[] {
  if (key.charityState === 'expired') return ['expired'];
  if (key.charityState === 'ended') return ['ended'];
  if (key.charityState === 'pending') return ['pending'];
  const states: string[] = [];
  if (!key.physicalEnabled) states.push('physical_disabled');
  if (key.charityState === 'suspended') states.push('suspended');
  if (key.streak.failureDisabled) states.push('failure_disabled');
  if (key.charityState === 'disabled' && key.physicalEnabled && !key.streak.failureDisabled) {
    states.push('charity_paused');
  }
  if (key.charityState === 'exhausted') {
    if (
      isDimensionExhausted(key.limits.price, key.usage.priceUsed, key.usage.priceInflight, true)
    ) {
      states.push('price_exhausted');
    }
    if (isDimensionExhausted(key.limits.calls, key.usage.callsUsed, key.usage.callsInflight)) {
      states.push('calls_exhausted');
    }
    if (isDimensionExhausted(key.limits.tokens, key.usage.tokensUsed, key.usage.tokensInflight)) {
      states.push('tokens_exhausted');
    }
    if (states.length === 0) states.push('exhausted');
  }
  if (key.charityState === 'available' && states.length === 0) states.push('available');
  return states.length > 0 ? states : [key.charityState];
}

function keyBlockingReasons(key: DonationKey): string[] {
  if (key.charityState === 'ended' || key.charityState === 'expired') return [];
  return keyStateKeys(key).filter((state) => state !== 'available');
}

function LimitValue({
  limit,
  used,
  inflight,
  amount = false,
  unit,
}: {
  limit: string | null;
  used: string;
  inflight: string;
  amount?: boolean;
  unit?: string;
}) {
  const { t } = useTranslation();
  const value = (input: string) =>
    amount ? <CreditAmount value={input} /> : <ExactCount value={input} unit={unit} />;
  return (
    <div className="economy-limit-values">
      <span>
        {t('user.charity.limit')}: {limit === null ? t('user.charity.unlimited') : value(limit)}
      </span>
      <span>
        {t('user.charity.used')}: {value(used)}
      </span>
      <span>
        {t('user.charity.inflight')}: {value(inflight)}
      </span>
    </div>
  );
}

export function DonationKeyPanel({
  donationKey,
  accountID,
  donationId,
  donationStatus,
  donationRevision,
  compact = false,
  returnTo,
  ruleSummary,
}: {
  donationKey: DonationKey;
  accountID?: string;
  donationId?: string;
  donationStatus?: Donation['status'];
  donationRevision?: string;
  compact?: boolean;
  returnTo?: string;
  ruleSummary?: ReactNode;
}) {
  const { t, i18n } = useTranslation();
  const controlCopy = charityControlCopy(i18n.language);
  const states = keyStateKeys(donationKey);
  const blockingReasons = keyBlockingReasons(donationKey);
  return (
    <article className="economy-donation-key">
      <div className="item-header">
        <div>
          {donationId ? (
            <Link
              className="eyebrow"
              to={`/charity/donations/${donationId}`}
              state={returnTo ? { returnTo } : undefined}
            >
              {t('user.charity.donationNumber', { id: donationId })}
            </Link>
          ) : null}
          <h4 className="mono">{maskedKey(donationKey.displayHead, donationKey.displayTail)}</h4>
          <p className="item-meta">
            {donationKey.source.kind === 'mainstream'
              ? `${donationKey.source.name} · ${donationKey.source.baseUrl} · ${donationKey.source.connectorType}`
              : `${t('user.charity.customSource')} · ${donationKey.source.baseUrl} · ${donationKey.source.connectorType}`}
          </p>
        </div>
        <div className="economy-status-stack">
          {donationStatus ? (
            <StatusBadge
              active={donationStatus === 'approved'}
              danger={statusDanger(donationStatus)}
              label={t(`user.charity.status.${donationStatus}`)}
            />
          ) : null}
          <StatusBadge
            active={donationKey.physicalEnabled}
            danger={!donationKey.physicalEnabled}
            label={
              donationKey.physicalEnabled
                ? t('user.charity.physicalEnabled')
                : t('user.charity.physicalDisabled')
            }
          />
          <StatusBadge
            active={donationKey.charityState === 'available'}
            danger={donationKey.charityState === 'ended' || donationKey.charityState === 'expired'}
            label={t(`user.charity.keyState.${donationKey.charityState}`)}
          />
          {states
            .filter((state) => state !== donationKey.charityState)
            .map((state) => (
              <StatusBadge
                key={state}
                active={state === 'available'}
                danger={
                  state.includes('expired') || state === 'ended' || state.includes('disabled')
                }
                label={t(`user.charity.keyState.${state}`)}
              />
            ))}
        </div>
      </div>
      <dl className="detail-grid economy-key-status-details">
        {blockingReasons.length > 0 ? (
          <div className="detail-row">
            <dt>{t('user.charity.blockingReasons')}</dt>
            <dd>
              {blockingReasons.length === 0
                ? t('user.charity.noBlockingReason')
                : blockingReasons.map((state) => t(`user.charity.keyState.${state}`)).join(' · ')}
            </dd>
          </div>
        ) : null}
        <div className="detail-row">
          <dt>{t('user.charity.effectiveExpiry')}</dt>
          <dd>
            {donationKey.expiresAt === null
              ? t('user.charity.never')
              : formatDateTime(donationKey.expiresAt)}
          </dd>
        </div>
      </dl>
      <details className="economy-key-details" open={!compact}>
        <summary>{t('user.charity.quotaDetails')}</summary>
        <div className="economy-limit-grid">
          <section>
            <h5>{t('user.charity.priceQuota')}</h5>
            <LimitValue
              limit={donationKey.limits.price}
              used={donationKey.usage.priceUsed}
              inflight={donationKey.usage.priceInflight}
              amount
            />
          </section>
          <section>
            <h5>{t('user.charity.callQuota')}</h5>
            <LimitValue
              limit={donationKey.limits.calls}
              used={donationKey.usage.callsUsed}
              inflight={donationKey.usage.callsInflight}
            />
          </section>
          <section>
            <h5>{t('user.charity.tokenQuota')}</h5>
            <LimitValue
              limit={donationKey.limits.tokens}
              used={donationKey.usage.tokensUsed}
              inflight={donationKey.usage.tokensInflight}
              unit={t('user.charity.tokensUnit')}
            />
          </section>
          <section>
            <h5>{controlCopy.inputTokens}</h5>
            <LimitValue
              limit={donationKey.limits.inputTokens ?? null}
              used={donationKey.usage.inputTokensUsed ?? '0'}
              inflight={donationKey.usage.inputTokensInflight ?? '0'}
              unit={t('user.charity.tokensUnit')}
            />
          </section>
          <section>
            <h5>{controlCopy.outputTokens}</h5>
            <LimitValue
              limit={donationKey.limits.outputTokens ?? null}
              used={donationKey.usage.outputTokensUsed ?? '0'}
              inflight={donationKey.usage.outputTokensInflight ?? '0'}
              unit={t('user.charity.tokensUnit')}
            />
          </section>
        </div>
        <dl className="detail-grid economy-key-metadata">
          {donationKey.inputTokenReserve != null && donationKey.outputTokenReserve != null ? (
            <>
              <div className="detail-row">
                <dt>{controlCopy.inputReserve}</dt>
                <dd>
                  <ExactCount value={donationKey.inputTokenReserve} />
                </dd>
              </div>
              <div className="detail-row">
                <dt>{controlCopy.outputReserve}</dt>
                <dd>
                  <ExactCount value={donationKey.outputTokenReserve} />
                </dd>
              </div>
            </>
          ) : null}
          {donationKey.breakdownStartedAt ? (
            <div className="detail-row">
              <dt>{controlCopy.breakdown}</dt>
              <dd>{formatDateTime(donationKey.breakdownStartedAt)}</dd>
            </div>
          ) : null}
          <div className="detail-row">
            <dt>{controlCopy.unattributed}</dt>
            <dd>
              <ExactCount value={donationKey.usage.unattributedTotalTokens ?? '0'} />
            </dd>
          </div>
          <div className="detail-row">
            <dt>{t('user.charity.tokenReserve')}</dt>
            <dd>
              <ExactCount
                value={String(donationKey.tokenReserve)}
                unit={t('user.charity.tokensPerRequest')}
              />
            </dd>
          </div>
          <div className="detail-row">
            <dt>{t('user.charity.failureStreak')}</dt>
            <dd>
              <ExactCount value={donationKey.streak.count} />
            </dd>
          </div>
          <div className="detail-row">
            <dt>{t('user.charity.failureDisabled')}</dt>
            <dd>
              {donationKey.streak.failureDisabled
                ? t('user.charity.failureDisabledYes')
                : t('user.charity.failureDisabledNo')}
            </dd>
          </div>
          <div className="detail-row">
            <dt>{t('user.charity.endedReason')}</dt>
            <dd>
              {donationKey.endedReason
                ? t(`user.charity.endedReasonValue.${donationKey.endedReason}`)
                : t('user.charity.notEnded')}
            </dd>
          </div>
        </dl>
      </details>
      {ruleSummary}
      {accountID && donationId && donationRevision ? (
        <FailurePolicyControl
          key={`policy:${accountID}:${donationId}:${donationKey.id}`}
          role="owner"
          donationID={donationId}
          keyID={donationKey.id}
          revision={donationRevision}
          threshold={donationKey.failureDisableThreshold}
        />
      ) : null}
      {accountID && donationId && donationRevision ? (
        <OwnerFailureReset
          key={`${accountID}:${donationId}:${donationKey.id}`}
          donationID={donationId}
          keyID={donationKey.id}
          revision={donationRevision}
          disabled={
            ['pending', 'expired', 'ended'].includes(donationKey.charityState) ||
            (donationStatus !== undefined && donationStatus !== 'approved')
          }
        />
      ) : null}
      {accountID && donationId ? (
        <RecurringLimitsDisclosure
          key={`${accountID}:${donationId}:${donationKey.id}`}
          role="owner"
          accountId={accountID}
          donationId={donationId}
          keyId={donationKey.id}
        />
      ) : null}
    </article>
  );
}

export function DonationCard({
  donation,
  accountID,
  showDetailLink = true,
  visibleKeys,
  keysContent,
  disabled = false,
}: {
  donation: Donation;
  accountID?: string;
  showDetailLink?: boolean;
  visibleKeys?: readonly DonationKey[];
  keysContent?: ReactNode;
  disabled?: boolean;
}) {
  const { t } = useTranslation();
  const edit = useEditDonation();
  const withdraw = useWithdrawDonation();
  const terminate = useTerminateDonation();
  const [editing, setEditing] = useState(false);
  const [description, setDescription] = useState(donation.description);
  const [confirmation, setConfirmation] = useState<'withdraw' | 'terminate' | null>(null);
  const [localError, setLocalError] = useState<unknown>(null);
  const [validation, setValidation] = useState('');
  const [successKey, setSuccessKey] = useState<string>();
  const [blockedAuthority, setBlockedAuthority] = useState<{
    operation: 'edit' | 'withdraw' | 'terminate';
    baselineGeneration: number;
  } | null>(null);
  const blockedMutation =
    blockedAuthority?.operation === 'edit'
      ? edit
      : blockedAuthority?.operation === 'withdraw'
        ? withdraw
        : blockedAuthority?.operation === 'terminate'
          ? terminate
          : null;
  const authorityAdvanced = Boolean(
    blockedAuthority &&
    blockedMutation &&
    blockedMutation.reconcileGeneration > blockedAuthority.baselineGeneration,
  );
  const waitingForAuthority = blockedAuthority !== null && !authorityAdvanced;
  const busy =
    disabled ||
    edit.isPending ||
    withdraw.isPending ||
    terminate.isPending ||
    edit.isReconciling ||
    withdraw.isReconciling ||
    terminate.isReconciling ||
    waitingForAuthority;
  const visibleError = authorityAdvanced ? null : localError;
  const visibleSuccessKey = authorityAdvanced ? 'user.charity.mutationReconciled' : successKey;
  const showEditor = editing && donation.status === 'pending';

  const save = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setLocalError(null);
    setValidation('');
    setSuccessKey(undefined);
    setBlockedAuthority(null);
    if (!validDonationDescription(description)) {
      setValidation(t('user.charity.descriptionInvalid'));
      return;
    }
    const baselineGeneration = edit.reconcileGeneration;
    try {
      await edit.mutateAsync({
        id: donation.id,
        description,
        expectedRevision: donation.revision,
      });
      setEditing(false);
      setSuccessKey('user.charity.descriptionSaved');
    } catch (error) {
      setLocalError(error);
      if (isConflictError(error) || isResponseUnknown(error)) {
        setBlockedAuthority({ operation: 'edit', baselineGeneration });
      }
    }
  };

  const confirm = async () => {
    if (!confirmation) return;
    setLocalError(null);
    setValidation('');
    setSuccessKey(undefined);
    setBlockedAuthority(null);
    const operation = confirmation;
    const operationMutation = operation === 'withdraw' ? withdraw : terminate;
    const baselineGeneration = operationMutation.reconcileGeneration;
    try {
      if (operation === 'withdraw') {
        await withdraw.mutateAsync({ id: donation.id, expectedRevision: donation.revision });
        setSuccessKey('user.charity.withdrawn');
      } else {
        await terminate.mutateAsync({ id: donation.id, expectedRevision: donation.revision });
        setSuccessKey('user.charity.terminated');
      }
      setConfirmation(null);
    } catch (error) {
      setLocalError(error);
      if (isConflictError(error) || isResponseUnknown(error)) {
        setBlockedAuthority({ operation, baselineGeneration });
      }
      setConfirmation(null);
    }
  };

  return (
    <Card className="economy-donation-card">
      <div className="item-header">
        <div>
          <h3>{t('user.charity.donationNumber', { id: donation.id })}</h3>
          <p className="item-meta">{formatDateTime(donation.createdAt)}</p>
        </div>
        <StatusBadge
          active={donation.status === 'approved'}
          danger={statusDanger(donation.status)}
          label={t(`user.charity.status.${donation.status}`)}
        />
      </div>

      {showEditor ? (
        <>
          <DonationThanks value={donation.discordPublicThanks} />
          <form onSubmit={save} className="economy-description-editor" noValidate>
            <label>
              <span>{t('user.charity.donationDescription')}</span>
              <textarea
                value={description}
                required
                aria-invalid={Boolean(validation) && !validDonationDescription(description)}
                onChange={(event) => setDescription(event.target.value)}
              />
              {validation && !validDonationDescription(description) ? (
                <p className="field-error" role="alert">
                  {validation}
                </p>
              ) : null}
            </label>
            <p className="muted">{t('user.charity.pendingDescriptionOnly')}</p>
            <div className="form-actions">
              <button
                type="button"
                className="btn btn-quiet"
                onClick={() => setEditing(false)}
                disabled={busy}
              >
                {t('common.cancel')}
              </button>
              <button type="submit" className="btn btn-primary" disabled={busy}>
                {busy ? t('common.working') : t('common.save')}
              </button>
            </div>
          </form>
        </>
      ) : (
        <>
          <MarkdownText>{donation.description}</MarkdownText>
          <DonationThanks value={donation.discordPublicThanks} />
        </>
      )}

      <dl className="detail-grid">
        <div className="detail-row">
          <dt>{t('user.charity.updatedAt')}</dt>
          <dd>{formatDateTime(donation.updatedAt)}</dd>
        </div>
      </dl>

      {donation.reviewResult ? (
        <details className="economy-review-result" open={!showDetailLink}>
          <summary>{t('user.charity.reviewResult')}</summary>
          <MarkdownText>{donation.reviewResult.reason}</MarkdownText>
          <span className="muted">{formatDateTime(donation.reviewResult.reviewedAt)}</span>
        </details>
      ) : null}

      {keysContent !== undefined ? (
        keysContent
      ) : visibleKeys ? (
        <div className="economy-donation-key-list">
          {visibleKeys.map((key) => (
            <DonationKeyPanel
              key={key.id}
              donationKey={key}
              donationId={donation.id}
              donationRevision={donation.revision}
              donationStatus={donation.status}
              accountID={accountID}
              compact
            />
          ))}
          {visibleKeys.length === 0 ? (
            <p className="muted">{t('user.charity.noRemainingKeys')}</p>
          ) : null}
        </div>
      ) : (
        <details className="economy-donation-details">
          <summary>{t('user.charity.resourceDetails', { count: donation.keys.length })}</summary>
          {donation.keys.length > 0 ? (
            <div className="economy-donation-key-list">
              {donation.keys.map((key) => (
                <DonationKeyPanel
                  key={key.id}
                  donationKey={key}
                  donationId={donation.id}
                  donationRevision={donation.revision}
                  donationStatus={donation.status}
                  accountID={accountID}
                />
              ))}
            </div>
          ) : (
            <p className="muted">{t('user.charity.noRemainingKeys')}</p>
          )}
        </details>
      )}

      <MutationNotice error={visibleError} successKey={visibleSuccessKey} />
      {waitingForAuthority && blockedMutation?.reconcileError ? (
        <ErrorState
          error={blockedMutation.reconcileError}
          onRetry={() => void blockedMutation.retryReconcile()}
        />
      ) : null}
      {validation && validDonationDescription(description) ? (
        <p className="field-error" role="alert">
          {validation}
        </p>
      ) : null}
      <div className="form-actions economy-donation-actions">
        {showDetailLink ? (
          <Link className="btn btn-quiet" to={`/charity/donations/${donation.id}`}>
            {t('user.charity.openDonationDetail')}
          </Link>
        ) : null}
        {donation.status === 'pending' ? (
          <>
            <button
              type="button"
              className="btn btn-secondary"
              onClick={() => {
                setDescription(donation.description);
                setEditing(true);
                setLocalError(null);
                setValidation('');
                setBlockedAuthority(null);
              }}
              disabled={busy}
            >
              {t('common.edit')}
            </button>
            <button
              type="button"
              className="btn btn-danger"
              onClick={() => setConfirmation('withdraw')}
              disabled={busy}
            >
              {t('user.charity.withdraw')}
            </button>
          </>
        ) : null}
        {donation.status === 'approved' ? (
          <button
            type="button"
            className="btn btn-danger"
            onClick={() => setConfirmation('terminate')}
            disabled={busy}
          >
            {t('user.charity.terminate')}
          </button>
        ) : null}
      </div>
      <ConfirmDialog
        open={confirmation !== null}
        title={t(
          `user.charity.${confirmation === 'withdraw' ? 'withdrawTitle' : 'terminateTitle'}`,
        )}
        description={t(
          `user.charity.${confirmation === 'withdraw' ? 'withdrawBody' : 'terminateBody'}`,
        )}
        confirmLabel={t(`user.charity.${confirmation === 'withdraw' ? 'withdraw' : 'terminate'}`)}
        danger
        busy={busy}
        onCancel={() => setConfirmation(null)}
        onConfirm={() => void confirm()}
      />
    </Card>
  );
}

function matchesDonationOverviewFilter(key: DonationKey, filter: DonationOverviewFilter): boolean {
  if (filter === 'all') return true;
  if (filter === 'available') return key.charityState === 'available';
  if (filter === 'ended') return key.charityState === 'ended' || key.charityState === 'expired';
  return (
    key.charityState !== 'available' &&
    key.charityState !== 'ended' &&
    key.charityState !== 'expired'
  );
}

export function DonationKeyOverview({
  donations,
  accountID,
}: {
  donations: readonly Donation[];
  accountID?: string;
}) {
  const { t } = useTranslation();
  const [filter, setFilter] = useState<DonationOverviewFilter>('all');
  const totalKeys = donations.reduce((total, donation) => total + donation.keys.length, 0);
  const groups = donations
    .map((donation) => ({
      donation,
      keys: donation.keys.filter((key) => matchesDonationOverviewFilter(key, filter)),
    }))
    .filter((group) => filter === 'all' || group.keys.length > 0);
  const visibleKeys = groups.reduce((total, group) => total + group.keys.length, 0);

  return (
    <section className="economy-donation-overview" aria-labelledby="donation-key-overview-title">
      <div className="card-title-row economy-section-heading">
        <div>
          <h2 id="donation-key-overview-title">{t('user.charity.donationOverviewTitle')}</h2>
          <p className="muted">
            {t('user.charity.donationOverviewCount', { visible: visibleKeys, total: totalKeys })}
          </p>
        </div>
        <label>
          <span>{t('user.charity.donationOverviewFilter')}</span>
          <select
            value={filter}
            onChange={(event) => setFilter(event.target.value as DonationOverviewFilter)}
            aria-label={t('user.charity.donationOverviewFilter')}
          >
            <option value="all">{t('user.charity.donationOverviewFilters.all')}</option>
            <option value="available">{t('user.charity.donationOverviewFilters.available')}</option>
            <option value="blocked">{t('user.charity.donationOverviewFilters.blocked')}</option>
            <option value="ended">{t('user.charity.donationOverviewFilters.ended')}</option>
          </select>
        </label>
      </div>
      {groups.length === 0 ? (
        <EmptyState
          title={t('user.charity.donationOverviewNoMatches')}
          body={t('user.charity.donationOverviewNoMatchesBody')}
        />
      ) : (
        <div className="item-list economy-donation-overview-list">
          {groups.map(({ donation, keys }) => (
            <DonationCard
              donation={donation}
              visibleKeys={keys}
              accountID={accountID}
              key={donation.id}
            />
          ))}
        </div>
      )}
    </section>
  );
}

export function DonationOverviewPartialError({ onRetry }: { onRetry: () => void }) {
  const { t } = useTranslation();
  return (
    <div className="state-panel error-state nb-state nb-state--error" role="alert">
      <div>
        <h2>{t('user.charity.donationOverviewPartialTitle')}</h2>
        <p>{t('user.charity.donationOverviewPartialBody')}</p>
        <button type="button" className="btn btn-secondary" onClick={onRetry}>
          {t('common.retry')}
        </button>
      </div>
    </div>
  );
}

export function CharitySafetyNotice() {
  const { t } = useTranslation();
  return (
    <div role="note">
      <Card className="economy-safety-card">
        <h2>{t('user.charity.upstreamPrivacyTitle')}</h2>
        <p>{t('user.charity.upstreamPrivacyWarning')}</p>
        <p>{t('user.charity.upstreamQualityWarning')}</p>
        <p className="inline-notice">{t('user.charity.dispatchedFailureWarning')}</p>
      </Card>
    </div>
  );
}
