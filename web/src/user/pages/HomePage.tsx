import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router';
import { PageHeader } from '@shared/components/States';
import { isNotFoundError, isUnauthorized } from '@shared/query/http';
import {
  CoreErrorPanel,
  CoreLoading,
  CoreProfileGate,
  CoreTime,
  CoreUnavailable,
  ExactCount,
  ExactCredits,
  StatusPill,
} from '../features/core/components';
import { useCoreCopy } from '../features/core/copy';
import { CORE_ROUTE_PATHS } from '../features/core/descriptors';
import {
  CapabilityUnavailableError,
  productionHomeAdapters,
} from '../features/core/adapters';
import { coreKeys, useCoreMe, useCoreSession } from '../features/core/queries';
import type { HomeAdapters, HomeGameSummary, UserProfile } from '../features/core/types';
import '../features/core/core.css';

const GAME_PATHS: Record<HomeGameSummary['route_id'], string> = {
  'game-fishing': '/games/fishing',
  'game-linklink': '/games/linklink',
  'game-rps': '/games/rps',
};

const GAME_LABELS = {
  'game-fishing': 'home.gameFishing',
  'game-linklink': 'home.gameLinklink',
  'game-rps': 'home.gameRps',
} as const;

function SignedOutHome() {
  const { t } = useCoreCopy();
  return (
    <div className="page core-page core-stack">
      <PageHeader
        icon="home"
        title={t('home.signedOutTitle')}
        description={t('home.signedOutBody')}
      />
      <section className="core-card">
        <div className="core-card__header">
          <div>
            <h2>{t('home.signedOutTitle')}</h2>
            <p className="core-muted">{t('home.signedOutBody')}</p>
          </div>
          <a className="btn btn-primary" href="/api/auth/discord/start">
            {t('home.signIn')}
          </a>
        </div>
      </section>
    </div>
  );
}

function ProfileCard({ user }: { user: UserProfile }) {
  const { t } = useCoreCopy();
  return (
    <section className="core-card">
      <div className="core-card__header">
        <h2>{t('home.profileTitle')}</h2>
      </div>
      <dl className="core-detail-list">
        <div>
          <dt>{t('home.username')}</dt>
          <dd>{user.guild_nick || user.username}</dd>
        </div>
        <div>
          <dt>{t('home.accountStatus')}</dt>
          <dd>
            <StatusPill tone={user.is_banned ? 'danger' : 'success'}>
              {user.is_banned ? t('home.banned') : t('home.active')}
            </StatusPill>
          </dd>
        </div>
        <div>
          <dt>{t('common.created')}</dt>
          <dd>
            <CoreTime value={user.created_at} />
          </dd>
        </div>
      </dl>
    </section>
  );
}

function EconomyCard({ accountId }: { accountId: string }) {
  const { t } = useCoreCopy();
  const me = useCoreMe(accountId);
  return (
    <section className="core-card">
      <div className="core-card__header">
        <h2>{t('home.economyTitle')}</h2>
      </div>
      {me.isPending ? (
        <CoreLoading compact />
      ) : me.error ? (
        <CoreErrorPanel error={me.error} compact onRetry={() => void me.refetch()} />
      ) : (
        <div className="core-metrics">
          <div className="core-metric">
            <span>{t('home.balance')}</span>
            <strong>
              <ExactCredits value={me.data.user.balance} />
            </strong>
          </div>
          <div className="core-metric">
            <span>{t('home.donationCredit')}</span>
            <strong>
              <ExactCredits value={me.data.user.donation_credit} />
            </strong>
          </div>
          <div className="core-metric">
            <span>{t('home.level')}</span>
            <strong>
              {t('home.levelValue', {
                level: me.data.user.effective_level,
                name: me.data.user.level_display_name,
              })}
            </strong>
          </div>
        </div>
      )}
    </section>
  );
}

function UsageCard({ user }: { user: UserProfile }) {
  const { t } = useCoreCopy();
  return (
    <section className="core-card">
      <div className="core-card__header">
        <h2>{t('home.usageTitle')}</h2>
      </div>
      <div className="core-metrics">
        <div className="core-metric">
          <span>{t('home.requests')}</span>
          <strong>
            <ExactCount value={user.usage.total_requests} />
          </strong>
        </div>
        <div className="core-metric">
          <span>{t('home.promptTokens')}</span>
          <strong>
            <ExactCount value={user.usage.total_prompt_tokens} />
          </strong>
        </div>
        <div className="core-metric">
          <span>{t('home.outputTokens')}</span>
          <strong>
            <ExactCount value={user.usage.total_output_tokens} />
          </strong>
        </div>
        <div className="core-metric">
          <span>{t('home.unknownUsage')}</span>
          <strong>
            <ExactCount value={user.usage.total_unknown_usage_requests} />
          </strong>
        </div>
      </div>
    </section>
  );
}

