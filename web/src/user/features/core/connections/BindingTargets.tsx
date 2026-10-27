import { ErrorState, LoadingState } from '@shared/components/States';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Fold, Note } from '@shared/components/ui';
import { ServiceRoutesSummary } from '@shared/components/ServiceRoutesSummary';
import { getBindings } from '../api';
import { coreKeys } from '../queries';
import type { Binding, Model } from '../types';
import { TargetMatrix, type MatrixTarget } from './TargetMatrix';
import { ConnectionOperationNotice } from './OperationNotice';
import { useConnectionOperation } from './useConnectionOperation';
import { useMetadataOperation } from './useMetadataOperation';
import { resourcePath } from './api';
import { useBindingCopy } from '../models/bindingCopy';
import { boundIdentities } from '../models/bindingIdentity';

export function BindingTargetEditor({
  accountId,
  model,
  binding,
  bindings = [],
  latestRevision,
  onSaved,
}: {
  accountId: string;
  model: Model;
  binding?: Binding;
  bindings?: readonly Binding[];
  latestRevision: string | undefined;
  onSaved?: () => void;
}) {
  const { t } = useTranslation();
  const copy = useBindingCopy();
  const [targets, setTargets] = useState<MatrixTarget[]>(
    () =>
      binding?.targets?.map((row) => ({
        ...row,
        options: row.native_services,
        label: row.base_url,
      })) ?? [],
  );
  const [expectedRevision, setExpectedRevision] = useState(model.binding_revision);
  const create = useConnectionOperation(accountId, (result) => {
    if (result.kind === 'binding') setExpectedRevision(result.bindings.binding_revision);
    setTargets([]);
    onSaved?.();
  });
  const replace = useMetadataOperation(
    accountId,
    '/api/models/' +
      resourcePath(model.id) +
      '/bindings/' +
      resourcePath(binding?.id ?? '') +
      '/targets',
    'PUT',
    (signal) => getBindings(model.id, signal),
    () => onSaved?.(),
    (saved) => setExpectedRevision(saved.binding_revision),
  );
  const operation = binding ? replace : create;
  const locked =
    operation.isPending ||
    operation.outcome === 'unknown' ||
    operation.outcome === 'refresh-failed';
  const ready =
    targets.length > 0 &&
    targets.every((row) => row.native_services.length > 0 && row.capabilities?.can_bind !== false);
  const save = () => {
    const input = targets.map(({ endpoint_key_id, upstream_model_id, native_services }) => ({
      endpoint_key_id,
      upstream_model_id,
      native_services,
    }));
    if (binding)
      void replace
        .mutateAsync({ expected_binding_revision: expectedRevision, targets: input })
        .catch(() => undefined);
    else
      void create
        .run({
          kind: 'binding',
          row: model.id,
          modelId: model.id,
          revision: expectedRevision,
          selection: { ...input[0]!, targets: input },
        })
        .catch(() => undefined);
  };
  return (
    <div className="nb-stack">
      <TargetMatrix
        accountId={accountId}
        modelId={model.id}
        value={targets}
        onChange={setTargets}
        disabled={locked}
        bound={boundIdentities(bindings.filter((row) => row.id !== binding?.id))}
      />
      <ConnectionOperationNotice operation={operation} />
      {operation.outcome === 'conflict' ? (
        <Note tone="warn">
          <p>{t('common.connections.conflictDraft')}</p>
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            disabled={locked || !latestRevision}
            onClick={() => {
              if (latestRevision) setExpectedRevision(latestRevision);
              operation.reset();
            }}
          >
            {t('common.connections.useCurrentRevision')}
          </button>
        </Note>
      ) : null}
      <button
        type="button"
        className="nb-btn nb-btn--primary"
        disabled={locked || !ready || operation.outcome === 'conflict'}
        onClick={save}
      >
        {binding ? copy.saveCombination : copy.addCombination}
      </button>
    </div>
  );
}
export function BindingTargets({ accountId, model }: { accountId: string; model: Model }) {
  const copy = useBindingCopy();
  const query = useQuery({
    queryKey: coreKeys.bindings(accountId, model.id),
    queryFn: ({ signal }) => getBindings(model.id, signal),
  });
  return (
    <Fold lazy title={copy.advanced}>
      <p className="nb-sub">{copy.advancedHelp}</p>
      <Fold lazy title={copy.addCombination}>
        {query.data ? (
          <BindingTargetEditor
            accountId={accountId}
            bindings={query.data.bindings}
            model={{ ...model, binding_revision: query.data.binding_revision }}
            latestRevision={
              query.isFetching || query.isError ? undefined : query.data.binding_revision
            }
          />
        ) : query.error ? (
          <ErrorState error={query.error} onRetry={() => void query.refetch()} />
        ) : (
          <LoadingState />
        )}
      </Fold>
      {query.data?.bindings.map((binding) => (
        <Fold lazy key={binding.id} title={`${copy.combination} ${binding.ord + 1}`}>
          <p className="nb-sub">{copy.combinationOrder}</p>
          {binding.targets?.map((target) => (
            <div key={target.id}>
              <strong>
                {target.base_url} · {target.upstream_model_id}
              </strong>
              <ServiceRoutesSummary value={target.service_routes} />
            </div>
          ))}
          <BindingTargetEditor
            accountId={accountId}
            bindings={query.data.bindings}
            model={{ ...model, binding_revision: query.data.binding_revision }}
            latestRevision={
              query.isFetching || query.isError ? undefined : query.data.binding_revision
            }
            binding={binding}
          />
        </Fold>
      ))}
    </Fold>
  );
}
