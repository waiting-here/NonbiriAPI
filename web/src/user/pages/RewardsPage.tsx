import { rewardLabels, rejectionLabels } from '@shared/rewards/copy';
import { useRef, useState } from 'react';
import { CursorPagination } from '@shared/operations/CursorPagination';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import {
  Button,
  Field,
  Fold,
  Note,
  Panel,
  PanelHead,
  PanelBody,
  StatusBadge,
} from '@shared/components/ui';
import { EmptyState, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { RewardSummary } from '@shared/rewards/RewardSummary';
import {
  getAssets,
  getReceipts,
  redeemReward,
  rewardKeys,
  type Redemption,
} from '@shared/rewards/api';
import { UserPageGate } from '../components/UserPageGate';
import { coreKeys } from '../features/core/queries';
import { useUserSession } from '../data';
import { creditHistoryKeys } from '../features/credits/queries';
import { limitedActivityKeys } from '../features/limitedactivities/queries';
import { stationSessionWrite } from '@shared/charityManagement';
import { responseOutcomeUnknown } from '@shared/operations/api';
import '@shared/rewards/rewards.css';
export function RewardsPage() {
  const session = useUserSession();
  return (
    <UserPageGate>
      {session.data ? (
        <RewardsContent key={session.data.user.id} account={session.data.user.id} />
      ) : null}
    </UserPageGate>
  );
}
function RewardsContent({ account }: { account: string }) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const time = useDateTimeFormatter();
  const [code, setCode] = useState('');
  const [busy, setBusy] = useState(false);
  const sending = useRef(false);
  const pending = useRef<{ code: string; key: string } | null>(null);
  const [unknownCode, setUnknownCode] = useState<string | null>(null);
  const unknown = unknownCode !== null && unknownCode === code.trim();
  const [error, setError] = useState<unknown>();
  const [result, setResult] = useState<Redemption>();
  const [rejection, setRejection] = useState('');
  const [bannedUntil, setBannedUntil] = useState<number | null>(null);
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
  const invalidateBalances = () => {
    void client.invalidateQueries({ queryKey: coreKeys.me(account) });
    void client.invalidateQueries({ queryKey: creditHistoryKeys.account(account) });
    void client.invalidateQueries({ queryKey: limitedActivityKeys.wallet(account) });
  };
  async function redeem() {
    if (sending.current) return;
    sending.current = true;
    setBusy(true);
    const submitted = code.trim();
    const operation =
      pending.current?.code === submitted
        ? pending.current
        : { code: submitted, key: crypto.randomUUID() };
    pending.current = operation;
    setError(undefined);
    setRejection('');
    setBannedUntil(null);
    setResult(undefined);
    try {
      const out = await stationSessionWrite(client, 'steward', () =>
        redeemReward(operation.code, operation.key),
      );
      pending.current = null;
      setUnknownCode(null);
      if ('rejection' in out) {
        setRejection(out.rejection);
        if (out.rejection === 'banned') {
          setBannedUntil(out.banned_until);
          void client.invalidateQueries({ queryKey: ['user', 'session'] });
        }
      } else {
        setResult(out);
        setCode('');
        invalidateBalances();
        void client.invalidateQueries({ queryKey: ['user', 'session'] });
        await client.invalidateQueries({ queryKey: rewardKeys.user(account) });
      }
    } catch (e) {
      const unresolved = responseOutcomeUnknown(e);
      if (unresolved) invalidateBalances();
      else pending.current = null;
      setUnknownCode(unresolved ? operation.code : null);
      setError(e);
    } finally {
      sending.current = false;
      setBusy(false);
    }
  }
  return (
    <div className="page reward-stack rewards-page">
      <PageHeader
        icon="rewards"
        title={t('common.rewards.userTitle')}
        description={t('common.rewards.userIntro')}
      />
      <Panel>
        <PanelBody>
          <form
            className="reward-form"
            onSubmit={(e) => {
              e.preventDefault();
              void redeem();
            }}
          >
            <Field
              label={t('common.rewards.codeText')}
              help={t('common.rewards.userCodeHelp')}
              span
            >
              {(p) => (
                <div className="reward-form__controls">
                  <input
                    {...p}
                    required
                    autoComplete="off"
                    maxLength={64}
                    disabled={busy}
                    value={code}
                    onChange={(e) => setCode(e.target.value)}
                    placeholder={t('common.rewards.codePlaceholder')}
                  />
                  <Button variant="primary" type="submit" disabled={busy || !code.trim()}>
                    {busy
                      ? t('common.rewards.redeeming')
                      : unknown
                        ? t('common.retry')
                        : t('common.rewards.redeem')}
                  </Button>
                </div>
              )}
            </Field>
          </form>
          {!!error && <ErrorState error={error} />}
          {unknown && <Note tone="warn">{t('common.rewards.retryUnknown')}</Note>}
          {bannedUntil !== null && (
            <Note tone="warn">
              {t('common.rewards.redemptionBanned', { time: time(bannedUntil) })}
            </Note>
          )}
          {rejection && rejection !== 'banned' && (
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
              <Note
                tone={result.already_claimed ? 'info' : 'ok'}
                title={t(
                  result.already_claimed
                    ? 'common.rewards.alreadyClaimed'
                    : 'common.rewards.received',
                )}
              >
                {result.title}
                {result.already_claimed && (
                  <p>
                    {t('common.rewards.previousReceipt', {
                      time: time(result.completed_at ?? result.created_at),
                    })}
                  </p>
                )}
              </Note>
              <RewardSummary items={result.results} assets={assets.data ?? []} results />
            </div>
          )}
          <Fold plain title={t('common.rewards.rules')}>
            <p>{t('common.rewards.userRules')}</p>
          </Fold>
        </PanelBody>
      </Panel>
      <Panel>
        <PanelHead
          title={t('common.rewards.history')}
          actions={
            <Button
              type="button"
              size="sm"
              disabled={history.isFetching}
              onClick={() => void history.refetch()}
            >
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
                  <div className="reward-entry__head">
                    <h3>{receipt.title}</h3>
                    <span className="reward-entry__meta">
                      <StatusBadge tone={receipt.kind === 'code' ? 'accent' : 'info'}>
                        {t(rewardLabels[receipt.kind === 'code' ? 'code' : 'broadcast'])}
                      </StatusBadge>
                      <time>{time(receipt.completed_at ?? receipt.created_at)}</time>
                    </span>
                  </div>
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
