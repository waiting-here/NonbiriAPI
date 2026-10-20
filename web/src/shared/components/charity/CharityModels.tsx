import { SessionHeaderFields } from './SessionHeaderFields';
import { PriceTools } from './PriceTools';
import {
  draftPriceConfig,
  priceDraft,
  priceDraftSignature,
  snapshotPriceDraft,
  switchPriceMode,
  type PriceDraft,
} from './priceDraft';
import { AsyncLeaveDialog } from '../AsyncLeaveDialog';
import { PageHeader } from '../States';
import type { SessionHeader } from '@shared/operations/charity';
import { ContextPricingFields } from './ContextPricingFields';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { ModelTypesField, ModelTypesSummary } from '@shared/components/ModelTypesField';
import { Card, EmptyState, ErrorState, LoadingState, StatusBadge } from '@shared/components/States';
import { TransportRuleField } from '@shared/components/TransportRuleField';
import {
  Button,
  DataTable,
  Field,
  FilterBar,
  Fold,
  Panel,
  PanelBody,
  PanelFoot,
  PanelHead,
} from '@shared/components/ui';
import { PriceConfigBrief } from './PriceConfigSummary';
import { GatewayCapabilitySummary } from '@shared/gateway/GatewayCapabilitySummary';
import type { ModelType } from '@shared/modelTypes';
import { responseOutcomeUnknown } from '@shared/operations/api';
import {
  charityKeys,
  createManagedCharityModel,
  deleteManagedBinding,
  deleteManagedCharityModel,
  getManagedBindings,
  orderManagedBindings,
  patchManagedCharityModel,
  type CharityModel,
  type CharityRole,
  type TokenPrices,
} from '@shared/operations/charity';
import {
  getManagedCharityModel,
  getManagedCharityModelsPage,
  validManagementSearch,
} from '@shared/operations/charityModelPages';
import { excludedFields, halfPrice } from '@shared/operations/charityScope';
import '@shared/operations/operations.css';
import { PagePagination } from '@shared/operations/PagePagination';
import { useDetailNavigation } from '@shared/operations/useDetailNavigation';
import { usePagePager } from '@shared/operations/usePagePager';
import { useSearchState } from '@shared/operations/useSearchState';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { buildRolePolicy, draftFromRolePolicy, type RolePolicyDraft } from '@shared/rolePolicy';
import '@shared/styles/charity-management.css';
import { timeDraftValue, type TimeDraft } from '@shared/time';
import type { TransportRule } from '@shared/transportRule';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useEffect, useId, useRef, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { useLocation } from 'react-router';
import { useRetainedOperation } from '../../../admin/features/operations/useRetainedOperation';
import type { CharityPickerKeyManagerProps } from '../CharityBindingPicker';
import { BindingAddForm } from './BindingAddForm';
import { charityControlCopy } from '../charityControlCopy';
import { CharityModelScope } from '../CharityModelScope';
import { CharityRolePolicyForm } from '../CharityRolePolicyForm';
import { KeyLimitSummary } from '../KeyRoutingLimits';
import { useRequestAdaptationCopy } from '../requestAdaptationCopy';
import { RequestAdaptationEditor } from '../RequestAdaptationEditor';
import { RolePolicyEditor } from '../RolePolicyEditor';
import { TimeContextNotice } from '../TimeContext';
import { TimeInput } from '../TimeInput';

import {
  charityCopyKey,
  MAX_UNIX_SECOND,
  MODEL_LEVELS,
  normalizePublicDescriptionInput,
  oneParam,
  samePageFamily,
  selectedID,
  snapshotPage,
  tokenPriceCopyKey,
  validAmount,
  validPublicDescription,
  validText,
  validTokenReserveCredits,
} from './managementFields';

