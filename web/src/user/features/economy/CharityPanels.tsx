import { useEffect, useMemo, useState, type FormEvent } from 'react';
import { Link } from 'react-router';
import { useTranslation } from 'react-i18next';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { Card, EmptyState, ErrorState, StatusBadge } from '@shared/components/States';
import { formatDateTime } from '@shared/utils/datetime';
import { isConflictError, isResponseUnknown, type CreateDonationInput } from './api';
import { CreditAmount, ExactCount } from './ExactValue';
import { maskedKey } from './format';
import { isDimensionExhausted } from './normalize';
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
} from './types';

const DRAFT_STORAGE_PREFIX = 'nonbiri:charity-donation-draft:v1';
const EMPTY_SELECTION = new Set<string>();

function validDonationDescription(value: string): boolean {
  if (Array.from(value).length > 1024) return false;
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
  const available = capability.state === 'available';
  return (
    <Card className="economy-capability-card">
      <div className="card-title-row">
        <div>
          <p className="eyebrow">{t('user.charity.callCapabilityEyebrow')}</p>
          <h2>{t('user.charity.callCapabilityTitle')}</h2>
        </div>
        <StatusBadge
          active={available}
          danger={capability.state === 'feature_disabled'}
          label={t(`user.charity.capabilityState.${capability.state}`)}
        />
      </div>
      <p>{t(`user.charity.capabilityBody.${capability.state}`)}</p>
      {available ? (
        <ul className="economy-model-list" aria-label={t('user.charity.availableModels')}>
          {capability.models.map((model) => (
            <li key={model.id}>
              <strong>{model.fullName}</strong>
              <span className="muted">
                {model.provider} · {model.model}
              </span>
            </li>
          ))}
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
          <p className="eyebrow">{t('user.charity.intakeEyebrow')}</p>
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

function eligibilityLabel(choice: EndpointKeyChoice, t: (key: string) => string): string {
  return choice.eligibility === 'eligible'
    ? t('user.charity.keyEligibility.eligible')
    : t(`user.charity.keyEligibility.${choice.eligibility}`);
}

export function DonationComposer({
  choices,
  draftNamespace,
}: {
  choices: readonly EndpointKeyChoice[];
  draftNamespace: string;
}) {
  const { t } = useTranslation();
  const mutation = useCreateDonation();
  const [description, setDescription] = useState(() => parseDraft(draftNamespace));
  const [selection, setSelection] = useState<{ signature: string; ids: Set<string> }>(() => ({
    signature: '',
    ids: new Set(),
  }));
  const [authorized, setAuthorized] = useState(false);
  const [validation, setValidation] = useState('');
  const [success, setSuccess] = useState(false);
  const [blockedAuthority, setBlockedAuthority] = useState<{
    baselineGeneration: number;
  } | null>(null);
  const authorityAdvanced =
    blockedAuthority !== null && mutation.reconcileGeneration > blockedAuthority.baselineGeneration;
  const waitingForAuthority = blockedAuthority !== null && !authorityAdvanced;
  const eligible = choices.filter((choice) => choice.eligibility === 'eligible');
  const eligibleSignature = eligible.map((choice) => choice.key.id).join('|');
  const selected = selection.signature === eligibleSignature ? selection.ids : EMPTY_SELECTION;

  useEffect(() => {
    storeDraft(draftNamespace, description);
  }, [description, draftNamespace]);

  const grouped = useMemo(() => {
    const groups = new Map<
      string,
      { endpoint: EndpointKeyChoice['endpoint']; choices: EndpointKeyChoice[] }
    >();
    for (const choice of choices) {
      const existing = groups.get(choice.endpoint.id);
      if (existing) existing.choices.push(choice);
      else groups.set(choice.endpoint.id, { endpoint: choice.endpoint, choices: [choice] });
    }
    return [...groups.values()];
  }, [choices]);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setValidation('');
    setSuccess(false);
    setBlockedAuthority(null);
    mutation.reset();
    if (!validDonationDescription(description)) {
      setValidation(t('user.charity.descriptionInvalid'));
      return;
    }
    if (selected.size === 0) {
      setValidation(t('user.charity.chooseExistingKeys'));
      return;
    }
    if (!authorized) {
      setValidation(t('user.charity.authorizationRequired'));
      return;
    }
    const input: CreateDonationInput = {
      description,
      endpointKeyIds: [...selected],
      ownershipAuthorized: true,
    };
    const baselineGeneration = mutation.reconcileGeneration;
    try {
      await mutation.mutateAsync(input);
      setDescription('');
      setSelection({ signature: eligibleSignature, ids: new Set() });
      setAuthorized(false);
      storeDraft(draftNamespace, '');
      setSuccess(true);
    } catch (error) {
      if (isConflictError(error) || isResponseUnknown(error)) {
        setSelection({ signature: eligibleSignature, ids: new Set() });
        setAuthorized(false);
        setBlockedAuthority({ baselineGeneration });
      }
      if (isConflictError(error)) setValidation(t('user.charity.reselectAfterConflict'));
      // React Query retains the bounded error for the explicit notice below.
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
      <form onSubmit={submit} noValidate>
        <label className="full-width">
          <span>{t('user.charity.donationDescription')}</span>
          <textarea
            value={description}
            onChange={(event) => setDescription(event.target.value)}
            aria-invalid={Boolean(validation)}
          />
        </label>

        {eligible.length === 0 ? (
          <EmptyState
            title={t('user.charity.noEligibleKeys')}
            body={t('user.charity.noEligibleKeysBody')}
            action={
              <Link className="btn btn-secondary" to="/endpoints" state={{ returnTo: '/charity' }}>
                {t('user.charity.manageEndpoints')}
              </Link>
            }
          />
        ) : (
          <fieldset className="economy-key-picker">
            <legend>{t('user.charity.selectExistingKeys')}</legend>
            {grouped.map((group) => (
              <section className="economy-key-group" key={group.endpoint.id}>
                <div>
                  <strong>{group.endpoint.baseUrl}</strong>
                  <span className="muted">{group.endpoint.connectorType}</span>
                </div>
                {group.choices.map((choice) => {
                  const disabled = choice.eligibility !== 'eligible';
                  return (
                    <label className="economy-key-choice" key={choice.key.id}>
                      <input
                        type="checkbox"
                        checked={selected.has(choice.key.id)}
                        disabled={disabled}
                        onChange={(event) =>
                          setSelection((current) => {
                            const next = new Set(
                              current.signature === eligibleSignature ? current.ids : [],
                            );
                            if (event.target.checked) next.add(choice.key.id);
                            else next.delete(choice.key.id);
                            return { signature: eligibleSignature, ids: next };
                          })
                        }
                      />
                      <span>
                        <span className="mono">
                          {maskedKey(choice.key.displayHead, choice.key.displayTail)}
                        </span>
                        {choice.key.note ? <span> · {choice.key.note}</span> : null}
                        <small className="muted">{eligibilityLabel(choice, t)}</small>
                      </span>
                    </label>
                  );
                })}
              </section>
            ))}
          </fieldset>
        )}

        <section className="economy-disclosure" aria-labelledby="donation-disclosure-title">
          <h3 id="donation-disclosure-title">{t('user.charity.disclosureTitle')}</h3>
          <ul>
            <li>{t('user.charity.disclosureMasked')}</li>
            <li>{t('user.charity.disclosureThirdParty')}</li>
            <li>{t('user.charity.disclosureCost')}</li>
            <li>{t('user.charity.disclosureResponsibility')}</li>
            <li>{t('user.charity.disclosureDeletion')}</li>
          </ul>
        </section>

        <label className="checkbox-label economy-authorization">
          <input
            type="checkbox"
            checked={authorized}
            onChange={(event) => setAuthorized(event.target.checked)}
          />
          <span>{t('user.charity.ownershipAuthorization')}</span>
        </label>
        {validation ? (
          <p className="field-error" role="alert">
            {validation}
          </p>
        ) : null}
        <MutationNotice
          error={authorityAdvanced ? null : mutation.error}
          successKey={
            authorityAdvanced
              ? 'user.charity.mutationReconciled'
              : success
                ? 'user.charity.submitted'
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
            disabled={
              mutation.isPending ||
              mutation.isReconciling ||
              eligible.length === 0 ||
              waitingForAuthority
            }
          >
            {mutation.isPending || mutation.isReconciling
              ? t('common.working')
              : t('user.charity.submit')}
          </button>
        </div>
      </form>
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

export function DonationKeyPanel({ donationKey }: { donationKey: DonationKey }) {
  const { t } = useTranslation();
  const states = keyStateKeys(donationKey);
  return (
    <article className="economy-donation-key">
      <div className="item-header">
        <div>
          <h4 className="mono">{maskedKey(donationKey.displayHead, donationKey.displayTail)}</h4>
          <p className="item-meta">
            {donationKey.source.baseUrl} · {donationKey.source.connectorType}
          </p>
        </div>
        <div className="economy-status-stack">
          {states.map((state) => (
            <StatusBadge
              key={state}
              active={state === 'available'}
              danger={state.includes('expired') || state === 'ended' || state.includes('disabled')}
              label={t(`user.charity.keyState.${state}`)}
            />
          ))}
        </div>
      </div>
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
      </div>
      <dl className="detail-grid economy-key-metadata">
        <div className="detail-row">
          <dt>{t('user.charity.failureGeneration')}</dt>
          <dd>
            <ExactCount value={donationKey.streak.generation} />
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
        {donationKey.endedReason ? (
          <div className="detail-row">
            <dt>{t('user.charity.endedReason')}</dt>
            <dd>{t(`user.charity.endedReasonValue.${donationKey.endedReason}`)}</dd>
          </div>
        ) : null}
      </dl>
    </article>
  );
}

export function DonationCard({
  donation,
  showDetailLink = true,
}: {
  donation: Donation;
  showDetailLink?: boolean;
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
          <p className="eyebrow">{t('user.charity.donationLabel')}</p>
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
        <form onSubmit={save} className="economy-description-editor">
          <label>
            <span>{t('user.charity.donationDescription')}</span>
            <textarea
              value={description}
              onChange={(event) => setDescription(event.target.value)}
            />
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
      ) : (
        <p className="economy-long-text">{donation.description}</p>
      )}

      <dl className="detail-grid">
        <div className="detail-row">
          <dt>{t('user.charity.expires')}</dt>
          <dd>
            {donation.expiresAt === null
              ? t('user.charity.never')
              : formatDateTime(donation.expiresAt)}
          </dd>
        </div>
        <div className="detail-row">
          <dt>{t('user.charity.updatedAt')}</dt>
          <dd>{formatDateTime(donation.updatedAt)}</dd>
        </div>
      </dl>

      {donation.reviewResult ? (
        <section className="economy-review-result">
          <h4>{t('user.charity.reviewResult')}</h4>
          <p>{donation.reviewResult.reason}</p>
          <span className="muted">{formatDateTime(donation.reviewResult.reviewedAt)}</span>
        </section>
      ) : null}

      <details className="economy-donation-details">
        <summary>{t('user.charity.resourceDetails', { count: donation.keys.length })}</summary>
        {donation.keys.length > 0 ? (
          <div className="economy-donation-key-list">
            {donation.keys.map((key) => (
              <DonationKeyPanel key={key.id} donationKey={key} />
            ))}
          </div>
        ) : (
          <p className="muted">{t('user.charity.noRemainingKeys')}</p>
        )}
      </details>

      <MutationNotice error={visibleError} successKey={visibleSuccessKey} />
      {waitingForAuthority && blockedMutation?.reconcileError ? (
        <ErrorState
          error={blockedMutation.reconcileError}
          onRetry={() => void blockedMutation.retryReconcile()}
        />
      ) : null}
      {validation ? (
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

export function CharitySafetyNotice() {
  const { t } = useTranslation();
  return (
    <div role="note">
      <Card className="economy-safety-card">
        <h2>{t('user.charity.upstreamPrivacyTitle')}</h2>
        <p>{t('user.charity.upstreamPrivacyWarning')}</p>
        <p className="inline-notice">{t('user.charity.dispatchedFailureWarning')}</p>
      </Card>
    </div>
  );
}
