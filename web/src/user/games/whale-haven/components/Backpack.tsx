import { useWhaleText } from '../copy';
import { useState } from 'react';
import { Amount } from '@shared/components/Amount';
import { Sparkles } from './Icons';
import {
  CONCH_GACHA_ART,
  CONCH_GACHA_POOL,
  CONCH_SHARD_ART,
  DECOR_CATALOG,
  FEED_CATALOG,
  getSceneAssetUrl,
} from '../game/catalog';
import type { DecorItemId, FeedItemId, GameSnapshot } from '../game/types';
import { UiSwitch } from './UiMotifs';

const FEEDS: readonly FeedItemId[] = ['silver_fish', 'sweet_shrimp', 'berry_teacake'];
const FEED_SLOTS: Record<FeedItemId, { art: string; label: [string, string] }> = {
  silver_fish: { art: 'props/rice-bowl-full.webp', label: ['白米饭', 'Rice'] },
  sweet_shrimp: { art: 'props/rice-bowl-token.webp', label: ['Token 盖饭', 'Token rice'] },
  berry_teacake: { art: 'props/rice-onigiri-snack.webp', label: ['小饭团', 'Rice ball'] },
};

interface BackpackProps {
  values: Record<string, number>;
  snapshot: GameSnapshot;
  onBuyFeed: (id: FeedItemId, quantity: number) => void;
  onUseFeed: (id: FeedItemId) => void;
  onToggleDecor: (id: DecorItemId) => void;
  onToggleAutoRefill: () => void;
  onActivateAroma: () => void;
  onSpinConchGacha: () => void;
}

type ItemSlot =
  | { kind: 'feed'; id: FeedItemId; name: string; count: number }
  | { kind: 'decor'; id: DecorItemId; name: string; count: 1 };

function BackpackArt({ item }: { item: ItemSlot }) {
  const src = getSceneAssetUrl(
    item.kind === 'feed' ? FEED_SLOTS[item.id].art : DECOR_CATALOG[item.id].artwork,
  );
  return <img src={src} alt="" />;
}