function CapabilitySections({
  accountId,
  adapters,
}: {
  accountId: string;
  adapters: HomeAdapters;
}) {
  const { t } = useCoreCopy();
  const gamesLoader = adapters.games.state === 'available' ? adapters.games.load : null;
  const announcementsLoader =
    adapters.announcements.state === 'available' ? adapters.announcements.load : null;
  const games = useQuery({
    queryKey: coreKeys.home(accountId, 'games'),
    queryFn: ({ signal }) => {
      if (!gamesLoader) throw new CapabilityUnavailableError();
      return gamesLoader(signal);
    },
    enabled: adapters.games.state === 'available',
    retry: false,
  });
  const announcements = useQuery({
    queryKey: coreKeys.home(accountId, 'announcements'),
    queryFn: ({ signal }) => {
      if (!announcementsLoader) throw new CapabilityUnavailableError();
      return announcementsLoader(signal);
    },
    enabled: adapters.announcements.state === 'available',
    retry: false,
  });
  return (
    <>
      <section className="core-card">
        <div className="core-card__header">
          <h2>{t('home.checkinTitle')}</h2>
        </div>
        <CoreUnavailable compact />
      </section>

      {adapters.games.state === 'unavailable' ? (
        <section className="core-card">
          <div className="core-card__header">
            <h2>{t('home.gamesTitle')}</h2>
          </div>
          <CoreUnavailable compact />
        </section>
      ) : games.isSuccess && games.data.length === 0 ? null : (
        <section className="core-card">
          <div className="core-card__header">
            <h2>{t('home.gamesTitle')}</h2>
          </div>
          {games.isPending ? (
            <CoreLoading compact />
          ) : games.error ? (
            <CoreErrorPanel compact error={games.error} onRetry={() => void games.refetch()} />
          ) : (
            <div className="core-choice-grid">
              {games.data.map((item) => (
                <Link
                  key={`${item.route_id}:${item.kind}:${item.resource_id ?? ''}`}
                  className="core-choice"
                  to={GAME_PATHS[item.route_id]}
                >
                  <strong>{t(GAME_LABELS[item.route_id])}</strong>
                  <span>{item.kind === 'continue' ? t('home.continue') : t('home.view')}</span>
                </Link>
              ))}
            </div>
          )}
        </section>
      )}

      {adapters.announcements.state === 'unavailable' ? (
        <section className="core-card">
          <div className="core-card__header">
            <h2>{t('home.announcementsTitle')}</h2>
          </div>
          <CoreUnavailable compact />
        </section>
      ) : announcements.isSuccess && announcements.data.length === 0 ? null : (
        <section className="core-card">
          <div className="core-card__header">
            <h2>{t('home.announcementsTitle')}</h2>
          </div>
          {announcements.isPending ? (
            <CoreLoading compact />
          ) : announcements.error ? (
            <CoreErrorPanel
              compact
              error={announcements.error}
              onRetry={() => void announcements.refetch()}
            />
          ) : (
            <ul className="core-endpoint-list">
              {announcements.data.map((item) => (
                <li key={item.id} className="core-endpoint-card">
                  <Link to={`/announcements/${encodeURIComponent(item.id)}`}>
                    <strong>{item.title}</strong>
                  </Link>
                  <p>{item.excerpt}</p>
                </li>
              ))}
            </ul>
          )}
        </section>
      )}
    </>
  );
}

export function HomeDashboard({
  user,
  adapters = productionHomeAdapters,
}: {
  user: UserProfile;
  adapters?: HomeAdapters;
}) {
  const { t } = useCoreCopy();
  return (
    <div className="page core-page core-stack">
      <PageHeader
        icon="home"
        title={t('home.title', { name: user.guild_nick || user.username })}
        description={t('home.description')}
      />
      <div className="core-grid core-grid--wide">
        <ProfileCard user={user} />
        <EconomyCard accountId={user.id} />
      </div>
      <UsageCard user={user} />
      <CapabilitySections accountId={user.id} adapters={adapters} />
      <section className="core-card">
        <div className="core-card__header">
          <h2>{t('home.quickTitle')}</h2>
        </div>
        <div className="core-choice-grid">
          <Link className="core-choice" to={CORE_ROUTE_PATHS.endpoints}>
            {t('home.endpoints')}
          </Link>
          <Link className="core-choice" to={CORE_ROUTE_PATHS.models}>
            {t('home.models')}
          </Link>
          <Link className="core-choice" to={CORE_ROUTE_PATHS.keys}>
            {t('home.callerKey')}
          </Link>
          <Link className="core-choice" to="/activities">
            {t('home.activities')}
          </Link>
          <Link className="core-choice" to="/games">
            {t('home.games')}
          </Link>
        </div>
      </section>
    </div>
  );
}

export function HomePage() {
  const session = useCoreSession();
  if (session.isPending)
    return (
      <div className="page core-page">
        <CoreLoading />
      </div>
    );
  if (session.error) {
    if (isUnauthorized(session.error) || isNotFoundError(session.error)) return <SignedOutHome />;
    return (
      <div className="page core-page">
        <CoreErrorPanel error={session.error} onRetry={() => void session.refetch()} />
      </div>
    );
  }
  if (session.data === null) return <SignedOutHome />;
  return (
    <CoreProfileGate
      key={session.data.accountId}
      session={session.data}
      signedOut={<SignedOutHome />}
    >
      {(user) => <HomeDashboard key={user.id} user={user} />}
    </CoreProfileGate>
  );
}
