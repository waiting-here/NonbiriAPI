import { useEffect, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { managementRoot, type ManagementRole } from '@shared/operations/managedUsers';
import {
  adjustManagedResource,
  getManagedResources,
  resourceAdjustmentPreview,
  type ManagedResource,
  type ResourceAdjustment,
} from '@shared/operations/managedResources';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import {
  Button,
  DataTable,
  Field,
  Note,
  OutcomeNote,
  Segmented,
  type OutcomePresentation,
} from './ui';
import { Card, EmptyState, ErrorState, LoadingState, SectionHead } from './States';
import { QueryFeedback } from './QueryFeedback';
import { ConfirmDialog } from './ConfirmDialog';

export function UserResources({
  role,
  account,
  userId,
  editable,
}: {
  role: ManagementRole;
  account: string;
  userId: string;
  editable: boolean;
}) {
  const { t, i18n } = useTranslation();
  const groupNames: Record<string, string> = {
    drawing: t('management.resources.groups.drawing'),
    gacha: t('management.resources.groups.gacha'),
    cards: t('management.resources.groups.cards'),
    lake: t('management.resources.groups.lake'),
  };

  const editor = useRef<HTMLDivElement>(null);
  const [filter, setFilter] = useState('');
  const [group, setGroup] = useState('all');
  const [selected, setSelected] = useState<ManagedResource | null>(null);
  useEffect(() => {
    if (selected) editor.current?.scrollIntoView?.({ block: 'nearest' });
  }, [selected]);
  const [direction, setDirection] = useState<ResourceAdjustment['direction']>('increase');
  const [quantity, setQuantity] = useState('');
  const [reason, setReason] = useState('');
  const [confirm, setConfirm] = useState<ResourceAdjustment | null>(null);
  const query = useQuery({
    queryKey: [...managementRoot(role), 'resources', account, userId],
    queryFn: ({ signal }) => getManagedResources(role, userId, signal),
    staleTime: 0,
    refetchOnWindowFocus: false,
  });
  const refresh = async () => {
    const result = await query.refetch();
    if (result.error) throw result.error;
  };
  const mutation = useRetainedOperation(
    (input: ResourceAdjustment, key, context) =>
      adjustManagedResource(role, userId, input, key, context.signal),
    async (_input, error) => {
      if (!error) {
        await refresh();
        setSelected(null);
      }
    },
    managementRoot(role),
  );
  const locked =
    mutation.isPending || ['unknown', 'conflict', 'refresh-failed'].includes(mutation.outcome);
  const recheck = () => {
    if (mutation.outcome === 'unknown' && mutation.variables) mutation.mutate(mutation.variables);
    else if (mutation.outcome === 'refresh-failed') void mutation.refresh();
    else
      void refresh()
        .then(() => {
          setSelected(null);
          mutation.reset();
        })
        .catch(() => undefined);
  };
  const outcome: OutcomePresentation =
    mutation.outcome === 'unknown'
      ? { kind: 'unknown', recheck }
      : mutation.outcome === 'conflict'
        ? { kind: 'conflict', reload: recheck }
        : mutation.outcome === 'refresh-failed'
          ? { kind: 'savedRefreshFailed', recheck }
          : mutation.isSuccess
            ? { kind: 'saved' }
            : { kind: 'idle' };
  const name = (r: ManagedResource) =>
    i18n.resolvedLanguage?.startsWith('zh') ? r.name : r.name_en;
  const next = selected ? resourceAdjustmentPreview(selected, direction, quantity) : null;
  const valid =
    next !== null &&
    reason.trim().length > 0 &&
    [...reason].length <= 512 &&
    [...reason].every(
      (char) => char.charCodeAt(0) >= 32 && (char.charCodeAt(0) < 127 || char.charCodeAt(0) > 159),
    );
  const items = (query.data ?? []).filter(
    (r) =>
      (group === 'all' || r.group === group) &&
      `${r.name} ${r.name_en}`.toLocaleLowerCase().includes(filter.toLocaleLowerCase()),
  );
  if (query.isPending) return <LoadingState />;
  if (!query.data) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />;
  return (
    <section className="nb-stack" aria-label={t('management.resources.title')}>
      <SectionHead
        title={t('management.resources.title')}
        description={t('management.resources.description')}
      />
      {!editable ? <Note>{t('management.resources.readOnly')}</Note> : null}
      <QueryFeedback query={query} />
      {query.error ? <ErrorState error={query.error} onRetry={() => void query.refetch()} /> : null}
      {selected && editable ? (
        <div ref={editor}>
          <Card>
            <SectionHead
              title={name(selected)}
              description={t('management.resources.adjustHelp')}
            />
            <Segmented
              label={t('management.resources.direction')}
              value={direction}
              disabled={locked}
              options={(['increase', 'decrease'] as const).map((value) => ({
                value,
                label: t(
                  value === 'increase'
                    ? 'management.resources.increase'
                    : 'management.resources.decrease',
                ),
              }))}
              onChange={setDirection}
            />
            <Field
              label={t('management.resources.quantity')}
              help={t('management.resources.maximum', { maximum: selected.maximum })}
              error={
                quantity && next === null ? t('management.resources.invalidAmount') : undefined
              }
            >
              {(field) => (
                <input
                  {...field}
                  inputMode="numeric"
                  maxLength={40}
                  value={quantity}
                  disabled={locked}
                  onChange={(event) => setQuantity(event.target.value)}
                />
              )}
            </Field>
            <p aria-live="polite">
              {t('management.resources.beforeAfter', {
                before: selected.quantity,
                after: next ?? '—',
              })}
            </p>
            <Field label={t('management.resources.reason')}>
              {(field) => (
                <input
                  {...field}
                  value={reason}
                  maxLength={512}
                  disabled={locked}
                  onChange={(event) => setReason(event.target.value)}
                />
              )}
            </Field>
            <Note>{t('management.resources.relatedChanges')}</Note>
            <div className="nb-actions">
              <Button
                disabled={locked || !valid}
                onClick={() =>
                  setConfirm({
                    asset: selected.id,
                    direction,
                    quantity,
                    expected_quantity: selected.quantity,
                    reason,
                  })
                }
              >
                {t('management.resources.review')}
              </Button>
              <Button disabled={locked} onClick={() => setSelected(null)}>
                {t('common.cancel')}
              </Button>
            </div>
          </Card>
        </div>
      ) : null}
      <OutcomeNote outcome={outcome} busy={mutation.isPending} />
      {mutation.error && outcome.kind === 'idle' ? <ErrorState error={mutation.error} /> : null}
      <div className="nb-stack">
        <Field label={t('management.resources.search')}>
          {(field) => (
            <input {...field} value={filter} onChange={(event) => setFilter(event.target.value)} />
          )}
        </Field>
        <Field label={t('management.resources.group')}>
          {(field) => (
            <select {...field} value={group} onChange={(event) => setGroup(event.target.value)}>
              <option value="all">{t('management.resources.allGroups')}</option>
              {[...new Set(query.data.map((r) => r.group))].map((id) => (
                <option key={id} value={id}>
                  {groupNames[id] ?? id}
                </option>
              ))}
            </select>
          )}
        </Field>
        <Button
          disabled={locked || query.isFetching}
          onClick={() => void refresh().catch(() => undefined)}
        >
          {t('common.refresh')}
        </Button>
      </div>
      {items.length === 0 ? (
        <EmptyState title={t('common.noResults')} body={t('common.noResultsBody')} />
      ) : (
        <DataTable
          caption={t('management.resources.title')}
          rows={items}
          rowKey={(r) => r.id}
          columns={[
            {
              key: 'name',
              header: t('management.resources.resource'),
              cell: 'title',
              render: name,
            },
            {
              key: 'group',
              header: t('management.resources.group'),
              mobileLabel: t('management.resources.group'),
              cell: 'meta',
              render: (r) => groupNames[r.group] ?? r.group,
            },
            {
              key: 'quantity',
              header: t('management.resources.owned'),
              mobileLabel: t('management.resources.owned'),
              cell: 'meta',
              render: (r) => r.quantity,
            },
            ...(editable
              ? [
                  {
                    key: 'adjust',
                    header: t('management.resources.adjust'),
                    cell: 'action' as const,
                    render: (r: ManagedResource) => (
                      <Button
                        disabled={locked}
                        onClick={() => {
                          setSelected(r);
                          setQuantity('');
                          setReason('');
                          setDirection('increase');
                          mutation.reset();
                        }}
                      >
                        {t('management.resources.adjust')}
                      </Button>
                    ),
                  },
                ]
              : []),
          ]}
        />
      )}
      {confirm && selected ? (
        <ConfirmDialog
          open
          title={t('management.resources.confirmTitle')}
          confirmLabel={t('management.resources.confirm')}
          danger={confirm.direction === 'decrease'}
          description={
            <>
              <p>
                {name(selected)} ·{' '}
                {t('management.resources.beforeAfter', {
                  before: confirm.expected_quantity,
                  after: next,
                })}
              </p>
              <p>{confirm.reason}</p>
            </>
          }
          busy={locked}
          onCancel={() => setConfirm(null)}
          onConfirm={() => {
            setConfirm(null);
            mutation.mutate(confirm);
          }}
        />
      ) : null}
    </section>
  );
}
