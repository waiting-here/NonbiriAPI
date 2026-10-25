import { DonationStatusBadge } from '@shared/components/UseSummary';
import { DonationUsesPanel } from '@shared/components/DonationUsesPanel';
import { OwnerUseAddition } from './OwnerUseAddition';
import { DonationShareFields } from './DonationShareFields';
import { contextPriceProps, contextRows } from './contextPrices';
import { ModelTypesSummary } from '@shared/components/ModelTypesField';
import { useEndpointCreateOptions } from '../core/queries';
import { useEffect, useId, useState, type FormEvent, type ReactNode } from 'react';
import { Link, useLocation } from 'react-router';
import { useTranslation } from 'react-i18next';
import { Field, Fold, Note, OutcomeNote, PanelFoot, Segmented } from '@shared/components/ui';
import { ExpandablePanel } from '@shared/components/ui/ExpandablePanel';
import { DonationConnectorLabel } from './DonationConnectorLabel';
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
import { KeyRateLimitTime } from '@shared/components/KeyRateLimitTime';
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
const keyStateLabels: Record<DonationKey['charityState'], string> = {
  pending: 'user.charity.keyState.pending',
  disabled: 'user.charity.keyState.disabled',
  suspended: 'user.charity.keyState.suspended',
  exhausted: 'user.charity.keyState.exhausted',
  expired: 'user.charity.keyState.expired',
  ended: 'user.charity.keyState.ended',
  available: 'user.charity.keyState.available',
  rate_limited: 'user.charity.keyState.rate_limited',
};

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
              placeholder={t('user.charity.modelSearchPlaceholder')}
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
              <option value="per_search_unit">{t('common.modalityPricing.perSearchUnit')}</option>
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
            const rows: CharityPriceRow[] = contextRows(model.pricing, t);
            return (
              <li key={model.id}>
                <div className="economy-model-heading">
                  <CopyValue value={model.fullName} label={t('user.charity.modelName')} />
                  <ModelTypesSummary value={model.modelTypes} />
                </div>
                <CharityPriceTable
                  {...contextPriceProps(model.pricing, t)}
                  mode={model.pricing.mode}
                  rows={rows}
                  serverNow={capability.serverNow}
                  effectiveDiscount={model.effectiveDiscount}
                  recurringDiscount={model.recurringDiscount}
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
    <Note title={t(`user.charity.intakeState.${state}`)}>
      {t(`user.charity.intakeBody.${state}`)}
    </Note>
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
  const protocolOptions = useEndpointCreateOptions(draftNamespace);
  const sharedServices = (choice: EndpointKeyChoice) =>
    protocolOptions.data?.native_service_options?.find(
      (row) => row.connector_type === choice.endpoint.connectorType,
    )?.native_services ?? [];
  const mutation = useCreateDonation();
  const location = useLocation();
  const [description, setDescription] = useState(() => parseDraft(draftNamespace));
  const [publicThanks, setPublicThanks] = useState<'' | 'yes' | 'no'>('');
  const [pickerOpen, setPickerOpen] = useState(() =>
    Boolean(new URLSearchParams(location.search).get('endpoint_id')),
  );
  const controlCopy = charityControlCopy(useTranslation().i18n.language);
  const [selectedChoices, setSelectedChoices] = useState<readonly EndpointKeyChoice[]>([]);
  const [expiryByKey, setExpiryByKey] = useState<Record<string, TimeDraft>>({});
  const [thresholdByKey, setThresholdByKey] = useState<Record<string, string>>({});
  const [authorized, setAuthorized] = useState(false);
  const [validation, setValidation] = useState('');
  const [success, setSuccess] = useState(false);
  const selectionMode = classifyDonationSelection(selectedChoices);
  const locked =
    !enabled ||
    mutation.isPending ||
    mutation.outcome === 'unknown' ||
    mutation.outcome === 'refresh-failed';
  const invalidSelection =
    selectedChoices.length === 0 ||
    selectedChoices.length > 100 ||
    selectedChoices.some(
      (choice) =>
        choice.eligibility !== 'eligible' ||
        choice.snapshotStale ||
        !sharedServices(choice).length ||
        !choice.key.credentialId ||
        !choice.key.credentialVersion ||
        !choice.key.grantRevision,
    ) ||
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
    if (locked) return;
    setValidation('');
    setSuccess(false);
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
      const expiresAt = expiryByKey[choice.key.id]
        ? timeDraftValue(expiryByKey[choice.key.id])
        : null;
      return expiresAt === undefined
        ? undefined
        : {
            endpointKeyId: choice.key.id,
            nativeServices: sharedServices(choice),
            credentialId: choice.key.credentialId,
            credentialVersion: choice.key.credentialVersion,
            grantRevision: choice.key.grantRevision,
            expiresAt,
            failureDisableThreshold,
          };
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
    await send(input);
  };
  const send = async (input: CreateDonationInput) => {
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
    } catch (error) {
      if (isConflictError(error)) setValidation(t('user.charity.reselectAfterConflict'));
    }
  };

  return (
    <Card className="economy-donation-composer">
      <div className="card-title-row">
        <h2>{t('user.charity.submitDonation')}</h2>
        <StatusBadge active label={t('user.charity.intakeState.open')} />
      </div>
      <div className="economy-donation-form">
        <section className="donate-step" aria-labelledby={formID + '-keys'}>
          <span className="donate-step__number" aria-hidden="true">
            1
          </span>
          <div>
            <h3 id={formID + '-keys'}>{t('user.charity.presentation.chooseKeys')}</h3>
            <p>{t('user.charity.presentation.chooseKeysHelp')}</p>
            <p className="muted">{t('user.charity.presentation.modelSetupHelp')}</p>
            <button
              type="button"
              className="nb-btn nb-btn--secondary"
              disabled={locked}
              onClick={() => setPickerOpen(true)}
            >
              {t('user.charity.presentation.openPicker')}
            </button>
            {selectedChoices.length > 0 ? (
              <div className="nb-chips donation-selected-chips">
                {selectedChoices.map((choice) => (
                  <span className="nb-chip donation-selected-chip" key={choice.key.id}>
                    <span className="nb-stack nb-stack--sm">
                      <span>
                        {choice.endpoint.note || choice.endpoint.baseUrl} ·{' '}
                        {choice.key.note ||
                          maskedKey(choice.key.displayHead, choice.key.displayTail)}
                      </span>
                    </span>
                    <button
                      type="button"
                      disabled={locked}
                      aria-label={t('user.charity.presentation.removeKey', {
                        name:
                          choice.key.note ||
                          maskedKey(choice.key.displayHead, choice.key.displayTail),
                      })}
                      onClick={() =>
                        setSelectedChoices((current) =>
                          current.filter((item) => item.key.id !== choice.key.id),
                        )
                      }
                    >
                      ×
                    </button>
                  </span>
                ))}
              </div>
            ) : null}
            {selectedChoices.length > 0 ? (
              <p className="inline-notice" role="status">
                {selectionMode.kind === 'mainstream'
                  ? t('user.charity.selectedMainstreamChannel', { name: selectionMode.channelName })
                  : selectionMode.kind === 'custom'
                    ? t('user.charity.selectedCustomSources')
                    : t('user.charity.splitDonationSources')}
              </p>
            ) : null}
          </div>
        </section>
        <ExpandablePanel
          open={pickerOpen}
          onClose={() => setPickerOpen(false)}
          title={t('user.charity.presentation.chooseKeys')}
          closeLabel={t('common.close')}
          busy={mutation.isPending}
          footer={
            <button
              type="button"
              className="nb-btn nb-btn--primary"
              onClick={() => setPickerOpen(false)}
            >
              {t('user.charity.presentation.selectionDone')}
            </button>
          }
        >
          <p className="muted">{t('common.smartBackoff.automaticHelp')}</p>
          <DonationResourcePicker
            accountId={draftNamespace}
            selected={selectedChoices}
            onChange={(choices) => {
              setSelectedChoices(choices);
            }}
            disabled={locked}
            enabled={enabled}
            renderSelected={(choice) => (
              <div className="economy-key-expiry">
                <DonationShareFields choice={choice} />
                <Fold title={t('user.workflows.donationOptions')}>
                  <Field label={t('user.charity.keyExpiry')} help={t('user.charity.expiryHint')}>
                    {(props) => (
                      <TimeInput
                        {...props}
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
                    )}
                  </Field>
                  <FailureThresholdInput
                    value={thresholdByKey[choice.key.id] ?? '10'}
                    disabled={locked}
                    onChange={(value) =>
                      setThresholdByKey((current) => ({ ...current, [choice.key.id]: value }))
                    }
                  />
                </Fold>
              </div>
            )}
          />
        </ExpandablePanel>
        <form id={formID} onSubmit={submit} noValidate>
          <section className="donate-step">
            <span className="donate-step__number" aria-hidden="true">
              2
            </span>
            <label>
              <span>{t('user.charity.donationDescription')}</span>
              <textarea
                aria-label={t('user.charity.donationDescription')}
                value={description}
                disabled={locked}
                onChange={(event) => setDescription(event.target.value)}
                required
                aria-invalid={Boolean(validation) && !validDonationDescription(description)}
                aria-describedby={
                  validation && !validDonationDescription(description)
                    ? formID + '-description-error'
                    : formID + '-description-help'
                }
              />
              <small id={formID + '-description-help'}>
                {t('user.charity.presentation.descriptionHelp')}
              </small>
              {validation && !validDonationDescription(description) ? (
                <p id={formID + '-description-error'} className="field-error" role="alert">
                  {validation}
                </p>
              ) : null}
            </label>
          </section>
          <section className="donate-step" aria-labelledby={formID + '-thanks'}>
            <span className="donate-step__number" aria-hidden="true">
              3
            </span>
            <div>
              <h3 id={formID + '-thanks'}>{controlCopy.thanks}</h3>
              <Segmented
                label={controlCopy.thanks}
                value={publicThanks}
                onChange={setPublicThanks}
                disabled={locked}
                options={[
                  { value: 'yes', label: controlCopy.yes },
                  { value: 'no', label: controlCopy.no },
                ]}
              />
              <small>{controlCopy.thanksHint}</small>
            </div>
          </section>
        </form>
        <Fold plain title={t('user.charity.disclosureTitle')}>
          <section className="economy-donation-notice">
            <h3>{t('user.charity.donationNoticeTitle')}</h3>
            <MarkdownText>{donationNotice}</MarkdownText>
          </section>
          <ul>
            <li>{t('user.charity.disclosureMasked')}</li>
            <li>{t('user.charity.disclosureThirdParty')}</li>
            <li>{t('user.charity.disclosureCost')}</li>
            <li>{t('user.charity.disclosureManagement')}</li>
            <li>{t('user.charity.disclosureLimitCounts')}</li>
            <li>{t('user.charity.disclosureResponsibility')}</li>
            <li>{t('user.charity.disclosureDeletion')}</li>
          </ul>
        </Fold>
        <Note>{t('user.charity.sharedKeyLimitsNotice')}</Note>
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
          error={mutation.error}
          successKey={success ? 'user.charity.submitted' : undefined}
        />
        {mutation.outcome === 'unknown' && mutation.variables ? (
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            disabled={!enabled || mutation.isPending}
            onClick={() => void send(mutation.variables!)}
          >
            {t('user.workflows.confirmDonation')}
          </button>
        ) : null}
        {mutation.outcome === 'refresh-failed' ? (
          <OutcomeNote
            outcome={{ kind: 'savedRefreshFailed', recheck: () => void mutation.refresh() }}
          />
        ) : mutation.refreshError ? (
          <ErrorState error={mutation.refreshError} onRetry={() => void mutation.check()} />
        ) : null}
        <PanelFoot>
          <span>
            {t('user.charity.presentation.selectedCount', { count: selectedChoices.length })}
          </span>
          <button
            className="nb-btn nb-btn--primary"
            type="submit"
            form={formID}
            disabled={locked || invalidSelection || invalidExpiry || publicThanks === ''}
          >
            {mutation.isPending ? t('common.working') : t('user.charity.submit')}
          </button>
        </PanelFoot>
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
  donationSummary,
  donationRevision,
  compact = false,
  returnTo,
  ruleSummary,
}: {
  donationKey: DonationKey;
  accountID?: string;
  donationId?: string;
  donationStatus?: Donation['status'];
  donationSummary?: Donation['useSummary'];
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
      {accountID &&
      donationId &&
      donationRevision &&
      donationKey.endpointId !== undefined &&
      (donationStatus === 'pending' || donationStatus === 'approved') &&
      donationKey.endedReason === null &&
      donationKey.charityState !== 'ended' &&
      donationKey.charityState !== 'expired' ? (
        <Fold lazy title={t('common.connections.addServices')}>
          <OwnerUseAddition
            accountId={accountID}
            donationId={donationId}
            donationRevision={donationRevision}
            donationKey={donationKey}
          />
        </Fold>
      ) : null}
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
              ? donationKey.source.name
              : t('user.charity.customSource')}{' '}
            · {donationKey.source.baseUrl} ·{' '}
            <DonationConnectorLabel value={donationKey.source.connectorType} />
          </p>
        </div>
        <div className="economy-status-stack">
          {donationStatus ? (
            <DonationStatusBadge status={donationStatus} summary={donationSummary} />
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
            label={t(keyStateLabels[donationKey.charityState])}
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
      <KeyRateLimitTime
        until={donationKey.rateLimitedUntil}
        smartBackoff={donationKey.smartBackoff}
      />
      {accountID && donationId && donationKey.endedReason === null ? (
        <DonationUsesPanel
          role="owner"
          accountId={accountID}
          donationId={donationId}
          donationKeyId={donationKey.id}
        />
      ) : null}
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
      <Fold
        className="economy-key-details"
        defaultOpen={!compact}
        title={t('user.charity.quotaDetails')}
      >
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
      </Fold>
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
        <DonationStatusBadge status={donation.status} summary={donation.useSummary} />
      </div>

      {!showDetailLink ? <Note>{t('user.charity.presentation.modelSetupHelp')}</Note> : null}

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
                className="nb-btn nb-btn--ghost"
                onClick={() => setEditing(false)}
                disabled={busy}
              >
                {t('common.cancel')}
              </button>
              <button type="submit" className="nb-btn nb-btn--primary" disabled={busy}>
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
        <Fold
          className="economy-review-result"
          defaultOpen={!showDetailLink}
          title={t('user.charity.reviewResult')}
        >
          <MarkdownText>{donation.reviewResult.reason}</MarkdownText>
          <span className="muted">{formatDateTime(donation.reviewResult.reviewedAt)}</span>
        </Fold>
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
              donationSummary={donation.useSummary}
              accountID={accountID}
              compact
            />
          ))}
          {visibleKeys.length === 0 ? (
            <p className="muted">{t('user.charity.noRemainingKeys')}</p>
          ) : null}
        </div>
      ) : (
        <Fold
          className="economy-donation-details"
          title={t('user.charity.resourceDetails', { count: donation.keys.length })}
        >
          {donation.keys.length > 0 ? (
            <div className="economy-donation-key-list">
              {donation.keys.map((key) => (
                <DonationKeyPanel
                  key={key.id}
                  donationKey={key}
                  donationId={donation.id}
                  donationRevision={donation.revision}
                  donationStatus={donation.status}
                  donationSummary={donation.useSummary}
                  accountID={accountID}
                />
              ))}
            </div>
          ) : (
            <p className="muted">{t('user.charity.noRemainingKeys')}</p>
          )}
        </Fold>
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
          <Link className="nb-btn nb-btn--ghost" to={`/charity/donations/${donation.id}`}>
            {t('user.charity.openDonationDetail')}
          </Link>
        ) : null}
        {donation.status === 'pending' ? (
          <>
            <button
              type="button"
              className="nb-btn nb-btn--secondary"
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
              className="nb-btn nb-btn--danger"
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
            className="nb-btn nb-btn--danger"
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
        <button type="button" className="nb-btn nb-btn--secondary" onClick={onRetry}>
          {t('common.retry')}
        </button>
      </div>
    </div>
  );
}

export function CharitySafetyNotice() {
  const { t } = useTranslation();
  return (
    <Fold
      title={t('user.charity.presentation.beforeUse')}
      summary={t('user.charity.presentation.beforeUseSummary')}
    >
      <div role="note" className="economy-safety-card">
        <p>{t('user.charity.upstreamPrivacyWarning')}</p>
        <p>{t('user.charity.upstreamQualityWarning')}</p>
        <p>{t('common.operations.charity.embeddingBillingHelp')}</p>
        <p>{t('user.charity.dispatchedFailureWarning')}</p>
      </div>
    </Fold>
  );
}