export function Backpack({
  snapshot,
  values,
  onBuyFeed,
  onUseFeed,
  onToggleDecor,
  onToggleAutoRefill,
  onActivateAroma,
  onSpinConchGacha,
}: BackpackProps) {
  const t = useWhaleText();
  const [quantity, setQuantity] = useState('1');
  const validQuantity = /^[1-9][0-9]?$/.test(quantity);
  const [selectedId, setSelectedId] = useState<FeedItemId | DecorItemId>('silver_fish');
  const items: ItemSlot[] = FEEDS.map((id) => ({
    kind: 'feed',
    id,
    name: FEED_CATALOG[id].name,
    count: snapshot.inventory[id] ?? 0,
  }));
  for (const id of snapshot.home.ownedDecor) {
    items.push({ kind: 'decor', id, name: DECOR_CATALOG[id].name, count: 1 });
  }
  const selected = items.find((item) => item.id === selectedId) ?? items[0];
  const unit = t('游戏积分', 'game credits');
  const unitPrice =
    selected.kind === 'feed' ? String(values['feed_' + selected.id + '_price_milli']) : '';
  const equipped = selected.kind === 'decor' && snapshot.home.equippedDecor.includes(selected.id);
  const totalFood = FEEDS.reduce((total, id) => total + (snapshot.inventory[id] ?? 0), 0);
  const buffs = snapshot.home.buffs ?? {
    autoRefillEnabled: false,
    autoRefillTickets: 0,
    aromaActiveGrams: 0,
    aromaCharges: 0,
    wardrobeDiscountCoupons: 0,
  };

  const shards = snapshot.wallet.conchShards ?? 0;
  const pullCost = values.conch_cost_shards;

  return (
    <>
      <section className="backpack-card" aria-labelledby="backpack-items">
        <header className="backpack-head">
          <h3 className="kit-section-title" id="backpack-items">
            {t('物品栏', 'Inventory')}
          </h3>
          <span>{t(`口粮 ${totalFood} 份`, `${totalFood} feed servings`)}</span>
        </header>
        <div className="backpack-grid">
          {items.map((item) => (
            <button
              key={item.id}
              type="button"
              className="backpack-slot"
              aria-pressed={item.id === selected.id}
              title={item.name}
              onClick={() => setSelectedId(item.id)}
            >
              <BackpackArt item={item} />
              <span>{item.kind === 'feed' ? t(...FEED_SLOTS[item.id].label) : item.name}</span>
              <span className="backpack-slot__count">×{item.count}</span>
            </button>
          ))}
        </div>
      </section>

      <section className="backpack-selection" aria-label={t('当前道具', 'Selected item')}>
        <div className="backpack-selection__summary">
          <span className="backpack-selection__art">
            <BackpackArt item={selected} />
          </span>
          <div>
            <h3>{selected.name}</h3>
            <p>
              {selected.kind === 'feed'
                ? FEED_CATALOG[selected.id].subtitle
                : DECOR_CATALOG[selected.id].description}
            </p>
          </div>
        </div>
        <div className="backpack-selection__stock">
          <span>
            {selected.kind === 'feed'
              ? t(`库存 ${selected.count} 份`, `In stock: ${selected.count}`)
              : equipped
                ? t('已布置', 'Placed')
                : t('已拥有', 'Owned')}
          </span>
          {selected.kind === 'feed' ? (
            <button
              type="button"
              className="nb-btn"
              disabled={selected.count <= 0}
              onClick={() => onUseFeed(selected.id)}
            >
              {selected.id === 'berry_teacake'
                ? t('亲手喂饭团', 'Hand-feed a treat')
                : t('盛入饭盆', 'Fill the bowl')}
            </button>
          ) : (
            <button type="button" className="nb-btn" onClick={() => onToggleDecor(selected.id)}>
              {equipped ? t('收起装扮', 'Put away') : t('布置小屋', 'Place in haven')}
            </button>
          )}
        </div>
        {selected.kind === 'feed' && (
          <form
            className="backpack-purchase"
            onSubmit={(event) => {
              event.preventDefault();
              if (validQuantity) onBuyFeed(selected.id, Number(quantity));
            }}
          >
            <div className="backpack-purchase__quantity">
              <label htmlFor="whale-feed-quantity">
                {t('购买份数', 'Servings to buy')} <small>1–99</small>
              </label>
              <input
                id="whale-feed-quantity"
                className="kit-input"
                type="number"
                inputMode="numeric"
                min={1}
                max={99}
                step={1}
                required
                value={quantity}
                aria-invalid={!validQuantity}
                aria-describedby={!validQuantity ? 'whale-feed-range' : undefined}
                onChange={(event) => setQuantity(event.target.value)}
              />
            </div>
            {!validQuantity && (
              <p id="whale-feed-range">
                {t('请输入 1–99 的整数。', 'Enter a whole number from 1 to 99.')}
              </p>
            )}
            <div className="backpack-purchase__checkout">
              <div className="backpack-purchase__price">
                <span>
                  {t('每份', 'Per serving')} <Amount value={unitPrice} unit={unit} />
                </span>
                {validQuantity && Number(quantity) > 1 && (
                  <strong>
                    {t('合计', 'Total')}{' '}
                    <Amount value={BigInt(unitPrice) * BigInt(quantity)} unit={unit} />
                  </strong>
                )}
              </div>
              <button className="nb-btn nb-btn--primary" disabled={!validQuantity} type="submit">
                {t('购买口粮', 'Buy feed')}
              </button>
            </div>
          </form>
        )}
      </section>

      <section className="voyage-card conch-gacha-card" aria-labelledby="conch-gacha-title">
        <div className="deck-section-head" style={{ marginBottom: 0 }}>
          <h3 className="kit-section-title" id="conch-gacha-title">
            <img
              src={getSceneAssetUrl(CONCH_SHARD_ART)}
              alt=""
              className="inline-coin-icon"
              style={{ marginRight: 5 }}
            />
            {t('海螺扭蛋与道具', 'Conch draws and boosts')}
          </h3>
          <span className="deck-balance">
            <img src={getSceneAssetUrl(CONCH_SHARD_ART)} alt="" /> {shards}{' '}
            {t('片碎片', 'conch shards')}
          </span>
        </div>

        <div className="conch-gacha-hero">
          <div className="conch-gacha-hero__art">
            <img
              src={getSceneAssetUrl(CONCH_GACHA_ART)}
              alt={t('深海海螺扭蛋机', 'Conch draw machine')}
            />
          </div>
          <div className="conch-gacha-hero__copy">
            <p>
              {t(
                `外出蹭饭与招待客鲸达每日社交鲸币上限后掉落虹彩海螺碎片，${pullCost} 片可转 1 次道具。`,
                `Once the daily social coin limit is reached, visits and hosting can earn conch shards. Spend ${pullCost} shards for one item draw.`,
              )}
            </p>
            <div className="conch-gacha-prizes">
              {CONCH_GACHA_POOL.map((g) => (
                <span key={g.id} className="conch-prize-pill" title={g.description}>
                  {g.artwork ? <img src={getSceneAssetUrl(g.artwork)} alt="" /> : g.emoji}
                  {g.name.split('（')[0].split(' ×')[0]}
                </span>
              ))}
            </div>
          </div>
        </div>

        <div className="conch-buff-list">
          <div className="conch-buff-item">
            <span className="conch-buff-item__icon">
              <img src={getSceneAssetUrl('props/buff-auto-refill.webp')} alt="" />
            </span>
            <div className="conch-buff-item__body">
              <UiSwitch
                checked={buffs.autoRefillEnabled && buffs.autoRefillTickets > 0}
                onChange={onToggleAutoRefill}
              >
                {t('空碗自动续饭', 'Refill an empty bowl')}
                <small className="wh-detail">
                  {t(
                    `保温券 ×${buffs.autoRefillTickets}`,
                    `Refill tickets ×${buffs.autoRefillTickets}`,
                  )}
                </small>
              </UiSwitch>
              {(values.feed_silver_fish_grams > values.trough_grams ||
                values.feed_sweet_shrimp_grams > values.trough_grams) && (
                <small className="wh-detail">
                  {t(
                    '自动续饭只用整份装得下的主粮。大份口粮可手动盛入。',
                    'Refill tickets only use servings that fit. You can add a larger serving by hand.',
                  )}
                </small>
              )}
            </div>
          </div>

          <div className="conch-buff-item">
            <span className="conch-buff-item__icon">
              <img src={getSceneAssetUrl('props/buff-aroma-spice.webp')} alt="" />
            </span>
            <div className="conch-buff-item__row">
              <span>
                {t('新米增香', 'Rice seasoning')}
                <small className="wh-detail">
                  {buffs.aromaActiveGrams > 0 &&
                    t(
                      `生效中 · ${Math.round(buffs.aromaActiveGrams)} g · `,
                      `Active · ${Math.round(buffs.aromaActiveGrams)} g · `,
                    )}
                  {t(`库存 ×${buffs.aromaCharges}`, `In stock ×${buffs.aromaCharges}`)}
                </small>
              </span>
              <button
                type="button"
                className="nb-btn nb-btn--sm"
                disabled={buffs.aromaCharges <= 0}
                onClick={onActivateAroma}
              >
                {t('使用增香包', 'Add seasoning')}
              </button>
            </div>
          </div>

          {buffs.wardrobeDiscountCoupons > 0 && (
            <div className="conch-buff-item conch-buff-item--coupon">
              <span className="conch-buff-item__icon">
                <img src={getSceneAssetUrl('props/buff-tailor-coupon.webp')} alt="" />
              </span>
              <span>
                已持有 <strong>裁缝海螺券 ×{buffs.wardrobeDiscountCoupons}</strong>
                ，下次解锁新服装或家具自动省 {values.coupon_discount_bp / 100}% 小鲸币！
              </span>
            </div>
          )}
        </div>

        <button
          type="button"
          className="nb-btn nb-btn--sm nb-btn--primary"
          disabled={shards < pullCost}
          onClick={onSpinConchGacha}
        >
          <Sparkles size={16} /> {t('转动扭蛋', 'Draw an item')}
        </button>
      </section>
    </>
  );
}
