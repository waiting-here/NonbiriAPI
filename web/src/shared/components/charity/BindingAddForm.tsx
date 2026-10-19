import { useEffect, useState, type ReactNode } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import {
  addManagedBindingsReceipt,
  charityKeys,
  getManagedBindings,
  type CharityModel,
  type CharityRole,
} from '@shared/operations/charity';
import type { ManagedDonationKeySummary } from '@shared/operations/donationPages';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import {
  CharityBindingPicker,
  type CharityPickerKeyManagerProps,
  type CharitySelection,
} from '../CharityBindingPicker';
import { CharityModelScope } from '../CharityModelScope';
import { ErrorState, LoadingState } from '../States';
import { Button, Note } from '../ui';

export function BindingAddForm({
  role,
  accountId,
  model,
  fixedKey,
  refresh,
  onCapabilityLoss,
  renderKeyManager,
  onSelectionCount,
}: {
  role: CharityRole;
  accountId: string;
  model: CharityModel;
  fixedKey?: ManagedDonationKeySummary;
  refresh: () => Promise<unknown>;
  onCapabilityLoss?: () => void;
  renderKeyManager?: (props: CharityPickerKeyManagerProps) => ReactNode;
  onSelectionCount?: (count: number) => void;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const queryKey = [...charityKeys.bindings(role, model.id), accountId] as const;
  const bindings = useQuery({
    queryKey,
    queryFn: ({ signal }) => getManagedBindings(role, model.id, signal),
    retry: false,
  });
  const [selected, setSelected] = useState<Record<string, CharitySelection>>({});
  const [revision, setRevision] = useState<string | null>(null);
  const [pickerBlocked, setPickerBlocked] = useState(true);
  const [reading, setReading] = useState(false);
  const [readError, setReadError] = useState<unknown>(null);
  const add = useRetainedOperation<
    { selections: { donation_key_id: string; upstream_model_id: string }[]; revision: string },
    unknown
  >(
    (input, key) =>
      addManagedBindingsReceipt(role, model.id, input.revision, input.selections, key),
    refresh,
    charityKeys.root(role),
  );
  const chosen = Object.values(selected).map((entry) => ({
    donation_key_id: entry.donation_key_id,
    upstream_model_id: entry.upstream_model_id,
  }));
  useEffect(() => {
    onSelectionCount?.(chosen.length);
  }, [chosen.length, onSelectionCount]);
  const lost = [bindings.error, add.error, readError].some(
    (error) => isForbidden(error) || isUnauthorized(error),
  );
  useEffect(() => {
    if (lost) onCapabilityLoss?.();
  }, [lost, onCapabilityLoss]);
  const existing = bindings.data?.bindings.length ?? 0;
  const capacity = Math.max(0, 256 - existing);
  const identities = new Set(
    bindings.data?.bindings.map((entry) => `${entry.donation_key_id}:${entry.upstream_model_id}`),
  );
  const duplicates = Object.keys(selected).filter((key) => identities.has(key));
  const locked =
    reading ||
    bindings.isFetching ||
    Boolean(bindings.error) ||
    add.isPending ||
    add.outcome === 'unknown' ||
    add.outcome === 'refresh-failed';
  const readCurrent = async () => {
    setReading(true);
    setReadError(null);
    try {
      await refresh();
      const current = client.getQueryData<NonNullable<typeof bindings.data>>(queryKey);
      if (current) {
        setRevision(current.binding_revision);
        add.reset();
      }
    } catch (error) {
      setReadError(error);
    } finally {
      setReading(false);
    }
  };
  if (lost) return <p role="alert">{t('common.operations.charity.accessLost')}</p>;
  return (
    <section className="nb-form" aria-label={t('common.workflow.addConnections')}>
      <h3 className="nb-workflow-id">
        {t('common.workflow.targetModel', { name: model.full_name })}
      </h3>
      <p role="status">
        {t('common.workflow.bindingCapacity', { existing, capacity, selected: chosen.length })}
      </p>
      {bindings.isPending ? (
        <LoadingState />
      ) : bindings.error ? (
        <ErrorState
          error={bindings.error}
          onRetry={() => {
            if (add.outcome === 'refresh-failed') void add.refresh();
            else void bindings.refetch();
          }}
        />
      ) : (
        <CharityModelScope modelID={role === 'steward' ? model.id : undefined}>
          <CharityBindingPicker
            role={role}
            modelId={model.id}
            fixedKey={fixedKey}
            capacity={capacity}
            selected={selected}
            onChange={(next) => {
              if (chosen.length === 0 && Object.keys(next).length > 0)
                setRevision(bindings.data.binding_revision);
              if (Object.keys(next).length === 0) setRevision(null);
              setSelected(next);
              if (add.outcome !== 'unknown' && add.outcome !== 'conflict') add.reset();
            }}
            locked={locked}
            onCapabilityLoss={onCapabilityLoss}
            onReadStateChange={setPickerBlocked}
            renderKeyManager={renderKeyManager}
          />
        </CharityModelScope>
      )}
      {duplicates.length ? (
        <Note tone="warn">
          {t('common.workflow.alreadyAdded', { count: duplicates.length })}
          <Button
            type="button"
            disabled={locked}
            onClick={() => {
              const next = { ...selected };
              for (const key of duplicates) delete next[key];
              setSelected(next);
            }}
          >
            {t('common.workflow.removeAdded')}
          </Button>
        </Note>
      ) : null}
      {add.outcome === 'conflict' ? (
        <Note tone="warn">
          {t('common.workflow.bindingConflict')}
          <Button type="button" disabled={reading} onClick={() => void readCurrent()}>
            {t('common.workflow.refreshSelection')}
          </Button>
        </Note>
      ) : null}
      {readError ? <ErrorState error={readError} onRetry={() => void readCurrent()} /> : null}
      {add.error ? <ErrorState error={add.error} /> : null}
      {add.refreshError && !bindings.error ? (
        <ErrorState
          error={add.refreshError}
          onRetry={() => {
            if (add.outcome === 'refresh-failed') void add.refresh();
            else void readCurrent();
          }}
        />
      ) : null}
      {add.isSuccess ? <Note tone="ok">{t('common.workflow.bindingsSaved')}</Note> : null}
      <Button
        type="button"
        variant="primary"
        disabled={
          locked ||
          pickerBlocked ||
          !revision ||
          chosen.length === 0 ||
          chosen.length > capacity ||
          duplicates.length > 0 ||
          add.outcome === 'conflict'
        }
        onClick={() =>
          revision &&
          add.mutate(
            { selections: chosen, revision },
            {
              onSuccess: () => {
                setSelected({});
                setRevision(null);
              },
            },
          )
        }
      >
        {t('common.workflow.addCount', { count: chosen.length })}
      </Button>
      {add.outcome === 'unknown' && add.variables ? (
        <Button
          type="button"
          disabled={add.isPending}
          onClick={() =>
            add.mutate(add.variables!, {
              onSuccess: () => {
                setSelected({});
                setRevision(null);
              },
            })
          }
        >
          {t('common.donationReview.retrySame')}
        </Button>
      ) : null}
    </section>
  );
}
