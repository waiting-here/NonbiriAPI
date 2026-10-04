import { Panel, PanelHead, PanelBody } from '@shared/components/ui';
import { GameWallets } from './common/GameWallets';
import { Link } from 'react-router';
import { GamePrivacyControl } from './common/GamePrivacyControl';
import { Leaderboard } from './ranking/Leaderboard';
import { ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { GameHero, type GameHeroKind } from './assets/GameHero';
import { useGameCopy, type GameCopyKey } from './copy';
import { isMaintenance } from './common/request';
import { formatCredits } from './common/strict';
import { useGamesSnapshot } from './common/snapshot';
import type { GamesSnapshot } from './common/types';
import './games.css';

type Availability = 'open' | 'closed' | 'maintenance';

interface CenterCard {
  readonly id: GameHeroKind;
  readonly path: string;
  readonly title: GameCopyKey;
  readonly body: GameCopyKey;
  readonly state: Availability;
  readonly detail: string;
  readonly total?: number;
}

function cardState(
  snapshot: GamesSnapshot,
  kind: GameHeroKind,
): { state: Availability; detail: string; total?: number } {
  if (kind === 'blackjack')
    return {
      state:
        snapshot.gamesEnabled && snapshot.blackjack.enabled && snapshot.blackjack.available
          ? 'open'
          : 'closed',
      detail: formatCredits(snapshot.blackjack.min_stake),
    };
  if (kind === 'fishing') {
    return {
      state:
        snapshot.gamesEnabled && snapshot.fishing.enabled && snapshot.fishing.available
          ? 'open'
          : 'closed',
      detail: formatCredits(snapshot.fishing.baitPrices.worm),
    };
  }
  if (kind === 'linklink') {
    const values = Object.values(snapshot.linklink.specs);
    const count =
      snapshot.gamesEnabled && snapshot.linklink.enabled
        ? values.filter((spec) => spec.enabled).length
        : 0;
    return {
      state: count > 0 ? 'open' : 'closed',
      detail: String(count),
    };
  }
  const game = snapshot[kind];
  const modes = Object.values(game.modes);
  const count =
    snapshot.gamesEnabled && game.enabled && (!('available' in game) || game.available)
      ? modes.filter((mode) => mode.enabled && (!('available' in mode) || mode.available)).length
      : 0;
  return { state: count > 0 ? 'open' : 'closed', detail: String(count), total: modes.length };
}

function GameCard({ card }: { card: CenterCard }) {
  const { text } = useGameCopy();
  const stateLabel =
    card.state === 'open'
      ? card.id === 'likes'
        ? text('presentation.test')
        : text('common.open')
      : text('presentation.unavailable');
  const detail =
    card.id === 'fishing' || card.id === 'blackjack'
      ? text('center.from', { amount: card.detail })
      : card.id === 'linklink'
        ? text('center.specs', { count: card.detail })
        : text('center.modes', { count: card.detail, total: card.total ?? 0 });
  return (
    <Link
      className={`card game-center-card game-center-card--${card.id}${card.state !== 'open' ? ' is-closed' : ''}`}
      to={card.path}
    >
      <div className="game-center-card__hero">
        <GameHero kind={card.id} />
        <span className={`nb-badge nb-badge--${card.state === 'open' ? 'ok' : 'plain'}`}>
          {stateLabel}
        </span>
      </div>
      <div className="game-center-card__body">
        <h2>{text(card.title)}</h2>
        <p>{text(card.body)}</p>
        <div className="game-card-foot">
          <span>{card.state !== 'maintenance' ? detail : ''}</span>
          <span>{text(card.state === 'open' ? 'presentation.enter' : 'presentation.learn')}</span>
        </div>
      </div>
    </Link>
  );
}

export function GameCenter() {
  const { text } = useGameCopy();
  const snapshot = useGamesSnapshot();
  const maintenance = isMaintenance(snapshot.error);
  if (snapshot.isPending) return <LoadingState label={text('common.loading')} />;
  if (snapshot.error && !maintenance)
    return <ErrorState error={snapshot.error} onRetry={() => void snapshot.refetch()} />;
  const cards: CenterCard[] = (
    ['fishing', 'linklink', 'rps', 'bidding', 'likes', 'blackjack'] as const
  ).map((id) => {
    const availability =
      maintenance || !snapshot.data
        ? { state: 'maintenance' as const, detail: '' }
        : cardState(snapshot.data, id);
    return {
      id,
      path: `/games/${id}`,
      title: `center.${id}.title`,
      body: `center.${id}.body`,
      ...availability,
    };
  });
  return (
    <main className="game-page game-center">
      <PageHeader
        title={text('center.title')}
        description={text('presentation.description')}
        actions={
          snapshot.data ? (
            <div className="game-center-wallet">
              <GameWallets wallets={snapshot.data} />
              <Link to="/activities">{text('presentation.activityWallet')}</Link>
            </div>
          ) : undefined
        }
      />
      <div className="game-center-grid">
        {cards.map((card) => (
          <GameCard key={card.id} card={card} />
        ))}
      </div>
      <div id="game-rankings">
        <Panel className="game-center-rankings">
          <PanelHead title={text('presentation.rankings')} actions={<GamePrivacyControl />} />
          <PanelBody>
            <div className="game-leaderboards">
              <Leaderboard board="game_charity" foldHelp />
              <Leaderboard board="game_net_profit" foldHelp />
            </div>
          </PanelBody>
        </Panel>
      </div>
    </main>
  );
}
