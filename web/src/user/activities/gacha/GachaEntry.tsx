import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { Icon } from '@shared/components/Icon';
import './gacha.css';

export function GachaEntry() {
  const { i18n } = useTranslation();
  const english = i18n.language.startsWith('en');
  return (
    <section className="gacha-entry">
      <img src="/assets/gacha/covers/entry.webp" alt="" />
      <div>
        <span>{english ? 'ALWAYS OPEN · COLLECT A LITTLE JOY' : '常驻开放 · 收藏每一次心动'}</span>
        <h2>{english ? 'The Gacha Simulator' : '真·抽卡模拟器'}</h2>
        <p>
          {english
            ? 'Meet five companions across eleven themes. Your first visit includes 10 standard tickets.'
            : '五位少女，十一段悠哉时光。初次相遇，送你 10 张常驻抽奖券。'}
        </p>
        <Link className="nb-btn nb-btn--primary" to="/activities/gacha">
          <Icon name="spark" />
          {english ? 'Open the collection' : '开启星光收藏册'}
        </Link>
      </div>
    </section>
  );
}