interface ModelDraft {
  sessionHeaders: SessionHeader[];
  longEnabled: boolean;
  longThreshold: string;
  longRequest: string;
  longPrices: TokenPrices;
  penaltyEnabled: boolean;
  penaltyThreshold: string;
  penaltyFine: string;
  modelTypes: ModelType[];
  transportRule: TransportRule;
  rolePolicy: RolePolicyDraft;
  isMainstream: boolean;
  excluded: string;
  routeStrategy: CharityModel['route_strategy'];
  affinityTTLSeconds: number;
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
function modelDraft(model?: CharityModel): ModelDraft {
  return {
    ...priceDraft(model),
    sessionHeaders: model?.session_headers?.map((value) => ({ ...value })) ?? [],
    modelTypes: model ? [...model.model_types] : ['chat_completions'],
    transportRule: model?.transport_rule ?? 'passthrough',
    rolePolicy: draftFromRolePolicy(model?.role_policy),
    isMainstream: model?.is_mainstream ?? false,
    excluded: (model?.excluded_request_fields ?? []).join(', '),
    routeStrategy: model?.route_strategy ?? 'cache_balanced',
    affinityTTLSeconds: model?.affinity_ttl_seconds ?? 300,
    provider: model?.provider ?? '',
    model: model?.model ?? '',
    enabled: model?.enabled ?? true,
    allowedLevels: model ? [...model.allowed_levels] : [...MODEL_LEVELS],
    publicDescription: model?.public_description ?? '',
    flatten: model?.flatten_tool_calls ?? false,
  };
}
function modelBody(
  draft: ModelDraft,
  includeTokenReserveCredits: boolean,
  includeAffinityTTL: boolean,
) {
  const start = timeDraftValue(draft.discountStart);
  const end = timeDraftValue(draft.discountEnd);
  if (start === undefined || end === undefined) throw new Error('Time is not ready');
  const rolePolicy = buildRolePolicy(draft.rolePolicy);
  if (rolePolicy.error) throw new Error('Role fields are not ready');
  const body = {
    session_headers: draft.sessionHeaders.map((v) => ({ ...v, name: v.name.trim() })),
    model_types: draft.modelTypes,
    transport_rule: draft.transportRule,
    role_policy: rolePolicy.policy,
    is_mainstream: draft.isMainstream,
    excluded_request_fields: excludedFields(draft.excluded) ?? [],
    route_strategy: draft.routeStrategy,
    provider: draft.provider.trim(),
    model: draft.model.trim(),
    enabled: draft.enabled,
    allowed_levels: MODEL_LEVELS.filter((level) => draft.allowedLevels.includes(level)),
    public_description: normalizePublicDescriptionInput(draft.publicDescription),
    pricing: {
      ...(draft.mode === 'per_request'
        ? { mode: 'per_request', user_price: draft.requestUser, donor_reward: draft.requestDonor }
        : { mode: 'per_token', user_prices: draft.userPrices, donor_rewards: draft.donorRewards }),
      ...(draft.longEnabled
        ? {
            long_context: {
              input_threshold: draft.longThreshold,
              ...(draft.mode === 'per_request'
                ? { user_price: draft.longRequest }
                : { user_prices: draft.longPrices }),
            },
          }
        : {}),
      ...(draft.penaltyEnabled
        ? {
            context_penalty: {
              input_threshold: draft.penaltyThreshold,
              fine: draft.penaltyFine,
            },
          }
        : {}),
    },
    discount: {
      enabled: draft.discountEnabled,
      percent: draft.discountPercent,
      start_at: start,
      end_at: end,
    },
    flatten_tool_calls: draft.flatten,
  };
  return {
    ...body,
    ...(includeTokenReserveCredits ? { token_reserve_credits: draft.tokenReserveCredits } : {}),
    ...(includeAffinityTTL ? { affinity_ttl_seconds: draft.affinityTTLSeconds } : {}),
  };
}

type ModelValidation =
  | 'modelTypes'
  | 'rolePolicy'
  | 'excludedFields'
  | 'modelIdentity'
  | 'modelLevels'
  | 'publicDescription'
  | 'modelPrices'
  | 'sessionHeaders'
  | 'contextThreshold'
  | 'tokenReserveCredits'
  | 'affinityTTLSeconds'
  | 'discountPercent'
  | 'discountDates';

function modelDraftError(draft: ModelDraft): ModelValidation | null {
  if (
    draft.sessionHeaders.some(
      (v) =>
        !v.name.trim() ||
        !Number.isInteger(v.idle_seconds) ||
        v.idle_seconds < 1 ||
        v.idle_seconds > 86400,
    )
  )
    return 'sessionHeaders';
  if (!draft.modelTypes.length) return 'modelTypes';
  if (buildRolePolicy(draft.rolePolicy).error) return 'rolePolicy';
  if (
    !Number.isInteger(draft.affinityTTLSeconds) ||
    draft.affinityTTLSeconds < 1 ||
    draft.affinityTTLSeconds > 86_400
  )
    return 'affinityTTLSeconds';
  if (excludedFields(draft.excluded) === null) return 'excludedFields';
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
  if (draft.longEnabled)
    prices.push(
      ...(draft.mode === 'per_request' ? [draft.longRequest] : Object.values(draft.longPrices)),
    );
  if (draft.penaltyEnabled) prices.push(draft.penaltyFine);
  if (
    (draft.longEnabled && !/^[1-9][0-9]*$/.test(draft.longThreshold)) ||
    (draft.penaltyEnabled && !/^[1-9][0-9]*$/.test(draft.penaltyThreshold))
  )
    return 'contextThreshold';
  if (prices.some((value) => !validAmount(value))) {
    return 'modelPrices';
  }
  if (!validTokenReserveCredits(draft.tokenReserveCredits)) return 'tokenReserveCredits';
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
    (start !== null && end !== null && end < start)
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
  onCreated,
}: {
  role: CharityRole;
  model?: CharityModel;
  refresh: () => Promise<unknown>;
  onDeleted?: () => void;
  onCapabilityLoss?: () => void;
  onCreated?: (model: CharityModel) => void;
}) {
  const { t, i18n } = useTranslation();
  const [draft, updateDraft] = useState(() => modelDraft(model));
  const [baseRevision, setBaseRevision] = useState(model?.revision);
  const [savedDraft, setSavedDraft] = useState(() => priceDraftSignature(modelDraft(model)));
  const [undoPrice, setUndoPrice] = useState<PriceDraft | null>(null);
  const [priceSource, setPriceSource] = useState('');
  const [createdModel, setCreatedModel] = useState<CharityModel | null>(null);
  useEffect(() => {
    if (createdModel) onCreated?.(createdModel);
  }, [createdModel, onCreated]);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [halfError, setHalfError] = useState(false);
  const priceInputs = useRef<Record<string, HTMLInputElement | null>>({});
  const pricingModeInput = useRef<HTMLSelectElement>(null);
  const [referenceOpen, setReferenceOpen] = useState(false);
  const referenceTrigger = useRef<HTMLButtonElement>(null);
  const referencePanel = useRef<HTMLDivElement>(null);
  const wasReferenceOpen = useRef(false);
  useEffect(() => {
    if (wasReferenceOpen.current === referenceOpen) return;
    wasReferenceOpen.current = referenceOpen;
    const target = referenceOpen ? referencePanel.current : referenceTrigger.current;
    target?.focus({ preventScroll: true });
    target?.scrollIntoView?.({ block: 'nearest', behavior: 'instant' });
  }, [referenceOpen]);
  const discountDisclosure = useRef<HTMLDetailsElement>(null);
  const save = useRetainedOperation<
    { body: ReturnType<typeof modelBody>; revision?: string },
    CharityModel
  >(
    async (input, key, context) => {
      const result = model
        ? await patchManagedCharityModel(
            role,
            model.id,
            { expected_revision: input.revision, ...input.body },
            key,
          )
        : await createManagedCharityModel(role, input.body, key);
      if (model) context.commit(() => setBaseRevision(result.revision));
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
  const setDraft: typeof updateDraft = (next) => {
    if (save.isSuccess) save.reset();
    setHalfError(false);
    updateDraft(next);
  };
  const validationError = modelDraftError(draft);
  const discountError =
    validationError === 'discountDates' || validationError === 'discountPercent';
  useEffect(() => {
    if (discountError && discountDisclosure.current) discountDisclosure.current.open = true;
  }, [discountError]);
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
  const copy = charityControlCopy(i18n.language);
  const content = (
    <>
      <fieldset
        className="ops-unframed"
        disabled={
          (!model && save.isPending) ||
          remove.isPending ||
          Boolean(unknownSave) ||
          save.outcome === 'refresh-failed'
        }
      >
        {model ? <h3>{model.full_name}</h3> : null}
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
        <fieldset className="ops-form-section">
          <legend>{copy.basicSettings}</legend>
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
            <label className="checkbox-label">
              <input
                type="checkbox"
                checked={draft.enabled}
                onChange={(event) => setDraft({ ...draft, enabled: event.target.checked })}
              />
              <span>{t('common.operations.charity.enableCharityModel')}</span>
            </label>
          </div>
        </fieldset>
        <ModelTypesField
          value={draft.modelTypes}
          onChange={(modelTypes) => setDraft({ ...draft, modelTypes })}
          disabled={save.isPending || remove.isPending || Boolean(unknownSave)}
          showError
        />
        <p className="nb-sub">{t('common.operations.charity.operationHelp')}</p>
        {draft.modelTypes.includes('embeddings') ? (
          <p className="nb-sub">{t('common.operations.charity.embeddingBillingHelp')}</p>
        ) : null}
        {draft.modelTypes.includes('images_generations') ? (
          <p className="nb-sub">{t('common.operations.charity.imageBillingHelp')}</p>
        ) : null}
        <fieldset className="ops-form-section">
          <legend>{copy.accessRouting}</legend>
          <div className="ops-field-grid">
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
                <option value="cache_balanced">
                  {t('common.operations.charity.routeCacheBalanced')}
                </option>
              </select>
              <small>
                {t(
                  draft.routeStrategy === 'ordered'
                    ? 'common.operations.charity.routeOrderedHelp'
                    : draft.routeStrategy === 'random'
                      ? 'common.operations.charity.routeRandomHelp'
                      : draft.routeStrategy === 'cache_balanced'
                        ? 'common.operations.charity.routeCacheBalancedHelp'
                        : 'common.operations.charity.routeExpiryWeightedHelp',
                )}
              </small>
            </label>
            {role === 'admin' ? (
              <label>
                <span>{t('common.operations.charity.routeAffinityTTL')}</span>
                <input
                  type="number"
                  min={1}
                  max={86_400}
                  step={1}
                  value={draft.affinityTTLSeconds}
                  onChange={(event) =>
                    setDraft({ ...draft, affinityTTLSeconds: Number(event.target.value) })
                  }
                />
                <small>{t('common.operations.charity.routeAffinityTTLHelp')}</small>
              </label>
            ) : null}
            {draft.modelTypes.includes('chat_completions') ? (
              <>
                <TransportRuleField
                  value={draft.transportRule}
                  onChange={(transportRule) => setDraft({ ...draft, transportRule })}
                />
                <label className="checkbox-label">
                  <input
                    type="checkbox"
                    checked={draft.flatten}
                    onChange={(event) => setDraft({ ...draft, flatten: event.target.checked })}
                  />
                  <span>{t(charityCopyKey(role, 'flattenExperimental'))}</span>
                </label>
              </>
            ) : null}
          </div>
          <div className="ops-model-settings">
            <fieldset className="ops-model-levels">
              <legend>{t('common.operations.charity.allowedLevels')}</legend>
              <div className="ops-model-level-actions">
                <button
                  className="nb-btn nb-btn--secondary"
                  type="button"
                  onClick={() =>
                    setDraft((current) => ({ ...current, allowedLevels: [...MODEL_LEVELS] }))
                  }
                >
                  {t('common.operations.charity.selectAllLevels')}
                </button>
                <button
                  className="nb-btn nb-btn--secondary"
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
            <label className="checkbox-label">
              <input
                type="checkbox"
                checked={draft.isMainstream}
                onChange={(event) => setDraft({ ...draft, isMainstream: event.target.checked })}
              />
              <span>{copy.mainstream}</span>
            </label>
            <label>
              <span>{copy.excluded}</span>
              <textarea
                value={draft.excluded}
                onChange={(event) => setDraft({ ...draft, excluded: event.target.value })}
              />
              <small>{copy.excludedHint}</small>
            </label>
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
        </fieldset>
        {draft.modelTypes.includes('chat_completions') ? (
          <RolePolicyEditor
            value={draft.rolePolicy}
            onChange={(rolePolicy) => {
              if (save.isPending || remove.isPending || unknownSave) return;
              setDraft((current) => ({ ...current, rolePolicy }));
              save.reset();
            }}
            disabled={save.isPending || remove.isPending || Boolean(unknownSave)}
          />
        ) : null}
        <div className={`nb-workflow-price-workspace${referenceOpen ? ' is-reference-open' : ''}`}>
          <div className="nb-workflow-price-editor">
            <fieldset className="ops-form-section">
              <legend>{copy.pricingRewards}</legend>
              <Button
                type="button"
                ref={referenceTrigger}
                aria-expanded={referenceOpen}
                onClick={() => setReferenceOpen(true)}
              >
                {t('common.workflow.openPriceReference')}
              </Button>
              {priceSource ? (
                <p role="status">{t('common.workflow.priceApplied', { source: priceSource })}</p>
              ) : null}
              {undoPrice ? (
                <button
                  type="button"
                  className="nb-btn nb-btn--secondary"
                  onClick={() => {
                    setDraft((current) => ({ ...current, ...structuredClone(undoPrice) }));
                    setUndoPrice(null);
                    setPriceSource('');
                    pricingModeInput.current?.focus();
                  }}
                >
                  {t('common.workflow.undoPrice')}
                </button>
              ) : null}
              <div className="ops-field-grid">
                <label>
                  <span>{t(charityCopyKey(role, 'pricingMode'))}</span>
                  <select
                    ref={pricingModeInput}
                    value={draft.mode}
                    onChange={(event) =>
                      setDraft(switchPriceMode(draft, event.target.value as ModelDraft['mode']))
                    }
                  >
                    <option value="per_request">{t(charityCopyKey(role, 'perRequest'))}</option>
                    <option value="per_token">{t(charityCopyKey(role, 'perToken'))}</option>
                  </select>
                </label>
              </div>
              <div className="ops-actions">
                <button
                  className="nb-btn nb-btn--secondary"
                  type="button"
                  onClick={() => {
                    const prices =
                      draft.mode === 'per_request'
                        ? [['request', draft.requestUser]]
                        : Object.entries(draft.userPrices);
                    const invalid = prices.find(([, value]) => halfPrice(value) === null);
                    if (invalid) {
                      setHalfError(true);
                      priceInputs.current[invalid[0]]?.focus();
                      return;
                    }
                    setHalfError(false);
                    if (draft.mode === 'per_request') {
                      const reward = halfPrice(draft.requestUser);
                      if (reward !== null) setDraft({ ...draft, requestDonor: reward });
                    } else {
                      const entries = Object.entries(draft.userPrices).map(([key, value]) => [
                        key,
                        halfPrice(value),
                      ]);
                      if (entries.every(([, value]) => value !== null))
                        setDraft({
                          ...draft,
                          donorRewards: Object.fromEntries(entries) as TokenPrices,
                        });
                    }
                  }}
                >
                  {copy.halfPrice}
                </button>
                {halfError ? (
                  <p className="field-error" role="alert">
                    {copy.halfInvalid}
                  </p>
                ) : null}
              </div>
              <Fold title={t('common.operations.charity.quotaReserve')}>
                <label>
                  <span>{t('common.operations.charity.tokenReserveCredits')}</span>
                  <input
                    aria-label={t('common.operations.charity.tokenReserveCredits')}
                    inputMode="decimal"
                    value={draft.tokenReserveCredits ?? ''}
                    onChange={(event) =>
                      setDraft({ ...draft, tokenReserveCredits: event.target.value || null })
                    }
                  />
                  <small>{t('common.operations.charity.tokenReserveCreditsHelp')}</small>
                </label>
                {draft.mode === 'per_request' ? <p>{t('common.workflow.reserveLater')}</p> : null}
              </Fold>
              {draft.mode === 'per_request' ? (
                <div className="ops-field-grid">
                  <label>
                    <span>{t(charityCopyKey(role, 'request_user_price_milli'))}</span>
                    <input
                      value={draft.requestUser}
                      ref={(element) => {
                        priceInputs.current.request = element;
                      }}
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
                  <div className="charity-price-grid">
                    <div className="charity-price-headings" aria-hidden="true">
                      {(['userPrices', 'donorRewards'] as const).map((side) => (
                        <strong key={side}>
                          {t(
                            `common.operations.charity.${side === 'userPrices' ? 'userPrices' : 'donorRewards'}`,
                          )}
                        </strong>
                      ))}
                    </div>
                    {(Object.keys(draft.userPrices) as (keyof TokenPrices)[]).map((field) => (
                      <div className="charity-price-row" key={field}>
                        {(['userPrices', 'donorRewards'] as const).map((side) => (
                          <label key={side}>
                            <span>{t(tokenPriceCopyKey(role, side, field))}</span>
                            <input
                              value={draft[side][field]}
                              ref={(element) => {
                                if (side === 'userPrices') priceInputs.current[field] = element;
                              }}
                              onChange={(event) => setPrice(side, field, event.target.value)}
                            />
                          </label>
                        ))}
                      </div>
                    ))}
                  </div>
                </>
              )}
            </fieldset>
            <ContextPricingFields
              value={draft}
              role={role}
              onChange={(fields) => setDraft((current) => ({ ...current, ...fields }))}
            />
            <details className="ops-advanced" ref={discountDisclosure}>
              <summary>
                <strong>{copy.discountSettings}</strong>
                {' · '}
                {draft.discountEnabled
                  ? t('common.operations.charity.discountSummary', {
                      percent: draft.discountPercent,
                    })
                  : t('common.disabled')}
              </summary>
              <fieldset className="ops-form-section">
                <legend>{copy.discountSettings}</legend>
                <div className="ops-field-grid ops-paired-fields">
                  <TimeContextNotice station={role === 'admin' ? 'admin' : 'steward'} />
                  <label className="checkbox-label">
                    <input
                      type="checkbox"
                      checked={draft.discountEnabled}
                      onChange={(event) =>
                        setDraft({ ...draft, discountEnabled: event.target.checked })
                      }
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
                    station={role === 'admin' ? 'admin' : 'steward'}
                    draft={draft.discountStart}
                    showZoneHint={false}
                    onChange={(update) =>
                      setDraft((current) => ({
                        ...current,
                        discountStart: update(current.discountStart),
                      }))
                    }
                  />
                  <TimeInput
                    label={t(charityCopyKey(role, 'discountEnd'))}
                    station={role === 'admin' ? 'admin' : 'steward'}
                    draft={draft.discountEnd}
                    showZoneHint={false}
                    onChange={(update) =>
                      setDraft((current) => ({
                        ...current,
                        discountEnd: update(current.discountEnd),
                      }))
                    }
                  />
                </div>
              </fieldset>
            </details>
          </div>
          <div
            className="nb-workflow-price-reference"
            hidden={!referenceOpen}
            ref={referencePanel}
            tabIndex={-1}
          >
            <Panel aria-label={t('common.workflow.priceReferencePanel')}>
              <PanelHead
                title={t('common.workflow.priceReferencePanel')}
                level={3}
                description={t('common.workflow.currentPriceTarget', {
                  name: model?.full_name ?? `[公益]${draft.provider}/${draft.model}`,
                })}
                actions={
                  <Button type="button" onClick={() => setReferenceOpen(false)}>
                    {t('common.workflow.returnPriceDraft')}
                  </Button>
                }
              />
              <PanelBody>
                <div className="nb-workflow-price-reference-body">
                  <PriceTools
                    role={role}
                    current={draftPriceConfig(draft)}
                    onCapabilityLoss={onCapabilityLoss}
                    onApply={(config, name, revision) => {
                      setUndoPrice(snapshotPriceDraft(draft));
                      setDraft((current) => ({ ...current, ...priceDraft(config) }));
                      setPriceSource(`${name} · ${revision}`);
                      if (window.matchMedia?.('(width < 64rem)').matches) setReferenceOpen(false);
                    }}
                  />
                </div>
              </PanelBody>
              <div className="nb-workflow-price-reference-mobile-return">
                <PanelFoot>
                  <Button type="button" onClick={() => setReferenceOpen(false)}>
                    {t('common.workflow.returnWithoutFilling')}
                  </Button>
                </PanelFoot>
              </div>
            </Panel>
          </div>
        </div>
        <SessionHeaderFields
          value={draft.sessionHeaders}
          onChange={(sessionHeaders) => setDraft((value) => ({ ...value, sessionHeaders }))}
        />
      </fieldset>
      {save.error ? (
        <ErrorState error={save.error} />
      ) : remove.error ? (
        <ErrorState error={remove.error} />
      ) : null}
      {model && model.revision !== baseRevision ? (
        <p role="status">{t('common.operations.charity.modelChanged')}</p>
      ) : null}
      {validationError && validationError !== 'rolePolicy' && validationError !== 'modelTypes' ? (
        <p className="field-error" role="alert">
          {validationError === 'excludedFields'
            ? copy.excludedInvalid
            : validationError === 'modelLevels'
              ? t('common.operations.charity.validation.modelLevels')
              : validationError === 'publicDescription'
                ? t('common.operations.charity.validation.publicDescription')
                : validationError === 'affinityTTLSeconds'
                  ? t('common.operations.charity.validation.affinityTTLSeconds')
                  : t(`common.operations.charity.validation.${validationError}`)}
        </p>
      ) : null}
      <div className="ops-actions">
        <button
          className="nb-btn nb-btn--primary"
          type="button"
          disabled={
            Boolean(validationError) ||
            save.isPending ||
            remove.isPending ||
            Boolean(unknownSave) ||
            save.outcome === 'refresh-failed'
          }
          onClick={() => {
            if (!modelDraftError(draft))
              save.mutate(
                {
                  body: modelBody(
                    draft,
                    !model || draft.tokenReserveCredits !== (model.token_reserve_credits ?? null),
                    role === 'admin',
                  ),
                  revision: baseRevision,
                },
                {
                  onSuccess: (result) => {
                    setSavedDraft(priceDraftSignature(draft));
                    if (!model) setCreatedModel(result);
                  },
                },
              );
          }}
        >
          {model
            ? t('common.workflow.saveTarget', { id: model.id })
            : t(charityCopyKey(role, 'newModel'))}
        </button>
        {unknownSave && save.variables ? (
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            disabled={save.isPending || remove.isPending}
            onClick={() =>
              save.mutate(save.variables!, {
                onSuccess: (result) => {
                  setSavedDraft(priceDraftSignature(draft));
                  if (!model) setCreatedModel(result);
                },
              })
            }
          >
            {t('common.operations.charity.retrySavedModel')}
          </button>
        ) : null}
        {model ? (
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            disabled={
              save.isPending ||
              remove.isPending ||
              Boolean(unknownSave) ||
              save.outcome === 'refresh-failed'
            }
            onClick={() => {
              setDraft(modelDraft(model));
              setSavedDraft(priceDraftSignature(modelDraft(model)));
              setBaseRevision(model.revision);
              save.reset();
            }}
          >
            {t('common.operations.charity.reloadModel')}
          </button>
        ) : null}
        {model ? (
          <button
            className="nb-btn nb-btn--danger"
            type="button"
            disabled={
              save.isPending ||
              remove.isPending ||
              Boolean(unknownSave) ||
              save.outcome === 'refresh-failed'
            }
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
    </>
  );
  return (
    <Card className="charity-model-editor">
      <AsyncLeaveDialog dirty={priceDraftSignature(draft) !== savedDraft} />
      {content}
      {save.refreshError ? (
        <ErrorState error={save.refreshError} onRetry={() => void save.refresh()} />
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
  showAdaptation,
  renderKeyManager,
}: {
  role: CharityRole;
  accountId: string;
  refresh: () => Promise<unknown>;
  model: CharityModel;
  onCapabilityLoss?: () => void;
  showAdaptation: boolean;
  renderKeyManager: (props: CharityPickerKeyManagerProps) => ReactNode;
}) {
  const { t } = useTranslation();
  const adaptationCopy = useRequestAdaptationCopy();
  const [adaptationBinding, setAdaptationBinding] = useState('');
  const [, setParams] = useSearchState();
  const pager = usePagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'charity-binding-order',
    scopeKey: `${accountId}:${model.id}`,
  });
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
    bindings.isFetching || Boolean(bindings.error) || order.isPending || remove.isPending;
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
    <>
      {showAdaptation ? (
        <Fold
          lazy
          title={t('common.operations.charity.adaptationSettings', {
            defaultValue: 'Request adaptation',
          })}
        >
          <RequestAdaptationEditor
            key={`${accountId}:${role}:${model.id}`}
            url={`${role === 'admin' ? '/admin/api' : '/api/steward'}/charity-models/${encodeURIComponent(model.id)}/request-adaptation`}
            scope="charity-model"
            gatewayCacheDefaults={Boolean(
              bindings.data?.bindings.length &&
              bindings.data.bindings.every(
                (entry) => entry.source.connector_type === 'ai-sdk-gateway-v3',
              ),
            )}
            editable={role === 'admin'}
          />
        </Fold>
      ) : null}
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
                        {entry.source_types.length === 0 ? (
                          <>
                            <br />
                            <StatusBadge
                              active={false}
                              label={t('common.operations.charity.sourceBrowser.sourceMissing')}
                            />
                          </>
                        ) : null}
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
                        {entry.source.connector_type === 'ai-sdk-gateway-v3' ? (
                          <GatewayCapabilitySummary entry={entry.gateway_capabilities} />
                        ) : null}
                      </td>
                      <td className="ops-cell-wide" data-label={t(charityCopyKey(role, 'actions'))}>
                        <button
                          className="nb-btn nb-btn--secondary"
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
                        {showAdaptation ? (
                          <button
                            className="nb-btn nb-btn--secondary"
                            type="button"
                            disabled={busy}
                            onClick={() => setAdaptationBinding(entry.id)}
                          >
                            {adaptationCopy('title')}
                          </button>
                        ) : null}
                        <button
                          className="nb-btn nb-btn--secondary"
                          type="button"
                          disabled={index === 0 || busy}
                          onClick={() => move(index, -1)}
                        >
                          {t('common.operations.charity.moveUp')}
                        </button>
                        <button
                          className="nb-btn nb-btn--secondary"
                          type="button"
                          disabled={index === orderedBindings.length - 1 || busy}
                          onClick={() => move(index, 1)}
                        >
                          {t('common.operations.charity.moveDown')}
                        </button>
                        <button
                          className="nb-btn nb-btn--danger"
                          type="button"
                          disabled={busy || orderChanged}
                          onClick={() =>
                            remove.mutate({
                              id: entry.id,
                              revision: bindings.data.binding_revision,
                            })
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
        {showAdaptation &&
        adaptationBinding &&
        bindings.data?.bindings.some((entry) => entry.id === adaptationBinding) ? (
          <Fold
            lazy
            title={t('common.operations.charity.adaptationSettings', {
              defaultValue: 'Request adaptation',
            })}
          >
            <RequestAdaptationEditor
              key={`${accountId}:${model.id}:${adaptationBinding}`}
              url={`${role === 'admin' ? '/admin/api' : '/api/steward'}/charity-models/${encodeURIComponent(model.id)}/bindings/${encodeURIComponent(adaptationBinding)}/request-adaptation`}
              scope="binding"
              connectorType={
                bindings.data.bindings.find((entry) => entry.id === adaptationBinding)?.source
                  .connector_type
              }
              editable={role === 'admin'}
            />
          </Fold>
        ) : null}
        {orderChanged ? (
          <div className="ops-actions">
            <button
              type="button"
              className="nb-btn nb-btn--primary"
              disabled={busy}
              onClick={() => {
                if (orderDraft) order.mutate(orderDraft, { onSuccess: () => setOrderDraft(null) });
              }}
            >
              {t('common.operations.charity.saveOrder')}
            </button>
            <button
              type="button"
              className="nb-btn nb-btn--ghost"
              disabled={busy}
              onClick={() => setOrderDraft(null)}
            >
              {t('common.cancel')}
            </button>
            <span>{t('common.operations.charity.saveOrderHelp')}</span>
          </div>
        ) : null}
        <BindingAddForm
          role={role}
          accountId={accountId}
          model={model}
          refresh={refresh}
          onCapabilityLoss={onCapabilityLoss}
          renderKeyManager={renderKeyManager}
        />
        {order.error ? (
          <ErrorState error={order.error} />
        ) : remove.error ? (
          <ErrorState error={remove.error} />
        ) : null}
      </Card>
    </>
  );
}

export function ModelsPanel({
  renderScopedKeys,
  renderKeyManager,
  role,
  accountId,
  onCapabilityLoss,
  trainee = false,
}: {
  renderScopedKeys: (props: {
    modelID: string;
    accountId: string;
    onCapabilityLoss?: () => void;
  }) => import('react').ReactNode;
  renderKeyManager: (props: CharityPickerKeyManagerProps) => ReactNode;
  role: CharityRole;
  accountId: string;
  onCapabilityLoss?: () => void;
  trainee?: boolean;
}) {
  const { t } = useTranslation();
  const location = useLocation();
  const manageTriggerId = useId();
  const client = useQueryClient();
  const [params, setParams] = useSearchState();
  const rawQuery = oneParam(params, 'model_q');
  const query = validManagementSearch(rawQuery) ? rawQuery : '';
  const rawEnabled = oneParam(params, 'model_enabled');
  const enabled = ['true', 'false'].includes(rawEnabled) ? rawEnabled : '';
  const selectedId = selectedID(params, 'charity_model');
  const creating = !trainee && oneParam(params, 'model_workspace') === 'new' && !selectedId;
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
    const returnKey = !id ? selectedID(params, 'model_from_key') : '';
    const returnDonation = !id ? selectedID(params, 'model_from_donation') : '';
    setParams(
      (current) => {
        const next = new URLSearchParams(current);
        if (id) next.set('charity_model', id);
        else next.delete('charity_model');
        next.delete('model_workspace');
        const bindingDonation = selectedID(current, 'binding_donation');
        const bindingKey = selectedID(current, 'binding_key');
        if (bindingDonation && bindingKey) {
          next.set('charity_section', 'donations');
          next.set('donation_id', bindingDonation);
          next.set('donation_key', bindingKey);
          if (id) next.set('binding_target', id);
          next.delete('charity_model');
          next.delete('binding_donation');
        }
        if (returnKey && returnDonation) {
          next.set('charity_section', 'donations');
          next.set('donation_id', returnDonation);
          next.set('donation_key', returnKey);
        }
        next.delete('model_from_key');
        next.delete('model_from_donation');
        return next;
      },
      { state: { ...location.state, restoreDonationKey: returnKey || undefined } },
    );
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
    trainee ? 'trainee' : 'full',
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
    enabled: !selectedId && !creating,
    placeholderData: (previous, previousQuery) =>
      samePageFamily(previousQuery?.queryKey, listKey) ? previous : undefined,
  });
  const detail = useQuery({
    queryKey: [
      ...charityKeys.root(role),
      'model-detail',
      accountId,
      trainee ? 'trainee' : 'full',
      selectedId,
    ],
    queryFn: ({ signal }) => getManagedCharityModel(role, selectedId, signal),
    retry: false,
    enabled: Boolean(selectedId),
  });
  const selected = detail.data;
  const navigationReady = selectedId
    ? !detail.isPending && !detail.isFetching
    : !models.isPending && !models.isFetching;
  const { listRef, detailRef, remember } = useDetailNavigation(selectedId, navigationReady);
  const capabilityLost = [models.error, detail.error].some(
    (error) => isUnauthorized(error) || isForbidden(error),
  );
  useEffect(() => {
    if (capabilityLost) onCapabilityLoss?.();
  }, [capabilityLost, onCapabilityLoss]);
  const refresh = () =>
    client.invalidateQueries({ queryKey: charityKeys.root(role) }, { throwOnError: true });
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
      <PageHeader
        title={t(
          creating
            ? 'common.workflow.newModel'
            : selectedId
              ? 'common.workflow.editModel'
              : 'common.workflow.models',
        )}
        description={selected ? `${selected.full_name} · #${selected.id}` : undefined}
        back={
          creating || selectedId ? (
            <button className="nb-btn nb-btn--ghost" type="button" onClick={() => setSelected('')}>
              {t(
                selectedID(params, 'binding_donation')
                  ? 'common.workflow.returnDonation'
                  : selectedID(params, 'model_from_key')
                    ? 'common.keyModels.returnToKey'
                    : 'common.operations.charity.returnToList',
              )}
            </button>
          ) : undefined
        }
        actions={
          !trainee && !creating && !selectedId ? (
            <button
              type="button"
              className="nb-btn nb-btn--primary"
              onClick={() =>
                setParams((current) => {
                  const next = new URLSearchParams(current);
                  next.set('model_workspace', 'new');
                  next.delete('charity_model');
                  return next;
                })
              }
            >
              {t('common.workflow.newModel')}
            </button>
          ) : undefined
        }
      />
      {creating ? (
        <ModelForm
          role={role}
          refresh={refresh}
          onCapabilityLoss={onCapabilityLoss}
          onCreated={(model) => setSelected(model.id)}
        />
      ) : null}
      {!selectedId && !creating ? (
        <Card>
          <FilterBar
            activeCount={[query, enabled].filter(Boolean).length}
            secondaryAlwaysVisible
            secondaryLabel={t('common.workflow.filters')}
            onSubmit={() => {
              if (validManagementSearch(queryDraft)) updateFilters(queryDraft.trim());
            }}
            search={
              <>
                <Field label={t('common.operations.charity.searchModels')}>
                  {(props) => (
                    <input
                      {...props}
                      value={queryDraft}
                      maxLength={256}
                      placeholder={t('common.workflow.modelSearch')}
                      aria-invalid={!validManagementSearch(queryDraft)}
                      onChange={(event) => setQueryDraft(event.target.value)}
                    />
                  )}
                </Field>
                <Button type="submit" disabled={!validManagementSearch(queryDraft)}>
                  {t('common.applyFilter')}
                </Button>
              </>
            }
            secondary={
              <Field label={t(charityCopyKey(role, 'enabled'))}>
                {(props) => (
                  <select
                    {...props}
                    value={enabled}
                    onChange={(event) => updateFilters(query, event.target.value)}
                  >
                    <option value="">{t('common.all')}</option>
                    <option value="true">{t(charityCopyKey(role, 'enabled'))}</option>
                    <option value="false">{t(charityCopyKey(role, 'disabled'))}</option>
                  </select>
                )}
              </Field>
            }
          />
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
              <DataTable
                caption={t('common.workflow.models')}
                rowKey={(model) => model.id}
                rows={models.data.data}
                columns={[
                  {
                    key: 'model',
                    header: t(charityCopyKey(role, 'model')),
                    cell: 'title',
                    render: (model) => (
                      <>
                        <span
                          className="charity-model-call-name nb-workflow-id"
                          title={model.full_name}
                        >
                          {model.full_name}
                        </span>
                        <ModelTypesSummary value={model.model_types} />
                      </>
                    ),
                  },
                  {
                    key: 'state',
                    header: t('common.operations.charity.state'),
                    cell: 'status',
                    render: (model) => (
                      <StatusBadge
                        active={model.enabled}
                        label={t(charityCopyKey(role, model.enabled ? 'enabled' : 'disabled'))}
                      />
                    ),
                  },
                  {
                    key: 'description',
                    header: t('common.operations.charity.publicDescription'),
                    cell: 'meta',
                    render: (model) => model.public_description,
                  },
                  {
                    key: 'levels',
                    header: t('common.operations.charity.allowedLevels'),
                    cell: 'meta',
                    render: (model) =>
                      model.allowed_levels.length
                        ? model.allowed_levels
                            .map((level) => t('common.operations.charity.levelLabel', { level }))
                            .join(', ')
                        : t('common.operations.charity.noAllowedLevels'),
                  },
                  {
                    key: 'pricing',
                    header: t('common.operations.charity.pricing'),
                    cell: 'meta',
                    render: (model) => <PriceConfigBrief config={model} role={role} />,
                  },
                  {
                    key: 'bindings',
                    header: t(charityCopyKey(role, 'bindings')),
                    cell: 'meta',
                    align: 'num',
                    render: (model) => model.binding_count,
                  },
                  {
                    key: 'open',
                    header: t('common.workflow.actions'),
                    cell: 'action',
                    render: (model) => (
                      <Button
                        id={manageTriggerId + '-' + model.id}
                        type="button"
                        disabled={models.isFetching}
                        onClick={() => setSelected(model.id)}
                      >
                        {t('common.operations.charity.manage')}
                      </Button>
                    ),
                  },
                ]}
              />
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
      ) : null}
      {selectedId ? (
        <div className="ops-stack ops-detail-target" ref={detailRef} tabIndex={-1}>
          {detail.isPending ? (
            <LoadingState />
          ) : detail.error ? (
            <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />
          ) : selected ? (
            <fieldset className="ops-stack ops-unframed" disabled={detail.isFetching}>
              {trainee ? (
                <CharityRolePolicyForm
                  key={`roles:${selected.id}`}
                  model={selected}
                  refresh={refresh}
                  onCapabilityLoss={onCapabilityLoss}
                />
              ) : (
                <ModelForm
                  key={`model:${selected.id}`}
                  role={role}
                  model={selected}
                  refresh={refresh}
                  onDeleted={() => setSelected('')}
                  onCapabilityLoss={onCapabilityLoss}
                />
              )}
              <BindingsPanel
                key={`bindings:${selected.id}`}
                role={role}
                accountId={accountId}
                model={selected}
                refresh={refresh}
                onCapabilityLoss={onCapabilityLoss}
                showAdaptation={!trainee}
                renderKeyManager={renderKeyManager}
              />
              {trainee ? (
                <CharityModelScope modelID={selected.id}>
                  {renderScopedKeys({ modelID: selected.id, accountId, onCapabilityLoss })}
                </CharityModelScope>
              ) : null}
            </fieldset>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
