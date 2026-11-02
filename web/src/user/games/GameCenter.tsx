import { CreditText } from '@shared/components/CreditText';
import { Panel, PanelHead, PanelBody } from '@shared/components/ui';
import { GameWallets } from './common/GameWallets';
import { Link } from 'react-router';
import { GamePrivacyControl } from './common/GamePrivacyControl';
import { Leaderboard } from './ranking/Leaderboard';
import { ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { GameHero, type GameHeroKind } from './assets/GameHero';
import { useGameCopy, type GameCopyKey } from './copy';
import { isMaintenance } from './common/request';
import { creditsToMilli, formatCredits } from './common/strict';
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
  readonly price?: string;
}

function minimumPrice(prices: readonly string[]): string | undefined {
  return prices.reduce<string | undefined>(
    (lowest, price) =>
      lowest === undefined || creditsToMilli(price) < creditsToMilli(lowest) ? price : lowest,
    undefined,
  );
}

function cardState(
  snapshot: GamesSnapshot,
  kind: GameHeroKind,
): { state: Availability; price?: string } {
  if (kind === 'lakenotes')
    return {
      state: snapshot.gamesEnabled && snapshot.lakenotes.enabled ? 'open' : 'closed',
      price: '0',
    };
  if (kind === 'steadycatch')
    return {
      state:
        snapshot.gamesEnabled && snapshot.steadycatch.enabled && snapshot.steadycatch.available
          ? 'open'
          : 'closed',
      price: snapshot.steadycatch.price,
    };
  if (kind === 'blackjack')
    return {
      state:
        snapshot.gamesEnabled && snapshot.blackjack.enabled && snapshot.blackjack.available
          ? 'open'
          : 'closed',
      price: snapshot.blackjack.min_stake,
    };
  if (kind === 'fishing')
    return {
      state:
        snapshot.gamesEnabled && snapshot.fishing.enabled && snapshot.fishing.available
          ? 'open'
          : 'closed',
      price: minimumPrice(Object.values(snapshot.fishing.baitPrices)),
    };
  if (kind === 'linklink') {
    const specs = Object.values(snapshot.linklink.specs).filter((spec) => spec.enabled);
    return {
      state:
        snapshot.gamesEnabled && snapshot.linklink.enabled && specs.length > 0 ? 'open' : 'closed',
      price: minimumPrice(specs.map((spec) => spec.price)),
    };
  }
  if (kind === 'rps')
    return {
      state:
        snapshot.gamesEnabled &&
        snapshot.rps.enabled &&
        Object.values(snapshot.rps.modes).some((mode) => mode.enabled)
          ? 'open'
          : 'closed',
    };
  const game = snapshot[kind];
  const modes = Object.values(game.modes).filter((mode) => mode.enabled && mode.available);
  return {
    state:
      snapshot.gamesEnabled && game.enabled && game.available && modes.length > 0
        ? 'open'
        : 'closed',
    price: minimumPrice(modes.map((mode) => mode.ticket)),
  };
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
    card.id === 'lakenotes' ? (
      ''
    ) : card.price === undefined ? (
      text('center.reviewCosts')
    ) : creditsToMilli(card.price) === 0n ? (
      text('center.free')
    ) : (
      <CreditText
        i18nKey="user.games.copy.center.from"
        amounts={{ amount: formatCredits(card.price) }}
      />
    );
  const reward = text(
    card.id === 'lakenotes'
      ? 'center.free'
      : card.id === 'linklink' || card.id === 'steadycatch'
        ? 'center.firstClearReward'
        : 'center.winCredits',
  );
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
          <span className="game-card-meta">
            {card.state === 'open' && detail ? <span>{detail}</span> : null}
            <span className="nb-badge nb-badge--plain">{reward}</span>
          </span>
          <span className="nb-btn nb-btn--sm nb-btn--secondary" aria-hidden="true">
            {text(card.state === 'open' ? 'presentation.enter' : 'presentation.learn')}
          </span>
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
    [
      'fishing',
      'linklink',
      'rps',
      'bidding',
      'likes',
      'blackjack',
      'gwent',
      'steadycatch',
      'lakenotes',
    ] as const
  ).map((id) => {
    const availability =
      maintenance || !snapshot.data
        ? { state: 'maintenance' as const }
        : cardState(snapshot.data, id);
    return {
      id,
      path:
        id === 'lakenotes'
          ? '/games/lake-notes'
          : id === 'steadycatch'
            ? '/games/steady-catch'
            : `/games/${id}`,
      title: `center.${id}.title`,
      body: `center.${id}.body`,
      ...availability,
    };
  });
  return (
    <main className="game-page game-center">
      <PageHeader
        icon="games"
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
