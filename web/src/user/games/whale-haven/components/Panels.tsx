import type { GameSnapshot, WhaleOutfitId, DecorItemId } from '../game/types';
import {
  WHALE_LORE_PROFILE,
  OUTFIT_CATALOG,
  DECOR_CATALOG,
  getSceneAssetUrl,
} from '../game/catalog';
import { MessageCircle, Waves } from './Icons';
import { UiSwitch } from './UiMotifs';
import type { Action, Profile } from '../api';
import { unlockPriceText, useWhaleText } from '../copy';
import { formatDateTime } from '@shared/utils/datetime';
const OUTFITS = (Object.keys(OUTFIT_CATALOG) as WhaleOutfitId[]).filter(
  (id) => !OUTFIT_CATALOG[id].hidden,
);
const COIN_ART = 'props/whale-coin.webp';

function MessageNote({ message }: { message: string }) {
  return (
    <blockquote className="deck-message">
      <MessageCircle size={15} aria-hidden="true" />
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
  const t = useWhaleText();
  const snapshot = profile.snapshot;
  const currentOutfit = snapshot.whale.outfit;
  return (
    <>
      <section aria-labelledby="wardrobe-outfits">
        <div className="deck-section-head">
          <h3 className="kit-section-title" id="wardrobe-outfits">
            {t('衣橱', 'Wardrobe')}
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
              <span
                className="deck-balance"
                title={t(
                  '解锁服装或家具时，实付低于原价才用券',
                  'Used when it lowers the price of a new outfit or furnishing',
                )}
              >
                <img src={getSceneAssetUrl('props/buff-tailor-coupon.webp')} alt="" />{' '}
                {t('抵用券可用', 'Coupon available')}
              </span>
            )}
            <span className="deck-balance">
              <img src={getSceneAssetUrl(COIN_ART)} alt="" /> {snapshot.wallet.pearls}{' '}
              {t('小鲸币', 'whale coins')}
            </span>
          </div>
        </div>
        <div className="outfit-gallery">
          {OUTFITS.map((id) => {
            const item = OUTFIT_CATALOG[id];
            const isActive = id === currentOutfit;
            const owned = id === 'maid_classic' || snapshot.home.ownedOutfits?.includes(id);
            return (
              <article className={`outfit-tile ${isActive ? 'is-equipped' : ''}`} key={id}>
                <div className="outfit-tile__art">
                  <img src={getSceneAssetUrl(item.previewSprite)} alt="" />
                </div>
                <div className="outfit-tile__content">
                  <h4>{item.name}</h4>
                  <p title={item.description}>{item.badge}</p>
                  {!owned && (
                    <span className="wh-item-price">
                      {unlockPriceText(
                        profile,
                        profile.settings.values['outfit_' + id + '_price_pearls'],
                        t,
                      )}
                    </span>
                  )}
                  <button
                    type="button"
                    className={`nb-btn nb-btn--sm ${isActive ? '' : 'nb-btn--primary'}`}
                    disabled={isActive}
                    onClick={() => dispatch({ type: 'select_outfit', outfitId: id })}
                  >
                    {isActive
                      ? t('已穿戴', 'Equipped')
                      : owned
                        ? t('换上这套', 'Wear outfit')
                        : t('解锁衣装', 'Unlock outfit')}
                  </button>
                </div>
              </article>
            );
          })}
        </div>
      </section>
      <section className="deck-preferences" aria-labelledby="wardrobe-behavior">
        <h3 className="kit-section-title deck-section-title" id="wardrobe-behavior">
          {t('待机偏好', 'Idle behavior')}
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
          {t('视线跟随鼠标', 'Follow the pointer')}
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
          {t('自由漫步', 'Roam around')}
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
  const t = useWhaleText();
  const snapshot = profile.snapshot;
  return (
    <section aria-labelledby="exchange-decor">
      <h3 className="kit-section-title deck-section-title" id="exchange-decor">
        {t('小屋家具', 'Furnishings')}
      </h3>
      <ul className="decor-shelf">
        {(Object.keys(DECOR_CATALOG) as DecorItemId[]).map((id) => {
          const item = DECOR_CATALOG[id];
          const owned = snapshot.home.ownedDecor.includes(id);
          const equipped = snapshot.home.equippedDecor.includes(id);
          return (
            <li key={id}>
              <span className="decor-shelf__image">
                <img src={getSceneAssetUrl(item.artwork)} alt="" />
              </span>
              <div>
                <h4>{item.name}</h4>
                <p>
                  {id === 'golden_clam'
                    ? t(
                        `存钱罐容量 ${profile.settings.values.bank_upgraded_pearls} 枚`,
                        `Coin bank capacity: ${profile.settings.values.bank_upgraded_pearls}`,
                      )
                    : profile.settings.values['decor_' + id + '_bonus_bp']
                      ? t('转化率', 'Conversion rate') +
                        ' +' +
                        profile.settings.values['decor_' + id + '_bonus_bp'] / 100 +
                        '%'
                      : t('小屋收藏', 'Collectible')}
                </p>
                {!owned && (
                  <span className="wh-item-price">
                    {unlockPriceText(
                      profile,
                      profile.settings.values['decor_' + id + '_price_pearls'],
                      t,
                    )}
                  </span>
                )}
              </div>
              <button
                type="button"
                className={`nb-btn nb-btn--sm ${equipped ? '' : 'nb-btn--primary'}`}
                onClick={() => dispatch({ type: 'toggle_decor', decorId: id })}
              >
                {owned
                  ? equipped
                    ? t('收起', 'Put away')
                    : t('布置', 'Place')
                  : t('解锁家具', 'Unlock')}
              </button>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
export function ActivityPane({ snapshot }: { snapshot: GameSnapshot }) {
  const t = useWhaleText();
  return (
    <>
      <section aria-labelledby="activity-timeline">
        <h3 className="kit-section-title deck-section-title" id="activity-timeline">
          {t('最近动态', 'Recent activity')}
        </h3>
        {snapshot.logs.length === 0 ? (
          <p className="deck-empty">{t('小屋还没有新动态。', 'There is no haven activity yet.')}</p>
        ) : (
          <ol className="deck-timeline deck-timeline--activity">
            {snapshot.logs.map((entry) => (
              <li key={entry.id} className="deck-timeline__entry" data-kind={entry.kind}>
                <div className="deck-timeline__heading">
                  <h4>{entry.title}</h4>
                  <time dateTime={new Date(entry.timestamp).toISOString()}>
                    {formatDateTime(entry.timestamp, undefined, undefined, 'second')}
                  </time>
                </div>
                {entry.detail && <p>{entry.detail}</p>}
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
