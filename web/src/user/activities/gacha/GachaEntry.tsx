import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router';
import { Icon } from '@shared/components/Icon';
import { apiFetch } from '@shared/query/http';
import { useUserSession } from '../../data';
import './gacha.css';

export function GachaEntry() {
  const { i18n } = useTranslation();
  const english = i18n.language.startsWith('en');
  const session = useUserSession();
  const entry = useQuery({
    queryKey: ['user', 'gacha-entry', session.data?.user.id],
    queryFn: ({ signal }) => apiFetch<{ enabled: boolean; entered: boolean }>('/api/gacha/entry', { signal }),
    enabled: !!session.data?.user.id,
  });
  if (!entry.data?.enabled) return null;
  return (
    <section className="gacha-entry">
      <img src="/assets/gacha/covers/entry.webp" alt="" />
      <div>
        <span>{english ? 'COLLECT A LITTLE JOY' : '收藏每一次心动'}</span>
        <h2>{english ? 'The Gacha Simulator' : '真·抽卡模拟器'}</h2>
        <p>{entry.data.entered
          ? (english ? 'Return to your collection and meet the companions waiting in the stars.' : '翻开收藏册，再赴一场星光里的相遇。')
          : (english ? 'Meet your companions across many themes. Your first visit includes 10 standard tickets.' : '五位少女，多彩主题。初次相遇，送你 10 张常驻抽奖券。')}</p>
        <Link className="nb-btn nb-btn--primary" to="/activities/gacha">
          <Icon name="spark" />
          {english ? 'Open the collection' : '开启星光收藏册'}
        </Link>
      </div>
    </section>
  );
}
