import { useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { operationKey, responseOutcomeUnknown } from '@shared/operations/api';
import {
  getDonationUses,
  mutateDonationUse,
  type DonationUse,
  type UseRole,
} from '@shared/operations/donationUses';
import {
  useRetainedOperation,
  type OperationContext,
} from '@shared/operations/useRetainedOperation';
import { charityKeys } from '@shared/operations/charity';
import { ConfirmDialog } from './ConfirmDialog';
import { ErrorState, LoadingState } from './States';
import { Note } from './ui';

type Write = {
  id: string;
  key: string;
  action: 'patch' | 'withdraw' | 'reset';
  input: Record<string, unknown>;
};
type Action = 'pause' | 'resume' | 'discoveryOn' | 'discoveryOff' | 'withdraw' | 'reset';

export function DonationKeyUseControls({
  role,
  accountId,
  donationId,
  donationKeyId,
  uses,
}: {
  role: UseRole;
  accountId: string;
  donationId: string;
  donationKeyId: string;
  uses?: DonationUse[];
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const root = role === 'admin' ? ['admin', 'operations'] : ['user'];
  const queryKey = [...root, 'donation-uses', accountId, donationId, donationKeyId, 'controls'];
  const query = useQuery({
    queryKey,
    queryFn: ({ signal }) => getDonationUses(role, donationId, '1', 100, '', signal, donationKeyId),
    retry: false,
    enabled: uses === undefined,
  });
  const receipts = useRef(new Set<string>());
  const [confirm, setConfirm] = useState<Write[] | null>(null);
  const refresh = async () => {
    const keys =
      role === 'owner'
        ? [
            ['user', 'economy', 'donations'],
            [...root, 'donation-uses', accountId, donationId],
          ]
        : [[...root, 'donation-uses', accountId, donationId], charityKeys.root(role)];
    await Promise.all(
      keys.map((queryKey) => client.invalidateQueries({ queryKey }, { throwOnError: true })),
    );
  };
  const run = async (writes: Write[], context: OperationContext) => {
    for (const write of writes) {
      context.assertCurrent();
      if (receipts.current.has(write.key)) continue;
      await mutateDonationUse(
        role,
        donationId,
        write.id,
        write.action,
        write.input,
        write.key,
        context.signal,
      );
      context.commit(() => receipts.current.add(write.key));
    }
  };
  const operation = useRetainedOperation<Write[], void>(
    (writes, _key, context) => run(writes, context),
    async (writes, error, context) => {
      if (error && responseOutcomeUnknown(error)) await run(writes, context);
      if (!error || responseOutcomeUnknown(error)) context.commit(() => setConfirm(null));
      await refresh();
      if (error && responseOutcomeUnknown(error)) return { operationConfirmed: true };
    },
    root,
  );
  const rows = uses ?? query.data?.data ?? [];
  const hasSource = (row: DonationUse) =>
    Boolean(row.pending?.discovery_source_id || row.approved?.discovery_source_id);
  const applicable = (action: Action) =>
    rows.filter((row) => {
      if (action === 'reset') return row.capabilities.can_reset_failures && row.approved;
      if (!row.capabilities.can_manage || (!row.pending && !row.approved)) return false;
      if (action === 'withdraw') return row.pending;
      if (action === 'pause') return row.enabled;
      if (action === 'resume') return !row.enabled;
      return hasSource(row) && row.discovery_enabled === (action === 'discoveryOff');
    });
  const locked =
    query.isFetching ||
    operation.isPending ||
    operation.outcome === 'unknown' ||
    Boolean(operation.refreshError);
  const begin = (action: Action) => {
    operation.reset();
    receipts.current.clear();
    const writes = applicable(action).map((row): Write => ({
      id: row.id,
      key: operationKey(),
      action: action === 'withdraw' || action === 'reset' ? action : 'patch',
      input: {
        expected_revision: row.revision,
        ...(action === 'pause' || action === 'resume'
          ? { enabled: action === 'resume' }
          : action === 'discoveryOn' || action === 'discoveryOff'
            ? { discovery_enabled: action === 'discoveryOn' }
            : {}),
      },
    }));
    if (action === 'withdraw') setConfirm(writes);
    else operation.mutate(writes);
  };
  const button = (action: Action, key: string) =>
    applicable(action).length ? (
      <button
        type="button"
        className="nb-btn nb-btn--secondary"
        disabled={locked}
        onClick={() => begin(action)}
      >
        {t(key)}
      </button>
    ) : null;
  const feedback = (
    <>
      {operation.error ? <ErrorState error={operation.error} /> : null}
      {operation.outcome === 'unknown' ? (
        <Note tone="warn">
          <p>{t('common.connections.unknownResult')}</p>
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            disabled={operation.isPending}
            onClick={() => void operation.check()}
          >
            {t('common.connections.checkResult')}
          </button>
        </Note>
      ) : null}
      {operation.refreshError ? (
        <ErrorState
          error={operation.refreshError}
          onRetry={operation.isSuccess ? () => void operation.refresh() : undefined}
        />
      ) : null}
    </>
  );
  return (
    <div className="nb-stack nb-stack--sm">
      {uses === undefined && query.isPending ? <LoadingState /> : null}
      {uses === undefined && query.error ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : null}
      <div className="nb-actions">
        {role === 'owner' ? (
          <>
            {button('pause', 'common.connections.pauseSharedKey')}
            {button('resume', 'common.connections.resumeSharedKey')}
            {button('discoveryOn', 'common.connections.enableKeyDiscovery')}
            {button('discoveryOff', 'common.connections.disableKeyDiscovery')}
            {button('withdraw', 'common.connections.withdrawPendingContent')}
          </>
        ) : null}
        {button('reset', 'common.connections.resetKeyFailures')}
      </div>
      {operation.isSuccess ? <Note tone="ok">{t('common.outcome.saved')}</Note> : null}
      {!confirm ? feedback : null}
      <ConfirmDialog
        open={Boolean(confirm)}
        title={t('common.connections.withdrawPendingContent')}
        description={t('common.connections.withdrawKeepsApproved')}
        confirmLabel={t('common.connections.withdrawPendingContent')}
        busy={operation.isPending}
        confirmDisabled={operation.outcome === 'unknown'}
        onCancel={() => setConfirm(null)}
        onConfirm={() => confirm && operation.mutate(confirm)}
        danger
      >
        {feedback}
      </ConfirmDialog>
    </div>
  );
}
