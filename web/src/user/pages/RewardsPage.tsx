import { rewardLabels, rejectionLabels } from '@shared/rewards/copy';
import { useState } from 'react';
import { CursorPagination } from '@shared/operations/CursorPagination';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Button, Field, Fold, Note, Panel, PanelHead, PanelBody } from '@shared/components/ui';
import { EmptyState, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { RewardSummary } from '@shared/rewards/RewardSummary';
import {
  getAssets,
  getReceipts,
  redeemReward,
  rewardKeys,
  type Receipt,
} from '@shared/rewards/api';
import { useUserSession } from '../data';
import '@shared/rewards/rewards.css';
export function RewardsPage() {
  const session = useUserSession();
  if (session.isPending) return <LoadingState />;
  if (session.error || !session.data) return <ErrorState error={session.error} />;
  return <RewardsContent key={session.data.user.id} account={session.data.user.id} />;
}
function RewardsContent({ account }: { account: string }) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const time = useDateTimeFormatter();
  const [code, setCode] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const [result, setResult] = useState<Receipt>();
  const [rejection, setRejection] = useState('');
  const [cursor, setCursor] = useState('');
  const [pages, setPages] = useState<string[]>([]);
  const assets = useQuery({
    queryKey: [...rewardKeys.user(account), 'assets'],
    queryFn: ({ signal }) => getAssets(false, signal),
  });
  const history = useQuery({
    queryKey: [...rewardKeys.user(account), 'history', cursor],
    queryFn: ({ signal }) => getReceipts(cursor, signal),
  });
  async function redeem() {
    setBusy(true);
    setError(undefined);
    setRejection('');
    setResult(undefined);
    try {
      const out = await redeemReward(code);
      if ('rejection' in out) {
        setRejection(out.rejection);
      } else {
        setResult(out);
        setCode('');
        await client.invalidateQueries({ queryKey: rewardKeys.user(account) });
        void client.invalidateQueries({ queryKey: ['user', 'session'] });
        void client.invalidateQueries({ queryKey: ['user', 'credits'] });
      }
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="reward-stack">
      <PageHeader
        title={t('common.rewards.userTitle')}
        description={t('common.rewards.userIntro')}
      />
      <Panel>
        <PanelHead title={t('common.rewards.redeem')} />
        <PanelBody>
          <form
            className="reward-form"
            onSubmit={(e) => {
              e.preventDefault();
              void redeem();
            }}
          >
            <Field label={t('common.rewards.codeText')} help={t('common.rewards.userCodeHelp')}>
              {(p) => (
                <input
                  {...p}
                  required
                  autoComplete="off"
                  maxLength={64}
                  value={code}
                  onChange={(e) => setCode(e.target.value)}
                  placeholder={t('common.rewards.codePlaceholder')}
                />
              )}
            </Field>
            <div>
              <Button variant="primary" type="submit" disabled={busy || !code.trim()}>
                {busy ? t('common.rewards.redeeming') : t('common.rewards.redeem')}
              </Button>
            </div>
          </form>
          {!!error && <ErrorState error={error} />}
          {rejection && (
            <Note tone="warn">
              {t(
                rejectionLabels[
                  rejection === 'conditions' || rejection === 'capacity' ? rejection : 'unavailable'
                ],
              )}
            </Note>
          )}
          {result && (
            <div className="reward-stack">
              <Note tone="ok" title={t('common.rewards.received')}>
                {result.title}
              </Note>
              <RewardSummary items={result.results} assets={assets.data ?? []} results />
            </div>
          )}
        </PanelBody>
      </Panel>
      <Fold title={t('common.rewards.rules')}>
        <p>{t('common.rewards.userRules')}</p>
      </Fold>
      <Panel>
        <PanelHead
          title={t('common.rewards.history')}
          actions={
            <Button type="button" onClick={() => void history.refetch()}>
              {t('common.rewards.refresh')}
            </Button>
          }
        />
        <PanelBody>
          {history.isPending ? (
            <LoadingState />
          ) : history.error ? (
            <ErrorState error={history.error} onRetry={() => void history.refetch()} />
          ) : history.data?.items.length === 0 ? (
            <EmptyState
              title={t('common.rewards.noHistory')}
              body={t('common.rewards.noHistoryHelp')}
            />
          ) : (
            <div className="reward-list">
              {history.data?.items.map((receipt) => (
                <article key={receipt.id} className="reward-entry">
                  <h3>{receipt.title}</h3>
                  <p>
                    {t(rewardLabels[receipt.kind === 'code' ? 'code' : 'broadcast'])} ·{' '}
                    {time(receipt.completed_at ?? receipt.created_at)}
                  </p>
                  <RewardSummary items={receipt.results} assets={assets.data ?? []} results />
                </article>
              ))}
            </div>
          )}
          {(pages.length > 0 || history.data?.next_cursor) && (
            <CursorPagination
              page={pages.length + 1}
              nextCursor={history.data?.next_cursor}
              onPrevious={() => {
                setCursor(pages.at(-1) ?? '');
                setPages(pages.slice(0, -1));
              }}
              onNext={(next) => {
                setPages([...pages, cursor]);
                setCursor(next);
              }}
            />
          )}
        </PanelBody>
      </Panel>
    </div>
  );
}
