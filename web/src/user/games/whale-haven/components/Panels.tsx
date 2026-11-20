import type { GameSnapshot, WhaleOutfitId, DecorItemId } from '../game/types';
import {
  WHALE_LORE_PROFILE,
  OUTFIT_CATALOG,
  DECOR_CATALOG,
  getSceneAssetUrl,
} from '../game/catalog';
import { MessageCircle, Waves } from './Icons';
import { UiSwitch } from './UiMotifs';
import { costWithCoupon, type Action, type Profile } from '../api';
const OUTFITS = (Object.keys(OUTFIT_CATALOG) as WhaleOutfitId[]).filter(
  (id) => !OUTFIT_CATALOG[id].hidden,
);
const COIN_ART = 'props/whale-coin.webp';

function formatTimeAgo(timestamp: number) {
  const seconds = Math.max(0, Math.floor((Date.now() - timestamp) / 1000));
  if (seconds < 60) return '刚刚';
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分钟前`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)} 小时前`;
  return `${Math.floor(seconds / 86400)} 天前`;
}

function MessageNote({ message }: { message: string }) {
  return (
    <blockquote className="deck-message">
      <MessageCircle size={15} aria-label="口信" />
      <span>「{message}」</span>
    </blockquote>
  );
}

export function WardrobePane({
  profile,
  dispatch,
}: {
  profile: Profile;
  dispatch: (a: Action) => void;
}) {
  const snapshot = profile.snapshot;
  const currentOutfit = snapshot.whale.outfit;
  return (
    <>
      <section aria-labelledby="wardrobe-outfits">
        <div className="deck-section-head">
          <h3 className="kit-section-title" id="wardrobe-outfits">
            衣橱
          </h3>
          <div
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: '0.35rem',
              flexWrap: 'wrap',
            }}
          >
            {Boolean(snapshot.home.buffs?.wardrobeDiscountCoupons) && (
              <span className="deck-balance" title="下次解锁服装或装扮时自动抵扣">
                <img src={getSceneAssetUrl('props/buff-tailor-coupon.webp')} alt="" /> 抵用券可用
              </span>
            )}
            <span className="deck-balance">
              <img src={getSceneAssetUrl(COIN_ART)} alt="" /> {snapshot.wallet.pearls} 小鲸币
            </span>
          </div>
        </div>
        <div className="outfit-gallery">
          {OUTFITS.map((id) => {
            const item = OUTFIT_CATALOG[id];
            const isActive = id === currentOutfit;
            const owned = id === 'maid_classic' || snapshot.home.ownedOutfits?.includes(id);
            const finalCost = costWithCoupon(
              profile,
              profile.settings.values['outfit_' + id + '_price_pearls'] ?? 0,
            );
            return (
              <article className={`outfit-tile ${isActive ? 'is-equipped' : ''}`} key={id}>
                <div className="outfit-tile__art">
                  <img src={getSceneAssetUrl(item.previewSprite)} alt="" />
                </div>
                <div className="outfit-tile__content">
                  <h4>{item.name}</h4>
                  <p title={item.description}>{item.badge}</p>
                  <button
                    type="button"
                    className={`nb-btn nb-btn--sm ${isActive ? '' : 'nb-btn--primary'}`}
                    disabled={isActive}
                    onClick={() => dispatch({ type: 'select_outfit', outfitId: id })}
                  >
                    {isActive ? '已穿戴' : owned ? '换上这套' : `${finalCost} 小鲸币解锁`}
                  </button>
                </div>
              </article>
            );
          })}
        </div>
      </section>
      <section className="deck-preferences" aria-labelledby="wardrobe-behavior">
        <h3 className="kit-section-title deck-section-title" id="wardrobe-behavior">
          待机偏好
        </h3>
        <UiSwitch
          checked={snapshot.whale.petLookEnabled ?? true}
          onChange={() =>
            dispatch({
              type: 'set_preference',
              key: 'petLookEnabled',
              enabled: !(snapshot.whale.petLookEnabled ?? true),
            })
          }
        >
          鼠标视线跟随
        </UiSwitch>
        <UiSwitch
          checked={snapshot.whale.petRoamEnabled ?? true}
          onChange={() =>
            dispatch({
              type: 'set_preference',
              key: 'petRoamEnabled',
              enabled: !(snapshot.whale.petRoamEnabled ?? true),
            })
          }
        >
          海底自由漫步
        </UiSwitch>
      </section>
    </>
  );
}
export function DecorPane({
  profile,
  dispatch,
}: {
  profile: Profile;
  dispatch: (a: Action) => void;
}) {
  const snapshot = profile.snapshot;
  return (
    <section aria-labelledby="exchange-decor">
      <h3 className="kit-section-title deck-section-title" id="exchange-decor">
        小屋装扮（提升转化率与存钱罐上限）
      </h3>
      <ul className="decor-shelf">
        {(Object.keys(DECOR_CATALOG) as DecorItemId[]).map((id) => {
          const item = DECOR_CATALOG[id];
          const owned = snapshot.home.ownedDecor.includes(id);
          const equipped = snapshot.home.equippedDecor.includes(id);
          const finalCost = costWithCoupon(
            profile,
            profile.settings.values['decor_' + id + '_price_pearls'],
          );
          return (
            <li key={id}>
              <span className="decor-shelf__image">
                <img src={getSceneAssetUrl(item.artwork)} alt="" />
              </span>
              <div>
                <h4>{item.name}</h4>
                <p>
                  {id === 'golden_clam'
                    ? '存钱罐容量 ' + profile.settings.values.bank_upgraded_pearls + ' 枚'
                    : profile.settings.values['decor_' + id + '_bonus_bp']
                      ? '转化率 +' +
                        profile.settings.values['decor_' + id + '_bonus_bp'] / 100 +
                        '%'
                      : '小屋雅致收藏'}
                </p>
              </div>
              <button
                type="button"
                className={`nb-btn nb-btn--sm ${equipped ? '' : 'nb-btn--primary'}`}
                onClick={() => dispatch({ type: 'toggle_decor', decorId: id })}
              >
                {owned ? (equipped ? '收起装扮' : '布置上场') : `解锁 (${finalCost} 鲸币)`}
              </button>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
export function ActivityPane({ snapshot }: { snapshot: GameSnapshot }) {
  return (
    <>
      <section aria-labelledby="activity-timeline">
        <h3 className="kit-section-title deck-section-title" id="activity-timeline">
          最近动态
        </h3>
        {snapshot.logs.length === 0 ? (
          <p className="deck-empty">小屋还没有新动态。</p>
        ) : (
          <ol className="deck-timeline deck-timeline--activity">
            {snapshot.logs.map((entry) => (
              <li key={entry.id} className="deck-timeline__entry" data-kind={entry.kind}>
                <div className="deck-timeline__heading">
                  <h4>{entry.title}</h4>
                  <span>{formatTimeAgo(entry.timestamp)}</span>
                </div>
                <p>{entry.detail}</p>
                {entry.highlight && (
                  <span className="nb-badge nb-badge--warm">{entry.highlight}</span>
                )}
                {entry.presetMessage && <MessageNote message={entry.presetMessage} />}
              </li>
            ))}
          </ol>
        )}
      </section>
      <details className="deck-details deck-details--lore">
        <summary>
          <Waves size={16} /> 鲸鱼娘设定档案
        </summary>
        <p>{WHALE_LORE_PROFILE.worldview}</p>
        <dl>
          {WHALE_LORE_PROFILE.traits.map((trait) => (
            <div key={trait.tag}>
              <dt>{trait.tag}</dt>
              <dd>{trait.desc}</dd>
            </div>
          ))}
        </dl>
        <small>{WHALE_LORE_PROFILE.credits}</small>
      </details>
    </>
  );
}
