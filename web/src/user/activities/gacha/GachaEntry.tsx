import { useTranslation } from 'react-i18next';
import { useState } from 'react';
import { ArtworkImage, ArtworkBadge, ArtworkUnavailable } from '@shared/artwork/ArtworkImage';
import { useArtworkPolicy } from '@shared/artwork/context';
import { Link } from 'react-router';
import { Icon } from '@shared/components/Icon';
import { formatDateTime } from '@shared/utils/datetime';
import { cardById } from './catalog';
import type { GachaPoolEntry } from './entry';
import './gacha.css';
import './gacha-entry.css';

export function GachaEntry() {
  const { i18n } = useTranslation();
  const english = i18n.language.startsWith('en');
  return (
    <section className="gacha-entry" aria-labelledby="gacha-entry-title">
      <ArtworkImage
        artworkKey="cover:permanent"
        src="/assets/gacha/covers/permanent.webp"
        alt=""
        loading="lazy"
      />
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
  return pools.map((pool) => (
    <GachaPoolBanner key={`${pool.id}:${pool.cover}`} pool={pool} english={english} />
  ));
}

function GachaPoolBanner({ pool, english }: { pool: GachaPoolEntry; english: boolean }) {
  const policy = useArtworkPolicy();
  const [revealed, setRevealed] = useState(false);
  const artworkKey = 'cover:' + pool.cover;
  const src = '/assets/gacha/covers/' + pool.cover + '.webp';
  const rated = policy.rating(artworkKey) === 'r18';
  const allowed = policy.allowed(artworkKey) && policy.sourceAllowed(src);
  const visible = allowed && revealed;
  const name = english && pool.name_en ? pool.name_en : pool.name;
  const featured = pool.featured.flatMap((id) => {
    const card = cardById[id];
    return card ? [english ? card.name_en : card.name] : [];
  });
  const href = '/activities/gacha?pool=' + encodeURIComponent(pool.id);
  const cover = <ArtworkImage artworkKey={artworkKey} src={src} alt={name} loading="lazy" />;
  const copy = (
    <>
      <h3>{name}</h3>
      <ArtworkBadge artworkKey={artworkKey} />
      <ArtworkUnavailable artworkKey={artworkKey} />
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
    </>
  );
  if (!rated) {
    return (
      <Link className="card limited-entry gacha-pool-entry" to={href}>
        <div className="limited-entry__cover">{cover}</div>
        <div className="limited-entry__copy">{copy}</div>
      </Link>
    );
  }
  const label = !allowed
    ? english
      ? `R-18 cover unavailable: ${name}`
      : `R-18 封面不可用：${name}`
    : visible
      ? english
        ? `Blur R-18 cover: ${name}`
        : `模糊 R-18 封面：${name}`
      : english
        ? `Reveal R-18 cover: ${name}`
        : `显示 R-18 封面：${name}`;
  return (
    <article className="card limited-entry gacha-pool-entry gacha-pool-entry--rated">
      <button
        type="button"
        className="limited-entry__cover gacha-pool-entry__toggle"
        aria-label={label}
        aria-pressed={visible}
        disabled={!allowed}
        onClick={() => setRevealed((current) => !current)}
      >
        {cover}
        {allowed ? (
          <span className="gacha-pool-entry__hint">
            {english
              ? visible
                ? 'R-18 · Click to blur cover'
                : 'R-18 · Click to reveal cover'
              : visible
                ? 'R-18 · 点击模糊封面'
                : 'R-18 · 点击显示封面'}
          </span>
        ) : null}
      </button>
      <Link className="limited-entry__copy gacha-pool-entry__link" to={href}>
        {copy}
      </Link>
    </article>
  );
}
