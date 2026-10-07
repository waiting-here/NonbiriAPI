import { useEffect, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { ErrorState, LoadingState } from '@shared/components/States';
import { GameWallets } from '../common/GameWallets';
import { useAuthoritativeCountdown } from '../common/countdown';
import { useDuel } from '../common/duel/api';
import { DuelFeedback } from '../common/duel/Feedback';
import { DuelFinance, DuelTerms } from '../common/duel/Finance';
import { DuelHistory } from '../common/duel/History';
import { DuelDialog } from '../common/duel/Dialog';
import { RandomnessProof } from '../common/RandomnessProof';
import { gameRequest } from '../common/request';
import { spendableGameCredits } from '../common/spendable';
import { creditsToMilli } from '../common/strict';
import { entryProblem, entryMessage } from '../common/duel/availability';
import { useDuelText } from '../common/duel/copy';
import type { DuelLobbyContext } from '../common/duel/types';
import { Battlefield } from './Battlefield';
import { DeckEditor } from './DeckEditor';
import { GwentLeaderboard } from './Leaderboard';
import { RoundLog } from './RoundLog';
import { help, useGwentText } from './copy';
import {
  deckCounts,
  FACTIONS,
  gwentCodec,
  starterDeck,
  type Action,
  type Catalog,
  type Deck,
} from './types';
import '../common/duel/duel.css';
import './gwent.css';

const savedDeckKey = 'nonbiri.gwent.deck.v1';
function restoredDeck(catalog: Catalog): Deck {
  try {
    const saved = JSON.parse(localStorage.getItem(savedDeckKey) ?? 'null') as Deck | null;
    if (
      saved &&
      FACTIONS.includes(saved.faction) &&
      typeof saved.leader === 'string' &&
      Array.isArray(saved.cards) &&
      saved.cards.every((entry) => typeof entry.id === 'string' && Number.isInteger(entry.count))
    )
      return saved;
  } catch {
    /* A local draft is optional. */
  }
  return starterDeck('openai', catalog.modes.standard.cards);
}
function Lobby({
  catalog,
  config,
  disabled,
  onQueue,
}: {
  catalog: Catalog;
  config: DuelLobbyContext['config'];
  disabled: boolean;
  onQueue: (deck: Deck) => void;
}) {
  const t = useGwentText();
  const [deck, setDeck] = useState(() => restoredDeck(catalog));
  const counts = deckCounts(deck, catalog.modes.standard.cards);
  const usable = counts.units >= 22 && counts.specials <= 10 && counts.heroes <= 4;
  const update = (value: Deck) => {
    setDeck(value);
    try {
      localStorage.setItem(savedDeckKey, JSON.stringify(value));
    } catch {
      /* Keep the draft in memory. */
    }
  };
  return (
    <section className="gwt-lobby">
      <DeckEditor
        catalog={catalog.modes.standard.cards}
        deck={deck}
        disabled={disabled}
        onChange={update}
      />
      {config.modes.standard && <DuelTerms mode={config.modes.standard} />}
      <p>
        {t(
          '双方门票组成奖池，扣除费用后由胜者获得；平局原路退票。',
          'Both entries form the prize pool. The winner receives it after fees; draws refund both entries.',
        )}
      </p>
      <button
        type="button"
        className="btn btn-primary"
        disabled={disabled || !usable}
        onClick={() => onQueue(deck)}
      >
        {t('开始匹配', 'Find opponent')}
      </button>
      {!usable && (
        <p role="status">
          {t(
            '至少 22 张单位，至多 4 张英雄和 10 张特殊牌。',
            'Use at least 22 units, at most 4 heroes and 10 specials.',
          )}
        </p>
      )}
    </section>
  );
}
export function GwentGame({ config, wallets, accepting, refreshWallets }: DuelLobbyContext) {
  const t = useGwentText(),
    common = useDuelText();
  const duel = useDuel(gwentCodec, refreshWallets);
  const catalog = useQuery({
    queryKey: ['user', 'games', 'gwent', 'catalog'],
    queryFn: async ({ signal }) =>
      (await gameRequest<Catalog>('/api/games/gwent/catalog', { signal, expectedStatuses: [200] }))
        .data,
    staleTime: Infinity,
  });
  const [history, setHistory] = useState(false),
    [rules, setRules] = useState(false),
    [surrender, setSurrender] = useState(false);
  const home = duel.query.data,
    current = home?.current,
    queue = home?.queue;
  const match = useRef<HTMLElement>(null);
  useEffect(() => {
    if (
      current?.id &&
      innerWidth > 760 &&
      match.current &&
      match.current.getBoundingClientRect().bottom > innerHeight
    )
      match.current.scrollIntoView({ block: 'start' });
  }, [current?.id]);
  const remaining = useAuthoritativeCountdown(
    current ? `${current.id}:${current.phaseSeq}` : (queue?.id ?? 'idle'),
    current?.deadline ?? queue?.deadline ?? null,
    home?.serverNow ?? 0,
    duel.refresh,
  );
  const unavailable = entryProblem({ config, accepting }, 'standard');
  const enough =
    config.modes.standard &&
    creditsToMilli(spendableGameCredits(wallets).total) >=
      creditsToMilli(config.modes.standard.ticket);
  const action = (value: Action) => {
    if (current)
      duel.run({
        kind: 'action',
        id: current.id,
        phaseSeq: current.phaseSeq,
        decisionID: current.decisionID,
        action: value,
      });
  };
  return (
    <div className={`gwent-game${current ? ' has-match' : ''}`}>
      <header className="gwt-heading">
        <div>
          <Link to="/games">← {t('小游戏', 'Games')}</Link>
          <h1>{t('AI 昆特牌', 'AI Gwent')}</h1>
        </div>
        <div className="duel-actions">
          {current && (
            <button
              type="button"
              className="btn btn-secondary"
              onClick={() => match.current?.scrollIntoView({ block: 'start', behavior: 'smooth' })}
            >
              {t('定位对局', 'Focus on match')}
            </button>
          )}
          {!current && <GameWallets wallets={wallets} />}
          <button type="button" className="btn btn-secondary" onClick={() => setRules(true)}>
            {t('规则', 'Rules')}
          </button>
          <button type="button" className="btn btn-secondary" onClick={() => setHistory(true)}>
            {t('历史', 'History')}
          </button>
        </div>
      </header>
      <DuelFeedback
        error={duel.error ?? duel.query.error}
        pending={duel.query.isPending}
        uncertain={duel.uncertain}
        onRetry={duel.uncertain ? duel.retry : duel.refresh}
      />
      {current ? (
        <section className="gwt-match" ref={match}>
          <div className="gwt-turn-bar">
            <strong>
              {t('第', 'Round')} {current.round} {t('小局', '')}
            </strong>
            <span>
              {current.locked[current.you]
                ? t('等待对手', 'Waiting for opponent')
                : current.phase === 'turn'
                  ? t('轮到你行动', 'Your turn')
                  : t('请完成选择', 'Make your choice')}
            </span>
            <strong className={(remaining ?? 0) <= 5 ? 'is-urgent' : ''}>
              {remaining ?? '—'}s
            </strong>
            <button
              type="button"
              className="btn btn-secondary"
              disabled={duel.blocked}
              onClick={() => setSurrender(true)}
            >
              {t('认输', 'Surrender')}
            </button>
            {current.view.legal_actions.some((a) => a.kind === 'pass') && (
              <button
                type="button"
                className="btn btn-primary"
                disabled={duel.blocked}
                onClick={() => action({ kind: 'pass' })}
              >
                {t('放弃本小局', 'Pass this round')}
              </button>
            )}
          </div>
          <Battlefield
            key={current.id}
            view={current.view}
            profiles={current.profiles}
            you={current.you}
            disabled={duel.blocked}
            onAction={action}
          />
        </section>
      ) : (
        <>
          {home?.latestResult && <DuelFinance result={home.latestResult} />}
          {queue ? (
            <section className="gwt-queue">
              <h2>{t('正在寻找对手', 'Finding an opponent')}</h2>
              <p>{remaining ?? '—'}s</p>
              <button
                type="button"
                className="btn btn-secondary"
                disabled={duel.blocked}
                onClick={() => duel.run({ kind: 'cancel', id: queue.id, revision: queue.revision })}
              >
                {t('取消匹配', 'Cancel search')}
              </button>
            </section>
          ) : (
            <>
              {unavailable && <p role="status">{entryMessage(unavailable, common)}</p>}
              {!unavailable && !enough && (
                <p>{t('可用积分不足。', 'Insufficient available credits.')}</p>
              )}
              {catalog.isPending ? (
                <LoadingState />
              ) : catalog.error ? (
                <ErrorState error={catalog.error} onRetry={() => void catalog.refetch()} />
              ) : (
                catalog.data && (
                  <Lobby
                    catalog={catalog.data}
                    config={config}
                    disabled={duel.blocked || !!unavailable || !enough}
                    onQueue={(deck) =>
                      duel.run({
                        kind: 'queue',
                        mode: 'standard',
                        termsHash: config.modes.standard.termsHash,
                        loadout: deck,
                      })
                    }
                  />
                )
              )}
            </>
          )}
          <GwentLeaderboard />
        </>
      )}
      <RandomnessProof
        game="gwent"
        id={current?.id ?? home?.latestResult?.id}
        terminal={!current && !!home?.latestResult}
      />
      {history && (
        <DuelHistory
          codec={gwentCodec}
          onClose={() => setHistory(false)}
          renderRound={(round, you) => <RoundLog round={round} you={you} />}
        />
      )}
      {rules && (
        <DuelDialog title={t('AI 昆特牌规则', 'AI Gwent rules')} onClose={() => setRules(false)}>
          <p>
            {t(
              '双方轮流出牌或使用领袖，比较各战线总战力。每人两点生命，小局失败失去一点；平局双方各失去一点。生命耗尽即结束。放弃后本小局不再行动。',
              'Take turns playing a card or using a leader. Total row power decides each round. Each player starts with two lives; the loser loses one, or both lose one on a tie. The match ends when a player has no lives. Passing ends your actions for this round.',
            )}
          </p>
          <p>
            {t(
              '开局抽十张，可换至多两张。换牌限时20秒，普通回合30秒，技能选择15秒；连续三次普通回合超时判负。领袖主动技能整场限一次。',
              'Draw ten cards and replace up to two. Mulligans allow 20 seconds, normal turns 30, and effect choices 15. Three consecutive normal-turn timeouts forfeit the match. An active leader ability can be used once per match.',
            )}
          </p>
          <h3>{t('阵营被动', 'Faction passives')}</h3>
          <p>
            OpenAI ·{' '}
            {t(
              '赢得小局后，下局开始抽一张。',
              'Draw one card at the start of the next round after winning.',
            )}
            <br />
            DeepSeek ·{' '}
            {t(
              '小局结束随机保留一张普通单位；第三局重启领袖改为第三局从弃牌堆随机恢复至多两张普通单位。',
              'Keep one random ordinary unit between rounds. The rebirth leader instead restores up to two random ordinary units from the discard pile in round three.',
            )}
            <br />
            Claude ·{' '}
            {t(
              '同战力时获胜，双方均为Claude时仍平局。',
              'Win tied rounds unless both factions are Claude.',
            )}
            <br />
            Gemini ·{' '}
            {t(
              '选择第一小局先手；双方均为Gemini时随机。',
              'Choose the opening player unless both factions are Gemini.',
            )}
          </p>
          <h3>{t('卡牌技能', 'Card abilities')}</h3>
          {Object.entries(help).map(([id, value]) => (
            <details key={id}>
              <summary>{value.name}</summary>
              <p>{value.description}</p>
            </details>
          ))}
        </DuelDialog>
      )}
      <ConfirmDialog
        description={t('本场按对手获胜结算。', 'The opponent wins and receives the settled prize.')}
        open={surrender}
        title={t('认输并结束对局？', 'Surrender this match?')}
        confirmLabel={t('认输', 'Surrender')}
        onCancel={() => setSurrender(false)}
        onConfirm={() => {
          setSurrender(false);
          if (current) duel.run({ kind: 'surrender', id: current.id, phaseSeq: current.phaseSeq });
        }}
      />
    </div>
  );
}
