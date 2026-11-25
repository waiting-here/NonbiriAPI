import { useAIText, type AIActionSource } from '@shared/aiPlayers';
export function AIActionLog({ sources }: { sources?: readonly AIActionSource[] }) {
  const t = useAIText();
  if (!sources?.length) return null;
  const labels: Record<string, string> = {
    human: t('玩家', 'Player'),
    ai: 'AI',
    rule: t('规则自动', 'Rule automatic'),
    fallback: t('备用决策', 'Fallback'),
    timeout: t('超时自动', 'Timeout'),
  };
  return (
    <details className="duel-round">
      <summary>{t('动作来源', 'Action sources')}</summary>
      <ol>
        {sources.map((s, i) => (
          <li key={i}>
            {t('第', 'Round ')} {s.round} {s.phase === 'joker' ? 'Joker' : t('出价', 'bid')} ·{' '}
            {t('席位', 'Seat')} {s.seat + 1} · {labels[s.origin] ?? t('未知', 'Unknown')}
            {s.failure ? ` · ${t('技术回退', 'Technical fallback')}` : ''}
          </li>
        ))}
      </ol>
    </details>
  );
}
