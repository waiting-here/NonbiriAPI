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
import '@shared/limitedactivities/limited.css';
import pictureBookCover from '@shared/limitedactivities/picture-book-cover.svg';

import { limitedActivityKeys } from './queries';

export function LimitedActivitiesSection() {
  const t = useActivityText(),
    session = useUserSession(),
    client = useQueryClient(),
    account = session.data?.user.id ?? '';
  const query = useQuery({
    queryKey: [...limitedActivityKeys.root(account), 'directory'],
    queryFn: () => economySessionRequest(client, getDirectory, account),
    enabled: !!account && !session.error && !session.isFetching,
  });
  return (
    <section aria-labelledby="limited-activities-heading">
      <h2 id="limited-activities-heading">{t('限时活动', 'Limited-time activities')}</h2>
      {query.isPending ? (
        <LoadingState />
      ) : query.error ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : null}
      {query.data?.length === 0 ? (
        <p>{t('暂无公开的限时活动。', 'No limited-time activities are listed.')}</p>
      ) : null}
      <div className="limited-grid">
        {query.data?.map((activity) => {
          const isBook = activity.key === 'picture-book';
          const name = isBook
            ? t('喵帕斯的绘本', 'Picture book')
            : t('饲养大肥鱼', 'Raise a big fish');
          return (
            <Card key={activity.key} className="limited-entry">
              <div className={`limited-entry__cover limited-entry__cover--${activity.cover_key}`}>
                {isBook ? (
                  <img
                    src={pictureBookCover}
                    alt={t('打开的绘本与星星', 'Open picture book with stars')}
                    width="960"
                    height="540"
                  />
                ) : (
                  <img
                    src="/assets/fatfish/cover.png"
                    alt={t(
                      '大肥鱼搬动障碍，开心地奔向大碗米饭',
                      'Fat Fish move obstacles and happily head toward bowls of rice',
                    )}
                    width="1536"
                    height="1024"
                    loading="lazy"
                  />
                )}
              </div>
              <div className="limited-entry__copy">
                <p className="limited-entry__eyebrow">{t('限时活动', 'Limited-time activity')}</p>
                <h3>{name}</h3>
                <p>
                  {isBook
                    ? t(
                        '翻开绘本，收集草稿纸与画笔。',
                        'Open the picture book and collect sketch paper and brushes.',
                      )
                    : t(
                        '搬动障碍，帮饥肠辘辘的大肥鱼找到米饭。',
                        'Move obstacles and guide hungry Fat Fish to their rice.',
                      )}
                </p>
                <p className="limited-entry__status">{statusLabel(activity.status, t)}</p>
                <Link className="btn btn-secondary" to={'/activities/' + activity.key}>
                  {t('查看活动', 'View activity')}: {name}
                </Link>
              </div>
            </Card>
          );
        })}
      </div>
    </section>
  );
}
function ActivityInformation({ detail }: { readonly detail: ActivityDetail }) {
  const t = useActivityText();
  return (
    <div className="limited-notice" role="status">
      <p>{statusLabel(detail.status, t)}</p>
      {detail.starts_at !== null && detail.ends_at !== null ? (
        <p>
          {formatDateTime(detail.starts_at)} — {formatDateTime(detail.ends_at)}
        </p>
      ) : null}
      <p>
        {t(
          '活动再次开放时，已有余额和记录会继续保留。',
          'Balances and records continue when the activity reopens.',
        )}
      </p>
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
  const t = useActivityText(),
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
      <h2>{t('活动钱包与兑换', 'Activity wallet and exchange')}</h2>
      {wallet.isPending ? (
        <LoadingState />
      ) : wallet.error ? (
        <ErrorState error={wallet.error} onRetry={() => void wallet.refetch()} />
      ) : null}
      {wallet.data ? (
        <dl className="limited-facts">
          <dt>{t('通用悠哉积分', 'General credits')}</dt>
          <dd>{wallet.data.general}</dd>
          <dt>{t('草稿纸', 'Sketch paper')}</dt>
          <dd>{wallet.data.sketch_paper}</dd>
          <dt>{t('画笔', 'Paint brushes')}</dt>
          <dd>{wallet.data.sketch_brush}</dd>
        </dl>
      ) : null}
      <p>
        {t('全站画笔剩余可兑量', 'Brushes remaining across the site')}: {supply.brush_remaining} /{' '}
        {supply.brush_cap}
      </p>
      <p>
        {t(
          '仅可使用通用悠哉积分兑换。不可退换、反向兑换或在两种活动币之间兑换。',
          'Exchange general credits for activity currency. Exchanges cannot be reversed or converted between activity currencies.',
        )}
      </p>
      <form
        className="limited-form"
        onSubmit={(event) => {
          event.preventDefault();
          submit();
        }}
      >
        <fieldset disabled={locked}>
          <label>
            {t('兑换币种', 'Currency')}
            <select
              value={asset}
              onChange={(event) => setAsset(event.target.value as ActivityAsset)}
            >
              <option value="sketch_paper">{currencyLabel('sketch_paper', t)}</option>
              <option value="sketch_brush">{currencyLabel('sketch_brush', t)}</option>
            </select>
          </label>
          <label>
            {t('兑换数量', 'Quantity')}
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
          {t('通用积分扣减', 'General credits charged')}: <output>{cost ?? '—'}</output>
        </p>
        {uncertain ? (
          <p role="status">
            {t(
              '上次兑换结果尚未确认。请重试同一次兑换，不会重复扣款。',
              'The previous result is unconfirmed. Retry the same exchange without a duplicate charge.',
            )}
          </p>
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
          {uncertain
            ? t('重试同一次兑换', 'Retry the same exchange')
            : t('确认兑换', 'Confirm exchange')}
        </button>
      </form>
      {receipt ? (
        <p role="status">
          {t('已兑换', 'Exchanged')} {receipt.receipt.quantity}{' '}
          {currencyLabel(receipt.receipt.asset, t)} · {t('通用积分扣减', 'General credits charged')}{' '}
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
  const t = useActivityText(),
    client = useQueryClient();
  const query = useQuery({
    queryKey: limitedActivityKeys.detail(account),
    queryFn: () => economySessionRequest(client, getDetail, account),
  });
  return (
    <div className="page">
      <PageHeader
        title={t('喵帕斯的绘本', 'Picture book')}
        eyebrow={t('限时活动', 'Limited-time activity')}
        icon="activities"
        back={<Link to="/activities">{t('返回活动', 'Back to activities')}</Link>}
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
