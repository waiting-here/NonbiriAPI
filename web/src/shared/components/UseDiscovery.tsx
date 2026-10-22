import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import {
  readDonationDiscovery,
  startDonationDiscovery,
  type DiscoveryEvidence,
} from '@shared/operations/donationDiscovery';
import { responseOutcomeUnknown } from '@shared/operations/api';
import type { DonationUse } from '@shared/operations/donationUses';
import type { CharityRole } from '@shared/operations/charity';
import { ErrorState } from './States';
import { Note } from './ui';
export function UseDiscovery({
  role,
  accountId,
  row,
}: {
  role: CharityRole;
  accountId: string;
  row: DonationUse;
}) {
  const { t } = useTranslation();
  const [started, setStarted] = useState(0);
  const [evidence, setEvidence] = useState<DiscoveryEvidence | null>(null);
  const ref = {
    donation_id: row.donation_id,
    key_id: row.donation_key_id,
    use_id: row.id,
    expected_revision: row.revision,
  };
  const root = role === 'admin' ? ['admin', 'operations'] : ['user'];
  const query = useQuery({
    queryKey: [...root, 'use-discovery', accountId, row.id, row.revision],
    queryFn: () => readDonationDiscovery(role, ref),
    enabled: started > 0,
    refetchInterval: (query) =>
      query.state.data?.state === 'checking' && Date.now() - started < 30000 ? 1500 : false,
  });
  const operation = useRetainedOperation<typeof ref, DiscoveryEvidence>(
    async (input, key, context) => {
      const value = await startDonationDiscovery(role, input, key);
      context.commit(() => {
        setEvidence(value);
        setStarted(Date.now());
      });
      return value;
    },
    async (input, error, context) => {
      if (error && responseOutcomeUnknown(error) && context.operationKey) {
        const value = await startDonationDiscovery(role, input, context.operationKey);
        context.commit(() => {
          setEvidence(value);
          setStarted(Date.now());
        });
        return { operationConfirmed: true };
      }
    },
    root,
  );
  const current = query.data ?? evidence ?? row.approved?.discovery;
  return (
    <div className="nb-stack nb-stack--sm">
      <button
        type="button"
        className="nb-btn nb-btn--secondary"
        disabled={
          !row.capabilities.can_discover ||
          operation.isPending ||
          operation.outcome === 'unknown' ||
          query.isFetching
        }
        onClick={() => {
          if (current?.state === 'checking') {
            setStarted(Date.now());
            void query.refetch();
          } else void operation.mutateAsync(ref).catch(() => undefined);
        }}
      >
        {t('common.connections.refreshSharedCatalog')}
      </button>
      {current ? (
        <Note>
          {t('common.connections.sharedDiscoveryState', {
            state: t(`common.connections.sourceStates.${current.state}`),
            count: current.count ?? '—',
          })}
        </Note>
      ) : null}
      {query.error || operation.error ? (
        <ErrorState error={query.error ?? operation.error} />
      ) : null}
      {operation.outcome === 'unknown' ? (
        <button
          type="button"
          className="nb-btn nb-btn--secondary"
          disabled={operation.isPending}
          onClick={() => void operation.check().catch(() => undefined)}
        >
          {t('common.connections.checkResult')}
        </button>
      ) : null}
    </div>
  );
}
