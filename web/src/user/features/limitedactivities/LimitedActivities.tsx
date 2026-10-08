import { useState, type ReactNode } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router';
import { Card, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
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
export function LimitedActivitiesSection({ hideEmpty = false }: { hideEmpty?: boolean } = {}) {
  const text = useActivityText(),
    session = useUserSession(),
    client = useQueryClient(),
    account = session.data?.user.id ?? '';
  const query = useQuery({
    queryKey: [...limitedActivityKeys.root(account), 'directory'],
    queryFn: () => economySessionRequest(client, getDirectory, account),
    enabled: !!account && !session.error && !session.isFetching,
  });
  if (hideEmpty && query.data?.length === 0 && !query.error) return null;
  return (
    <section aria-labelledby="limited-activities-heading">
      <h2 id="limited-activities-heading">{text('common.limitedTimeActivities')}</h2>
      {query.isPending ? (
        <LoadingState />
      ) : query.error ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : null}
      {query.data?.length === 0 ? <p>{text('common.noLimitedTimeActivitiesAreListed')}</p> : null}
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
                <p className="limited-entry__eyebrow">{text('common.limitedTimeActivity')}</p>
                <h3>{name}</h3>
                <p>
                  {isBook
                    ? text('common.openThePictureBookAndCollectSketch')
                    : text('common.moveObstaclesAndGuideHungryFatFish')}
                </p>
                <p className="limited-entry__status">{statusLabel(activity.status, text)}</p>
                {activity.starts_at !== null && activity.ends_at !== null && (
                  <p>
                    <span>{text('common.schedule')}: </span>
                    {formatDateTime(activity.starts_at)} — {formatDateTime(activity.ends_at)}
                  </p>
                )}
                <p>{text(nextStep[activity.status])}</p>
                <span className="limited-entry__action">
                  {text('common.viewActivity')}: {name}
                </span>
              </div>
            </Link>
          );
        })}
      </div>
    </section>
  );
}
function ActivityInformation({ detail }: { readonly detail: ActivityDetail }) {
  const text = useActivityText();
  return (
    <div className="limited-notice" role="status">
      <p>{statusLabel(detail.status, text)}</p>
      {detail.starts_at !== null && detail.ends_at !== null ? (
        <p>
          {formatDateTime(detail.starts_at)} — {formatDateTime(detail.ends_at)}
        </p>
      ) : null}
      <p>{text('common.balancesAndRecordsContinueWhenTheActivity')}</p>
    </div>
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
      if (!error) {
        await Promise.all([
          client.invalidateQueries({ queryKey: limitedActivityKeys.root(account) }),
          client.invalidateQueries({ queryKey: ['user', 'credits'] }),
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
      onSuccess: (value) => {
        setReceipt(value);
        client.setQueryData(limitedActivityKeys.wallet(account), value.wallet);
      },
    });
  };
  return (
    <Card>
      <h2>{text('common.activityWalletAndExchange')}</h2>
      {wallet.isPending ? (
        <LoadingState />
      ) : wallet.error ? (
        <ErrorState error={wallet.error} onRetry={() => void wallet.refetch()} />
      ) : null}
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
      <p>
        {text('common.brushesRemainingAcrossTheSite')}:{' '}
        <ExactCount value={supply.brush_remaining} /> / <ExactCount value={supply.brush_cap} />
      </p>
      <p>{text('common.exchangeGeneralCreditsForActivityCurrencyExchanges')}</p>
      <form
        className="limited-form"
        onSubmit={(event) => {
          event.preventDefault();
          submit();
        }}
      >
        <fieldset disabled={locked}>
          <label>
            {text('common.currency')}
            <select
              value={asset}
              onChange={(event) => setAsset(event.target.value as ActivityAsset)}
            >
              <option value="sketch_paper">{currencyLabel('sketch_paper', text)}</option>
              <option value="sketch_brush">{currencyLabel('sketch_brush', text)}</option>
            </select>
          </label>
          <label>
            {text('common.quantity')}
            <input
              value={quantity}
              inputMode="numeric"
              pattern="[1-9][0-9]*"
              maxLength={39}
              onChange={(event) => setQuantity(event.target.value)}
              required
            />
          </label>
        </fieldset>
        <p>
          {text('common.generalCreditsCharged')}:{' '}
          <output>{cost === null ? '—' : <ExactCredits value={cost} />}</output>
        </p>
        {uncertain ? (
          <p role="status">{text('common.thePreviousResultIsUnconfirmedRetryThe')}</p>
        ) : null}
        {operation.error ? <ErrorState error={operation.error} /> : null}
        <button
          className="btn btn-primary"
          type="submit"
          disabled={
            operation.isPending ||
            (!uncertain && (detail.status !== 'open' || cost === null || !wallet.data))
          }
        >
          {uncertain ? text('common.retryTheSameExchange') : text('common.confirmExchange')}
        </button>
      </form>
      {receipt ? (
        <p role="status">
          {text('common.exchanged')} {receipt.receipt.quantity}{' '}
          {currencyLabel(receipt.receipt.asset, text)} · {text('common.generalCreditsCharged')}{' '}
          {receipt.receipt.cost}
        </p>
      ) : null}
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
        icon="activities"
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
