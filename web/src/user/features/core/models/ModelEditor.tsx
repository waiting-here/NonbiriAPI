import { Field } from '@shared/components/ui';
import { sameRolePolicy } from './policy';
import { ModelTypesField } from '@shared/components/ModelTypesField';
import { RolePolicyEditor } from '@shared/components/RolePolicyEditor';
import { TransportRuleField } from '@shared/components/TransportRuleField';
import {
  Fold,
  Panel,
  PanelBody,
  PanelFoot,
  PanelHead,
  SaveBar,
  Toggle,
} from '@shared/components/ui';
import { useRegisteredCopy } from '@shared/i18n/useRegisteredCopy';
import { sameModelTypes, type ModelType } from '@shared/modelTypes';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { buildRolePolicy, draftFromRolePolicy, type RolePolicy } from '@shared/rolePolicy';
import type { TransportRule } from '@shared/transportRule';
import { useQueryClient } from '@tanstack/react-query';
import { useRef, useState, type FormEvent, type ReactNode } from 'react';
import { createModel, getModel, patchModel } from '../api';
import { CoreErrorPanel, SafeCopyValue } from '../components';
import { useCoreCopy } from '../copy';
import { validateLogicalName, validatePersonalProviderName } from '../normalizers';
import { coreKeys, invalidateResourceDependents } from '../queries';
import { useQuickstartCopy } from '../quickstartCopy';
import { isOutcomeUnknown } from '../request';
import { readResourceResult, resourceStatus } from '../resourceOperation';
import type { Model, ModelCreateInput, ModelPatchInput, RouteStrategy } from '../types';
import { useModelText } from './copy';

import { asNotice, isAccessLoss, type VisibleOutcome } from './feedback';

const roleSummaryKeys = {
  title: 'common.rolePolicy.title',
  defaultAction: 'common.rolePolicy.defaultAction',
  nativeHelp: 'common.rolePolicy.nativeHelp',
  toolsHelp: 'common.rolePolicy.toolsHelp',
  native: 'common.rolePolicy.action.native',
  passthrough: 'common.rolePolicy.action.passthrough',
  system: 'common.rolePolicy.action.system',
  user: 'common.rolePolicy.action.user',
  assistant: 'common.rolePolicy.action.assistant',
  reject: 'common.rolePolicy.action.reject',
} as const;

export function ModelRoleSummary({ policy }: { policy?: RolePolicy }) {
  const { t: text } = useRegisteredCopy(roleSummaryKeys);
  const current = policy ?? { default_action: 'native', rules: {} };
  return (
    <section className="core-card">
      <h2>{text('title')}</h2>
      <dl className="core-detail-list">
        <div>
          <dt>{text('defaultAction')}</dt>
          <dd>{text(current.default_action)}</dd>
        </div>
        {Object.entries(current.rules).map(([role, action]) => (
          <div key={role}>
            <dt>{role}</dt>
            <dd>{text(action)}</dd>
          </div>
        ))}
      </dl>
      <p className="core-muted">{text('nativeHelp')}</p>
      <p className="core-muted">{text('toolsHelp')}</p>
    </section>
  );
}

