import { Field, Fields, FormActions, Note, StatusBadge } from '@shared/components/ui';
import { useState, type ReactNode } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router';
import {
  Card,
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
  SectionHead,
} from '@shared/components/States';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { responseOutcomeUnknown } from '@shared/operations/api';
import {
  decodeSupply,
  exchange,
  exchangeCost,
  getDetail,
  getDirectory,
  getWallet,
  type ActivityAsset,
  type ActivityDetail,
  type ExchangeInput,
  type ExchangeResult,
} from '@shared/limitedactivities/api';
import { currencyLabel, statusLabel, useActivityText } from '@shared/limitedactivities/copy';
import { formatDateTime } from '@shared/utils/datetime';
import { useUserSession } from '../../data';
import { UserPageGate } from '../../components/UserPageGate';
import { economySessionRequest } from '../economy/queries';
import { creditHistoryKeys } from '../credits/queries';
import { ExactCredits, ExactCount } from '../core/components';
import '@shared/limitedactivities/limited.css';
import pictureBookCover from '@shared/limitedactivities/picture-book-cover.webp';
import { limitedActivityKeys } from './queries';
const nextStep = {
  scheduled: 'common.nextScheduled',
  paused: 'common.nextPaused',
  ended: 'common.nextEnded',
  unavailable: 'common.nextUnavailable',
  unconfigured: 'common.nextUnconfigured',
  open: 'common.nextOpen',
} as const;
export function LimitedActivitiesSection({
  hideEmpty = false,
  children,
}: { hideEmpty?: boolean; children?: ReactNode } = {}) {
  const text = useActivityText(),
    session = useUserSession(),
    client = useQueryClient(),
    account = session.data?.user.id ?? '';
  const query = useQuery({
    queryKey: [...limitedActivityKeys.root(account), 'directory'],
    queryFn: () => economySessionRequest(client, getDirectory, account),
    enabled: !!account && !session.error && !session.isFetching,
  });
  if (hideEmpty && query.data?.length === 0 && !query.error && !children) return null;
  return (
    <section className="limited-section" aria-label={text('common.limitedTimeActivities')}>
      <SectionHead title={text('common.limitedTimeActivities')} />
      {query.isPending ? (
        <LoadingState />
      ) : query.error ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : null}
      {query.data?.length === 0 && !children ? (
        <EmptyState title={text('common.noLimitedTimeActivitiesAreListed')} body="" />
      ) : null}
      {(query.data?.length ?? 0) > 0 ? (
        <div className="limited-grid">
          {query.data?.map((activity) => {
            const isBook = activity.key === 'picture-book';
            const name = isBook ? text('common.pictureBook') : text('common.raiseABigFish');
            return (
              <Link
                key={activity.key}
                className="card limited-entry"
                to={'/activities/' + activity.key}
              >
                <div className={`limited-entry__cover limited-entry__cover--${activity.cover_key}`}>
                  {isBook ? (
                    <img
                      src={pictureBookCover}
                      alt={text('common.rengeCarefullyDrawingASimplePictureOn')}
                      width="1280"
                      height="720"
                    />
                  ) : (
                    <img
                      src="/assets/fatfish/cover.png"
                      alt={text('common.fatFishMoveObstaclesAndHappilyHead')}
                      width="1536"
                      height="1024"
                      loading="lazy"
                    />
                  )}
                </div>
                <div className="limited-entry__copy">
                  <div className="limited-entry__head">
                    <h3>{name}</h3>
                    <StatusBadge
                      tone={
                        activity.status === 'open'
                          ? 'ok'
                          : activity.status === 'paused' || activity.status === 'unavailable'
                            ? 'warn'
                            : activity.status === 'scheduled'
                              ? 'info'
                              : 'neutral'
                      }
                    >
                      {statusLabel(activity.status, text)}
                    </StatusBadge>
                  </div>
                  <p>
                    {isBook
                      ? text('common.openThePictureBookAndCollectSketch')
                      : text('common.moveObstaclesAndGuideHungryFatFish')}
                  </p>
                  {activity.starts_at !== null && activity.ends_at !== null && (
                    <p className="limited-entry__schedule">
                      <span>{text('common.schedule')}: </span>
                      {formatDateTime(activity.starts_at)} — {formatDateTime(activity.ends_at)}
                    </p>
                  )}
                  <p className="limited-entry__next">{text(nextStep[activity.status])}</p>
                  <span className="limited-entry__action">
                    {text('common.viewActivity')}: {name}
                  </span>
                </div>
              </Link>
            );
          })}
        </div>
      ) : null}
      {children && <div className="limited-grid">{children}</div>}
    </section>
  );
}
function ActivityInformation({ detail }: { readonly detail: ActivityDetail }) {
  const text = useActivityText();
  return (
    <Note
      tone={
        detail.status === 'open'
          ? 'ok'
          : detail.status === 'paused' || detail.status === 'unavailable'
            ? 'warn'
            : 'info'
      }
      title={statusLabel(detail.status, text)}
    >
      {detail.starts_at !== null && detail.ends_at !== null ? (
        <p>
          {formatDateTime(detail.starts_at)} — {formatDateTime(detail.ends_at)}
        </p>
      ) : null}
      <p>{text('common.balancesAndRecordsContinueWhenTheActivity')}</p>
    </Note>
  );
}
function ExchangePanel({
  account,
  detail,
}: {
  readonly account: string;
  readonly detail: ActivityDetail;
}) {
  const text = useActivityText(),
    client = useQueryClient();
  const [asset, setAsset] = useState<ActivityAsset>('sketch_paper'),
    [quantity, setQuantity] = useState('1'),
    [receipt, setReceipt] = useState<ExchangeResult | null>(null);
  const wallet = useQuery({
    queryKey: limitedActivityKeys.wallet(account),
    queryFn: () => economySessionRequest(client, getWallet, account),
  });
  const operation = useRetainedOperation(
    (input: ExchangeInput, key: string) =>
      economySessionRequest(client, () => exchange(input, key), account),
    async (_input, error) => {
      if (!error || responseOutcomeUnknown(error)) {
        await Promise.all([
          client.invalidateQueries({ queryKey: limitedActivityKeys.root(account) }),
          client.invalidateQueries({ queryKey: creditHistoryKeys.account(account) }),
        ]);
      }
    },
    ['user', 'limited-activities'],
  );
  const uncertain = operation.isError && responseOutcomeUnknown(operation.error),
    locked = operation.isPending || uncertain;
  const supply = decodeSupply(detail.module_config),
    cost = exchangeCost(
      asset === 'sketch_paper' ? supply.paper_price : supply.brush_price,
      quantity,
    );
  const submit = () => {
    const input = uncertain && operation.variables ? operation.variables : { asset, quantity };
    if (operation.isPending || (!uncertain && (cost === null || detail.status !== 'open'))) return;
    operation.mutate(input, {
      onSuccess: setReceipt,
    });
  };
  return (
    <Card className="limited-exchange-card">
      <h2>{text('common.activityWalletAndExchange')}</h2>
      {wallet.isPending ? (
        <LoadingState />
      ) : wallet.error ? (
        <ErrorState error={wallet.error} onRetry={() => void wallet.refetch()} />
      ) : null}
      <div className="limited-exchange-layout">
        <div className="limited-wallet-panel">
          {wallet.data ? (
            <dl className="limited-facts">
              <dt>{text('common.generalCredits')}</dt>
              <dd>
                <ExactCredits value={wallet.data.general} />
              </dd>
              <dt>{text('common.sketchPaper')}</dt>
              <dd>
                <ExactCount value={wallet.data.sketch_paper} />
              </dd>
              <dt>{text('common.paintBrushes')}</dt>
              <dd>
                <ExactCount value={wallet.data.sketch_brush} />
              </dd>
            </dl>
          ) : null}
          <p className="limited-wallet-panel__supply">
            {text('common.brushesRemainingAcrossTheSite')}:{' '}
            <ExactCount value={supply.brush_remaining} /> / <ExactCount value={supply.brush_cap} />
          </p>
        </div>
        <div className="limited-exchange-main">
          <p className="nb-small nb-muted">
            {text('common.exchangeGeneralCreditsForActivityCurrencyExchanges')}
          </p>
          <form
            className="limited-form"
            onSubmit={(event) => {
              event.preventDefault();
              submit();
            }}
          >
            <fieldset disabled={locked}>
              <Fields>
                <Field label={text('common.currency')} size="short">
                  {(props) => (
                    <select
                      {...props}
                      value={asset}
                      onChange={(event) => setAsset(event.target.value as ActivityAsset)}
                    >
                      <option value="sketch_paper">{currencyLabel('sketch_paper', text)}</option>
                      <option value="sketch_brush">{currencyLabel('sketch_brush', text)}</option>
                    </select>
                  )}
                </Field>
                <Field label={text('common.quantity')} size="short">
                  {(props) => (
                    <input
                      {...props}
                      className="limited-exchange-quantity"
                      value={quantity}
                      inputMode="numeric"
                      pattern="[1-9][0-9]*"
                      maxLength={39}
                      onChange={(event) => setQuantity(event.target.value)}
                      required
                    />
                  )}
                </Field>
              </Fields>
            </fieldset>
            {uncertain ? (
              <p role="status">{text('common.thePreviousResultIsUnconfirmedRetryThe')}</p>
            ) : null}
            {operation.error ? <ErrorState error={operation.error} /> : null}
            <div className="limited-exchange-footer">
              <p className="limited-exchange-cost">
                {text('common.generalCreditsCharged')}:{' '}
                <output>{cost === null ? '—' : <ExactCredits value={cost} />}</output>
              </p>
              <FormActions>
                <button
                  className="nb-btn nb-btn--primary"
                  type="submit"
                  disabled={
                    operation.isPending ||
                    (!uncertain && (detail.status !== 'open' || cost === null || !wallet.data))
                  }
                >
                  {uncertain ? text('common.retryTheSameExchange') : text('common.confirmExchange')}
                </button>
              </FormActions>
            </div>
          </form>
          {receipt ? (
            <Note tone="ok">
              {text('common.exchanged')} <ExactCount value={receipt.receipt.quantity} />{' '}
              {currencyLabel(receipt.receipt.asset, text)} · {text('common.generalCreditsCharged')}{' '}
              <ExactCredits value={receipt.receipt.cost} />
            </Note>
          ) : null}
        </div>
      </div>
    </Card>
  );
}
function PictureBookContent({
  account,
  children,
}: {
  readonly account: string;
  readonly children?: ReactNode;
}) {
  const text = useActivityText(),
    client = useQueryClient();
  const query = useQuery({
    queryKey: limitedActivityKeys.detail(account),
    queryFn: () => economySessionRequest(client, getDetail, account),
  });
  return (
    <div className="page">
      <PageHeader
        title={text('common.pictureBook')}
        eyebrow={text('common.limitedTimeActivity')}
        back={<Link to="/activities">{text('common.backToActivities')}</Link>}
      />
      {query.isPending ? (
        <LoadingState />
      ) : query.error ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : null}
      {query.data ? (
        <>
          <ActivityInformation detail={query.data} />
          <ExchangePanel account={account} detail={query.data} />
          {children}
        </>
      ) : null}
    </div>
  );
}
export function PictureBookPage({ children }: { readonly children?: ReactNode }) {
  const session = useUserSession(),
    account = session.data?.user.id;
  return (
    <UserPageGate>
      {account ? (
        <PictureBookContent key={account} account={account}>
          {children}
        </PictureBookContent>
      ) : null}
    </UserPageGate>
  );
}
