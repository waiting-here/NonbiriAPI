import { guestVisitLabel, useWhaleText } from '../copy';
import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import {
  PRESET_MESSAGES,
  displayNeighborName,
  OUTFIT_CATALOG,
  getSceneAssetUrl,
} from '../game/catalog';
import type { NeighborFilterKind, NeighborStation, CatchActionKind } from '../game/types';
import { searchNeighbors, type Profile, type Action } from '../api';

export type Dispatch = (action: Action, onError?: (reason: string) => void) => Promise<boolean>;
interface Props {
  profile: Profile;
  dispatch: Dispatch;
  busy: boolean;
  onPreview: (neighbor: NeighborStation) => void;
  onDepart: (neighbor: NeighborStation) => void;
  onRefresh: () => void;
  refreshing: boolean;
  onActivity: () => void;
  onWallet: () => void;
}
const filters: readonly [NeighborFilterKind, string, string][] = [
  ['all', '全部', 'All'],
  ['has_food', '有饭', 'Food available'],
  ['pinned', '已收藏', 'Pinned'],
  ['visited', '来往过', 'Visited'],
];
const reactions: readonly [CatchActionKind, string, string][] = [
  ['hire', '雇佣帮工', 'Hire as a helper'],
  ['treat', '招待', 'Offer a meal'],
  ['gentle', '温和送回', 'Send home gently'],
  ['flick', '弹脑瓜送回', 'Flick and send home'],
];
export function SocialPane({
  profile,
  dispatch,
  busy,
  onPreview,
  onDepart,
  onRefresh,
  refreshing,
  onActivity,
  onWallet,
}: Props) {
  const t = useWhaleText();
  const formatTime = useDateTimeFormatter();
  const social = profile.social;
  const values = profile.settings.values;
  const [search, setSearch] = useState('');
  const [filter, setFilter] = useState<NeighborFilterKind>('all');
  const [close, setClose] = useState(false);
  const [dialogError, setDialogError] = useState('');
  const [guestAction, setGuestAction] = useState<{
    id: string;
    action: CatchActionKind;
    label: string;
    arrived: number;
    working: boolean;
  } | null>(null);
  const [message, setMessage] = useState('');
  const neighbors = useQuery({
    queryKey: ['user', 'games', 'whale-haven', 'neighbors', profile.social.door, filter, search],
    queryFn: ({ signal }) => searchNeighbors(search.trim(), filter, signal),
    enabled: social.open,
    retry: false,
    staleTime: 0,
    refetchInterval: 20000,
  });
  const activeGuest =
    guestAction &&
    social.guests.find(
      (g) =>
        g.neighbor_id === guestAction.id &&
        g.started_at_ms === guestAction.arrived &&
        g.working === guestAction.working &&
        (guestAction.action !== 'hire' || (g.allow_hire && !g.hire_blocked)),
    );
  const latestSettlement = profile.snapshot.logs?.find((entry) => entry.kind === 'settlement');
  const shiftEnd = (at?: number) =>
    typeof at === 'number' && (
      <p>
        {t('预计收工', 'Scheduled finish')}{' '}
        <time dateTime={new Date(at).toISOString()}>{formatTime(at)}</time>
      </p>
    );
  return (
    <>
      <div className="wh-social-tools">
        <button
          className="nb-btn nb-btn--sm"
          type="button"
          disabled={busy || refreshing || (social.open && neighbors.isFetching)}
          onClick={() => {
            onRefresh();
            if (social.open) void neighbors.refetch();
          }}
        >
          {refreshing ? t('刷新中…', 'Refreshing…') : t('刷新串门状态', 'Refresh visits')}
        </button>
        <button type="button" className="nb-btn nb-btn--sm" onClick={onActivity}>
          {t('查看来往与结算记录', 'View visits and settlements')}
        </button>
      </div>
      {social.away && (
        <section className="voyage-card">
          <h3>
            {social.away.working
              ? t('在外帮工', 'Working away')
              : t('正在串门', 'Visiting a neighbor')}
          </h3>
          <p>
            {social.away.label} ·{' '}
            {social.away.allow_hire
              ? t('本次允许帮工', 'Hiring allowed on this visit')
              : t('本次只串门', 'Visiting only')}
          </p>
          {social.away.working && shiftEnd(social.away.work_ends_at_ms)}
          <button
            type="button"
            disabled={busy}
            className="nb-btn"
            onClick={() => void dispatch({ type: 'recall_home' })}
          >
            {t('召回并结算', 'Recall and settle visit')}
          </button>
        </section>
      )}
      {social.guests.length > 0 && (
        <section aria-label={t('来访客鲸', 'Visiting whales')}>
          <h3 className="kit-section-title">{t('来访客鲸', 'Visiting whales')}</h3>
          {social.guests.map((guest) => (
            <article className="voyage-card" key={guest.neighbor_id}>
              <h4 id={'whale-guest-' + guest.neighbor_id} tabIndex={-1}>
                {guest.whale_name} · {guest.label}
              </h4>
              <p>{guestVisitLabel(guest, t)}</p>
              {guest.working && shiftEnd(guest.work_ends_at_ms)}
              <div className="wh-buttons">
                {reactions
                  .filter(([id]) =>
                    guest.working
                      ? id === 'gentle'
                      : id !== 'hire' || (guest.allow_hire && !guest.hire_blocked),
                  )
                  .map(([action, title, en]) => (
                    <button
                      type="button"
                      className="nb-btn"
                      disabled={busy}
                      key={action}
                      onClick={() => {
                        setMessage('');
                        setDialogError('');
                        setGuestAction({
                          id: guest.neighbor_id,
                          action,
                          arrived: guest.started_at_ms,
                          working: guest.working,
                          label: guest.working
                            ? t('结薪送回', 'Pay wages and send home')
                            : t(title, en),
                        });
                      }}
                    >
                      {guest.working ? t('结薪送回', 'Pay wages and send home') : t(title, en)}
                    </button>
                  ))}
              </div>
            </article>
          ))}
        </section>
      )}
      {!social.away && social.guests.length === 0 && latestSettlement && (
        <p className="wh-last-settlement">
          <strong>{t('最近一次结算', 'Latest settlement')}</strong>
          <span>
            {latestSettlement.title}
            {latestSettlement.detail ? ' · ' + latestSettlement.detail : ''}
          </span>
        </p>
      )}
      {((profile.snapshot.wallet.pendingSocialPearls ?? 0) > 0 ||
        (profile.snapshot.wallet.pendingConchShards ?? 0) > 0) && (
        <button type="button" className="nb-btn" onClick={onWallet}>
          {t('查看并收取待结算收益', 'Review and collect pending earnings')}
        </button>
      )}
      <ConfirmDialog
        open={!!guestAction}
        title={guestAction?.label ?? ''}
        confirmDisabled={!activeGuest}
        description={
          guestAction?.action === 'hire'
            ? t(
                `工资为帮工产出的 ${values.guest_wage_bp / 100}%，计入客鲸每日 ${values.daily_social_pearls} 枚社交鲸币额度。双方都可提前结束。`,
                `Guest wages are ${values.guest_wage_bp / 100}% of production and count toward the guest’s daily social limit of ${values.daily_social_pearls} whale coins. Either keeper can end the shift.`,
              )
            : guestAction?.action === 'treat'
              ? t(
                  `款待将使用 ${values.treat_cost_grams} 克口粮，并结算本次串门。`,
                  `Serve ${values.treat_cost_grams} g of feed and settle this visit.`,
                )
              : t('送回客鲸并结算本次串门。', 'Send the guest home and settle this visit.')
        }
        busy={busy}
        confirmLabel={t('确认', 'Confirm')}
        onCancel={() => setGuestAction(null)}
        onConfirm={async () => {
          if (
            guestAction &&
            activeGuest &&
            (await dispatch(
              {
                type: 'catch_guest',
                neighborId: guestAction.id,
                action: guestAction.action,
                ...(message ? { presetId: message } : {}),
              },
              setDialogError,
            ))
          )
            setGuestAction(null);
        }}
      >
        {!busy && (dialogError || !activeGuest) && (
          <p role="alert">
            {dialogError ||
              t(
                '这次来访已结束或状态已变。请返回查看最新的客鲸。',
                'This visit has ended or changed. Go back to see the current guests.',
              )}
          </p>
        )}
        <label>
          {t('给客鲸的口信', 'Message for the guest')}{' '}
          <select value={message} onChange={(e) => setMessage(e.target.value)}>
            <option value="">{t('不留口信', 'No message')}</option>
            {PRESET_MESSAGES.filter((m) => m.category === guestAction?.action).map((m) => (
              <option key={m.id} value={m.id}>
                {m.text}
              </option>
            ))}
          </select>
        </label>
      </ConfirmDialog>
      <section className="voyage-card">
        <h3 className="kit-section-title">{t('我的小筑', 'My haven')}</h3>
        <p>
          {t('门牌', 'Haven code')} <strong>{social.door}</strong> ·{' '}
          {profile.snapshot.gameProfilePublic
            ? t('公开身份', 'Public keeper')
            : t('匿名饲养员', 'Anonymous keeper')}{' '}
          · <Link to="/games#game-privacy">{t('游戏隐私设置', 'Game privacy settings')}</Link>
        </p>
        <label className="wh-choice">
          <input
            type="checkbox"
            checked={social.open}
            disabled={busy}
            onChange={() => {
              setDialogError('');
              if (social.open) setClose(true);
              else void dispatch({ type: 'set_open', open: true });
            }}
          />{' '}
          {t('开放小筑，允许互相串门', 'Open my haven for mutual visits')}
        </label>
        <div>
          <h4>{t('今日社交所得（含工资）', 'Social earnings today (including wages)')}</h4>
          <dl className="wh-metrics">
            <div>
              <dt>{t('小鲸币', 'Whale coins')}</dt>
              <dd>
                {profile.snapshot.wallet.dailySocialPearlsGained} / {values.daily_social_pearls}
              </dd>
            </div>
            <div>
              <dt>{t('海螺碎片', 'Conch shards')}</dt>
              <dd>
                {profile.snapshot.wallet.dailyConchShardsGained} / {values.daily_conch_shards}
              </dd>
            </div>
          </dl>
        </div>
        {values.stove_required_grams > 0 && (
          <p>
            {t(
              `串门互动的鲸币奖励要求近 ${values.stove_window_seconds / 3600} 小时自家消耗至少 ${values.stove_required_grams} 克口粮。帮工工资按产出计算。`,
              `Visit rewards require at least ${values.stove_required_grams} g of feed used at home in the past ${values.stove_window_seconds / 3600} hours. Wages are based on production.`,
            )}
          </p>
        )}
      </section>
      <ConfirmDialog
        open={close}
        title={t('关闭小筑？', 'Close your haven?')}
        description={t(
          '会召回自己的鲸、送回所有客鲸，并结清本次来往。之后可随时重新开放。',
          'Your whale will return, guests will go home, and current visits will be settled. You can reopen at any time.',
        )}
        confirmLabel={t('关闭并结算', 'Close and settle')}
        busy={busy}
        onCancel={() => setClose(false)}
        onConfirm={async () => {
          if (await dispatch({ type: 'set_open', open: false }, setDialogError)) setClose(false);
        }}
      >
        {dialogError && !busy && <p role="alert">{dialogError}</p>}
      </ConfirmDialog>
      {social.open ? (
        <section className="voyage-card">
          <h3 className="kit-section-title">{t('邻居海湾', 'Neighbor havens')}</h3>
          <label className="wh-field">
            {t('查找邻居', 'Find neighbors')}
            <input
              className="kit-input"
              type="search"
              value={search}
              placeholder={t('门牌、鲸名或公开的饲养员', 'Haven code, whale name or public keeper')}
              onChange={(e) => setSearch(e.target.value)}
            />
          </label>
          <label className="wh-field">
            {t('筛选', 'Filter')}{' '}
            <select
              value={filter}
              onChange={(e) => setFilter(e.target.value as NeighborFilterKind)}
            >
              {filters.map(([value, label, en]) => (
                <option value={value} key={value}>
                  {t(label, en)}
                </option>
              ))}
            </select>
          </label>
          {filter !== 'has_food' && (
            <p>
              {t(
                '普通列表的余粮取自小筑上次结算。',
                'Feed amounts in this list reflect the last settlement.',
              )}
            </p>
          )}
          {neighbors.isPending ? (
            <p role="status">{t('正在查找邻居…', 'Finding neighbors…')}</p>
          ) : neighbors.error ? (
            <button className="nb-btn" onClick={() => void neighbors.refetch()}>
              {t('未能读取邻居，重试', 'Could not load neighbors. Retry')}
            </button>
          ) : !neighbors.data?.length ? (
            <p>
              {t(
                '没有找到开放的小筑。可以更换搜索条件，或把门牌分享给朋友。',
                'No open havens found. Try a different search or share your haven code with a friend.',
              )}
            </p>
          ) : (
            <ul className="wh-neighbors">
              {neighbors.data.map((n) => (
                <li key={n.id}>
                  <img
                    src={getSceneAssetUrl(
                      OUTFIT_CATALOG[n.whaleOutfit ?? 'maid_classic'].previewSprite,
                    )}
                    loading="lazy"
                    alt=""
                  />
                  <div>
                    <strong>{displayNeighborName(n, t('匿名饲养员', 'Anonymous keeper'))}</strong>
                    <p>
                      {n.whaleName} · {t('余粮', 'Feed left')} {Math.floor(n.troughGrams)} g
                    </p>
                  </div>
                  <div className="wh-buttons">
                    <button className="nb-btn" onClick={() => onPreview(n)}>
                      {t('看看小筑', 'View haven')}
                    </button>
                    <button
                      className="nb-btn"
                      disabled={busy || !!social.away}
                      onClick={() => onDepart(n)}
                    >
                      {t('去串门', 'Visit')}
                    </button>
                    <button
                      className="nb-btn"
                      disabled={busy}
                      onClick={async () => {
                        if (await dispatch({ type: 'toggle_pin_neighbor', neighborId: n.id }))
                          void neighbors.refetch();
                      }}
                    >
                      {(profile.snapshot.pinnedNeighborIds ?? []).includes(n.id)
                        ? t('取消收藏', 'Unpin')
                        : t('收藏', 'Pin')}
                    </button>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </section>
      ) : (
        <p className="deck-empty">
          {t(
            '开放自己的小筑后，就能查找同样开放的邻居。',
            'Open your haven to find neighbors who also accept visits.',
          )}
        </p>
      )}
    </>
  );
}
export function DepartureDialog({
  neighbor,
  busy,
  dispatch,
  onClose,
  initialMessage = '',
  dailyWageLimitReached = false,
}: {
  neighbor: NeighborStation;
  busy: boolean;
  dispatch: Dispatch;
  onClose: () => void;
  initialMessage?: string;
  dailyWageLimitReached?: boolean;
}) {
  const t = useWhaleText();
  const [hire, setHire] = useState(false);
  const [message, setMessage] = useState(initialMessage);
  const [error, setError] = useState('');
  return (
    <ConfirmDialog
      open
      title={t(
        '去' + displayNeighborName(neighbor) + '家串门？',
        'Visit ' + displayNeighborName(neighbor, 'Anonymous keeper') + '?',
      )}
      description={t(
        '可以随时召回自己的鲸，并领取本次已获得的奖励。',
        'Recall your whale at any time to collect rewards earned on this visit.',
      )}
      confirmLabel={t('出发', 'Depart')}
      busy={busy}
      onCancel={onClose}
      onConfirm={async () => {
        if (
          await dispatch(
            {
              type: 'send_visit',
              target: neighbor.id,
              allowHire: hire,
              ...(message ? { presetId: message } : {}),
            },
            setError,
          )
        )
          onClose();
      }}
    >
      {error && !busy && <p role="alert">{error}</p>}
      <label className="wh-choice">
        <input type="checkbox" checked={hire} onChange={(e) => setHire(e.target.checked)} />{' '}
        {t('本次允许主人雇佣帮工', 'Allow the host to hire my whale on this visit')}
      </label>
      <p>
        {dailyWageLimitReached
          ? t(
              '今日社交鲸币已满，仍可串门。若允许帮工，屋主可在你有剩余额度时雇佣。',
              'Your daily social coin limit is full. You can still visit. If you allow hiring, the host can hire your whale when you have allowance left.',
            )
          : t(
              '帮工工资计入当日社交鲸币额度。',
              'Wages count toward your daily social whale coin limit.',
            )}
      </p>
      <label>
        {t('串门口信', 'Message for the host')}{' '}
        <select value={message} onChange={(e) => setMessage(e.target.value)}>
          <option value="">{t('不留口信', 'No message')}</option>
          {PRESET_MESSAGES.filter((m) => m.category === 'visit').map((m) => (
            <option key={m.id} value={m.id}>
              {m.text}
            </option>
          ))}
        </select>
      </label>
    </ConfirmDialog>
  );
}
