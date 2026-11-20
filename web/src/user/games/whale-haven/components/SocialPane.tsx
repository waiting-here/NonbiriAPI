import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import {
  PRESET_MESSAGES,
  displayNeighborName,
  OUTFIT_CATALOG,
  getSceneAssetUrl,
} from '../game/catalog';
import type { NeighborFilterKind, NeighborStation, CatchActionKind } from '../game/types';
import { searchNeighbors, type Profile, type Action } from '../api';

export type Dispatch = (action: Action) => Promise<boolean>;
interface Props {
  profile: Profile;
  dispatch: Dispatch;
  busy: boolean;
  onPreview: (neighbor: NeighborStation) => void;
  onDepart: (neighbor: NeighborStation) => void;
}
const filters: readonly [NeighborFilterKind, string][] = [
  ['all', '全部'],
  ['has_food', '有饭'],
  ['pinned', '已收藏'],
  ['visited', '来往过'],
];
const reactions: readonly [CatchActionKind, string][] = [
  ['hire', '雇佣帮工'],
  ['treat', '招待'],
  ['gentle', '温和送回'],
  ['flick', '弹脑瓜送回'],
];
export function SocialPane({ profile, dispatch, busy, onPreview, onDepart }: Props) {
  const social = profile.social;
  const values = profile.settings.values;
  const [search, setSearch] = useState('');
  const [filter, setFilter] = useState<NeighborFilterKind>('all');
  const [close, setClose] = useState(false);
  const [guestAction, setGuestAction] = useState<{
    id: string;
    action: CatchActionKind;
    label: string;
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
  const activeGuest = social.guests.find((g) => g.neighbor_id === guestAction?.id);
  return (
    <>
      <section className="voyage-card">
        <h3 className="kit-section-title">我的小筑</h3>
        <p>
          门牌 <strong>{social.door}</strong> ·{' '}
          {profile.snapshot.gameProfilePublic ? '公开身份' : '匿名饲养员'} ·{' '}
          <Link to="/games#game-privacy">游戏隐私设置</Link>
        </p>
        <label className="wh-choice">
          <input
            type="checkbox"
            checked={social.open}
            disabled={busy}
            onChange={() =>
              social.open ? setClose(true) : void dispatch({ type: 'set_open', open: true })
            }
          />{' '}
          开放小筑，允许互相串门
        </label>
        <p>
          今日社交所得 {profile.snapshot.wallet.dailySocialPearlsGained} /{' '}
          {values.daily_social_pearls} 小鲸币（含工资），海螺碎片{' '}
          {profile.snapshot.wallet.dailyConchShardsGained} / {values.daily_conch_shards}。
        </p>
        <p>
          近 {values.stove_window_seconds / 3600} 小时自家消耗至少 {values.stove_required_grams}{' '}
          克口粮可获得社交鲸币。
        </p>
      </section>
      <ConfirmDialog
        open={close}
        title="关闭小筑？"
        description="会召回自己的鲸、送回所有客鲸，并结清本次来往。之后可随时重新开放。"
        confirmLabel="关闭并结算"
        busy={busy}
        onCancel={() => setClose(false)}
        onConfirm={async () => {
          if (await dispatch({ type: 'set_open', open: false })) setClose(false);
        }}
      />
      {social.away && (
        <section className="voyage-card">
          <h3>{social.away.working ? '在外帮工' : '正在串门'}</h3>
          <p>
            {social.away.label} · {social.away.allow_hire ? '本次允许帮工' : '本次只串门'}
          </p>
          <button
            type="button"
            disabled={busy}
            className="nb-btn"
            onClick={() => void dispatch({ type: 'recall_home' })}
          >
            召回并结算
          </button>
        </section>
      )}
      {social.guests.length > 0 && (
        <section aria-label="来访客鲸">
          <h3 className="kit-section-title">来访客鲸</h3>
          {social.guests.map((guest) => (
            <article className="voyage-card" key={guest.neighbor_id}>
              <h4>
                {guest.whale_name} · {guest.label}
              </h4>
              <p>{guest.working ? '正在帮工' : guest.allow_hire ? '愿意帮工' : '只来串门'}</p>
              <div className="wh-buttons">
                {reactions
                  .filter(([id]) =>
                    guest.working ? id === 'gentle' : id !== 'hire' || guest.allow_hire,
                  )
                  .map(([action, title]) => (
                    <button
                      type="button"
                      className="nb-btn"
                      disabled={busy}
                      key={action}
                      onClick={() => {
                        setMessage('');
                        setGuestAction({ id: guest.neighbor_id, action, label: title });
                      }}
                    >
                      {guest.working ? '结薪送回' : title}
                    </button>
                  ))}
              </div>
            </article>
          ))}
        </section>
      )}
      <ConfirmDialog
        open={!!activeGuest && !!guestAction}
        title={guestAction?.label ?? ''}
        description={
          guestAction?.action === 'hire'
            ? '工资计入客鲸当日社交鲸币额度，双方都可提前结束。'
            : guestAction?.action === 'treat'
              ? '款待将使用 ' + values.treat_cost_grams + ' 克口粮，并结算本次串门。'
              : '送回客鲸并结算本次串门。'
        }
        busy={busy}
        confirmLabel="确认"
        onCancel={() => setGuestAction(null)}
        onConfirm={async () => {
          if (
            guestAction &&
            (await dispatch({
              type: 'catch_guest',
              neighborId: guestAction.id,
              action: guestAction.action,
              ...(message ? { presetId: message } : {}),
            }))
          )
            setGuestAction(null);
        }}
      >
        <label>
          给客鲸的口信{' '}
          <select value={message} onChange={(e) => setMessage(e.target.value)}>
            <option value="">不留口信</option>
            {PRESET_MESSAGES.filter((m) => m.category === guestAction?.action).map((m) => (
              <option key={m.id} value={m.id}>
                {m.text}
              </option>
            ))}
          </select>
        </label>
      </ConfirmDialog>
      {social.open ? (
        <section className="voyage-card">
          <h3 className="kit-section-title">邻居海湾</h3>
          <label>
            查找邻居
            <input
              className="kit-input"
              type="search"
              value={search}
              placeholder="门牌、鲸名或公开的饲养员"
              onChange={(e) => setSearch(e.target.value)}
            />
          </label>
          <label>
            筛选{' '}
            <select
              value={filter}
              onChange={(e) => setFilter(e.target.value as NeighborFilterKind)}
            >
              {filters.map(([value, label]) => (
                <option value={value} key={value}>
                  {label}
                </option>
              ))}
            </select>
          </label>
          {neighbors.isPending ? (
            <p role="status">正在查找邻居…</p>
          ) : neighbors.error ? (
            <button className="nb-btn" onClick={() => void neighbors.refetch()}>
              未能读取邻居，重试
            </button>
          ) : !neighbors.data?.length ? (
            <p>没有找到开放的小筑。可以更换搜索条件，或把门牌分享给朋友。</p>
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
                    <strong>{displayNeighborName(n)}</strong>
                    <p>
                      {n.whaleName} · 口粮 {Math.floor(n.troughGrams)} 克
                    </p>
                    <div className="wh-buttons">
                      <button className="nb-btn" onClick={() => onPreview(n)}>
                        看看小筑
                      </button>
                      <button
                        className="nb-btn"
                        disabled={busy || !!social.away}
                        onClick={() => onDepart(n)}
                      >
                        去串门
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
                          ? '取消收藏'
                          : '收藏'}
                      </button>
                    </div>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </section>
      ) : (
        <p className="deck-empty">开放自己的小筑后，就能查找同样开放的邻居。</p>
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
}: {
  neighbor: NeighborStation;
  busy: boolean;
  dispatch: Dispatch;
  onClose: () => void;
  initialMessage?: string;
}) {
  const [hire, setHire] = useState(false);
  const [message, setMessage] = useState(initialMessage);
  return (
    <ConfirmDialog
      open
      title={'去' + displayNeighborName(neighbor) + '家串门？'}
      description="可以随时召回自己的鲸，并领取本次已获得的奖励。"
      confirmLabel="出发"
      busy={busy}
      onCancel={onClose}
      onConfirm={async () => {
        if (
          await dispatch({
            type: 'send_visit',
            target: neighbor.id,
            allowHire: hire,
            ...(message ? { presetId: message } : {}),
          })
        )
          onClose();
      }}
    >
      <label className="wh-choice">
        <input type="checkbox" checked={hire} onChange={(e) => setHire(e.target.checked)} />{' '}
        本次允许主人雇佣帮工
      </label>
      <p>帮工工资计入当日社交鲸币额度。</p>
      <label>
        串门口信{' '}
        <select value={message} onChange={(e) => setMessage(e.target.value)}>
          <option value="">不留口信</option>
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
