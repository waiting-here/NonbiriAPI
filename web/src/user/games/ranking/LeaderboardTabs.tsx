import { Tabs } from '@shared/components/ui/Tabs';
import { useId, useState, type ReactNode } from 'react';
import { useDuelText } from '../common/duel/copy';
import './ranking.css';
type RankingTab = {
  id: string;
  label: string;
  content: ReactNode;
};
export function LeaderboardTabs({
  items,
}: {
  readonly items: readonly [RankingTab, ...RankingTab[]];
}) {
  const text = useDuelText();
  const prefix = useId();
  const [selected, setSelected] = useState(items[0].id);
  const active = items.find((item) => item.id === selected) ?? items[0];
  return (
    <section className="rank-switcher" aria-label={text('ranking.leaderboards')}>
      <Tabs
        label={text('ranking.chooseALeaderboard')}
        value={active.id}
        onChange={setSelected}
        tabs={items.map((item) => ({
          value: item.id,
          label: item.label,
          id: `${prefix}-${item.id}`,
          panelId: `${prefix}-panel`,
        }))}
      />
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
