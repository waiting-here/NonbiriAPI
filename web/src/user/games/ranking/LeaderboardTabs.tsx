import { useId, useState, type ReactNode } from 'react';
import { useDuelText } from '../common/duel/copy';
import './ranking.css';

type RankingTab = { id: string; label: string; content: ReactNode };

export function LeaderboardTabs({
  items,
}: {
  readonly items: readonly [RankingTab, ...RankingTab[]];
}) {
  const t = useDuelText();
  const prefix = useId();
  const [selected, setSelected] = useState(items[0].id);
  const active = items.find((item) => item.id === selected) ?? items[0];
  return (
    <section className="rank-switcher" aria-label={t('排行榜', 'Leaderboards')}>
      <div
        className="rank-tabs"
        role="tablist"
        aria-label={t('选择排行榜', 'Choose a leaderboard')}
        onKeyDown={(event) => {
          if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
          event.preventDefault();
          const current = items.findIndex((item) => item.id === active.id);
          const next =
            event.key === 'Home'
              ? 0
              : event.key === 'End'
                ? items.length - 1
                : (current + (event.key === 'ArrowRight' ? 1 : -1) + items.length) % items.length;
          setSelected(items[next].id);
          event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="tab"]')[next]?.focus();
        }}
      >
        {items.map((item) => (
          <button
            key={item.id}
            type="button"
            role="tab"
            id={`${prefix}-${item.id}`}
            aria-controls={`${prefix}-panel`}
            aria-selected={item.id === active.id}
            tabIndex={item.id === active.id ? 0 : -1}
            className={`btn ${item.id === active.id ? 'btn-primary' : 'btn-secondary'}`}
            onClick={() => setSelected(item.id)}
          >
            {item.label}
          </button>
        ))}
      </div>
      <div
        key={active.id}
        role="tabpanel"
        id={`${prefix}-panel`}
        aria-labelledby={`${prefix}-${active.id}`}
      >
        {active.content}
      </div>
    </section>
  );
}
