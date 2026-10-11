import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { Icon } from '@shared/components/Icon';
import { formatDateTime } from '@shared/utils/datetime';
import { cardById } from './catalog';
import type { GachaPoolEntry } from './entry';
import './gacha.css';

export function GachaEntry() {
  const { i18n } = useTranslation();
  const english = i18n.language.startsWith('en');
  return (
    <section className="gacha-entry" aria-labelledby="gacha-entry-title">
      <img src="/assets/gacha/covers/permanent.webp" alt="" loading="lazy" />
      <div>
        <span>{english ? 'COLLECT A LITTLE JOY' : '收藏每一次心动'}</span>
        <h2 id="gacha-entry-title">{english ? 'The Gacha Simulator' : '真·抽卡模拟器'}</h2>
        <p>
          {english
            ? 'Collect illustrated AI companions. Complete themes for credits, tickets and donation credit, and unlock site appearances with SP cards.'
            : '抽取并收藏 AI 少女卡牌，集齐主题可领取悠哉积分、抽奖券与捐赠回馈，获得 SP 卡牌还能解锁主题装扮。'}
        </p>
        <Link className="nb-btn nb-btn--primary" to="/activities/gacha">
          <Icon name="spark" />
          {english ? 'Open the collection' : '开启星光收藏册'}
        </Link>
      </div>
    </section>
  );
}

export function GachaPoolEntries({ pools }: { pools: readonly GachaPoolEntry[] }) {
  const { i18n } = useTranslation();
  const english = i18n.language.startsWith('en');
  return pools.map((pool) => {
    const name = english && pool.name_en ? pool.name_en : pool.name;
    const featured = pool.featured.flatMap((id) => {
      const card = cardById[id];
      return card ? [english ? card.name_en : card.name] : [];
    });
    return (
      <Link
        key={pool.id}
        className="card limited-entry gacha-pool-entry"
        to={'/activities/gacha?pool=' + encodeURIComponent(pool.id)}
      >
        <div className="limited-entry__cover">
          <img src={'/assets/gacha/covers/' + pool.cover + '.webp'} alt="" loading="lazy" />
        </div>
        <div className="limited-entry__copy">
          <h3>{name}</h3>
          <p>
            {english ? 'Limited cards: ' : '可获取限定卡牌：'}
            {new Intl.ListFormat(english ? 'en' : 'zh', {
              style: 'long',
              type: 'conjunction',
            }).format(featured)}
          </p>
          <p className="gacha-pool-entry__schedule">
            {english ? 'Open: ' : '开放时间：'}
            {formatDateTime(pool.starts_at)} — {formatDateTime(pool.ends_at)}
          </p>
          <span className="limited-entry__action">
            {english ? 'Visit pool: ' : '进入奖池：'}
            {name}
          </span>
        </div>
      </Link>
    );
  });
}
