import { useWhaleText } from '../copy';
import { useState } from 'react';
import { Plus, ShoppingBag, Sparkles } from './Icons';
import {
  CONCH_GACHA_ART,
  CONCH_GACHA_POOL,
  CONCH_SHARD_ART,
  DECOR_CATALOG,
  FEED_CATALOG,
  getSceneAssetUrl,
} from '../game/catalog';
import { formatMilliCredits } from '../api';
import type { DecorItemId, FeedItemId, GameSnapshot } from '../game/types';
import { UiSwitch } from './UiMotifs';

const FEEDS: readonly FeedItemId[] = ['silver_fish', 'sweet_shrimp', 'berry_teacake'];
const FEED_ART: Record<FeedItemId, string> = {
  silver_fish: 'props/rice-bowl-full.webp',
  sweet_shrimp: 'props/rice-bowl-token.webp',
  berry_teacake: 'props/rice-onigiri-snack.webp',
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
    item.kind === 'feed' ? FEED_ART[item.id] : DECOR_CATALOG[item.id].artwork,
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
            {t('背包（仅游戏积分购粮）', 'Bag and feed')}
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
              aria-label={t(
                `${item.name}，数量 ${item.count}`,
                `${item.name}, quantity ${item.count}`,
              )}
              title={item.name}
              onClick={() => setSelectedId(item.id)}
            >
              <BackpackArt item={item} />
              <span>×{item.count}</span>
            </button>
          ))}
          {Array.from({ length: Math.max(0, 8 - items.length) }, (_, index) => (
            <div
              className="backpack-slot backpack-slot--empty"
              key={`empty-${index}`}
              aria-hidden="true"
            >
              {index === 0 && <Plus size={21} />}
            </div>
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
            <span className="nb-badge nb-badge--info">
              {selected.kind === 'feed'
                ? t(`库存 ×${selected.count}`, `In stock ×${selected.count}`)
                : equipped
                  ? t('已布置', 'Placed')
                  : t('已拥有', 'Owned')}
            </span>
            <p>
              {selected.kind === 'feed'
                ? FEED_CATALOG[selected.id].subtitle
                : DECOR_CATALOG[selected.id].description}
            </p>
          </div>
        </div>
        {selected.kind === 'feed' ? (
          <div className="backpack-selection__actions">
            <label>
              {t('购买份数（1–99）', 'Servings to buy (1–99)')}{' '}
              <input
                type="number"
                min={1}
                max={99}
                step={1}
                className="kit-input"
                value={quantity}
                onChange={(e) => setQuantity(e.target.value)}
              />
            </label>
            <button
              type="button"
              className="nb-btn nb-btn--sm"
              disabled={!validQuantity}
              onClick={() => onBuyFeed(selected.id, Number(quantity))}
            >
              <ShoppingBag size={15} /> {t('购买', 'Buy')} ·{' '}
              {formatMilliCredits(values['feed_' + selected.id + '_price_milli'])}{' '}
              {t('游戏积分 / 份', 'game credits per serving')}
            </button>
            <button
              type="button"
              className="nb-btn nb-btn--sm nb-btn--primary"
              disabled={selected.count <= 0}
              onClick={() => onUseFeed(selected.id)}
            >
              {selected.id === 'berry_teacake'
                ? t('亲手喂饭团', 'Hand-feed a treat')
                : t('盛入饭盆', 'Fill the bowl')}
            </button>
          </div>
        ) : (
          <div className="backpack-selection__actions">
            <button
              type="button"
              className="nb-btn nb-btn--sm nb-btn--primary"
              onClick={() => onToggleDecor(selected.id)}
            >
              {equipped ? t('收起装扮', 'Put away') : t('布置小屋', 'Place in haven')}
            </button>
          </div>
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
            {t('深海海螺扭蛋 & 保温增益', 'Conch draws and boosts')}
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
                空碗自动续米饭（保温券 ×{buffs.autoRefillTickets}）
              </UiSwitch>
            </div>
          </div>

          <div className="conch-buff-item">
            <span className="conch-buff-item__icon">
              <img src={getSceneAssetUrl('props/buff-aroma-spice.webp')} alt="" />
            </span>
            <div className="conch-buff-item__row">
              <span>
                新米增香：
                {buffs.aromaActiveGrams > 0
                  ? `生效中 (${Math.round(buffs.aromaActiveGrams)}g 增香中)`
                  : '未激活'}{' '}
                · 库存 ×{buffs.aromaCharges}
              </span>
              <button
                type="button"
                className="nb-btn nb-btn--sm"
                disabled={buffs.aromaCharges <= 0}
                onClick={onActivateAroma}
              >
                撒入增香包
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
          <Sparkles size={14} /> 转动海螺扭蛋机（消耗 {pullCost} 片碎片）
        </button>
      </section>
    </>
  );
}
