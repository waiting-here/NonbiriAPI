import { Link } from 'react-router';
import { GamePrivacyControl } from './common/GamePrivacyControl';
import { Card, ErrorState, LoadingState, PageHeader, StatusBadge } from '@shared/components/States';
import { GameHero, type GameHeroKind } from './assets/GameHero';
import { useGameCopy, type GameCopyKey } from './copy';
import { isMaintenance } from './common/request';
import { formatCredits } from './common/strict';
import { useGamesSnapshot } from './common/snapshot';
import type { GamesSnapshot } from './common/types';
import './games.css';

type Availability = 'open' | 'partial' | 'closed' | 'maintenance';

interface CenterCard {
  readonly id: GameHeroKind;
  readonly path: string;
  readonly title: GameCopyKey;
  readonly body: GameCopyKey;
  readonly state: Availability;
  readonly detail: string;
}

function cardState(
  snapshot: GamesSnapshot,
  kind: GameHeroKind,
): { state: Availability; detail: string } {
  if (!snapshot.gamesEnabled) return { state: 'closed', detail: '' };
  if (kind === 'fishing') {
    return {
      state: snapshot.fishing.enabled && snapshot.fishing.available ? 'open' : 'closed',
      detail: formatCredits(snapshot.fishing.baitPrices.worm),
    };
  }
  if (kind === 'linklink') {
    const values = Object.values(snapshot.linklink.specs);
    const count = snapshot.linklink.enabled ? values.filter((spec) => spec.enabled).length : 0;
    return {
      state: count === 3 ? 'open' : count > 0 ? 'partial' : 'closed',
      detail: String(count),
    };
  }
  const modes = Object.values(snapshot.rps.modes);
  const count = snapshot.rps.enabled ? modes.filter((mode) => mode.enabled).length : 0;
  return { state: count === 3 ? 'open' : count > 0 ? 'partial' : 'closed', detail: String(count) };
}

function GameCard({ card }: { card: CenterCard }) {
  const { text } = useGameCopy();
  const stateLabel = text(`common.${card.state}` as GameCopyKey);
  const detail =
    card.id === 'fishing'
      ? text('center.from', { amount: card.detail })
      : card.id === 'linklink'
        ? text('center.specs', { count: card.detail })
        : text('center.modes', { count: card.detail });
  return (
    <Card className={`game-center-card game-center-card--${card.id}`}>
      <div className="game-center-card__hero">
        <GameHero kind={card.id} />
      </div>
      <div className="game-center-card__body">
        <div className="game-card-heading">
          <h2>{text(card.title)}</h2>
          <StatusBadge
            active={card.state === 'open'}
            danger={card.state === 'closed' || card.state === 'maintenance'}
            label={stateLabel}
          />
        </div>
        <p>{text(card.body)}</p>
        {card.state !== 'maintenance' ? (
          <strong className="game-card-detail">{detail}</strong>
        ) : null}
        {card.state === 'maintenance' ? (
          <span className="btn btn-secondary" aria-disabled="true">
            {text('common.maintenance')}
          </span>
        ) : (
          <Link className="btn btn-primary" to={card.path}>
            {text(card.state === 'closed' ? 'center.details' : 'center.enter')}
          </Link>
        )}
      </div>
    </Card>
  );
}

export function GameCenter() {
  const { text } = useGameCopy();
  const snapshot = useGamesSnapshot();
  const maintenance = isMaintenance(snapshot.error);
  if (snapshot.isPending) return <LoadingState label={text('common.loading')} />;
  if (snapshot.error && !maintenance)
    return <ErrorState error={snapshot.error} onRetry={() => void snapshot.refetch()} />;
  const cards: CenterCard[] = (['fishing', 'linklink', 'rps'] as const).map((id) => {
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
        eyebrow={text('center.eyebrow')}
        title={text('center.title')}
        description={text('center.description')}
      />
      <div className="game-center-grid">
        {cards.map((card) => (
          <GameCard key={card.id} card={card} />
        ))}
      </div>
      <GamePrivacyControl />
    </main>
  );
}
