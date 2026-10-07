import { useState, type ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { ErrorState } from '@shared/components/States';
import {
  aiPlayerLabel,
  useAIText,
  type AIHome,
  type AIView,
  type AIActionSource,
} from '@shared/aiPlayers';
import { gameRequest } from '../common/request';
import { GameMoney } from '../common/GameMoney';
import { Toggle } from '@shared/components/ui';
import type { DuelIntent } from '../common/duel/api';
import { useGameVisibility } from '../common/visibility';

const root = '/api/games/bidding/ai';
export function BiddingLobby({
  children,
  ...props
}: Parameters<typeof AIPlayers>[0] & { children: ReactNode }) {
  const t = useAIText();
  const [kind, setKind] = useState<'human' | 'ai'>('human');
  return (
    <>
      <div className="bid-opponent-tabs" role="group" aria-label={t('对战类型', 'Opponent type')}>
        <button type="button" aria-pressed={kind === 'human'} onClick={() => setKind('human')}>
          {t('真人对战', 'Player matches')}
        </button>
        <button type="button" aria-pressed={kind === 'ai'} onClick={() => setKind('ai')}>
          {t('AI 玩家', 'AI players')}
        </button>
      </div>
      {kind === 'human' ? children : <AIPlayers {...props} />}
    </>
  );
}
export function AIPlayers({
  blocked,
  onStart,
  lastMatch,
}: {
  blocked: boolean;
  onStart: (intent: DuelIntent) => void;
  lastMatch?: string;
}) {
  const t = useAIText();
  const [selectedID, setSelectedID] = useState('');
  const visible = useGameVisibility();
  const query = useQuery({
    queryKey: ['user', 'games', 'bidding', 'ai', lastMatch],
    queryFn: async ({ signal }) =>
      (await gameRequest<AIHome>(root, { signal, expectedStatuses: [200] })).data!,
    retry: false,
    refetchInterval: visible ? 15000 : false,
  });
  const preference = useRetainedOperation<{ bot_id: string; memory_enabled: boolean }, unknown>(
    async (body, key, context) =>
      (
        await gameRequest(root + '/preference', {
          method: 'POST',
          json: body,
          idempotencyKey: key,
          signal: context.signal,
          expectedStatuses: [200],
        })
      ).data,
    () => query.refetch(),
    ['user', 'games', 'bidding', 'ai'],
  );
  if (query.isPending) return null;
  if (query.error) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />;
  if (!query.data?.bots.length)
    return <p role="status">{t('暂无开放的 AI 玩家。', 'No AI players are available.')}</p>;
  const selected =
    query.data.bots.find((offer) => offer.terms.ai.bot_id === selectedID) ?? query.data.bots[0]!;
  return (
    <section className="bid-ai-lobby" aria-label={t('AI 玩家', 'AI players')}>
      <div>
        <span className="bid-eyebrow">{t('人机挑战', 'AI CHALLENGE')}</span>
        <h2>{t('选择你的对手', 'Choose your opponent')}</h2>
        <p>
          {t(
            '入场时扣除门票。正常完成并获胜可领取一次首通奖励；人机局不计入对战排行榜。正常结束或 AI 技术回退不退门票；系统中止按原支付资产退还。',
            'The ticket is charged when your match starts. Complete a match and win for a one-time first-clear reward. AI matches do not enter PvP rankings. Normal completion or an AI fallback does not refund the ticket; system cancellation returns the original payment.',
          )}
        </p>
      </div>
      <div
        className="bid-ai-select"
        role="group"
        aria-label={t('选择 AI 对手', 'Choose an AI opponent')}
      >
        {query.data.bots.map(({ terms: { ai: bot } }) => (
          <button
            type="button"
            key={bot.bot_id}
            aria-pressed={bot.bot_id === selected.terms.ai.bot_id}
            onClick={() => setSelectedID(bot.bot_id)}
          >
            <strong>{bot.bot_name}</strong>
            <small>{aiPlayerLabel(bot.source_id, t)}</small>
          </button>
        ))}
      </div>
      <div className="bid-ai-cards">
        {[selected].map((offer) => {
          const bot = offer.terms.ai;
          return (
            <article key={bot.bot_id} className="bid-ai-card">
              <span className="bid-ai-mark">{aiPlayerLabel(bot.source_id, t)}</span>
              <h3>{bot.bot_name}</h3>
              <p>{bot.description}</p>
              <dl>
                <div>
                  <dt>{t('门票', 'Ticket')}</dt>
                  <dd>
                    <GameMoney value={offer.terms.ticket} />
                  </dd>
                </div>
                <div>
                  <dt>
                    {offer.completed
                      ? t('已完成首通', 'First clear completed')
                      : t('首通奖励 · 游戏积分', 'First clear · game credits')}
                  </dt>
                  <dd>{offer.completed ? '✓' : <GameMoney value={bot.first_reward} />}</dd>
                </div>
              </dl>
              <p className="game-inline-notice game-inline-notice--warning">
                <strong>{t('没有逐局积分奖励。', 'No per-game credit rewards.')} </strong>
                {offer.completed
                  ? t(
                      '此挑战已完成首通，再次获胜不再发放积分。',
                      'This challenge is already cleared. Further wins award no credits.',
                    )
                  : t(
                      '仅首次获胜可领取上方首通奖励。',
                      'Only your first win awards the first-clear reward shown above.',
                    )}
              </p>
              <p>
                {t('可用样本', 'Available samples')}: {offer.memory_samples}
              </p>
              <Toggle
                label={t('使用对战记忆', 'Use match memory')}
                checked={offer.memory_enabled}
                disabled={preference.isPending || preference.outcome === 'unknown'}
                onChange={(memory_enabled) =>
                  preference.mutate({ bot_id: bot.bot_id, memory_enabled })
                }
              />
              <small>
                {t(
                  `按最近 ${bot.memory_days} 天、最多 ${bot.memory_games} 局的合格出价调整决策。关闭后仍积累样本，从下一局起不将其用于决策。`,
                  `Adapts to eligible bids from up to ${bot.memory_games} matches within ${bot.memory_days} days. Turning this off keeps collecting samples but excludes them from decisions starting with your next match.`,
                )}
              </small>
              <button
                type="button"
                className="btn btn-primary"
                disabled={
                  blocked ||
                  !query.data.enabled ||
                  preference.isPending ||
                  preference.outcome === 'unknown'
                }
                onClick={() =>
                  onStart({
                    kind: 'queue',
                    mode: 'ai',
                    botID: bot.bot_id,
                    termsHash: offer.terms_hash,
                  })
                }
              >
                {t('挑战', 'Challenge')}
              </button>
            </article>
          );
        })}
      </div>
      {preference.error && (
        <ErrorState
          error={preference.error}
          onRetry={() => {
            if (preference.variables) preference.mutate(preference.variables);
          }}
        />
      )}
    </section>
  );
}
export function AIMatchInfo({ ai, sources }: { ai: AIView; sources?: readonly AIActionSource[] }) {
  const t = useAIText();
  const fallback = sources?.some((s) => s.origin === 'fallback');
  return (
    <aside className="bid-ai-info">
      <strong>
        {aiPlayerLabel(ai.terms.source_id, t)} · {ai.terms.bot_name}
      </strong>
      <span>
        {ai.memory_enabled
          ? t('已启用对战记忆', 'Match memory on')
          : t('未使用对战记忆', 'Match memory off')}
      </span>
      {fallback && (
        <span role="status">
          {t(
            'AI 本局曾使用备用决策，比赛和首通资格继续有效。',
            'The AI used a fallback decision. The match and first-clear eligibility remain valid.',
          )}
        </span>
      )}
    </aside>
  );
}
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