export function ModelEditor({
  accountId,
  initial,
  onCancel,
  onSaved,
  onCapabilityLoss,
  sources,
  initialStrategy,
}: {
  accountId: string;
  initial?: Model;
  initialStrategy?: RouteStrategy;
  sources?: (
    strategy: RouteStrategy,
    onChange: (value: RouteStrategy) => void,
    locked: boolean,
  ) => ReactNode;
  onCancel: () => void;
  onSaved: (model: Model) => void;
  onCapabilityLoss?: (error: unknown) => void;
}) {
  const { t } = useCoreCopy();
  const queryClient = useQueryClient();
  const [provider, setProvider] = useState(initial?.provider ?? '');
  const [modelName, setModelName] = useState(initial?.model ?? '');
  const [strategy, setStrategy] = useState<RouteStrategy>(
    initialStrategy ?? initial?.route_strategy ?? 'ordered',
  );
  const [modelTypes, setModelTypes] = useState<ModelType[]>(
    initial?.model_types ?? ['chat_completions'],
  );
  const [showTypeError, setShowTypeError] = useState(false);
  const [silentRetry, setSilentRetry] = useState(initial?.silent_retry ?? false);
  const [transportRule, setTransportRule] = useState<TransportRule>(
    initial?.transport_rule ?? 'passthrough',
  );
  const [flattenTools, setFlattenTools] = useState(initial?.flatten_tool_calls ?? false);
  const [roleDraft, setRoleDraft] = useState(() => draftFromRolePolicy(initial?.role_policy));
  const roleResult = buildRolePolicy(roleDraft);
  const textModel = useModelText();
  const formRef = useRef<HTMLFormElement>(null);
  const { t: text } = useQuickstartCopy();
  const [validation, setValidation] = useState(false);
  const [permissionLost, setPermissionLost] = useState<unknown>(null);
  const savedResult = useRef<Model | null>(null);
  const retryAllowed = useRef(false);
  const operation = useRetainedOperation<ModelCreateInput | ModelPatchInput, Model>(
    async (input, key, context) => {
      try {
        const identity = { idempotencyKey: key, actionId: key };
        const saved = initial
          ? await patchModel(initial.id, input as ModelPatchInput, identity, context.signal)
          : await createModel(input as ModelCreateInput, identity, context.signal);
        context.commit(() => {
          savedResult.current = saved;
          queryClient.setQueryData(coreKeys.model(accountId, saved.id), saved);
        });
        return saved;
      } catch (error) {
        if (isAccessLoss(error))
          context.commit(() => {
            setPermissionLost(error);
            onCapabilityLoss?.(error);
          });
        throw error;
      }
    },
    async (input, error, context) => {
      let confirmed = false;
      retryAllowed.current = false;
      if (error && !initial && isOutcomeUnknown(error) && context.operationKey) {
        const status = await resourceStatus(context.operationKey, context.signal);
        context.assertCurrent();
        retryAllowed.current = status.status === 'not_recorded';
        const result = await readResourceResult(
          { kind: 'model', row: '', input: input as ModelCreateInput },
          status,
          context.signal,
        );
        if (result?.kind === 'model') {
          context.commit(() => {
            savedResult.current = result.model;
            queryClient.setQueryData(coreKeys.model(accountId, result.model.id), result.model);
          });
          confirmed = true;
        }
      } else if (error && initial && isOutcomeUnknown(error)) {
        const current = await getModel(initial.id, context.signal);
        context.assertCurrent();
        const patch = input as ModelPatchInput;
        retryAllowed.current = current.revision === patch.expected_revision;
        confirmed =
          BigInt(current.revision) > BigInt(patch.expected_revision) &&
          Object.entries(patch).every(
            ([field, value]) =>
              field === 'expected_revision' ||
              (field === 'model_types'
                ? sameModelTypes(current.model_types, value as ModelType[])
                : field === 'role_policy'
                  ? sameRolePolicy(current.role_policy, value as RolePolicy)
                  : current[field as keyof Model] === value),
          );
        if (confirmed)
          context.commit(() => {
            savedResult.current = current;
          });
      }
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: coreKeys.modelsRoot(accountId) }),
        initial
          ? queryClient.invalidateQueries({ queryKey: coreKeys.model(accountId, initial.id) })
          : Promise.resolve(),
        invalidateResourceDependents(queryClient, accountId),
      ]);
      context.assertCurrent();
      if (!error || confirmed) {
        const saved = savedResult.current;
        if (saved) context.commit(() => onSaved(saved));
      }
      if (confirmed) return { operationConfirmed: true };
    },
    ['user', 'core'],
    {
      clearSecrets: () => {
        savedResult.current = null;
      },
    },
  );
  const busy = operation.isPending,
    hasAttempt = busy || operation.outcome === 'unknown';
  const outcome: VisibleOutcome =
    operation.outcome === 'unknown' || operation.outcome === 'conflict'
      ? operation.outcome
      : operation.outcome === 'failed'
        ? 'error'
        : null;
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setValidation(false);
    if (operation.isSuccess) {
      await operation.refresh();
      return;
    }
    if (operation.outcome === 'unknown' && operation.variables) {
      await operation.check();
      if (retryAllowed.current)
        await operation.mutateAsync(operation.variables).catch(() => undefined);
      return;
    }
    let input = operation.outcome === 'unknown' ? operation.variables : undefined;
    if (!input) {
      if (modelTypes.length === 0) {
        setShowTypeError(true);
        return;
      }
      if (!roleResult.policy) return;
      try {
        validatePersonalProviderName(provider);
        validateLogicalName(modelName);
      } catch {
        setValidation(true);
        return;
      }
      input = {
        provider,
        model: modelName,
        route_strategy: strategy,
        model_types: modelTypes,
        silent_retry: silentRetry,
        transport_rule: transportRule,
        flatten_tool_calls: flattenTools,
        role_policy: roleResult.policy,
        ...(initial ? { expected_revision: initial.revision } : {}),
      };
    }
    await operation.mutateAsync(input).catch(() => undefined);
  };

  if (permissionLost) {
    return (
      <section className="core-card">
        <CoreErrorPanel compact error={permissionLost} />
      </section>
    );
  }

  const dirtyCount = initial
    ? [
        provider !== initial.provider,
        modelName !== initial.model,
        strategy !== initial.route_strategy,
        !sameModelTypes(modelTypes, initial.model_types),
        silentRetry !== initial.silent_retry,
        transportRule !== initial.transport_rule,
        flattenTools !== initial.flatten_tool_calls,
        !roleResult.policy || !sameRolePolicy(initial.role_policy, roleResult.policy),
      ].filter(Boolean).length
    : 0;
  const isDefault =
    !silentRetry &&
    !flattenTools &&
    transportRule === 'passthrough' &&
    roleResult.policy &&
    sameRolePolicy(undefined, roleResult.policy);
  const discard = () => {
    if (!initial || hasAttempt) return;
    setProvider(initial.provider);
    setModelName(initial.model);
    setStrategy(initial.route_strategy);
    setModelTypes(initial.model_types);
    setShowTypeError(false);
    setSilentRetry(initial.silent_retry);
    setTransportRule(initial.transport_rule);
    setFlattenTools(initial.flatten_tool_calls);
    setRoleDraft(draftFromRolePolicy(initial.role_policy));
  };
  return (
    <form ref={formRef} className="model-editor core-form" onSubmit={(event) => void submit(event)}>
      <Panel>
        <PanelHead
          title={initial ? textModel('callName') : t('models.create')}
          actions={
            <button
              type="button"
              className="nb-btn nb-btn--secondary"
              disabled={hasAttempt}
              onClick={onCancel}
            >
              {t('common.cancel')}
            </button>
          }
        />
        <PanelBody>
          <div className="core-field-grid">
            <Field label={t('models.provider')} help={textModel('prefixHelp')}>
              {(props) => (
                <input
                  {...props}
                  className="core-mono"
                  value={provider}
                  maxLength={128}
                  required
                  disabled={hasAttempt}
                  onChange={(event) => setProvider(event.target.value)}
                />
              )}
            </Field>
            <Field label={t('models.model')}>
              {(props) => (
                <input
                  {...props}
                  className="core-mono"
                  value={modelName}
                  maxLength={128}
                  required
                  disabled={hasAttempt}
                  onChange={(event) => setModelName(event.target.value)}
                />
              )}
            </Field>
          </div>
          <ModelTypesField
            value={modelTypes}
            onChange={setModelTypes}
            disabled={hasAttempt}
            showError={showTypeError}
          />
          <div className="core-model-preview">
            <span>{textModel('clientValue')}</span>
            <SafeCopyValue
              value={`${provider || t('models.provider')}/${modelName || t('models.model')}`}
              label={textModel('callName')}
            />
          </div>
        </PanelBody>
      </Panel>
      {initial ? (
        <>
          {sources?.(strategy, setStrategy, hasAttempt)}
          <Fold
            title={textModel('advanced')}
            summary={textModel('advancedHelp')}
            persistKey="model-advanced"
            meta={textModel(isDefault ? 'default' : 'customized')}
          >
            <Toggle
              label={t('models.silentRetry')}
              description={textModel('retryHelp')}
              checked={silentRetry}
              disabled={hasAttempt}
              onChange={setSilentRetry}
            />
            {modelTypes.includes('chat_completions') ? (
              <>
                <TransportRuleField
                  value={transportRule}
                  onChange={setTransportRule}
                  disabled={hasAttempt}
                />
                <Toggle
                  label={t('models.flattenTools')}
                  description={textModel('toolsHelp')}
                  checked={flattenTools}
                  disabled={hasAttempt}
                  onChange={setFlattenTools}
                />
                <RolePolicyEditor value={roleDraft} onChange={setRoleDraft} disabled={hasAttempt} />
              </>
            ) : null}
          </Fold>
        </>
      ) : null}
      {validation ? (
        <p className="core-inline-error" role="alert">
          {t('models.invalidName')}
        </p>
      ) : null}
      {asNotice(
        outcome,
        () => void (operation.isSuccess ? operation.refresh() : operation.check()),
        busy,
        operation.outcome === 'refresh-failed',
      )}
      {initial && dirtyCount > 3 && !hasAttempt ? (
        <SaveBar
          dirtyCount={dirtyCount}
          busy={busy}
          saveDisabled={!!roleResult.error}
          onSave={() => formRef.current?.requestSubmit()}
          onDiscard={discard}
          saveLabel={t('common.save')}
          discardLabel={textModel('discard')}
          dirtyLabel={(count) => textModel('dirtyCount', { count })}
        />
      ) : (
        <PanelFoot>
          <button
            type="submit"
            className="nb-btn nb-btn--primary"
            disabled={busy || (!hasAttempt && !!roleResult.error)}
          >
            {busy ? t('common.working') : hasAttempt ? text('checkResult') : t('common.save')}
          </button>
        </PanelFoot>
      )}
    </form>
  );
}
